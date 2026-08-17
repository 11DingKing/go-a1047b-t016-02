package domain

import (
	"errors"
	"time"
)

// CargoType identifies the category of high-value new-energy goods.
type CargoType string

const (
	CargoTypePowerBattery  CargoType = "power_battery"
	CargoTypeEnergyStorage CargoType = "energy_storage"
	CargoTypePhotovoltaic  CargoType = "photovoltaic"
)

// LoadType distinguishes full-container-load from less-than-container-load.
type LoadType string

const (
	LoadTypeFCL LoadType = "FCL"
	LoadTypeLCL LoadType = "LCL"
)

// ErrBatteryMustBeFCL enforces the rule that power batteries may never be LCL.
var ErrBatteryMustBeFCL = errors.New("power battery cargo must be booked as full container load; LCL is not permitted")

// Cargo describes the physical goods carried under a booking.
type Cargo struct {
	Type        CargoType `json:"type"`
	Description string    `json:"description"`
	LoadType    LoadType  `json:"load_type"`
	WeightKg    float64   `json:"weight_kg"`
	DeclaredAt  time.Time `json:"declared_at"`
}

// IsDangerous reports whether the cargo requires dangerous-goods review.
func (c Cargo) IsDangerous() bool {
	return c.Type == CargoTypePowerBattery
}

// NeedsTemperatureControl reports whether the cargo requires reefer settings.
func (c Cargo) NeedsTemperatureControl() bool {
	return c.Type == CargoTypeEnergyStorage || c.Type == CargoTypePhotovoltaic
}

// Validate applies cargo-level business rules.
func (c Cargo) Validate() error {
	if c.IsDangerous() && c.LoadType != LoadTypeFCL {
		return ErrBatteryMustBeFCL
	}
	return nil
}
