package store

import (
	"sync"
	"testing"
	"time"

	"arcticexpress/internal/domain"
)

func TestStore_BookingAndSpaceCRUD(t *testing.T) {
	s := New()
	now := time.Now()

	sp := &domain.Space{
		ID: "SP-1", VesselID: "VS-1", VoyageID: "VG-1", Reefer: true,
		Status: domain.SpaceStatusAvailable,
	}
	s.SaveSpace(sp)

	// Reserve.
	if err := s.ReserveSpace("SP-1", "BK-1", now); err != nil {
		t.Fatalf("ReserveSpace: %v", err)
	}
	got, _ := s.GetSpace("SP-1")
	if got.Status != domain.SpaceStatusReserved || got.ReservedBy != "BK-1" {
		t.Fatalf("space state = %+v", got)
	}

	// Confirm.
	if err := s.ConfirmSpace("SP-1", "BK-1"); err != nil {
		t.Fatalf("ConfirmSpace: %v", err)
	}
	got, _ = s.GetSpace("SP-1")
	if got.Status != domain.SpaceStatusConfirmed {
		t.Fatalf("status = %s, want confirmed", got.Status)
	}

	// Booking CRUD.
	b := &domain.Booking{
		ID: "BK-1", VoyageID: "VG-1", SpaceID: "SP-1",
		Status: domain.BookingStatusSubmitted,
	}
	s.SaveBooking(b)
	gotB, err := s.GetBooking("BK-1")
	if err != nil {
		t.Fatalf("GetBooking: %v", err)
	}
	if gotB.ID != "BK-1" {
		t.Fatalf("booking id = %s", gotB.ID)
	}

	// MutateBooking.
	if err := s.MutateBooking("BK-1", func(b *domain.Booking) error {
		b.Status = domain.BookingStatusPendingLoading
		return nil
	}); err != nil {
		t.Fatalf("MutateBooking: %v", err)
	}
	gotB, _ = s.GetBooking("BK-1")
	if gotB.Status != domain.BookingStatusPendingLoading {
		t.Fatalf("status = %s", gotB.Status)
	}

	// Release.
	if err := s.ReleaseSpace("SP-1"); err != nil {
		t.Fatalf("ReleaseSpace: %v", err)
	}
	got, _ = s.GetSpace("SP-1")
	if got.Status != domain.SpaceStatusAvailable {
		t.Fatalf("status = %s, want available", got.Status)
	}

	// Not found.
	if _, err := s.GetBooking("nope"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestStore_ConcurrentSpaceReservation(t *testing.T) {
	s := New()
	s.SaveSpace(&domain.Space{
		ID: "SP-RACE", VesselID: "VS-1", VoyageID: "VG-1", Reefer: true,
		Status: domain.SpaceStatusAvailable,
	})

	var wg sync.WaitGroup
	successes := make(chan bool, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := s.ReserveSpace("SP-RACE", "BK-"+itoa(i), time.Now())
			successes <- (err == nil)
		}(i)
	}
	wg.Wait()
	close(successes)

	count := 0
	for ok := range successes {
		if ok {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 successful reservation, got %d", count)
	}
}

func TestStore_ContainerAllocationAdjudication(t *testing.T) {
	s := New()
	t0 := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	t1 := t0.Add(5 * time.Minute)
	t2 := t0.Add(10 * time.Minute)

	// First allocation at t1 → succeeds.
	loser, err := s.AllocateContainer("CN-1", "BK-A", t1)
	if err != nil {
		t.Fatalf("first allocation: %v", err)
	}
	if loser != "" {
		t.Fatalf("unexpected loser %s", loser)
	}

	// Later booking at t2 → rejected (container occupied by earlier).
	_, err = s.AllocateContainer("CN-1", "BK-B", t2)
	if err != ErrContainerOccupied {
		t.Fatalf("expected ErrContainerOccupied, got %v", err)
	}

	// Earlier booking at t0 → wins, BK-A is the loser.
	loser, err = s.AllocateContainer("CN-1", "BK-C", t0)
	if err != nil {
		t.Fatalf("earlier allocation: %v", err)
	}
	if loser != "BK-A" {
		t.Fatalf("loser = %s, want BK-A", loser)
	}

	// Idempotent: same booking re-allocates without error.
	_, err = s.AllocateContainer("CN-1", "BK-C", t0)
	if err != nil {
		t.Fatalf("idempotent allocation: %v", err)
	}

	// Release and reallocate.
	s.ReleaseContainer("CN-1", "BK-C")
	_, err = s.AllocateContainer("CN-1", "BK-D", t0)
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
}

func TestStore_FindExpiredReservations(t *testing.T) {
	s := New()
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

	// Reserved 3 hours ago (expired).
	s.SaveSpace(&domain.Space{
		ID: "SP-OLD", VoyageID: "VG-1", Reefer: true,
		Status: domain.SpaceStatusReserved, ReservedAt: ptrTime(now.Add(-3 * time.Hour)), ReservedBy: "BK-X",
	})
	// Reserved 30 minutes ago (not expired).
	s.SaveSpace(&domain.Space{
		ID: "SP-NEW", VoyageID: "VG-1", Reefer: true,
		Status: domain.SpaceStatusReserved, ReservedAt: ptrTime(now.Add(-30 * time.Minute)), ReservedBy: "BK-Y",
	})
	// Confirmed (not expired even if old).
	s.SaveSpace(&domain.Space{
		ID: "SP-CONF", VoyageID: "VG-1", Reefer: true,
		Status: domain.SpaceStatusConfirmed, ReservedAt: ptrTime(now.Add(-3 * time.Hour)), ConfirmedBy: "BK-Z",
	})

	expired := s.FindExpiredReservations(2*time.Hour, now)
	if len(expired) != 1 || expired[0] != "SP-OLD" {
		t.Fatalf("expired = %v, want [SP-OLD]", expired)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf []byte
	for i > 0 {
		buf = append([]byte{byte('0' + i%10)}, buf...)
		i /= 10
	}
	return string(buf)
}

func ptrTime(t time.Time) *time.Time {
	return &t
}
