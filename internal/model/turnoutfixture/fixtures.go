// Package turnoutfixture provides canonical turnout definitions shared by
// driver, service and API conformance tests.
package turnoutfixture

import "github.com/agm650/TrainPilot-server/internal/model"

func Simple() model.Turnout {
	return model.Turnout{
		ID: "simple", Name: "Simple", Kind: model.TurnoutKindSimple,
		Endpoints: []model.AccessoryEndpoint{{ID: "A", LinearAddress: 10}},
		Positions: []model.TurnoutPositionDefinition{
			{ID: "straight", Label: "Direct", Endpoints: vector("A", model.AccessoryPosition1)},
			{ID: "diverging", Label: "Deviated", Endpoints: vector("A", model.AccessoryPosition2)},
		},
		DesiredPosition: "straight", ReportedPosition: "straight",
	}
}

func ThreeWay() model.Turnout {
	return model.Turnout{
		ID: "triple", Name: "Three way", Kind: model.TurnoutKindThreeWay,
		Endpoints: []model.AccessoryEndpoint{{ID: "A", LinearAddress: 20}, {ID: "B", LinearAddress: 21}},
		Positions: []model.TurnoutPositionDefinition{
			{ID: "left", Label: "Left", Endpoints: vector("A", model.AccessoryPosition2, "B", model.AccessoryPosition1)},
			{ID: "straight", Label: "Straight", Endpoints: vector("A", model.AccessoryPosition1, "B", model.AccessoryPosition1)},
			{ID: "right", Label: "Right", Endpoints: vector("A", model.AccessoryPosition1, "B", model.AccessoryPosition2)},
		},
		DesiredPosition: "straight", ReportedPosition: "straight",
	}
}

func DoubleSlip() model.Turnout {
	return model.Turnout{
		ID: "double-slip", Name: "Double slip", Kind: model.TurnoutKindDoubleSlip,
		Endpoints: []model.AccessoryEndpoint{{ID: "A", LinearAddress: 30}, {ID: "B", LinearAddress: 31}},
		Positions: []model.TurnoutPositionDefinition{
			{ID: "route_a", Label: "Route A", Endpoints: vector("A", model.AccessoryPosition1, "B", model.AccessoryPosition1)},
			{ID: "route_b", Label: "Route B", Endpoints: vector("A", model.AccessoryPosition1, "B", model.AccessoryPosition2)},
			{ID: "route_c", Label: "Route C", Endpoints: vector("A", model.AccessoryPosition2, "B", model.AccessoryPosition1)},
			{ID: "route_d", Label: "Route D", Endpoints: vector("A", model.AccessoryPosition2, "B", model.AccessoryPosition2)},
		},
		DesiredPosition: "route_a", ReportedPosition: "route_a",
	}
}

func SingleSlip() model.Turnout {
	return model.Turnout{
		ID: "single-slip", Name: "Single slip", Kind: model.TurnoutKindSingleSlip,
		Endpoints: []model.AccessoryEndpoint{{ID: "A", LinearAddress: 40}, {ID: "B", LinearAddress: 41}},
		Positions: []model.TurnoutPositionDefinition{
			{ID: "route_a", Label: "Route A", Endpoints: vector("A", model.AccessoryPosition1, "B", model.AccessoryPosition1)},
			{ID: "route_b", Label: "Route B", Endpoints: vector("A", model.AccessoryPosition1, "B", model.AccessoryPosition2)},
			{ID: "route_c", Label: "Route C", Endpoints: vector("A", model.AccessoryPosition2, "B", model.AccessoryPosition1)},
		},
		DesiredPosition: "route_a", ReportedPosition: "route_a",
	}
}

func All() []model.Turnout {
	return []model.Turnout{Simple(), ThreeWay(), DoubleSlip(), SingleSlip()}
}

func vector(values ...any) map[string]model.AccessoryPosition {
	result := make(map[string]model.AccessoryPosition, len(values)/2)
	for index := 0; index < len(values); index += 2 {
		result[values[index].(string)] = values[index+1].(model.AccessoryPosition)
	}
	return result
}
