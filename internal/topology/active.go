package topology

import (
	"sort"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station"
)

type ActiveEdge struct {
	Edge
	ReportedPosition string
	Quality          station.AccessoryReportQuality
}

type ActiveGraph struct {
	static   *Graph
	edges    []ActiveEdge
	incident map[string][]int
}

// ActiveView returns a snapshot containing fixed track and only the turnout
// connections confirmed by reported state. It never mutates the static graph.
func (g *Graph) ActiveView(turnouts map[string]model.Turnout) *ActiveGraph {
	active := &ActiveGraph{
		static:   g,
		incident: map[string][]int{},
	}
	if g == nil {
		return active
	}
	active.incident = make(map[string][]int, len(g.nodeIDs))
	for _, nodeID := range g.nodeIDs {
		active.incident[nodeID] = nil
	}
	for _, edge := range g.edges {
		activeEdge := ActiveEdge{Edge: cloneEdge(edge)}
		if edge.Kind == EdgeKindTurnout {
			turnout, exists := turnouts[edge.TurnoutID]
			if !exists || !turnoutConnectionConfirmed(edge, turnout) {
				continue
			}
			activeEdge.ReportedPosition = turnout.ReportedPosition
			activeEdge.Quality = turnout.Quality
		}
		index := len(active.edges)
		active.edges = append(active.edges, activeEdge)
		active.incident[edge.NodeAID] = append(active.incident[edge.NodeAID], index)
		if edge.NodeBID != edge.NodeAID {
			active.incident[edge.NodeBID] = append(active.incident[edge.NodeBID], index)
		}
	}
	return active
}

func (g *ActiveGraph) Node(id string) (model.TopologyNode, bool) {
	if g == nil || g.static == nil {
		return model.TopologyNode{}, false
	}
	return g.static.Node(id)
}

func (g *ActiveGraph) Nodes() []model.TopologyNode {
	if g == nil || g.static == nil {
		return nil
	}
	return g.static.Nodes()
}

func (g *ActiveGraph) Edges() []ActiveEdge {
	if g == nil {
		return nil
	}
	edges := make([]ActiveEdge, len(g.edges))
	for index, edge := range g.edges {
		edges[index] = cloneActiveEdge(edge)
	}
	return edges
}

func (g *ActiveGraph) IncidentEdges(nodeID string) []ActiveEdge {
	if g == nil {
		return nil
	}
	indexes, exists := g.incident[nodeID]
	if !exists {
		return nil
	}
	edges := make([]ActiveEdge, 0, len(indexes))
	for _, index := range indexes {
		edges = append(edges, cloneActiveEdge(g.edges[index]))
	}
	return edges
}

func (g *ActiveGraph) ConnectedComponents() [][]string {
	if g == nil || g.static == nil {
		return nil
	}
	visited := make(map[string]bool, len(g.static.nodes))
	components := make([][]string, 0)
	for _, start := range g.static.nodeIDs {
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

func turnoutConnectionConfirmed(edge Edge, turnout model.Turnout) bool {
	if turnout.Pending || turnout.ReportedStatus != station.AccessoryReportKnown || turnout.ReportedPosition == "" {
		return false
	}
	index := sort.SearchStrings(edge.PositionIDs, turnout.ReportedPosition)
	return index < len(edge.PositionIDs) && edge.PositionIDs[index] == turnout.ReportedPosition
}

func cloneActiveEdge(edge ActiveEdge) ActiveEdge {
	edge.Edge = cloneEdge(edge.Edge)
	return edge
}
