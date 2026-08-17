package domain

import "time"

// SpaceStatus represents the lifecycle of a vessel slot.
type SpaceStatus string

const (
	SpaceStatusAvailable SpaceStatus = "available"
	SpaceStatusReserved  SpaceStatus = "reserved"  // released to a booking, pending confirmation
	SpaceStatusConfirmed SpaceStatus = "confirmed" // confirmed by the booking process
)

// Space is a single slot on a vessel voyage.
type Space struct {
	ID            string      `json:"id"`
	VesselID      string      `json:"vessel_id"`
	VoyageID      string      `json:"voyage_id"`
	ContainerType string      `json:"container_type"`
	Reefer        bool        `json:"reefer"`
	Status        SpaceStatus `json:"status"`
	ReservedAt    *time.Time  `json:"reserved_at,omitempty"`
	ReservedBy    string      `json:"reserved_by,omitempty"`
	ConfirmedBy   string      `json:"confirmed_by,omitempty"`
}

// Voyage links a vessel to a scheduled departure and arrival.
type Voyage struct {
	ID            string    `json:"id"`
	VesselID      string    `json:"vessel_id"`
	VesselName    string    `json:"vessel_name"`
	DeparturePort string    `json:"departure_port"`
	ArrivalPort   string    `json:"arrival_port"`
	DepartureAt   time.Time `json:"departure_at"`
	ArrivalAt     time.Time `json:"arrival_at"`
	Departed      bool      `json:"departed"`
	NextVoyageID  string    `json:"next_voyage_id,omitempty"`
}

// CanAcceptPortChange reports whether a port-change request is within the
// 24-hour pre-departure window required by business rules.
func (v Voyage) CanAcceptPortChange(now time.Time) bool {
	return now.Before(v.DepartureAt.Add(-PortChangeWindow))
}
