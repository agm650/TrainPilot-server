// Package topologyfixture provides physical topology definitions shared by tests.
package topologyfixture

import (
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/model/turnoutfixture"
)

func Simple() model.LayoutDefinition {
	return turnoutLayout(
		turnoutfixture.Simple(),
		[]string{"stem", "straight", "diverging"},
		map[string][]model.PortConnection{
			"straight":  {connection("stem", "straight")},
			"diverging": {connection("stem", "diverging")},
		},
	)
}

func ThreeWay() model.LayoutDefinition {
	return turnoutLayout(
		turnoutfixture.ThreeWay(),
		[]string{"stem", "left", "straight", "right"},
		map[string][]model.PortConnection{
			"left":     {connection("stem", "left")},
			"straight": {connection("stem", "straight")},
			"right":    {connection("stem", "right")},
		},
	)
}

func DoubleSlip() model.LayoutDefinition {
	return turnoutLayout(
		turnoutfixture.DoubleSlip(),
		[]string{"a", "b", "c", "d"},
		map[string][]model.PortConnection{
			"route_a": {connection("a", "c"), connection("b", "d")},
			"route_b": {connection("a", "c")},
			"route_c": {connection("b", "d")},
			"route_d": {connection("a", "d"), connection("b", "c")},
		},
	)
}

func SingleSlip() model.LayoutDefinition {
	return turnoutLayout(
		turnoutfixture.SingleSlip(),
		[]string{"a", "b", "c", "d"},
		map[string][]model.PortConnection{
			"route_a": {connection("a", "c"), connection("b", "d")},
			"route_b": {connection("a", "d")},
			"route_c": {connection("b", "c")},
		},
	)
}

// PassingStation models a through station with a passing loop and a siding.
// Three simple turnouts connect the west approach, two station tracks, the
// east approach, and the siding buffer stop.
func PassingStation() model.LayoutDefinition {
	west := model.NewSimpleTurnout("station-west", "West throat", 50, "straight", "straight")
	east := model.NewSimpleTurnout("station-east", "East throat", 51, "straight", "straight")
	branch := model.NewSimpleTurnout("station-branch", "Siding branch", 52, "straight", "straight")
	turnouts := []model.Turnout{west, east, branch}

	nodes := []model.TopologyNode{
		{ID: "west-boundary", Kind: model.TopologyNodeBoundary},
		{ID: "east-boundary", Kind: model.TopologyNodeBoundary},
		{ID: "siding-buffer", Kind: model.TopologyNodeBuffer},
	}
	var topologies []model.TurnoutTopology
	for _, turnout := range turnouts {
		ports := []model.TurnoutPort{
			{ID: "stem", NodeID: turnout.ID + "-stem"},
			{ID: "straight", NodeID: turnout.ID + "-straight"},
			{ID: "diverging", NodeID: turnout.ID + "-diverging"},
		}
		for _, port := range ports {
			nodes = append(nodes, model.TopologyNode{ID: port.NodeID, Kind: model.TopologyNodeJoint})
		}
		topologies = append(topologies, model.TurnoutTopology{
			TurnoutID: turnout.ID,
			Ports:     ports,
			Positions: []model.TurnoutTopologyPosition{
				{PositionID: "straight", Connections: []model.PortConnection{connection("stem", "straight")}},
				{PositionID: "diverging", Connections: []model.PortConnection{connection("stem", "diverging")}},
			},
		})
	}

	sections := []model.TrackSection{
		{ID: "approach-west", Name: "West approach", NodeAID: "west-boundary", NodeBID: "station-west-stem"},
		{ID: "main-west", Name: "Main platform west", NodeAID: "station-west-straight", NodeBID: "station-branch-stem"},
		{ID: "main-east", Name: "Main platform east", NodeAID: "station-branch-straight", NodeBID: "station-east-straight"},
		{ID: "passing-loop", Name: "Passing loop", NodeAID: "station-west-diverging", NodeBID: "station-east-diverging"},
		{ID: "siding", Name: "Siding", NodeAID: "station-branch-diverging", NodeBID: "siding-buffer"},
		{ID: "approach-east", Name: "East approach", NodeAID: "station-east-stem", NodeBID: "east-boundary"},
	}

	return model.LayoutDefinition{
		Turnouts:          turnouts,
		TopologyNodes:     nodes,
		TrackSections:     sections,
		TurnoutTopologies: topologies,
		Blocks: []model.BlockDefinition{
			{ID: "block-west", Name: "West approach", TrackSectionIDs: []string{"approach-west"}},
			{ID: "block-main", Name: "Main platform", TrackSectionIDs: []string{"main-west", "main-east"}, TurnoutIDs: []string{"station-branch"}},
			{ID: "block-loop", Name: "Passing loop", TrackSectionIDs: []string{"passing-loop"}},
			{ID: "block-siding", Name: "Siding", TrackSectionIDs: []string{"siding"}},
			{ID: "block-east", Name: "East approach", TrackSectionIDs: []string{"approach-east"}},
		},
	}
}

func turnoutLayout(
	turnout model.Turnout,
	portIDs []string,
	connections map[string][]model.PortConnection,
) model.LayoutDefinition {
	nodes := make([]model.TopologyNode, 0, len(portIDs))
	ports := make([]model.TurnoutPort, 0, len(portIDs))
	for _, portID := range portIDs {
		nodeID := turnout.ID + "-" + portID
		nodes = append(nodes, model.TopologyNode{ID: nodeID, Kind: model.TopologyNodeJoint})
		ports = append(ports, model.TurnoutPort{ID: portID, NodeID: nodeID})
	}
	positions := make([]model.TurnoutTopologyPosition, 0, len(turnout.Positions))
	for _, position := range turnout.Positions {
		positions = append(positions, model.TurnoutTopologyPosition{
			PositionID:  position.ID,
			Connections: connections[position.ID],
		})
	}
	return model.LayoutDefinition{
		Turnouts:      []model.Turnout{turnout},
		TopologyNodes: nodes,
		TurnoutTopologies: []model.TurnoutTopology{{
			TurnoutID: turnout.ID,
			Ports:     ports,
			Positions: positions,
		}},
	}
}

func connection(a, b string) model.PortConnection {
	return model.PortConnection{PortAID: a, PortBID: b}
}
