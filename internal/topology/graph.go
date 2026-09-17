package topology

import (
	"errors"
	"fmt"
	"sort"

	"github.com/agm650/TrainPilot-server/internal/model"
)

type EdgeKind string

const (
	EdgeKindTrackSection EdgeKind = "track_section"
	EdgeKindTurnout      EdgeKind = "turnout"
)

type Edge struct {
	Kind           EdgeKind
	NodeAID        string
	NodeBID        string
	TrackSectionID string
	LengthMM       int
	TurnoutID      string
	PortAID        string
	PortBID        string
	PositionIDs    []string
}

func (e Edge) Other(nodeID string) (string, bool) {
	switch nodeID {
	case e.NodeAID:
		return e.NodeBID, true
	case e.NodeBID:
		return e.NodeAID, true
	default:
		return "", false
	}
}

type Graph struct {
	nodes             map[string]model.TopologyNode
	nodeIDs           []string
	sections          map[string]model.TrackSection
	sectionIDs        []string
	turnoutTopologies map[string]model.TurnoutTopology
	turnoutIDs        []string
	edges             []Edge
	incident          map[string][]int
}

var ErrInvalidGraph = errors.New("invalid topology graph")

func Build(definition model.LayoutDefinition) (*Graph, error) {
	if err := model.ValidateTopologyDefinition(definition); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidGraph, err)
	}

	graph := &Graph{
		nodes:             make(map[string]model.TopologyNode, len(definition.TopologyNodes)),
		sections:          make(map[string]model.TrackSection, len(definition.TrackSections)),
		turnoutTopologies: make(map[string]model.TurnoutTopology, len(definition.TurnoutTopologies)),
		incident:          make(map[string][]int, len(definition.TopologyNodes)),
	}

	for _, node := range definition.TopologyNodes {
		graph.nodes[node.ID] = node
		graph.nodeIDs = append(graph.nodeIDs, node.ID)
		graph.incident[node.ID] = nil
	}
	sort.Strings(graph.nodeIDs)

	fixedIncidence := make(map[string]int, len(definition.TopologyNodes))
	for _, section := range definition.TrackSections {
		graph.sections[section.ID] = section
		graph.sectionIDs = append(graph.sectionIDs, section.ID)
		fixedIncidence[section.NodeAID]++
		fixedIncidence[section.NodeBID]++
		graph.edges = append(graph.edges, Edge{
			Kind:           EdgeKindTrackSection,
			NodeAID:        section.NodeAID,
			NodeBID:        section.NodeBID,
			TrackSectionID: section.ID,
			LengthMM:       section.LengthMM,
		})
	}
	sort.Strings(graph.sectionIDs)

	portNodes := make(map[string]bool)
	turnoutEdges := make(map[turnoutEdgeKey]*turnoutEdge)
	for _, topology := range definition.TurnoutTopologies {
		canonical := canonicalTurnoutTopology(topology)
		graph.turnoutTopologies[topology.TurnoutID] = canonical
		graph.turnoutIDs = append(graph.turnoutIDs, topology.TurnoutID)

		ports := make(map[string]string, len(canonical.Ports))
		for _, port := range canonical.Ports {
			ports[port.ID] = port.NodeID
			portNodes[port.NodeID] = true
		}
		for _, position := range canonical.Positions {
			for _, connection := range position.Connections {
				key := newTurnoutEdgeKey(topology.TurnoutID, connection.PortAID, connection.PortBID)
				edge := turnoutEdges[key]
				if edge == nil {
					edge = &turnoutEdge{
						edge: Edge{
							Kind:      EdgeKindTurnout,
							NodeAID:   ports[key.portAID],
							NodeBID:   ports[key.portBID],
							TurnoutID: key.turnoutID,
							PortAID:   key.portAID,
							PortBID:   key.portBID,
						},
						positions: map[string]bool{},
					}
					turnoutEdges[key] = edge
				}
				edge.positions[position.PositionID] = true
			}
		}
	}
	sort.Strings(graph.turnoutIDs)
	for _, conditional := range turnoutEdges {
		for positionID := range conditional.positions {
			conditional.edge.PositionIDs = append(conditional.edge.PositionIDs, positionID)
		}
		sort.Strings(conditional.edge.PositionIDs)
		graph.edges = append(graph.edges, conditional.edge)
	}
	sort.Slice(graph.edges, func(i, j int) bool {
		return edgeLess(graph.edges[i], graph.edges[j])
	})

	if err := validateNodeStructure(graph, fixedIncidence, portNodes); err != nil {
		return nil, err
	}
	for index, edge := range graph.edges {
		graph.incident[edge.NodeAID] = append(graph.incident[edge.NodeAID], index)
		if edge.NodeBID != edge.NodeAID {
			graph.incident[edge.NodeBID] = append(graph.incident[edge.NodeBID], index)
		}
	}
	return graph, nil
}

