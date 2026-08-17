package app

import (
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"arcticexpress/internal/domain"
	"arcticexpress/internal/store"
)

// IDGenerator produces monotonically increasing IDs with a prefix.
type IDGenerator struct {
	counter atomic.Int64
}

// Next returns a new unique ID.
func (g *IDGenerator) Next(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, g.counter.Add(1))
}

// Service orchestrates booking, space, temperature, and voyage operations.
type Service struct {
	store *store.Store
	idGen *IDGenerator
	now   func() time.Time
}

// NewService creates a Service backed by the given store.
func NewService(s *store.Store) *Service {
	return &Service{
		store: s,
		idGen: &IDGenerator{},
		now:   time.Now,
	}
}

// SetClock injects a custom clock (used in tests).
func (s *Service) SetClock(fn func() time.Time) {
	s.now = fn
}

// SubmitBookingRequest is the input for creating a booking.
type SubmitBookingRequest struct {
	ForwarderID string
	VoyageID    string
	CargoType   string
	Description string
	LoadType    string
	WeightKg    float64
	ContainerID string
	SpaceID     string
}

// SubmitBooking creates a booking, reserves the space, and allocates the
// container. If a concurrent booking with a later declaration held the same
// container, that loser is automatically rolled back.
func (s *Service) SubmitBooking(req SubmitBookingRequest) (*domain.Booking, error) {
	now := s.now()
	bookingID := s.idGen.Next("BK")

	cargo := domain.Cargo{
		Type:        domain.CargoType(req.CargoType),
		Description: req.Description,
		LoadType:    domain.LoadType(req.LoadType),
		WeightKg:    req.WeightKg,
		DeclaredAt:  now,
	}

	booking, err := domain.NewBooking(bookingID, req.ForwarderID, req.VoyageID, req.ContainerID, req.SpaceID, cargo, now)
	if err != nil {
		return nil, err
	}

	if err := s.store.ReserveSpace(req.SpaceID, bookingID, now); err != nil {
		return nil, fmt.Errorf("reserve space: %w", err)
	}

	loser, err := s.store.AllocateContainer(req.ContainerID, bookingID, now)
	if err != nil {
		_ = s.store.ReleaseSpace(req.SpaceID)
		return nil, fmt.Errorf("allocate container: %w", err)
	}
	if loser != "" {
		_ = s.store.RollbackBooking(loser, now)
	}

	s.store.SaveBooking(booking)
	return booking, nil
}

// ConfirmSpace locks the vessel slot for a booking (shipping company action).
func (s *Service) ConfirmSpace(bookingID string) error {
	return s.store.ConfirmBookingSpace(bookingID, s.now())
}

// ApproveDG completes dangerous-goods document review (shipping company action).
func (s *Service) ApproveDG(bookingID string) error {
	return s.store.MutateBooking(bookingID, func(b *domain.Booking) error {
		if b.DGApproved {
			return nil
		}
		return b.ApproveDG(s.now())
	})
}

// ConfigureTemp sets refrigeration parameters (temperature-control provider).
func (s *Service) ConfigureTemp(bookingID, containerID string, minC, maxC, setC float64) error {
	now := s.now()
	tc := &domain.TempConfig{
		ContainerID:  containerID,
		BookingID:    bookingID,
		MinTempC:     minC,
		MaxTempC:     maxC,
		SetTempC:     setC,
		ConfiguredAt: now,
	}
	if err := s.store.MutateBooking(bookingID, func(b *domain.Booking) error {
		if b.TempSet {
			return nil
		}
		return b.ConfigureTemp(now)
	}); err != nil {
		return err
	}
	s.store.SaveTempConfig(tc)
	return nil
}

// LoadCargo moves a booking from pending-loading to loaded.
func (s *Service) LoadCargo(bookingID string) error {
	return s.store.MutateBooking(bookingID, func(b *domain.Booking) error {
		return b.Load(s.now())
	})
}

// DepartVessel marks the cargo as in transit.
func (s *Service) DepartVessel(bookingID string) error {
	return s.store.MutateBooking(bookingID, func(b *domain.Booking) error {
		return b.Depart(s.now())
	})
}

// RecordTemperature stores a temperature reading and updates the anomaly
// streak. Returns true if the reading was out of range.
func (s *Service) RecordTemperature(bookingID, containerID string, tempC float64, stage domain.TempStage) (bool, error) {
	tc, err := s.store.GetTempConfig(bookingID)
	if err != nil {
		return false, err
	}
	r := domain.TempReading{
		ID:          s.idGen.Next("TR"),
		ContainerID: containerID,
		BookingID:   bookingID,
		TempC:       tempC,
		Stage:       stage,
		RecordedAt:  s.now(),
	}
	outOfRange := s.store.AddTempReading(r, tc)
	return outOfRange, nil
}

// ArriveAtDestination records arrival at the destination port.
func (s *Service) ArriveAtDestination(bookingID string) error {
	return s.store.MutateBooking(bookingID, func(b *domain.Booking) error {
		return b.Arrive(s.now())
	})
}

