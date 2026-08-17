package domain

import (
	"testing"
	"time"
)

func TestBooking_StateTransitions_FullFlow(t *testing.T) {
	now := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	cargo := Cargo{
		Type:       CargoTypeEnergyStorage,
		LoadType:   LoadTypeFCL,
		DeclaredAt: now,
	}
	b, err := NewBooking("BK-1", "FF-1", "VG-1", "CN-1", "SP-1", cargo, now)
	if err != nil {
		t.Fatalf("NewBooking: %v", err)
	}

	if b.Status != BookingStatusSubmitted {
		t.Fatalf("initial status = %s, want submitted", b.Status)
	}

	// Confirm space alone should not advance (temp and DG not done).
	if err := b.ConfirmSpace(now); err != nil {
		t.Fatalf("ConfirmSpace: %v", err)
	}
	if b.Status != BookingStatusSubmitted {
		t.Fatalf("after space confirm status = %s, want submitted", b.Status)
	}

	// Energy storage needs temp control, not DG. Configure temp to advance.
	if err := b.ConfigureTemp(now); err != nil {
		t.Fatalf("ConfigureTemp: %v", err)
	}
	if b.Status != BookingStatusPendingLoading {
		t.Fatalf("after temp config status = %s, want pending_loading", b.Status)
	}

	// Continue through the lifecycle.
	for _, step := range []struct {
		name string
		fn   func(time.Time) error
		want BookingStatus
	}{
		{"load", b.Load, BookingStatusLoaded},
		{"depart", b.Depart, BookingStatusInTransit},
		{"arrive", b.Arrive, BookingStatusArrived},
		{"customs", b.ClearCustoms, BookingStatusCustomsCleared},
	} {
		if err := step.fn(now); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		if b.Status != step.want {
			t.Fatalf("after %s status = %s, want %s", step.name, b.Status, step.want)
		}
	}

	// Deliver should succeed within free storage.
	if err := b.Deliver(now); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if b.Status != BookingStatusDelivered {
		t.Fatalf("after deliver status = %s, want delivered", b.Status)
	}

	// Cannot transition from a terminal state.
	if err := b.Load(now); err == nil {
		t.Fatal("Load from delivered should fail")
	}
}

func TestBooking_BatteryFCLValidation(t *testing.T) {
	now := time.Now()

	// LCL battery must be rejected.
	lcl := Cargo{Type: CargoTypePowerBattery, LoadType: LoadTypeLCL, DeclaredAt: now}
	if _, err := NewBooking("BK-1", "FF-1", "VG-1", "CN-1", "SP-1", lcl, now); err != ErrBatteryMustBeFCL {
		t.Fatalf("LCL battery should be rejected, got %v", err)
	}

	// FCL battery is accepted.
	fcl := Cargo{Type: CargoTypePowerBattery, LoadType: LoadTypeFCL, DeclaredAt: now}
	b, err := NewBooking("BK-2", "FF-1", "VG-1", "CN-1", "SP-1", fcl, now)
	if err != nil {
		t.Fatalf("FCL battery should be accepted: %v", err)
	}

	// Battery needs DG review but not temp control.
	if err := b.ConfirmSpace(now); err != nil {
		t.Fatalf("ConfirmSpace: %v", err)
	}
	if b.Status != BookingStatusSubmitted {
		t.Fatalf("space alone should not advance battery: %s", b.Status)
	}
	if err := b.ApproveDG(now); err != nil {
		t.Fatalf("ApproveDG: %v", err)
	}
	if b.Status != BookingStatusPendingLoading {
		t.Fatalf("after space+DG status = %s, want pending_loading", b.Status)
	}
}

