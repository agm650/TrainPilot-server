package model_test

import (
	"errors"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/model/topologyfixture"
)

func TestValidateTopologyDefinitionAcceptsPhysicalLayouts(t *testing.T) {
	tests := []struct {
		name   string
		layout model.LayoutDefinition
	}{
		{name: "empty topology", layout: model.LayoutDefinition{}},
		{name: "minimal topology", layout: model.LayoutDefinition{
			TopologyNodes: []model.TopologyNode{node("joint", model.TopologyNodeJoint)},
		}},
		{name: "simple section", layout: sectionLayout(
			node("a", model.TopologyNodeJoint),
			node("b", model.TopologyNodeJoint),
			model.TrackSection{ID: "ab", NodeAID: "a", NodeBID: "b", LengthMM: 250},
		)},
		{name: "unknown length", layout: sectionLayout(
			node("a", model.TopologyNodeJoint),
			node("b", model.TopologyNodeJoint),
			section("ab", "a", "b"),
		)},
		{name: "parallel tracks", layout: model.LayoutDefinition{
			TopologyNodes: []model.TopologyNode{node("a", model.TopologyNodeJoint), node("b", model.TopologyNodeJoint)},
			TrackSections: []model.TrackSection{section("first", "a", "b"), section("second", "a", "b")},
		}},
		{name: "buffer stop", layout: sectionLayout(
			node("joint", model.TopologyNodeJoint),
			node("buffer", model.TopologyNodeBuffer),
			section("siding", "joint", "buffer"),
		)},
		{name: "boundary", layout: sectionLayout(
			node("joint", model.TopologyNodeJoint),
			node("boundary", model.TopologyNodeBoundary),
			section("exit", "joint", "boundary"),
		)},
		{name: "cycle", layout: model.LayoutDefinition{
			TopologyNodes: []model.TopologyNode{
				node("a", model.TopologyNodeJoint), node("b", model.TopologyNodeJoint), node("c", model.TopologyNodeJoint),
			},
			TrackSections: []model.TrackSection{
				section("ab", "a", "b"), section("bc", "b", "c"), section("ca", "c", "a"),
			},
		}},
		{name: "fixed crossing", layout: model.LayoutDefinition{
			TopologyNodes: []model.TopologyNode{
				node("north", model.TopologyNodeJoint), node("south", model.TopologyNodeJoint),
				node("east", model.TopologyNodeJoint), node("west", model.TopologyNodeJoint),
			},
			TrackSections: []model.TrackSection{
				section("north-south", "north", "south"), section("east-west", "east", "west"),
			},
		}},
		{name: "simple turnout", layout: topologyfixture.Simple()},
		{name: "three-way turnout", layout: topologyfixture.ThreeWay()},
		{name: "double-slip turnout", layout: topologyfixture.DoubleSlip()},
		{name: "single-slip turnout", layout: topologyfixture.SingleSlip()},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := model.ValidateTopologyDefinition(test.layout); err != nil {
				t.Fatalf("ValidateTopologyDefinition() error = %v", err)
			}
		})
	}
}

func TestDoubleSlipTopologySupportsMultipleConnectionsPerPosition(t *testing.T) {
	layout := topologyfixture.DoubleSlip()
	positions := layout.TurnoutTopologies[0].Positions
	if len(positions[0].Connections) != 2 || len(positions[3].Connections) != 2 {
		t.Fatalf("double-slip fixture does not contain simultaneous connections: %+v", positions)
	}
}

func TestValidateTopologyDefinitionRejectsInvalidDefinitions(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*model.LayoutDefinition)
	}{
		{name: "empty node id", mutate: func(layout *model.LayoutDefinition) { layout.TopologyNodes[0].ID = " " }},
		{name: "duplicate node id", mutate: func(layout *model.LayoutDefinition) {
			layout.TopologyNodes[1].ID = layout.TopologyNodes[0].ID
		}},
		{name: "unknown node kind", mutate: func(layout *model.LayoutDefinition) { layout.TopologyNodes[0].Kind = "platform" }},
		{name: "empty section id", mutate: func(layout *model.LayoutDefinition) { layout.TrackSections[0].ID = "" }},
		{name: "duplicate section id", mutate: func(layout *model.LayoutDefinition) {
			layout.TrackSections = append(layout.TrackSections, layout.TrackSections[0])
		}},
		{name: "unknown node a", mutate: func(layout *model.LayoutDefinition) { layout.TrackSections[0].NodeAID = "missing" }},
		{name: "unknown node b", mutate: func(layout *model.LayoutDefinition) { layout.TrackSections[0].NodeBID = "missing" }},
		{name: "section self-loop", mutate: func(layout *model.LayoutDefinition) {
			layout.TrackSections[0].NodeBID = layout.TrackSections[0].NodeAID
		}},
		{name: "negative length", mutate: func(layout *model.LayoutDefinition) { layout.TrackSections[0].LengthMM = -1 }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			layout := sectionLayout(
				node("a", model.TopologyNodeJoint),
				node("b", model.TopologyNodeJoint),
				section("ab", "a", "b"),
			)
			test.mutate(&layout)
			assertInvalidTopology(t, layout)
		})
	}
}

