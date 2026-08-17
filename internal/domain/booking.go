package domain

import (
	"errors"
	"time"
)

// BookingStatus enumerates the booking lifecycle states.
type BookingStatus string

const (
	BookingStatusSubmitted      BookingStatus = "submitted"
	BookingStatusPendingLoading BookingStatus = "pending_loading"
	BookingStatusLoaded         BookingStatus = "loaded"
	BookingStatusInTransit      BookingStatus = "in_transit"
	BookingStatusArrived        BookingStatus = "arrived"
	BookingStatusCustomsCleared BookingStatus = "customs_cleared"
	BookingStatusDelivered      BookingStatus = "delivered"
	BookingStatusRolled         BookingStatus = "rolled"
	BookingStatusCancelled      BookingStatus = "cancelled"
)

// PortChangeWindow is the minimum lead time for a port-change request.
const PortChangeWindow = 24 * time.Hour

var (
	ErrInvalidTransition  = errors.New("invalid booking state transition")
	ErrPortChangeTooLate  = errors.New("port change must be requested at least 24 hours before vessel departure")
	ErrFreeStorageExpired = errors.New("delivery exceeds the free storage period")
	ErrAlreadyRolled      = errors.New("booking has already been rolled")
)

// FreeStorageDuration is the default window the consignee has to collect goods
// after customs clearance before demurrage applies.
const FreeStorageDuration = 72 * time.Hour

// Booking is the aggregate root tying cargo, space, container, and voyage.
type Booking struct {
	ID          string        `json:"id"`
	ForwarderID string        `json:"forwarder_id"`
	VoyageID    string        `json:"voyage_id"`
	Cargo       Cargo         `json:"cargo"`
	ContainerID string        `json:"container_id"`
	SpaceID     string        `json:"space_id"`
	Status      BookingStatus `json:"status"`
	DeclaredAt  time.Time     `json:"declared_at"`

	SpaceConfirmed bool `json:"space_confirmed"`
	DGApproved     bool `json:"dg_approved"`
	TempSet        bool `json:"temp_set"`

	SpaceConfirmedAt *time.Time `json:"space_confirmed_at,omitempty"`
	DGReviewedAt     *time.Time `json:"dg_reviewed_at,omitempty"`
	TempConfiguredAt *time.Time `json:"temp_configured_at,omitempty"`

	LoadedAt         *time.Time `json:"loaded_at,omitempty"`
	ArrivedAt        *time.Time `json:"arrived_at,omitempty"`
	CustomsClearedAt *time.Time `json:"customs_cleared_at,omitempty"`
	DeliveredAt      *time.Time `json:"delivered_at,omitempty"`
	RolledAt         *time.Time `json:"rolled_at,omitempty"`

	FreeStorageUntil time.Time  `json:"free_storage_until"`
	NewArrivalPort   string     `json:"new_arrival_port,omitempty"`
	PortChangeAt     *time.Time `json:"port_change_at,omitempty"`

	BackupContainerID  string `json:"backup_container_id,omitempty"`
	TempCurveBacktrace bool   `json:"temp_curve_backtrace"`
}

// NewBooking constructs a booking after validating cargo rules.
func NewBooking(id, forwarderID, voyageID, containerID, spaceID string, cargo Cargo, now time.Time) (*Booking, error) {
	if err := cargo.Validate(); err != nil {
		return nil, err
	}
	return &Booking{
		ID:               id,
		ForwarderID:      forwarderID,
		VoyageID:         voyageID,
		Cargo:            cargo,
		ContainerID:      containerID,
		SpaceID:          spaceID,
		Status:           BookingStatusSubmitted,
		DeclaredAt:       cargo.DeclaredAt,
		FreeStorageUntil: now.Add(FreeStorageDuration),
	}, nil
}

// ConfirmSpace locks the vessel slot for the booking.
func (b *Booking) ConfirmSpace(now time.Time) error {
	if b.Status != BookingStatusSubmitted {
		return ErrInvalidTransition
	}
	b.SpaceConfirmed = true
	b.SpaceConfirmedAt = &now
	b.tryAdvanceToPendingLoading()
	return nil
}

// ApproveDG completes the dangerous-goods document review.
func (b *Booking) ApproveDG(now time.Time) error {
	if b.Status != BookingStatusSubmitted {
		return ErrInvalidTransition
	}
	b.DGApproved = true
	b.DGReviewedAt = &now
	b.tryAdvanceToPendingLoading()
	return nil
}