func TestBooking_PortChangeWindow(t *testing.T) {
	departure := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	cargo := Cargo{Type: CargoTypePhotovoltaic, LoadType: LoadTypeFCL, DeclaredAt: time.Now()}
	b, err := NewBooking("BK-1", "FF-1", "VG-1", "CN-1", "SP-1", cargo, time.Now())
	if err != nil {
		t.Fatalf("NewBooking: %v", err)
	}

	// 25 hours before departure → allowed.
	if err := b.RequestPortChange("Hamburg", departure, departure.Add(-25*time.Hour)); err != nil {
		t.Fatalf("port change 25h before should succeed: %v", err)
	}
	if b.NewArrivalPort != "Hamburg" {
		t.Fatalf("new port = %s, want Hamburg", b.NewArrivalPort)
	}

	// Exactly 24 hours before → too late (must be strictly before the 24h cutoff).
	if err := b.RequestPortChange("Hamburg", departure, departure.Add(-24*time.Hour)); err != ErrPortChangeTooLate {
		t.Fatalf("port change at 24h cutoff should fail, got %v", err)
	}

	// 10 hours before → too late.
	if err := b.RequestPortChange("Hamburg", departure, departure.Add(-10*time.Hour)); err != ErrPortChangeTooLate {
		t.Fatalf("port change 10h before should fail, got %v", err)
	}
}

func TestBooking_CargoClassification(t *testing.T) {
	tests := []struct {
		cargoType CargoType
		dangerous bool
		reefer    bool
	}{
		{CargoTypePowerBattery, true, false},
		{CargoTypeEnergyStorage, false, true},
		{CargoTypePhotovoltaic, false, true},
	}
	for _, tc := range tests {
		c := Cargo{Type: tc.cargoType, LoadType: LoadTypeFCL}
		if c.IsDangerous() != tc.dangerous {
			t.Errorf("%s: IsDangerous = %v, want %v", tc.cargoType, c.IsDangerous(), tc.dangerous)
		}
		if c.NeedsTemperatureControl() != tc.reefer {
			t.Errorf("%s: NeedsTemperatureControl = %v, want %v", tc.cargoType, c.NeedsTemperatureControl(), tc.reefer)
		}
	}
}

func TestBooking_DeliverAfterFreeStorage(t *testing.T) {
	now := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	cargo := Cargo{Type: CargoTypePhotovoltaic, LoadType: LoadTypeFCL, DeclaredAt: now}
	b, err := NewBooking("BK-1", "FF-1", "VG-1", "CN-1", "SP-1", cargo, now)
	if err != nil {
		t.Fatalf("NewBooking: %v", err)
	}
	// Advance to customs cleared.
	b.ConfirmSpace(now)
	b.ConfigureTemp(now)
	b.Load(now)
	b.Depart(now)
	b.Arrive(now)
	b.ClearCustoms(now)

	// Deliver within free storage → OK.
	if err := b.Deliver(now.Add(48 * time.Hour)); err != nil {
		t.Fatalf("deliver within free storage should succeed: %v", err)
	}
}

func TestBooking_RollAndRebook(t *testing.T) {
	now := time.Now()
	cargo := Cargo{Type: CargoTypePhotovoltaic, LoadType: LoadTypeFCL, DeclaredAt: now}
	b, err := NewBooking("BK-1", "FF-1", "VG-1", "CN-1", "SP-1", cargo, now)
	if err != nil {
		t.Fatalf("NewBooking: %v", err)
	}
	b.ConfirmSpace(now)
	b.ConfigureTemp(now) // pending_loading

	if err := b.Roll(now); err != nil {
		t.Fatalf("Roll: %v", err)
	}
	if b.Status != BookingStatusRolled {
		t.Fatalf("status = %s, want rolled", b.Status)
	}

	newArrival := now.Add(14 * 24 * time.Hour)
	if err := b.RebookOnNextVoyage("VG-2", newArrival, now); err != nil {
		t.Fatalf("RebookOnNextVoyage: %v", err)
	}
	if b.Status != BookingStatusPendingLoading {
		t.Fatalf("after rebook status = %s, want pending_loading", b.Status)
	}
	if b.VoyageID != "VG-2" {
		t.Fatalf("voyage = %s, want VG-2", b.VoyageID)
	}
	want := newArrival.Add(FreeStorageDuration)
	if !b.FreeStorageUntil.Equal(want) {
		t.Fatalf("free storage = %v, want %v", b.FreeStorageUntil, want)
	}
}