func (g *Graph) Node(id string) (model.TopologyNode, bool) {
	if g == nil {
		return model.TopologyNode{}, false
	}
	node, exists := g.nodes[id]
	return node, exists
}

func (g *Graph) Nodes() []model.TopologyNode {
	if g == nil {
		return nil
	}
	nodes := make([]model.TopologyNode, 0, len(g.nodeIDs))
	for _, id := range g.nodeIDs {
		nodes = append(nodes, g.nodes[id])
	}
	return nodes
}

func (g *Graph) TrackSection(id string) (model.TrackSection, bool) {
	if g == nil {
		return model.TrackSection{}, false
	}
	section, exists := g.sections[id]
	return section, exists
}

func (g *Graph) TrackSections() []model.TrackSection {
	if g == nil {
		return nil
	}
	sections := make([]model.TrackSection, 0, len(g.sectionIDs))
	for _, id := range g.sectionIDs {
		sections = append(sections, g.sections[id])
	}
	return sections
}

func (g *Graph) TurnoutTopology(id string) (model.TurnoutTopology, bool) {
	if g == nil {
		return model.TurnoutTopology{}, false
	}
	topology, exists := g.turnoutTopologies[id]
	return cloneTurnoutTopology(topology), exists
}

func (g *Graph) TurnoutTopologies() []model.TurnoutTopology {
	if g == nil {
		return nil
	}
	topologies := make([]model.TurnoutTopology, 0, len(g.turnoutIDs))
	for _, id := range g.turnoutIDs {
		topologies = append(topologies, cloneTurnoutTopology(g.turnoutTopologies[id]))
	}
	return topologies
}

func (g *Graph) Edges() []Edge {
	if g == nil {
		return nil
	}
	edges := make([]Edge, len(g.edges))
	for index, edge := range g.edges {
		edges[index] = cloneEdge(edge)
	}
	return edges
}

func (g *Graph) IncidentEdges(nodeID string) []Edge {
	if g == nil {
		return nil
	}
	indexes, exists := g.incident[nodeID]
	if !exists {
		return nil
	}
	edges := make([]Edge, 0, len(indexes))
	for _, index := range indexes {
		edges = append(edges, cloneEdge(g.edges[index]))
	}
	return edges
}

func (g *Graph) ConnectedComponents() [][]string {
	if g == nil {
		return nil
	}
	visited := make(map[string]bool, len(g.nodes))
	components := make([][]string, 0)
	for _, start := range g.nodeIDs {
		if visited[start] {
			continue
		}
		visited[start] = true
		queue := []string{start}
		component := make([]string, 0)
		for head := 0; head < len(queue); head++ {
			nodeID := queue[head]
			component = append(component, nodeID)
			for _, edgeIndex := range g.incident[nodeID] {
				next, _ := g.edges[edgeIndex].Other(nodeID)
				if !visited[next] {
					visited[next] = true
					queue = append(queue, next)
				}
			}
		}
		sort.Strings(component)
		components = append(components, component)
	}
	return components
}

