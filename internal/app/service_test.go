package app

import (
	"errors"
	"sync"
	"testing"
	"time"

	"arcticexpress/internal/domain"
	"arcticexpress/internal/store"
)

// setupService creates a service pre-seeded with two voyages and four spaces.
func setupService(t *testing.T) (*Service, time.Time) {
	t.Helper()
	st := store.New()
	svc := NewService(st)
	base := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return base })

	voyage1 := &domain.Voyage{
		ID: "VG-001", VesselID: "VS-1", VesselName: "Arctic Express",
		DeparturePort: "Shanghai", ArrivalPort: "Rotterdam",
		DepartureAt:  base.Add(72 * time.Hour),
		ArrivalAt:    base.Add(35 * 24 * time.Hour),
		NextVoyageID: "VG-002",
	}
	voyage2 := &domain.Voyage{
		ID: "VG-002", VesselID: "VS-1", VesselName: "Arctic Express",
		DeparturePort: "Shanghai", ArrivalPort: "Rotterdam",
		DepartureAt: base.Add(7 * 24 * time.Hour),
		ArrivalAt:   base.Add(42 * 24 * time.Hour),
	}
	svc.SeedVoyage(voyage1)
	svc.SeedVoyage(voyage2)

	spaces := []*domain.Space{
		{ID: "SP-001", VesselID: "VS-1", VoyageID: "VG-001", ContainerType: "40HC", Reefer: true, Status: domain.SpaceStatusAvailable},
		{ID: "SP-002", VesselID: "VS-1", VoyageID: "VG-001", ContainerType: "40HC", Reefer: true, Status: domain.SpaceStatusAvailable},
		{ID: "SP-003", VesselID: "VS-1", VoyageID: "VG-001", ContainerType: "40GP", Reefer: false, Status: domain.SpaceStatusAvailable},
		{ID: "SP-004", VesselID: "VS-1", VoyageID: "VG-002", ContainerType: "40HC", Reefer: true, Status: domain.SpaceStatusAvailable},
	}
	for _, sp := range spaces {
		svc.SeedSpace(sp)
	}

	return svc, base
}

func TestService_FullFlowToDelivered(t *testing.T) {
	svc, base := setupService(t)
	clock := base

	b, err := svc.SubmitBooking(SubmitBookingRequest{
		ForwarderID: "FF-1", VoyageID: "VG-001",
		CargoType: "energy_storage", Description: "BESS 5MWh",
		LoadType: "FCL", WeightKg: 28000,
		ContainerID: "CN-1", SpaceID: "SP-001",
	})
	if err != nil {
		t.Fatalf("SubmitBooking: %v", err)
	}
	if b.Status != domain.BookingStatusSubmitted {
		t.Fatalf("status = %s, want submitted", b.Status)
	}

	// Confirm space (shipping company).
	if err := svc.ConfirmSpace(b.ID); err != nil {
		t.Fatalf("ConfirmSpace: %v", err)
	}

	// Configure temp (temp-control provider).
	if err := svc.ConfigureTemp(b.ID, "CN-1", -10, 25, 5); err != nil {
		t.Fatalf("ConfigureTemp: %v", err)
	}

	b, _ = svc.GetBooking(b.ID)
	if b.Status != domain.BookingStatusPendingLoading {
		t.Fatalf("status = %s, want pending_loading", b.Status)
	}

	// Record temperatures at each stage.
	clock = clock.Add(2 * time.Hour)
	svc.SetClock(func() time.Time { return clock })
	if _, err := svc.RecordTemperature(b.ID, "CN-1", 5.0, domain.TempStageLoading); err != nil {
		t.Fatalf("RecordTemp loading: %v", err)
	}
	if err := svc.LoadCargo(b.ID); err != nil {
		t.Fatalf("LoadCargo: %v", err)
	}

	clock = clock.Add(24 * time.Hour)
	svc.SetClock(func() time.Time { return clock })
	if err := svc.DepartVessel(b.ID); err != nil {
		t.Fatalf("DepartVessel: %v", err)
	}
	if _, err := svc.RecordTemperature(b.ID, "CN-1", 6.0, domain.TempStageTransit); err != nil {
		t.Fatalf("RecordTemp transit: %v", err)
	}

	clock = clock.Add(20 * 24 * time.Hour)
	svc.SetClock(func() time.Time { return clock })
	if err := svc.ArriveAtDestination(b.ID); err != nil {
		t.Fatalf("Arrive: %v", err)
	}
	if _, err := svc.RecordTemperature(b.ID, "CN-1", 7.0, domain.TempStageDestination); err != nil {
		t.Fatalf("RecordTemp destination: %v", err)
	}

	clock = clock.Add(48 * time.Hour)
	svc.SetClock(func() time.Time { return clock })
	if err := svc.ClearCustoms(b.ID); err != nil {
		t.Fatalf("ClearCustoms: %v", err)
	}
	if err := svc.Deliver(b.ID); err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	b, _ = svc.GetBooking(b.ID)
	if b.Status != domain.BookingStatusDelivered {
		t.Fatalf("status = %s, want delivered", b.Status)
	}

	curve := svc.GetStore().ListTempReadings("CN-1")
	if len(curve) != 3 {
		t.Fatalf("temperature curve length = %d, want 3", len(curve))
	}
}

