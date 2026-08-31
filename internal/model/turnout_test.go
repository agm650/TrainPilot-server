package model

import (
	"errors"
	"testing"
)

func TestValidateTurnoutDefinitions(t *testing.T) {
	tests := []struct {
		name    string
		turnout Turnout
		valid   bool
	}{
		{"simple", simpleTurnoutFixture(), true},
		{"three way", threeWayTurnoutFixture(), true},
		{"double slip", doubleSlipTurnoutFixture(), true},
		{"single slip", singleSlipTurnoutFixture(), true},
		{"custom three endpoints", customTurnoutFixture(), true},
		{"empty turnout id", mutateTurnout(simpleTurnoutFixture(), func(x *Turnout) { x.ID = "" }), false},
		{"no endpoint", mutateTurnout(simpleTurnoutFixture(), func(x *Turnout) { x.Endpoints = nil }), false},
		{"missing endpoint", mutateTurnout(threeWayTurnoutFixture(), func(x *Turnout) { delete(x.Positions[0].Endpoints, "B") }), false},
		{"invalid address", mutateTurnout(simpleTurnoutFixture(), func(x *Turnout) { x.Endpoints[0].LinearAddress = 0 }), false},
		{"duplicate endpoint id", mutateTurnout(threeWayTurnoutFixture(), func(x *Turnout) { x.Endpoints[1].ID = "A" }), false},
		{"duplicate endpoint address", mutateTurnout(threeWayTurnoutFixture(), func(x *Turnout) { x.Endpoints[1].LinearAddress = x.Endpoints[0].LinearAddress }), false},
		{"duplicate position", mutateTurnout(threeWayTurnoutFixture(), func(x *Turnout) { x.Positions[1].ID = x.Positions[0].ID }), false},
		{"duplicate vector", mutateTurnout(threeWayTurnoutFixture(), func(x *Turnout) { x.Positions[1].Endpoints = cloneEndpointVector(x.Positions[0].Endpoints) }), false},
		{"unknown endpoint", mutateTurnout(simpleTurnoutFixture(), func(x *Turnout) {
			x.Positions[0].Endpoints = map[string]AccessoryPosition{"missing": AccessoryPosition1}
		}), false},
		{"invalid binary position", mutateTurnout(simpleTurnoutFixture(), func(x *Turnout) { x.Positions[0].Endpoints["A"] = "sideways" }), false},
		{"reserved unknown position", mutateTurnout(simpleTurnoutFixture(), func(x *Turnout) { x.Positions[0].ID = "unknown" }), false},
		{"unknown desired position", mutateTurnout(simpleTurnoutFixture(), func(x *Turnout) { x.DesiredPosition = "missing" }), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTurnout(tt.turnout)
			if tt.valid && err != nil {
				t.Fatalf("ValidateTurnout() error = %v", err)
			}
			if !tt.valid && !errors.Is(err, ErrInvalidTurnout) {
				t.Fatalf("ValidateTurnout() error = %v, want ErrInvalidTurnout", err)
			}
		})
	}
}