func validateNodeStructure(graph *Graph, fixedIncidence map[string]int, portNodes map[string]bool) error {
	for _, nodeID := range graph.nodeIDs {
		node := graph.nodes[nodeID]
		fixedCount := fixedIncidence[nodeID]
		switch node.Kind {
		case model.TopologyNodeJoint:
			if fixedCount > 2 && !portNodes[nodeID] {
				return fmt.Errorf("%w: joint node %q has %d fixed track sections without a turnout", ErrInvalidGraph, nodeID, fixedCount)
			}
		case model.TopologyNodeBuffer:
			if fixedCount > 1 {
				return fmt.Errorf("%w: buffer node %q has %d fixed track sections", ErrInvalidGraph, nodeID, fixedCount)
			}
			if portNodes[nodeID] {
				return fmt.Errorf("%w: buffer node %q is a turnout port", ErrInvalidGraph, nodeID)
			}
		case model.TopologyNodeBoundary:
			if fixedCount > 1 {
				return fmt.Errorf("%w: boundary node %q has %d fixed track sections", ErrInvalidGraph, nodeID, fixedCount)
			}
		}
	}
	return nil
}

func canonicalTurnoutTopology(topology model.TurnoutTopology) model.TurnoutTopology {
	canonical := cloneTurnoutTopology(topology)
	sort.Slice(canonical.Ports, func(i, j int) bool {
		return canonical.Ports[i].ID < canonical.Ports[j].ID
	})
	for positionIndex := range canonical.Positions {
		for connectionIndex, connection := range canonical.Positions[positionIndex].Connections {
			if connection.PortAID > connection.PortBID {
				connection.PortAID, connection.PortBID = connection.PortBID, connection.PortAID
				canonical.Positions[positionIndex].Connections[connectionIndex] = connection
			}
		}
		sort.Slice(canonical.Positions[positionIndex].Connections, func(i, j int) bool {
			a := canonical.Positions[positionIndex].Connections[i]
			b := canonical.Positions[positionIndex].Connections[j]
			if a.PortAID != b.PortAID {
				return a.PortAID < b.PortAID
			}
			return a.PortBID < b.PortBID
		})
	}
	sort.Slice(canonical.Positions, func(i, j int) bool {
		return canonical.Positions[i].PositionID < canonical.Positions[j].PositionID
	})
	return canonical
}

func cloneTurnoutTopology(topology model.TurnoutTopology) model.TurnoutTopology {
	clone := topology
	clone.Ports = append([]model.TurnoutPort(nil), topology.Ports...)
	clone.Positions = make([]model.TurnoutTopologyPosition, len(topology.Positions))
	for index, position := range topology.Positions {
		clone.Positions[index] = position
		clone.Positions[index].Connections = append([]model.PortConnection(nil), position.Connections...)
	}
	return clone
}

func cloneEdge(edge Edge) Edge {
	edge.PositionIDs = append([]string(nil), edge.PositionIDs...)
	return edge
}

func edgeLess(a, b Edge) bool {
	if a.Kind != b.Kind {
		return a.Kind == EdgeKindTrackSection
	}
	if a.Kind == EdgeKindTrackSection {
		return a.TrackSectionID < b.TrackSectionID
	}
	if a.TurnoutID != b.TurnoutID {
		return a.TurnoutID < b.TurnoutID
	}
	if a.PortAID != b.PortAID {
		return a.PortAID < b.PortAID
	}
	return a.PortBID < b.PortBID
}

type turnoutEdgeKey struct {
	turnoutID string
	portAID   string
	portBID   string
}

func newTurnoutEdgeKey(turnoutID, portAID, portBID string) turnoutEdgeKey {
	if portAID > portBID {
		portAID, portBID = portBID, portAID
	}
	return turnoutEdgeKey{turnoutID: turnoutID, portAID: portAID, portBID: portBID}
}

type turnoutEdge struct {
	edge      Edge
	positions map[string]bool
}
