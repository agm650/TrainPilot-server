package model

import (
	"errors"
	"testing"
	"time"
)

func TestOccupancyStatesAndLegacyCompatibility(t *testing.T) {
	for _, state := range []OccupancyState{OccupancyUnknown, OccupancyFree, OccupancyOccupied} {
		if !state.Valid() {
			t.Fatalf("state %q should be valid", state)
		}
		occupancy := BlockOccupancy{BlockID: "block-a", State: state}
		if err := ValidateBlockOccupancy(occupancy); err != nil {
			t.Fatalf("state %q: %v", state, err)
		}
		if got := occupancy.LegacyOccupied(); got != (state == OccupancyOccupied) {
			t.Fatalf("state %q legacy occupied = %t", state, got)
		}
	}
	if OccupancyState("clear").Valid() {
		t.Fatal("unexpected valid state")
	}
	if err := ValidateBlockOccupancy(BlockOccupancy{BlockID: "block-a", State: "clear"}); !errors.Is(err, ErrInvalidOccupancy) {
		t.Fatalf("invalid state error = %v", err)
	}
}

func TestOccupancyProviderValidation(t *testing.T) {
	base := OccupancyProvider{
		ID:                "camera-yard",
		Type:              "vision",
		Priority:          90,
		Required:          true,
		StaleAfter:        3 * time.Minute,
		FreshnessRequired: true,
	}
	for _, priority := range []int{0, 100} {
		provider := base
		provider.Priority = priority
		if err := ValidateOccupancyProvider(provider); err != nil {
			t.Fatalf("priority %d: %v", priority, err)
		}
	}
	for _, priority := range []int{-1, 101} {
		provider := base
		provider.Priority = priority
		if err := ValidateOccupancyProvider(provider); !errors.Is(err, ErrInvalidOccupancy) {
			t.Fatalf("priority %d error = %v", priority, err)
		}
	}
	for _, required := range []bool{false, true} {
		provider := base
		provider.Required = required
		if err := ValidateOccupancyProvider(provider); err != nil {
			t.Fatalf("required %t: %v", required, err)
		}
	}
	provider := base
	provider.StaleAfter = 0
	if err := ValidateOccupancyProvider(provider); !errors.Is(err, ErrInvalidOccupancy) {
		t.Fatalf("fresh provider without staleAfter error = %v", err)
	}
	provider.FreshnessRequired = false
	if err := ValidateOccupancyProvider(provider); err != nil {
		t.Fatalf("sticky provider: %v", err)
	}
	provider.StaleAfter = -time.Second
	if err := ValidateOccupancyProvider(provider); !errors.Is(err, ErrInvalidOccupancy) {
		t.Fatalf("negative staleAfter error = %v", err)
	}
}

func TestOccupancyValuesValidateOccupants(t *testing.T) {
	occupant := &OccupantRef{Type: "locomotive", ID: "BB72084"}
	tests := []struct {
		name     string
		state    OccupancyState
		occupant *OccupantRef
		valid    bool
	}{
		{name: "occupied without occupant", state: OccupancyOccupied, valid: true},
		{name: "occupied with occupant", state: OccupancyOccupied, occupant: occupant, valid: true},
		{name: "free without occupant", state: OccupancyFree, valid: true},
		{name: "unknown without occupant", state: OccupancyUnknown, valid: true},
		{name: "free with occupant", state: OccupancyFree, occupant: occupant},
		{name: "unknown with occupant", state: OccupancyUnknown, occupant: occupant},
		{name: "incomplete occupant", state: OccupancyOccupied, occupant: &OccupantRef{Type: "locomotive"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateOccupancyObservation(OccupancyObservation{
				ProviderID: "provider", SensorID: "sensor", State: test.state,
				Sequence: 1, Occupant: test.occupant,
			}, true)
			if test.valid && err != nil {
				t.Fatal(err)
			}
			if !test.valid && !errors.Is(err, ErrInvalidOccupancy) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestOccupancyObservationValidation(t *testing.T) {
	valid := OccupancyObservation{ProviderID: "provider", SensorID: "sensor", State: OccupancyFree, Sequence: 1}
	if err := ValidateOccupancyObservation(valid, true); err != nil {
		t.Fatal(err)
	}
	valid.Sequence = 0
	if err := ValidateOccupancyObservation(valid, true); !errors.Is(err, ErrInvalidOccupancy) {
		t.Fatalf("required sequence error = %v", err)
	}
	if err := ValidateOccupancyObservation(valid, false); err != nil {
		t.Fatalf("optional sequence: %v", err)
	}
	valid.ProviderID = ""
	if err := ValidateOccupancyObservation(valid, false); !errors.Is(err, ErrInvalidOccupancy) {
		t.Fatalf("empty provider error = %v", err)
	}
	valid.ProviderID = "provider"
	valid.SensorID = ""
	if err := ValidateOccupancyObservation(valid, false); !errors.Is(err, ErrInvalidOccupancy) {
		t.Fatalf("empty sensor error = %v", err)
	}
}

func TestOccupancySensorMappingInheritanceAndOverrides(t *testing.T) {
	provider := OccupancyProvider{ID: "z21-rbus", Type: "current-detection", Priority: 100, Required: true}
	mapping := OccupancySensorMapping{ProviderID: provider.ID, SensorID: "12", BlockID: "B12"}
	resolved, err := mapping.Resolve(provider)
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.Required || resolved.Priority != 100 || resolved.BlockID != "B12" {
		t.Fatalf("inherited mapping = %+v", resolved)
	}
	required, priority := false, 42
	mapping.Required = &required
	mapping.Priority = &priority
	resolved, err = mapping.Resolve(provider)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Required || resolved.Priority != 42 {
		t.Fatalf("overridden mapping = %+v", resolved)
	}
	priority = 101
	if err := ValidateOccupancySensorMapping(mapping); !errors.Is(err, ErrInvalidOccupancy) {
		t.Fatalf("invalid override error = %v", err)
	}
}

func TestNewBlockOccupancyStartsUnknown(t *testing.T) {
	now := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	occupancy := NewUnknownBlockOccupancy("block-a", now)
	if occupancy.State != OccupancyUnknown || occupancy.Occupant != nil || occupancy.UpdatedAt != now {
		t.Fatalf("initial occupancy = %+v", occupancy)
	}
	if occupancy.LegacyOccupied() {
		t.Fatal("unknown occupancy reported occupied")
	}
}
