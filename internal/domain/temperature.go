package domain

import "time"

// TempStage identifies the phase of the supply chain for a reading.
type TempStage string

const (
	TempStageLoading     TempStage = "loading"
	TempStageTransit     TempStage = "transit"
	TempStageDestination TempStage = "destination"
)

// TempConfig holds the agreed refrigeration parameters for a container.
type TempConfig struct {
	ContainerID  string    `json:"container_id"`
	BookingID    string    `json:"booking_id"`
	MinTempC     float64   `json:"min_temp_c"`
	MaxTempC     float64   `json:"max_temp_c"`
	SetTempC     float64   `json:"set_temp_c"`
	ConfiguredAt time.Time `json:"configured_at"`
}

// IsWithinRange reports whether a reading is inside the acceptable band.
func (tc TempConfig) IsWithinRange(tempC float64) bool {
	return tempC >= tc.MinTempC && tempC <= tc.MaxTempC
}

// TempReading is a single temperature measurement from a reefer container.
type TempReading struct {
	ID          string    `json:"id"`
	ContainerID string    `json:"container_id"`
	BookingID   string    `json:"booking_id"`
	TempC       float64   `json:"temp_c"`
	Stage       TempStage `json:"stage"`
	RecordedAt  time.Time `json:"recorded_at"`
}

// AnomalyStatus tracks the lifecycle of a temperature excursion.
type AnomalyStatus string

const (
	AnomalyStatusOpen     AnomalyStatus = "open"
	AnomalyStatusResolved AnomalyStatus = "resolved"
)

// Anomaly is a work order generated when a temperature excursion exceeds
// the 15-minute threshold, notifying freight forwarder, shipping company,
// and temperature-control service provider.
type Anomaly struct {
	ID              string        `json:"id"`
	ContainerID     string        `json:"container_id"`
	BookingID       string        `json:"booking_id"`
	StartedAt       time.Time     `json:"started_at"`
	DetectedAt      time.Time     `json:"detected_at"`
	Status          AnomalyStatus `json:"status"`
	Reason          string        `json:"reason"`
	NotifiedParties []string      `json:"notified_parties"`
}

// EquipmentFailure records a reefer malfunction and the recovery actions taken.
type EquipmentFailure struct {
	ID                string        `json:"id"`
	ContainerID       string        `json:"container_id"`
	BookingID         string        `json:"booking_id"`
	OccurredAt        time.Time     `json:"occurred_at"`
	BackupContainerID string        `json:"backup_container_id"`
	TempCurve         []TempReading `json:"temp_curve"`
	Resolved          bool          `json:"resolved"`
}
