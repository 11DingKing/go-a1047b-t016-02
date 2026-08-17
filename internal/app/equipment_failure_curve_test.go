package app

import (
	"testing"
	"time"

	"arcticexpress/internal/domain"
)

// failureCurveFixture records three readings on CN-1 and then reports a reefer
// failure, returning the recorded failure and the booking ID.
func failureCurveFixture(t *testing.T) (*Service, string, *domain.EquipmentFailure) {
	t.Helper()
	svc, base := setupService(t)
	clock := base

	b, err := svc.SubmitBooking(SubmitBookingRequest{
		ForwarderID: "FF-1", VoyageID: "VG-001",
		CargoType: "energy_storage", Description: "BESS cabinet 5MWh",
		LoadType: "FCL", WeightKg: 28000,
		ContainerID: "CN-1", SpaceID: "SP-001",
	})
	if err != nil {
		t.Fatalf("SubmitBooking: %v", err)
	}
	if err := svc.ConfirmSpace(b.ID); err != nil {
		t.Fatalf("ConfirmSpace: %v", err)
	}
	if err := svc.ConfigureTemp(b.ID, "CN-1", -10, 25, 5); err != nil {
		t.Fatalf("ConfigureTemp: %v", err)
	}
	if err := svc.LoadCargo(b.ID); err != nil {
		t.Fatalf("LoadCargo: %v", err)
	}

	for _, temp := range []float64{5, 6, 7} {
		clock = clock.Add(1 * time.Hour)
		svc.SetClock(func() time.Time { return clock })
		if _, err := svc.RecordTemperature(b.ID, "CN-1", temp, domain.TempStageTransit); err != nil {
			t.Fatalf("RecordTemperature %.0f: %v", temp, err)
		}
	}

	clock = clock.Add(1 * time.Hour)
	svc.SetClock(func() time.Time { return clock })
	ef, err := svc.HandleEquipmentFailure(b.ID)
	if err != nil {
		t.Fatalf("HandleEquipmentFailure: %v", err)
	}
	return svc, b.ID, ef
}

func TestEquipmentFailureCurveKeepsTheFailedContainerID(t *testing.T) {
	_, _, ef := failureCurveFixture(t)

	if ef.ContainerID != "CN-1" {
		t.Fatalf("failure record container = %s, want CN-1", ef.ContainerID)
	}
	if len(ef.TempCurve) != 3 {
		t.Fatalf("claim curve length = %d, want 3", len(ef.TempCurve))
	}
	wantTemps := []float64{5, 6, 7}
	for i, r := range ef.TempCurve {
		if r.ContainerID != "CN-1" {
			t.Fatalf("claim curve entry %d container = %q, want CN-1 (the failed reefer)", i, r.ContainerID)
		}
		if r.TempC != wantTemps[i] {
			t.Fatalf("claim curve entry %d temp = %.1f, want %.1f", i, r.TempC, wantTemps[i])
		}
		if r.Stage != domain.TempStageTransit {
			t.Fatalf("claim curve entry %d stage = %s, want transit", i, r.Stage)
		}
	}
}

func TestEquipmentFailureLeavesFailedContainerHistoryIntact(t *testing.T) {
	svc, _, ef := failureCurveFixture(t)

	history := svc.GetStore().ListTempReadings("CN-1")
	if len(history) != 3 {
		t.Fatalf("CN-1 history length = %d, want 3", len(history))
	}
	wantTemps := []float64{5, 6, 7}
	for i, r := range history {
		if r.ContainerID != "CN-1" {
			t.Fatalf("CN-1 history entry %d container = %q, want CN-1", i, r.ContainerID)
		}
		if r.TempC != wantTemps[i] {
			t.Fatalf("CN-1 history entry %d temp = %.1f, want %.1f", i, r.TempC, wantTemps[i])
		}
	}

	carried := svc.GetStore().ListTempReadings(ef.BackupContainerID)
	if len(carried) != 3 {
		t.Fatalf("backup container history length = %d, want the 3 carried readings", len(carried))
	}
	for i, r := range carried {
		if r.ContainerID != ef.BackupContainerID {
			t.Fatalf("carried entry %d container = %q, want %q", i, r.ContainerID, ef.BackupContainerID)
		}
		if r.TempC != wantTemps[i] {
			t.Fatalf("carried entry %d temp = %.1f, want %.1f", i, r.TempC, wantTemps[i])
		}
	}
}

func TestEquipmentFailureCurveIsNotDisturbedByLaterReadings(t *testing.T) {
	svc, bookingID, ef := failureCurveFixture(t)

	b, err := svc.GetBooking(bookingID)
	if err != nil {
		t.Fatalf("GetBooking: %v", err)
	}
	if b.ContainerID != ef.BackupContainerID {
		t.Fatalf("booking container = %s, want backup %s", b.ContainerID, ef.BackupContainerID)
	}

	if _, err := svc.RecordTemperature(bookingID, ef.BackupContainerID, 8, domain.TempStageTransit); err != nil {
		t.Fatalf("RecordTemperature on backup container: %v", err)
	}

	history := svc.GetStore().ListTempReadings("CN-1")
	if len(history) != 3 {
		t.Fatalf("CN-1 history length = %d, want 3 after recording on the backup container", len(history))
	}
	for i, r := range history {
		if r.ContainerID != "CN-1" {
			t.Fatalf("CN-1 history entry %d container = %q, want CN-1", i, r.ContainerID)
		}
	}
	for i, r := range ef.TempCurve {
		if r.ContainerID != "CN-1" {
			t.Fatalf("claim curve entry %d container = %q, want CN-1", i, r.ContainerID)
		}
	}

	backup := svc.GetStore().ListTempReadings(ef.BackupContainerID)
	if len(backup) != 4 {
		t.Fatalf("backup container history length = %d, want 4 (3 carried + 1 new)", len(backup))
	}
	if backup[3].TempC != 8 {
		t.Fatalf("newest backup reading = %.1f, want 8", backup[3].TempC)
	}
}
