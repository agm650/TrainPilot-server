package model

import (
	"errors"
	"fmt"
	"strings"
)

type TopologyNodeKind string

const (
	TopologyNodeJoint    TopologyNodeKind = "joint"
	TopologyNodeBuffer   TopologyNodeKind = "buffer"
	TopologyNodeBoundary TopologyNodeKind = "boundary"
)

func (k TopologyNodeKind) Valid() bool {
	switch k {
	case TopologyNodeJoint, TopologyNodeBuffer, TopologyNodeBoundary:
		return true
	default:
		return false
	}
}

type TopologyNode struct {
	ID   string           `json:"id"`
	Name string           `json:"name,omitempty"`
	Kind TopologyNodeKind `json:"kind"`
}

type TrackSection struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	NodeAID  string `json:"nodeAId"`
	NodeBID  string `json:"nodeBId"`
	LengthMM int    `json:"lengthMm,omitempty"`
}

type TurnoutTopology struct {
	TurnoutID string                    `json:"turnoutId"`
	Ports     []TurnoutPort             `json:"ports"`
	Positions []TurnoutTopologyPosition `json:"positions"`
}

type TurnoutPort struct {
	ID     string `json:"id"`
	NodeID string `json:"nodeId"`
}

type TurnoutTopologyPosition struct {
	PositionID  string           `json:"positionId"`
	Connections []PortConnection `json:"connections"`
}

// TopologyDefinition is the canonical public description of the physical
// railway topology. Runtime state and DCC endpoint configuration stay on the
// existing turnout and block resources.
type TopologyDefinition struct {
	Revision          string            `json:"revision"`
	Nodes             []TopologyNode    `json:"nodes"`
	TrackSections     []TrackSection    `json:"trackSections"`
	TurnoutTopologies []TurnoutTopology `json:"turnoutTopologies"`
	Blocks            []BlockDefinition `json:"blocks"`
}

type PortConnection struct {
	PortAID string `json:"portAId"`
	PortBID string `json:"portBId"`
}

var ErrInvalidTopology = errors.New("invalid topology definition")

// ValidateTopologyDefinition validates the static physical topology of a layout.
// It does not inspect turnout runtime state or build an active connectivity graph.
func ValidateTopologyDefinition(layout LayoutDefinition) error {
	invalid := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalidTopology, fmt.Sprintf(format, args...))
	}

	nodes := make(map[string]TopologyNode, len(layout.TopologyNodes))
	for _, node := range layout.TopologyNodes {
		if strings.TrimSpace(node.ID) == "" {
			return invalid("node id is required")
		}
		if _, exists := nodes[node.ID]; exists {
			return invalid("duplicate node id %q", node.ID)
		}
		if !node.Kind.Valid() {
			return invalid("node %q has invalid kind %q", node.ID, node.Kind)
		}
		nodes[node.ID] = node
	}

	sections := make(map[string]bool, len(layout.TrackSections))
	for _, section := range layout.TrackSections {
		if strings.TrimSpace(section.ID) == "" {
			return invalid("track section id is required")
		}
		if sections[section.ID] {
			return invalid("duplicate track section id %q", section.ID)
		}
		sections[section.ID] = true
		if _, exists := nodes[section.NodeAID]; !exists {
			return invalid("track section %q references unknown node %q", section.ID, section.NodeAID)
		}
		if _, exists := nodes[section.NodeBID]; !exists {
			return invalid("track section %q references unknown node %q", section.ID, section.NodeBID)
		}
		if section.NodeAID == section.NodeBID {
			return invalid("track section %q connects node %q to itself", section.ID, section.NodeAID)
		}
		if section.LengthMM < 0 {
			return invalid("track section %q has negative length %d", section.ID, section.LengthMM)
		}
	}

	turnouts := make(map[string]Turnout, len(layout.Turnouts))
	for _, turnout := range layout.Turnouts {
		turnouts[turnout.ID] = turnout
	}
	topologies := make(map[string]bool, len(layout.TurnoutTopologies))
	for _, topology := range layout.TurnoutTopologies {
		if strings.TrimSpace(topology.TurnoutID) == "" {
			return invalid("turnout topology requires a turnout id")
		}
		if topologies[topology.TurnoutID] {
			return invalid("duplicate topology for turnout %q", topology.TurnoutID)
		}
		topologies[topology.TurnoutID] = true
		turnout, exists := turnouts[topology.TurnoutID]
		if !exists {
			return invalid("topology references unknown turnout %q", topology.TurnoutID)
		}
		if err := validateTurnoutTopology(topology, turnout, nodes, invalid); err != nil {
			return err
		}
	}

	return nil
}

func validateTurnoutTopology(
	topology TurnoutTopology,
	turnout Turnout,
	nodes map[string]TopologyNode,
	invalid func(string, ...any) error,
) error {
	if len(topology.Ports) < 2 {
		return invalid("turnout %q topology requires at least two ports", topology.TurnoutID)
	}
	ports := make(map[string]bool, len(topology.Ports))
	for _, port := range topology.Ports {
		if strings.TrimSpace(port.ID) == "" {
			return invalid("turnout %q topology has a port without id", topology.TurnoutID)
		}
		if ports[port.ID] {
			return invalid("turnout %q topology has duplicate port id %q", topology.TurnoutID, port.ID)
		}
		ports[port.ID] = true
		if _, exists := nodes[port.NodeID]; !exists {
			return invalid("turnout %q port %q references unknown node %q", topology.TurnoutID, port.ID, port.NodeID)
		}
	}

	turnoutPositions := make(map[string]bool, len(turnout.Positions))
	for _, position := range turnout.Positions {
		turnoutPositions[position.ID] = true
	}
	topologyPositions := make(map[string]bool, len(topology.Positions))
	for _, position := range topology.Positions {
		if strings.TrimSpace(position.PositionID) == "" {
			return invalid("turnout %q topology has a position without id", topology.TurnoutID)
		}
		if topologyPositions[position.PositionID] {
			return invalid("turnout %q topology has duplicate position %q", topology.TurnoutID, position.PositionID)
		}
		topologyPositions[position.PositionID] = true
		if !turnoutPositions[position.PositionID] {
			return invalid("turnout %q topology references unknown position %q", topology.TurnoutID, position.PositionID)
		}

		connections := make(map[portConnectionKey]bool, len(position.Connections))
		for _, connection := range position.Connections {
			if !ports[connection.PortAID] {
				return invalid("turnout %q position %q references unknown port %q", topology.TurnoutID, position.PositionID, connection.PortAID)
			}
			if !ports[connection.PortBID] {
				return invalid("turnout %q position %q references unknown port %q", topology.TurnoutID, position.PositionID, connection.PortBID)
			}
			if connection.PortAID == connection.PortBID {
				return invalid("turnout %q position %q connects port %q to itself", topology.TurnoutID, position.PositionID, connection.PortAID)
			}
			key := newPortConnectionKey(connection.PortAID, connection.PortBID)
			if connections[key] {
				return invalid("turnout %q position %q has duplicate connection %q to %q", topology.TurnoutID, position.PositionID, key.a, key.b)
			}
			connections[key] = true
		}
	}

	for positionID := range turnoutPositions {
		if !topologyPositions[positionID] {
			return invalid("turnout %q topology does not define position %q", topology.TurnoutID, positionID)
		}
	}
	return nil
}

type portConnectionKey struct {
	a string
	b string
}

func newPortConnectionKey(a, b string) portConnectionKey {
	if a > b {
		a, b = b, a
	}
	return portConnectionKey{a: a, b: b}
}