func TestService_BatteryFCLRejected(t *testing.T) {
	svc, _ := setupService(t)

	_, err := svc.SubmitBooking(SubmitBookingRequest{
		ForwarderID: "FF-1", VoyageID: "VG-001",
		CargoType: "power_battery", Description: "Li-ion battery pack",
		LoadType: "LCL", WeightKg: 5000,
		ContainerID: "CN-2", SpaceID: "SP-002",
	})
	if !errors.Is(err, domain.ErrBatteryMustBeFCL) {
		t.Fatalf("expected ErrBatteryMustBeFCL, got %v", err)
	}

	// Space should be available again (rolled back).
	sp, _ := svc.GetStore().GetSpace("SP-002")
	if sp.Status != domain.SpaceStatusAvailable {
		t.Fatalf("space status = %s, want available after rejection", sp.Status)
	}
}

func TestService_EquipmentFailureRecovery(t *testing.T) {
	svc, base := setupService(t)
	clock := base

	b, err := svc.SubmitBooking(SubmitBookingRequest{
		ForwarderID: "FF-1", VoyageID: "VG-001",
		CargoType: "energy_storage", Description: "BESS cabinet",
		LoadType: "FCL", WeightKg: 28000,
		ContainerID: "CN-1", SpaceID: "SP-001",
	})
	if err != nil {
		t.Fatalf("SubmitBooking: %v", err)
	}
	svc.ConfirmSpace(b.ID)
	svc.ConfigureTemp(b.ID, "CN-1", -10, 25, 5)
	svc.LoadCargo(b.ID)

	// Record readings before failure.
	clock = clock.Add(1 * time.Hour)
	svc.SetClock(func() time.Time { return clock })
	svc.RecordTemperature(b.ID, "CN-1", 5.0, domain.TempStageTransit)
	clock = clock.Add(2 * time.Hour)
	svc.SetClock(func() time.Time { return clock })
	svc.RecordTemperature(b.ID, "CN-1", 6.0, domain.TempStageTransit)

	// Trigger equipment failure.
	ef, err := svc.HandleEquipmentFailure(b.ID)
	if err != nil {
		t.Fatalf("HandleEquipmentFailure: %v", err)
	}
	if ef.BackupContainerID == "" {
		t.Fatal("backup container not assigned")
	}
	if len(ef.TempCurve) != 2 {
		t.Fatalf("temp curve length = %d, want 2", len(ef.TempCurve))
	}

	b, _ = svc.GetBooking(b.ID)
	if b.BackupContainerID != ef.BackupContainerID {
		t.Fatalf("booking backup = %s, ef backup = %s", b.BackupContainerID, ef.BackupContainerID)
	}
	if !b.TempCurveBacktrace {
		t.Fatal("temp curve backtrace flag not set")
	}
	if b.ContainerID != ef.BackupContainerID {
		t.Fatalf("booking container = %s, want backup %s", b.ContainerID, ef.BackupContainerID)
	}
}

func TestService_RollCargoToNextVoyage(t *testing.T) {
	svc, base := setupService(t)
	clock := base

	b, err := svc.SubmitBooking(SubmitBookingRequest{
		ForwarderID: "FF-1", VoyageID: "VG-001",
		CargoType: "photovoltaic", Description: "PV modules",
		LoadType: "FCL", WeightKg: 20000,
		ContainerID: "CN-1", SpaceID: "SP-001",
	})
	if err != nil {
		t.Fatalf("SubmitBooking: %v", err)
	}
	svc.ConfirmSpace(b.ID)
	svc.ConfigureTemp(b.ID, "CN-1", -10, 30, 15)

	clock = clock.Add(1 * time.Hour)
	svc.SetClock(func() time.Time { return clock })

	// Roll cargo to next voyage with a new space.
	if err := svc.HandleRollCargo(b.ID, "SP-004"); err != nil {
		t.Fatalf("HandleRollCargo: %v", err)
	}

	b, _ = svc.GetBooking(b.ID)
	if b.VoyageID != "VG-002" {
		t.Fatalf("voyage = %s, want VG-002", b.VoyageID)
	}
	if b.SpaceID != "SP-004" {
		t.Fatalf("space = %s, want SP-004", b.SpaceID)
	}
	if b.Status != domain.BookingStatusPendingLoading {
		t.Fatalf("status = %s, want pending_loading", b.Status)
	}

	// Old space should be available.
	oldSp, _ := svc.GetStore().GetSpace("SP-001")
	if oldSp.Status != domain.SpaceStatusAvailable {
		t.Fatalf("old space status = %s, want available", oldSp.Status)
	}
	// New space should be reserved.
	newSp, _ := svc.GetStore().GetSpace("SP-004")
	if newSp.Status != domain.SpaceStatusReserved {
		t.Fatalf("new space status = %s, want reserved", newSp.Status)
	}

	// Free storage window should be based on VG-002 arrival.
	v2, _ := svc.GetStore().GetVoyage("VG-002")
	wantFree := v2.ArrivalAt.Add(domain.FreeStorageDuration)
	if !b.FreeStorageUntil.Equal(wantFree) {
		t.Fatalf("free storage = %v, want %v", b.FreeStorageUntil, wantFree)
	}
}

