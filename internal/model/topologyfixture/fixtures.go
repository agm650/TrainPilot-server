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