func TestResolveTurnoutPosition(t *testing.T) {
	tests := []struct {
		name    string
		turnout Turnout
		states  map[string]AccessoryPosition
		want    string
		ok      bool
	}{
		{"simple straight", simpleTurnoutFixture(), positions("A", AccessoryPosition1), "straight", true},
		{"simple diverging", simpleTurnoutFixture(), positions("A", AccessoryPosition2), "diverging", true},
		{"triple left", threeWayTurnoutFixture(), positions("A", AccessoryPosition2, "B", AccessoryPosition1), "left", true},
		{"triple straight", threeWayTurnoutFixture(), positions("A", AccessoryPosition1, "B", AccessoryPosition1), "straight", true},
		{"triple right", threeWayTurnoutFixture(), positions("A", AccessoryPosition1, "B", AccessoryPosition2), "right", true},
		{"triple invalid fourth vector", threeWayTurnoutFixture(), positions("A", AccessoryPosition2, "B", AccessoryPosition2), "", false},
	}
	for _, vector := range doubleSlipTurnoutFixture().Positions {
		tests = append(tests, struct {
			name    string
			turnout Turnout
			states  map[string]AccessoryPosition
			want    string
			ok      bool
		}{"double slip vector " + vector.ID, doubleSlipTurnoutFixture(), cloneEndpointVector(vector.Endpoints), vector.ID, true})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ResolveTurnoutPosition(tt.turnout, tt.states)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("ResolveTurnoutPosition() = %q,%v, want %q,%v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestTurnoutEndpointInversion(t *testing.T) {
	turnout := simpleTurnoutFixture()
	turnout.Endpoints[0].Inverted = true
	got, ok := ResolveTurnoutPosition(turnout, positions("A", AccessoryPosition2))
	if !ok || got != "straight" {
		t.Fatalf("inverted physical position2 resolved as %q,%v", got, ok)
	}
	if got := PhysicalAccessoryPosition(turnout.Endpoints[0], AccessoryPosition1); got != AccessoryPosition2 {
		t.Fatalf("PhysicalAccessoryPosition() = %q", got)
	}
}

func TestNormalizeLegacySimpleTurnout(t *testing.T) {
	turnout, err := NormalizeTurnout(Turnout{
		ID:            "legacy",
		Name:          "Legacy",
		DCCAddress:    12,
		DesiredState:  "straight",
		ReportedState: "diverging",
	})
	if err != nil {
		t.Fatal(err)
	}
	if turnout.Kind != TurnoutKindSimple || turnout.DesiredPosition != "straight" || turnout.ReportedPosition != "diverging" {
		t.Fatalf("unexpected normalized turnout: %+v", turnout)
	}
	if len(turnout.Endpoints) != 1 || turnout.Endpoints[0].ID != "main" || turnout.Endpoints[0].LinearAddress != 12 {
		t.Fatalf("unexpected endpoints: %+v", turnout.Endpoints)
	}
}

func simpleTurnoutFixture() Turnout {
	return Turnout{
		ID: "simple", Name: "Simple", Kind: TurnoutKindSimple,
		Endpoints: []AccessoryEndpoint{{ID: "A", LinearAddress: 10}},
		Positions: []TurnoutPositionDefinition{
			{ID: "straight", Endpoints: positions("A", AccessoryPosition1)},
			{ID: "diverging", Endpoints: positions("A", AccessoryPosition2)},
		},
		DesiredPosition: "straight", ReportedPosition: "straight",
	}
}

func threeWayTurnoutFixture() Turnout {
	return Turnout{
		ID: "triple", Name: "Three way", Kind: TurnoutKindThreeWay,
		Endpoints: []AccessoryEndpoint{{ID: "A", LinearAddress: 20}, {ID: "B", LinearAddress: 21}},
		Positions: []TurnoutPositionDefinition{
			{ID: "left", Endpoints: positions("A", AccessoryPosition2, "B", AccessoryPosition1)},
			{ID: "straight", Endpoints: positions("A", AccessoryPosition1, "B", AccessoryPosition1)},
			{ID: "right", Endpoints: positions("A", AccessoryPosition1, "B", AccessoryPosition2)},
		},
	}
}

func doubleSlipTurnoutFixture() Turnout {
	return Turnout{
		ID: "double-slip", Name: "Double slip", Kind: TurnoutKindDoubleSlip,
		Endpoints: []AccessoryEndpoint{{ID: "A", LinearAddress: 30}, {ID: "B", LinearAddress: 31}},
		Positions: []TurnoutPositionDefinition{
			{ID: "route_a", Endpoints: positions("A", AccessoryPosition1, "B", AccessoryPosition1)},
			{ID: "route_b", Endpoints: positions("A", AccessoryPosition1, "B", AccessoryPosition2)},
			{ID: "route_c", Endpoints: positions("A", AccessoryPosition2, "B", AccessoryPosition1)},
			{ID: "route_d", Endpoints: positions("A", AccessoryPosition2, "B", AccessoryPosition2)},
		},
	}
}

func singleSlipTurnoutFixture() Turnout {
	return Turnout{
		ID: "single-slip", Name: "Single slip", Kind: TurnoutKindSingleSlip,
		Endpoints: []AccessoryEndpoint{{ID: "A", LinearAddress: 40}, {ID: "B", LinearAddress: 41}},
		Positions: []TurnoutPositionDefinition{
			{ID: "route_a", Endpoints: positions("A", AccessoryPosition1, "B", AccessoryPosition1)},
			{ID: "route_b", Endpoints: positions("A", AccessoryPosition1, "B", AccessoryPosition2)},
			{ID: "route_c", Endpoints: positions("A", AccessoryPosition2, "B", AccessoryPosition1)},
		},
	}
}

func customTurnoutFixture() Turnout {
	return Turnout{
		ID: "custom", Name: "Custom", Kind: TurnoutKindCustom,
		Endpoints: []AccessoryEndpoint{{ID: "A", LinearAddress: 50}, {ID: "B", LinearAddress: 51}, {ID: "C", LinearAddress: 52}},
		Positions: []TurnoutPositionDefinition{
			{ID: "one", Endpoints: positions("A", AccessoryPosition1, "B", AccessoryPosition1, "C", AccessoryPosition1)},
			{ID: "two", Endpoints: positions("A", AccessoryPosition2, "B", AccessoryPosition1, "C", AccessoryPosition2)},
		},
	}
}

func mutateTurnout(turnout Turnout, mutate func(*Turnout)) Turnout {
	turnout.Endpoints = append([]AccessoryEndpoint(nil), turnout.Endpoints...)
	turnout.Positions = append([]TurnoutPositionDefinition(nil), turnout.Positions...)
	for i := range turnout.Positions {
		turnout.Positions[i].Endpoints = cloneEndpointVector(turnout.Positions[i].Endpoints)
	}
	mutate(&turnout)
	return turnout
}

func positions(values ...any) map[string]AccessoryPosition {
	out := make(map[string]AccessoryPosition, len(values)/2)
	for i := 0; i < len(values); i += 2 {
		out[values[i].(string)] = values[i+1].(AccessoryPosition)
	}
	return out
}

func cloneEndpointVector(source map[string]AccessoryPosition) map[string]AccessoryPosition {
	out := make(map[string]AccessoryPosition, len(source))
	for id, position := range source {
		out[id] = position
	}
	return out
}