func TestService_ConcurrentContainerConflict(t *testing.T) {
	svc, base := setupService(t)

	// Submit booking A with container CN-SHARED at T1.
	mockNow := base
	svc.SetClock(func() time.Time { return mockNow })
	bkA, err := svc.SubmitBooking(SubmitBookingRequest{
		ForwarderID: "FF-1", VoyageID: "VG-001",
		CargoType: "energy_storage", Description: "BESS A",
		LoadType: "FCL", WeightKg: 28000,
		ContainerID: "CN-SHARED", SpaceID: "SP-001",
	})
	if err != nil {
		t.Fatalf("SubmitBooking A: %v", err)
	}

	// Submit booking B with same container at T2 > T1 → should fail.
	mockNow = base.Add(5 * time.Minute)
	svc.SetClock(func() time.Time { return mockNow })
	_, err = svc.SubmitBooking(SubmitBookingRequest{
		ForwarderID: "FF-2", VoyageID: "VG-001",
		CargoType: "energy_storage", Description: "BESS B",
		LoadType: "FCL", WeightKg: 26000,
		ContainerID: "CN-SHARED", SpaceID: "SP-002",
	})
	if err == nil {
		t.Fatal("booking B should fail (container occupied by earlier booking)")
	}

	// Booking A should still be active.
	bkA2, _ := svc.GetBooking(bkA.ID)
	if bkA2.Status == domain.BookingStatusCancelled {
		t.Fatal("booking A should not be cancelled")
	}

	// Submit booking C with same container at T0 < T1 → should win, A rolled back.
	mockNow = base.Add(-10 * time.Minute)
	svc.SetClock(func() time.Time { return mockNow })
	bkC, err := svc.SubmitBooking(SubmitBookingRequest{
		ForwarderID: "FF-3", VoyageID: "VG-001",
		CargoType: "energy_storage", Description: "BESS C",
		LoadType: "FCL", WeightKg: 27000,
		ContainerID: "CN-SHARED", SpaceID: "SP-003",
	})
	if err != nil {
		t.Fatalf("SubmitBooking C: %v", err)
	}

	// Booking A should be cancelled (rolled back).
	bkA3, _ := svc.GetBooking(bkA.ID)
	if bkA3.Status != domain.BookingStatusCancelled {
		t.Fatalf("booking A status = %s, want cancelled", bkA3.Status)
	}

	// Booking C should be active.
	bkC2, _ := svc.GetBooking(bkC.ID)
	if bkC2.Status == domain.BookingStatusCancelled {
		t.Fatal("booking C should be active")
	}

	// Booking A's space should be available again.
	sp1, _ := svc.GetStore().GetSpace("SP-001")
	if sp1.Status != domain.SpaceStatusAvailable {
		t.Fatalf("SP-001 status = %s, want available", sp1.Status)
	}
}

func TestService_ConcurrentSubmitGoroutines(t *testing.T) {
	svc, _ := setupService(t)
	svc.SetClock(time.Now)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	results := make([]*domain.Booking, 2)

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			spaceID := "SP-001"
			if i == 1 {
				spaceID = "SP-002"
			}
			b, err := svc.SubmitBooking(SubmitBookingRequest{
				ForwarderID: "FF-1", VoyageID: "VG-001",
				CargoType: "energy_storage", Description: "BESS",
				LoadType: "FCL", WeightKg: 28000,
				ContainerID: "CN-RACE", SpaceID: spaceID,
			})
			results[i] = b
			errs[i] = err
		}(i)
	}
	wg.Wait()

	success := 0
	for _, e := range errs {
		if e == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("expected exactly 1 successful booking, got %d", success)
	}
}