func TestValidateTopologyDefinitionRejectsInvalidTurnoutTopologies(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*model.LayoutDefinition)
	}{
		{name: "empty turnout id", mutate: func(layout *model.LayoutDefinition) { layout.TurnoutTopologies[0].TurnoutID = "" }},
		{name: "unknown turnout", mutate: func(layout *model.LayoutDefinition) { layout.TurnoutTopologies[0].TurnoutID = "missing" }},
		{name: "duplicate turnout topology", mutate: func(layout *model.LayoutDefinition) {
			layout.TurnoutTopologies = append(layout.TurnoutTopologies, layout.TurnoutTopologies[0])
		}},
		{name: "fewer than two ports", mutate: func(layout *model.LayoutDefinition) {
			layout.TurnoutTopologies[0].Ports = layout.TurnoutTopologies[0].Ports[:1]
		}},
		{name: "empty port id", mutate: func(layout *model.LayoutDefinition) { layout.TurnoutTopologies[0].Ports[0].ID = "" }},
		{name: "duplicate port id", mutate: func(layout *model.LayoutDefinition) {
			layout.TurnoutTopologies[0].Ports[1].ID = layout.TurnoutTopologies[0].Ports[0].ID
		}},
		{name: "unknown port node", mutate: func(layout *model.LayoutDefinition) {
			layout.TurnoutTopologies[0].Ports[0].NodeID = "missing"
		}},
		{name: "empty position id", mutate: func(layout *model.LayoutDefinition) {
			layout.TurnoutTopologies[0].Positions[0].PositionID = ""
		}},
		{name: "unknown turnout position", mutate: func(layout *model.LayoutDefinition) {
			layout.TurnoutTopologies[0].Positions[0].PositionID = "missing"
		}},
		{name: "duplicate topology position", mutate: func(layout *model.LayoutDefinition) {
			layout.TurnoutTopologies[0].Positions[1].PositionID = layout.TurnoutTopologies[0].Positions[0].PositionID
		}},
		{name: "missing topology position", mutate: func(layout *model.LayoutDefinition) {
			layout.TurnoutTopologies[0].Positions = layout.TurnoutTopologies[0].Positions[:1]
		}},
		{name: "unknown connection port a", mutate: func(layout *model.LayoutDefinition) {
			layout.TurnoutTopologies[0].Positions[0].Connections[0].PortAID = "missing"
		}},
		{name: "unknown connection port b", mutate: func(layout *model.LayoutDefinition) {
			layout.TurnoutTopologies[0].Positions[0].Connections[0].PortBID = "missing"
		}},
		{name: "connection self-loop", mutate: func(layout *model.LayoutDefinition) {
			connection := &layout.TurnoutTopologies[0].Positions[0].Connections[0]
			connection.PortBID = connection.PortAID
		}},
		{name: "duplicate undirected connection", mutate: func(layout *model.LayoutDefinition) {
			connection := layout.TurnoutTopologies[0].Positions[0].Connections[0]
			layout.TurnoutTopologies[0].Positions[0].Connections = append(
				layout.TurnoutTopologies[0].Positions[0].Connections,
				model.PortConnection{PortAID: connection.PortBID, PortBID: connection.PortAID},
			)
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			layout := topologyfixture.Simple()
			test.mutate(&layout)
			assertInvalidTopology(t, layout)
		})
	}
}

func TestTurnoutTopologyEditorValidationCodes(t *testing.T) {
	for _, test := range []struct {
		name   string
		code   string
		mutate func(*model.LayoutDefinition)
	}{
		{"missing logical position", "turnout_topology_position_missing", func(layout *model.LayoutDefinition) {
			layout.TurnoutTopologies[0].Positions = layout.TurnoutTopologies[0].Positions[:1]
		}},
		{"undeclared topology position", "turnout_topology_position_undeclared", func(layout *model.LayoutDefinition) {
			layout.TurnoutTopologies[0].Positions[0].PositionID = "undefined"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			layout := topologyfixture.Simple()
			test.mutate(&layout)
			err := model.ValidateTopologyDefinition(layout)
			var validation *model.TurnoutTopologyValidationError
			if !errors.As(err, &validation) || !errors.Is(err, model.ErrInvalidTopology) || validation.Code != test.code || validation.TurnoutID != layout.Turnouts[0].ID {
				t.Fatalf("error=%v diagnostic=%+v", err, validation)
			}
		})
	}
}

func assertInvalidTopology(t *testing.T, layout model.LayoutDefinition) {
	t.Helper()
	if err := model.ValidateTopologyDefinition(layout); !errors.Is(err, model.ErrInvalidTopology) {
		t.Fatalf("ValidateTopologyDefinition() error = %v, want ErrInvalidTopology", err)
	}
}

func node(id string, kind model.TopologyNodeKind) model.TopologyNode {
	return model.TopologyNode{ID: id, Kind: kind}
}

func section(id, nodeAID, nodeBID string) model.TrackSection {
	return model.TrackSection{ID: id, NodeAID: nodeAID, NodeBID: nodeBID}
}

func sectionLayout(nodeA, nodeB model.TopologyNode, trackSection model.TrackSection) model.LayoutDefinition {
	return model.LayoutDefinition{
		TopologyNodes: []model.TopologyNode{nodeA, nodeB},
		TrackSections: []model.TrackSection{trackSection},
	}
}