// ConfigureTemp sets refrigeration parameters for the container.
func (b *Booking) ConfigureTemp(now time.Time) error {
	if b.Status != BookingStatusSubmitted {
		return ErrInvalidTransition
	}
	b.TempSet = true
	b.TempConfiguredAt = &now
	b.tryAdvanceToPendingLoading()
	return nil
}

// tryAdvanceToPendingLoading transitions once all required checks pass.
// DG review is mandatory only for dangerous cargo; temp configuration only
// for cargo that needs temperature control.
func (b *Booking) tryAdvanceToPendingLoading() {
	spaceOK := b.SpaceConfirmed
	dgOK := !b.Cargo.IsDangerous() || b.DGApproved
	tempOK := !b.Cargo.NeedsTemperatureControl() || b.TempSet
	if spaceOK && dgOK && tempOK && b.Status == BookingStatusSubmitted {
		b.Status = BookingStatusPendingLoading
	}
}

// Load moves the booking from pending-loading to loaded at the port of loading.
func (b *Booking) Load(now time.Time) error {
	if b.Status != BookingStatusPendingLoading {
		return ErrInvalidTransition
	}
	b.Status = BookingStatusLoaded
	b.LoadedAt = &now
	return nil
}

// Depart marks the vessel as sailed with the cargo aboard.
func (b *Booking) Depart(now time.Time) error {
	if b.Status != BookingStatusLoaded {
		return ErrInvalidTransition
	}
	b.Status = BookingStatusInTransit
	return nil
}

// Arrive records arrival at the destination port.
func (b *Booking) Arrive(now time.Time) error {
	if b.Status != BookingStatusInTransit {
		return ErrInvalidTransition
	}
	b.Status = BookingStatusArrived
	b.ArrivedAt = &now
	return nil
}

// ClearCustoms completes customs broker release and resets the free-storage
// window to begin from the clearance moment.
func (b *Booking) ClearCustoms(now time.Time) error {
	if b.Status != BookingStatusArrived {
		return ErrInvalidTransition
	}
	b.Status = BookingStatusCustomsCleared
	b.CustomsClearedAt = &now
	b.FreeStorageUntil = now.Add(FreeStorageDuration)
	return nil
}

// Deliver completes inland delivery to the consignee within the free period.
func (b *Booking) Deliver(now time.Time) error {
	if b.Status != BookingStatusCustomsCleared {
		return ErrInvalidTransition
	}
	if now.After(b.FreeStorageUntil) {
		return ErrFreeStorageExpired
	}
	b.Status = BookingStatusDelivered
	b.DeliveredAt = &now
	return nil
}

// Roll marks the booking as rolled (bumped) from the current voyage.
func (b *Booking) Roll(now time.Time) error {
	if b.Status != BookingStatusPendingLoading && b.Status != BookingStatusLoaded {
		return ErrInvalidTransition
	}
	b.Status = BookingStatusRolled
	b.RolledAt = &now
	return nil
}

// RebookOnNextVoyage assigns the rolled booking to the next voyage and returns
// it to pending-loading, updating the free-storage window from the new ETA.
func (b *Booking) RebookOnNextVoyage(nextVoyageID string, newArrivalAt time.Time, now time.Time) error {
	if b.Status != BookingStatusRolled {
		return ErrInvalidTransition
	}
	b.VoyageID = nextVoyageID
	b.Status = BookingStatusPendingLoading
	b.FreeStorageUntil = newArrivalAt.Add(FreeStorageDuration)
	return nil
}

// RequestPortChange records a port-change request, enforcing the 24-hour rule.
func (b *Booking) RequestPortChange(newPort string, departureAt time.Time, now time.Time) error {
	if b.Status != BookingStatusSubmitted && b.Status != BookingStatusPendingLoading {
		return ErrInvalidTransition
	}
	if !now.Before(departureAt.Add(-PortChangeWindow)) {
		return ErrPortChangeTooLate
	}
	b.NewArrivalPort = newPort
	b.PortChangeAt = &now
	return nil
}

// HandleEquipmentFailure records the backup container and flags a full
// temperature-curve backtrace for claims.
func (b *Booking) HandleEquipmentFailure(backupContainerID string, now time.Time) {
	b.BackupContainerID = backupContainerID
	b.TempCurveBacktrace = true
}

// Cancel aborts the booking from any non-terminal state.
func (b *Booking) Cancel(now time.Time) error {
	if b.Status == BookingStatusDelivered || b.Status == BookingStatusCancelled {
		return ErrInvalidTransition
	}
	b.Status = BookingStatusCancelled
	return nil
}
