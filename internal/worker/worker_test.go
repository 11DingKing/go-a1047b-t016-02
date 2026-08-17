package worker

import (
	"testing"
	"time"

	"arcticexpress/internal/app"
	"arcticexpress/internal/domain"
	"arcticexpress/internal/store"
)

func TestWorker_AutoReleaseExpiredSpace(t *testing.T) {
	st := store.New()
	svc := app.NewService(st)
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

	// Space reserved 3 hours ago (exceeds 2-hour window).
	st.SaveSpace(&domain.Space{
		ID: "SP-EXP", VesselID: "VS-1", VoyageID: "VG-1", Reefer: true,
		Status:     domain.SpaceStatusReserved,
		ReservedAt: pTime(now.Add(-3 * time.Hour)),
		ReservedBy: "BK-EXP",
	})
	st.SaveBooking(&domain.Booking{
		ID: "BK-EXP", SpaceID: "SP-EXP", Status: domain.BookingStatusSubmitted,
	})

	wkr := New(svc, Config{
		SpaceTimeout:     2 * time.Hour,
		AnomalyThreshold: 15 * time.Minute,
	})

	n := wkr.RunReleaseOnce(now)
	if n != 1 {
		t.Fatalf("released = %d, want 1", n)
	}

	sp, _ := st.GetSpace("SP-EXP")
	if sp.Status != domain.SpaceStatusAvailable {
		t.Fatalf("space status = %s, want available", sp.Status)
	}
	bk, _ := st.GetBooking("BK-EXP")
	if bk.Status != domain.BookingStatusCancelled {
		t.Fatalf("booking status = %s, want cancelled", bk.Status)
	}
}

func TestWorker_KeepsConfirmedSpace(t *testing.T) {
	st := store.New()
	svc := app.NewService(st)
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

	// Confirmed space (even if reserved long ago) should not be released.
	st.SaveSpace(&domain.Space{
		ID: "SP-CONF", VesselID: "VS-1", VoyageID: "VG-1", Reefer: true,
		Status:      domain.SpaceStatusConfirmed,
		ReservedAt:  pTime(now.Add(-3 * time.Hour)),
		ConfirmedBy: "BK-CONF",
	})
	// Space reserved only 30 minutes ago should not be released.
	st.SaveSpace(&domain.Space{
		ID: "SP-RECENT", VesselID: "VS-1", VoyageID: "VG-1", Reefer: true,
		Status:     domain.SpaceStatusReserved,
		ReservedAt: pTime(now.Add(-30 * time.Minute)),
		ReservedBy: "BK-RECENT",
	})

	wkr := New(svc, Config{
		SpaceTimeout:     2 * time.Hour,
		AnomalyThreshold: 15 * time.Minute,
	})

	n := wkr.RunReleaseOnce(now)
	if n != 0 {
		t.Fatalf("released = %d, want 0", n)
	}

	sp, _ := st.GetSpace("SP-CONF")
	if sp.Status != domain.SpaceStatusConfirmed {
		t.Fatalf("confirmed space status = %s, want confirmed", sp.Status)
	}
}

func TestWorker_TemperatureAnomalyDetection(t *testing.T) {
	st := store.New()
	svc := app.NewService(st)
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

	tc := &domain.TempConfig{
		ContainerID: "CN-ANOM", BookingID: "BK-ANOM",
		MinTempC: 0, MaxTempC: 10, SetTempC: 5,
	}
	st.SaveTempConfig(tc)

	// Out-of-range reading recorded 16 minutes ago (exceeds 15-min threshold).
	outOfRange := st.AddTempReading(domain.TempReading{
		ID:          "TR-1",
		ContainerID: "CN-ANOM",
		BookingID:   "BK-ANOM",
		TempC:       25, // far above 10
		Stage:       domain.TempStageTransit,
		RecordedAt:  now.Add(-16 * time.Minute),
	}, tc)
	if !outOfRange {
		t.Fatal("reading should be out of range")
	}

	wkr := New(svc, Config{
		SpaceTimeout:     2 * time.Hour,
		AnomalyThreshold: 15 * time.Minute,
	})

	n := wkr.RunAnomalyOnce(now)
	if n != 1 {
		t.Fatalf("anomalies = %d, want 1", n)
	}

	a, err := st.GetAnomaly("CN-ANOM")
	if err != nil {
		t.Fatalf("GetAnomaly: %v", err)
	}
	if a.Status != domain.AnomalyStatusOpen {
		t.Fatalf("anomaly status = %s, want open", a.Status)
	}
	if len(a.NotifiedParties) != 3 {
		t.Fatalf("notified parties = %d, want 3", len(a.NotifiedParties))
	}

	// Running again should not create a duplicate anomaly.
	n = wkr.RunAnomalyOnce(now)
	if n != 0 {
		t.Fatalf("second pass anomalies = %d, want 0", n)
	}
}

func TestWorker_AnomalyClearedOnRecovery(t *testing.T) {
	st := store.New()
	svc := app.NewService(st)
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

	tc := &domain.TempConfig{
		ContainerID: "CN-RECV", BookingID: "BK-RECV",
		MinTempC: 0, MaxTempC: 10, SetTempC: 5,
	}
	st.SaveTempConfig(tc)

	// Out-of-range reading 20 minutes ago.
	st.AddTempReading(domain.TempReading{
		ContainerID: "CN-RECV", BookingID: "BK-RECV",
		TempC: 25, Stage: domain.TempStageTransit,
		RecordedAt: now.Add(-20 * time.Minute),
	}, tc)

	wkr := New(svc, Config{
		SpaceTimeout:     2 * time.Hour,
		AnomalyThreshold: 15 * time.Minute,
	})

	// First pass creates an anomaly.
	if n := wkr.RunAnomalyOnce(now); n != 1 {
		t.Fatalf("first pass = %d, want 1", n)
	}

	// An in-range reading clears the streak.
	st.AddTempReading(domain.TempReading{
		ContainerID: "CN-RECV", BookingID: "BK-RECV",
		TempC: 5, Stage: domain.TempStageTransit,
		RecordedAt: now,
	}, tc)

	// Second pass should not create a new anomaly.
	if n := wkr.RunAnomalyOnce(now); n != 0 {
		t.Fatalf("after recovery = %d, want 0", n)
	}
}

func pTime(t time.Time) *time.Time {
	return &t
}