// ClearCustoms completes customs broker release.
func (s *Service) ClearCustoms(bookingID string) error {
	return s.store.MutateBooking(bookingID, func(b *domain.Booking) error {
		return b.ClearCustoms(s.now())
	})
}

// Deliver completes inland delivery to the consignee within the free period.
func (s *Service) Deliver(bookingID string) error {
	return s.store.MutateBooking(bookingID, func(b *domain.Booking) error {
		return b.Deliver(s.now())
	})
}

// RequestPortChange enforces the 24-hour pre-departure window.
func (s *Service) RequestPortChange(bookingID, newPort string) error {
	now := s.now()
	booking, err := s.store.GetBooking(bookingID)
	if err != nil {
		return err
	}
	voyage, err := s.store.GetVoyage(booking.VoyageID)
	if err != nil {
		return err
	}
	return s.store.MutateBooking(bookingID, func(b *domain.Booking) error {
		return b.RequestPortChange(newPort, voyage.DepartureAt, now)
	})
}

// HandleRollCargo rolls the booking to the next voyage, releases the old space,
// reserves a new one, and updates the arrival window for the consignee.
func (s *Service) HandleRollCargo(bookingID, newSpaceID string) error {
	now := s.now()

	booking, err := s.store.GetBooking(bookingID)
	if err != nil {
		return err
	}
	voyage, err := s.store.GetVoyage(booking.VoyageID)
	if err != nil {
		return err
	}
	if voyage.NextVoyageID == "" {
		return errors.New("no next voyage available for roll")
	}
	nextVoyage, err := s.store.GetVoyage(voyage.NextVoyageID)
	if err != nil {
		return err
	}

	oldSpaceID := booking.SpaceID

	if err := s.store.MutateBooking(bookingID, func(b *domain.Booking) error {
		return b.Roll(now)
	}); err != nil {
		return err
	}

	if err := s.store.ReserveSpace(newSpaceID, bookingID, now); err != nil {
		return fmt.Errorf("reserve new space: %w", err)
	}

	if err := s.store.MutateBooking(bookingID, func(b *domain.Booking) error {
		b.SpaceID = newSpaceID
		return b.RebookOnNextVoyage(voyage.NextVoyageID, nextVoyage.ArrivalAt, now)
	}); err != nil {
		_ = s.store.ReleaseSpace(newSpaceID)
		return err
	}

	_ = s.store.ReleaseSpace(oldSpaceID)
	return nil
}

// HandleEquipmentFailure reassigns a backup reefer container, backtraces the
// full temperature curve, and records the failure for claims.
func (s *Service) HandleEquipmentFailure(bookingID string) (*domain.EquipmentFailure, error) {
	now := s.now()

	booking, err := s.store.GetBooking(bookingID)
	if err != nil {
		return nil, err
	}

	backupSpace, err := s.store.FindAvailableReeferSpace(booking.VoyageID)
	if err != nil {
		return nil, fmt.Errorf("no backup reefer available: %w", err)
	}
	backupContainerID := backupSpace.ID

	// Hand the recorded curve over to the backup container so the reefer history
	// stays continuous, then snapshot the failed container's own curve for claims.
	s.store.CarryOverTempReadings(booking.ContainerID, backupContainerID)
	curve := s.store.ListTempReadings(booking.ContainerID)

	ef := &domain.EquipmentFailure{
		ID:                s.idGen.Next("EF"),
		ContainerID:       booking.ContainerID,
		BookingID:         bookingID,
		OccurredAt:        now,
		BackupContainerID: backupContainerID,
		TempCurve:         curve,
		Resolved:          true,
	}

	if err := s.store.MutateBooking(bookingID, func(b *domain.Booking) error {
		b.HandleEquipmentFailure(backupContainerID, now)
		b.ContainerID = backupContainerID
		return nil
	}); err != nil {
		return nil, err
	}

	s.store.SaveEquipmentFailure(ef)
	return ef, nil
}

// CreateAnomaly builds an open anomaly work order notifying the three parties:
// freight forwarder, shipping company, and temperature-control provider.
func (s *Service) CreateAnomaly(containerID, bookingID string, startedAt time.Time) *domain.Anomaly {
	now := s.now()
	return &domain.Anomaly{
		ID:              s.idGen.Next("AN"),
		ContainerID:     containerID,
		BookingID:       bookingID,
		StartedAt:       startedAt,
		DetectedAt:      now,
		Status:          domain.AnomalyStatusOpen,
		Reason:          "temperature excursion exceeded 15-minute threshold",
		NotifiedParties: []string{"freight_forwarder", "shipping_company", "temp_control_provider"},
	}
}

// SeedVoyage and SeedSpace are helpers for setting up test/demo data.
func (s *Service) SeedVoyage(v *domain.Voyage) {
	s.store.SaveVoyage(v)
}

func (s *Service) SeedSpace(sp *domain.Space) {
	s.store.SaveSpace(sp)
}

// GetBooking retrieves a booking by ID.
func (s *Service) GetBooking(bookingID string) (*domain.Booking, error) {
	return s.store.GetBooking(bookingID)
}

// GetStore exposes the underlying store (used by the worker).
func (s *Service) GetStore() *store.Store {
	return s.store
}
