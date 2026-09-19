package model

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type OccupancyState string

const (
	OccupancyUnknown  OccupancyState = "unknown"
	OccupancyFree     OccupancyState = "free"
	OccupancyOccupied OccupancyState = "occupied"
)

func (s OccupancyState) Valid() bool {
	switch s {
	case OccupancyUnknown, OccupancyFree, OccupancyOccupied:
		return true
	default:
		return false
	}
}

type OccupantRef struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type BlockOccupancy struct {
	BlockID   string         `json:"blockId"`
	State     OccupancyState `json:"state"`
	Occupant  *OccupantRef   `json:"occupant,omitempty"`
	UpdatedAt time.Time      `json:"updatedAt"`
}

func NewUnknownBlockOccupancy(blockID string, now time.Time) BlockOccupancy {
	return BlockOccupancy{BlockID: blockID, State: OccupancyUnknown, UpdatedAt: now}
}

// LegacyOccupied derives the legacy boolean without treating unknown as free
// in occupancy-domain decisions.
func (o BlockOccupancy) LegacyOccupied() bool {
	return o.State == OccupancyOccupied
}

type OccupancyProvider struct {
	ID                string        `json:"id"`
	Type              string        `json:"type"`
	Priority          int           `json:"priority"`
	Required          bool          `json:"required"`
	StaleAfter        time.Duration `json:"staleAfter"`
	FreshnessRequired bool          `json:"freshnessRequired"`
}

type OccupancySensorMapping struct {
	ProviderID string `json:"providerId"`
	SensorID   string `json:"sensorId"`
	BlockID    string `json:"blockId"`
	Required   *bool  `json:"required,omitempty"`
	Priority   *int   `json:"priority,omitempty"`
}

type ResolvedOccupancySensorMapping struct {
	ProviderID string        `json:"providerId"`
	SensorID   string        `json:"sensorId"`
	BlockID    string        `json:"blockId"`
	Required   bool          `json:"required"`
	Priority   int           `json:"priority"`
	StaleAfter time.Duration `json:"staleAfter"`
}

func (m OccupancySensorMapping) Resolve(provider OccupancyProvider) (ResolvedOccupancySensorMapping, error) {
	if err := ValidateOccupancyProvider(provider); err != nil {
		return ResolvedOccupancySensorMapping{}, err
	}
	if err := ValidateOccupancySensorMapping(m); err != nil {
		return ResolvedOccupancySensorMapping{}, err
	}
	if m.ProviderID != provider.ID {
		return ResolvedOccupancySensorMapping{}, fmt.Errorf("%w: mapping provider %q does not match provider %q", ErrInvalidOccupancy, m.ProviderID, provider.ID)
	}
	required := provider.Required
	if m.Required != nil {
		required = *m.Required
	}
	priority := provider.Priority
	if m.Priority != nil {
		priority = *m.Priority
	}
	return ResolvedOccupancySensorMapping{
		ProviderID: m.ProviderID,
		SensorID:   m.SensorID,
		BlockID:    m.BlockID,
		Required:   required,
		Priority:   priority,
		StaleAfter: provider.StaleAfter,
	}, nil
}

type OccupancyObservation struct {
	ProviderID string         `json:"providerId"`
	SensorID   string         `json:"sensorId"`
	State      OccupancyState `json:"state"`
	Sequence   uint64         `json:"sequence"`
	ObservedAt time.Time      `json:"observedAt"`
	ReceivedAt time.Time      `json:"receivedAt"`
	Occupant   *OccupantRef   `json:"occupant,omitempty"`
}

var ErrInvalidOccupancy = errors.New("invalid occupancy")

func ValidateBlockOccupancy(occupancy BlockOccupancy) error {
	if strings.TrimSpace(occupancy.BlockID) == "" {
		return invalidOccupancy("block id is required")
	}
	return validateOccupancyValue(occupancy.State, occupancy.Occupant)
}

func ValidateOccupancyProvider(provider OccupancyProvider) error {
	if strings.TrimSpace(provider.ID) == "" {
		return invalidOccupancy("provider id is required")
	}
	if strings.TrimSpace(provider.Type) == "" {
		return invalidOccupancy("provider %q type is required", provider.ID)
	}
	if provider.Priority < 0 || provider.Priority > 100 {
		return invalidOccupancy("provider %q priority must be between 0 and 100", provider.ID)
	}
	if provider.StaleAfter < 0 {
		return invalidOccupancy("provider %q staleAfter cannot be negative", provider.ID)
	}
	if provider.FreshnessRequired && provider.StaleAfter <= 0 {
		return invalidOccupancy("provider %q requires a positive staleAfter", provider.ID)
	}
	return nil
}

func ValidateOccupancySensorMapping(mapping OccupancySensorMapping) error {
	if strings.TrimSpace(mapping.ProviderID) == "" {
		return invalidOccupancy("mapping provider id is required")
	}
	if strings.TrimSpace(mapping.SensorID) == "" {
		return invalidOccupancy("mapping sensor id is required")
	}
	if strings.TrimSpace(mapping.BlockID) == "" {
		return invalidOccupancy("mapping block id is required")
	}
	if mapping.Priority != nil && (*mapping.Priority < 0 || *mapping.Priority > 100) {
		return invalidOccupancy("mapping priority must be between 0 and 100")
	}
	return nil
}

func ValidateOccupancyObservation(observation OccupancyObservation, sequenceRequired bool) error {
	if strings.TrimSpace(observation.ProviderID) == "" {
		return invalidOccupancy("observation provider id is required")
	}
	if strings.TrimSpace(observation.SensorID) == "" {
		return invalidOccupancy("observation sensor id is required")
	}
	if sequenceRequired && observation.Sequence == 0 {
		return invalidOccupancy("observation sequence must be greater than zero")
	}
	return validateOccupancyValue(observation.State, observation.Occupant)
}

func validateOccupancyValue(state OccupancyState, occupant *OccupantRef) error {
	if !state.Valid() {
		return invalidOccupancy("unknown state %q", state)
	}
	if occupant == nil {
		return nil
	}
	if state != OccupancyOccupied {
		return invalidOccupancy("occupant is only allowed for occupied state")
	}
	if strings.TrimSpace(occupant.Type) == "" || strings.TrimSpace(occupant.ID) == "" {
		return invalidOccupancy("occupant type and id are required")
	}
	return nil
}

func invalidOccupancy(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidOccupancy, fmt.Sprintf(format, args...))
}
