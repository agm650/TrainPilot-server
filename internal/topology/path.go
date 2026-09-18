package topology

import (
	"sort"
	"strconv"
	"strings"

	"github.com/agm650/TrainPilot-server/internal/station"
)

type PathConstraints struct {
	ExcludedTrackSections    map[string]bool
	ExcludedTurnouts         map[string]bool
	ExcludedBlocks           map[string]bool
	RequiredTurnoutPositions map[string]string
}

type SectionTraversal struct {
	TrackSectionID string
	FromNodeID     string
	ToNodeID       string
}

type TurnoutTraversal struct {
	TurnoutID           string
	FromNodeID          string
	ToNodeID            string
	EntryPortID         string
	ExitPortID          string
	RequiredPositionID  string
	PossiblePositionIDs []string
	ActivePositionID    string
	Quality             station.AccessoryReportQuality
}

type Traversal struct {
	Kind         EdgeKind
	TrackSection *SectionTraversal
	Turnout      *TurnoutTraversal
}

type PathCostModel string

const PathCostTraversalCount PathCostModel = "traversal_count"

type PhysicalPath struct {
	FromNodeID string
	ToNodeID   string
	CostModel  PathCostModel
	Cost       int
	Traversals []Traversal
}

type Path = PhysicalPath

func (g *Graph) FindPath(fromNodeID, toNodeID string, constraints PathConstraints) (Path, bool) {
	if g == nil {
		return Path{}, false
	}
	return findPath(pathView{
		static: g,
		nodeExists: func(nodeID string) bool {
			_, exists := g.nodes[nodeID]
			return exists
		},
		incident: func(nodeID string) []pathEdge {
			indexes := g.incident[nodeID]
			edges := make([]pathEdge, 0, len(indexes))
			for _, index := range indexes {
				edges = append(edges, pathEdge{edge: g.edges[index]})
			}
			return edges
		},
	}, fromNodeID, toNodeID, constraints)
}

func (g *Graph) FindPhysicalPath(fromNodeID, toNodeID string, constraints PathConstraints) (PhysicalPath, bool) {
	return g.FindPath(fromNodeID, toNodeID, constraints)
}

func (g *Graph) Reachable(fromNodeID, toNodeID string, constraints PathConstraints) bool {
	_, reachable := g.FindPath(fromNodeID, toNodeID, constraints)
	return reachable
}

func (g *ActiveGraph) FindPath(fromNodeID, toNodeID string, constraints PathConstraints) (Path, bool) {
	if g == nil || g.static == nil {
		return Path{}, false
	}
	return findPath(pathView{
		static: g.static,
		nodeExists: func(nodeID string) bool {
			_, exists := g.static.nodes[nodeID]
			return exists
		},
		incident: func(nodeID string) []pathEdge {
			indexes := g.incident[nodeID]
			edges := make([]pathEdge, 0, len(indexes))
			for _, index := range indexes {
				active := g.edges[index]
				edges = append(edges, pathEdge{
					edge:           active.Edge,
					activePosition: active.ReportedPosition,
					quality:        active.Quality,
				})
			}
			return edges
		},
	}, fromNodeID, toNodeID, constraints)
}

func (g *ActiveGraph) FindActivePath(fromNodeID, toNodeID string, constraints PathConstraints) (PhysicalPath, bool) {
	return g.FindPath(fromNodeID, toNodeID, constraints)
}

func (g *ActiveGraph) Reachable(fromNodeID, toNodeID string, constraints PathConstraints) bool {
	_, reachable := g.FindPath(fromNodeID, toNodeID, constraints)
	return reachable
}

type pathEdge struct {
	edge           Edge
	activePosition string
	quality        station.AccessoryReportQuality
}

type pathView struct {
	static     *Graph
	nodeExists func(string) bool
	incident   func(string) []pathEdge
}

type pathState struct {
	nodeID       string
	requirements map[string][]string
	key          string
}

type pathPredecessor struct {
	previousKey string
	fromNodeID  string
	edge        pathEdge
}

func findPath(view pathView, fromNodeID, toNodeID string, constraints PathConstraints) (Path, bool) {
	if !view.nodeExists(fromNodeID) || !view.nodeExists(toNodeID) {
		return Path{}, false
	}
	path := Path{FromNodeID: fromNodeID, ToNodeID: toNodeID, CostModel: PathCostTraversalCount}
	if fromNodeID == toNodeID {
		return path, true
	}

	start := pathState{nodeID: fromNodeID, requirements: map[string][]string{}}
	start.key = pathStateKey(start.nodeID, start.requirements)
	queue := []pathState{start}
	seen := map[string]bool{start.key: true}
	predecessors := map[string]pathPredecessor{}

	for head := 0; head < len(queue); head++ {
		current := queue[head]
		for _, candidate := range view.incident(current.nodeID) {
			if edgeExcluded(view.static, candidate.edge, constraints) {
				continue
			}
			nextNodeID, exists := candidate.edge.Other(current.nodeID)
			if !exists {
				continue
			}
			requirements, compatible := extendRequirements(current.requirements, candidate, constraints)
			if !compatible {
				continue
			}
			next := pathState{nodeID: nextNodeID, requirements: requirements}
			next.key = pathStateKey(next.nodeID, next.requirements)
			if seen[next.key] {
				continue
			}
			seen[next.key] = true
			predecessors[next.key] = pathPredecessor{
				previousKey: current.key,
				fromNodeID:  current.nodeID,
				edge:        candidate,
			}
			if next.nodeID == toNodeID {
				return buildPath(path, start.key, next, predecessors), true
			}
			queue = append(queue, next)
		}
	}
	return Path{}, false
}

func buildPath(path Path, startKey string, target pathState, predecessors map[string]pathPredecessor) Path {
	type step struct {
		fromNodeID string
		toNodeID   string
		edge       pathEdge
	}
	var reversed []step
	currentKey := target.key
	toNodeID := target.nodeID
	for currentKey != startKey {
		predecessor := predecessors[currentKey]
		reversed = append(reversed, step{
			fromNodeID: predecessor.fromNodeID,
			toNodeID:   toNodeID,
			edge:       predecessor.edge,
		})
		toNodeID = predecessor.fromNodeID
		currentKey = predecessor.previousKey
	}
	path.Traversals = make([]Traversal, len(reversed))
	for index := range reversed {
		step := reversed[len(reversed)-1-index]
		path.Traversals[index] = traversalFromStep(step.edge, step.fromNodeID, step.toNodeID, target.requirements)
	}
	path.Cost = len(path.Traversals)
	return path
}

func traversalFromStep(candidate pathEdge, fromNodeID, toNodeID string, requirements map[string][]string) Traversal {
	edge := candidate.edge
	if edge.Kind == EdgeKindTrackSection {
		return Traversal{
			Kind: EdgeKindTrackSection,
			TrackSection: &SectionTraversal{
				TrackSectionID: edge.TrackSectionID,
				FromNodeID:     fromNodeID,
				ToNodeID:       toNodeID,
			},
		}
	}
	entryPortID, exitPortID := edge.PortAID, edge.PortBID
	if fromNodeID == edge.NodeBID {
		entryPortID, exitPortID = exitPortID, entryPortID
	}
	possible := append([]string(nil), requirements[edge.TurnoutID]...)
	required := ""
	if len(possible) > 0 {
		required = possible[0]
	}
	return Traversal{
		Kind: EdgeKindTurnout,
		Turnout: &TurnoutTraversal{
			TurnoutID:           edge.TurnoutID,
			FromNodeID:          fromNodeID,
			ToNodeID:            toNodeID,
			EntryPortID:         entryPortID,
			ExitPortID:          exitPortID,
			RequiredPositionID:  required,
			PossiblePositionIDs: possible,
			ActivePositionID:    candidate.activePosition,
			Quality:             candidate.quality,
		},
	}
}

func extendRequirements(current map[string][]string, candidate pathEdge, constraints PathConstraints) (map[string][]string, bool) {
	if candidate.edge.Kind != EdgeKindTurnout {
		return current, true
	}
	allowed := candidate.edge.PositionIDs
	if candidate.activePosition != "" {
		allowed = []string{candidate.activePosition}
	}
	if required := constraints.RequiredTurnoutPositions[candidate.edge.TurnoutID]; required != "" {
		allowed = intersectSortedStrings(allowed, []string{required})
		if len(allowed) == 0 {
			return nil, false
		}
	}
	if existing, exists := current[candidate.edge.TurnoutID]; exists {
		allowed = intersectSortedStrings(existing, allowed)
		if len(allowed) == 0 {
			return nil, false
		}
	}
	next := make(map[string][]string, len(current)+1)
	for turnoutID, positions := range current {
		next[turnoutID] = positions
	}
	next[candidate.edge.TurnoutID] = allowed
	return next, true
}

func intersectSortedStrings(a, b []string) []string {
	result := make([]string, 0)
	for aIndex, bIndex := 0, 0; aIndex < len(a) && bIndex < len(b); {
		switch {
		case a[aIndex] < b[bIndex]:
			aIndex++
		case a[aIndex] > b[bIndex]:
			bIndex++
		default:
			result = append(result, a[aIndex])
			aIndex++
			bIndex++
		}
	}
	return result
}

func pathStateKey(nodeID string, requirements map[string][]string) string {
	turnoutIDs := make([]string, 0, len(requirements))
	for turnoutID := range requirements {
		turnoutIDs = append(turnoutIDs, turnoutID)
	}
	sort.Strings(turnoutIDs)
	var key strings.Builder
	key.WriteString(strconv.Quote(nodeID))
	for _, turnoutID := range turnoutIDs {
		key.WriteByte('|')
		key.WriteString(strconv.Quote(turnoutID))
		key.WriteByte('=')
		for _, positionID := range requirements[turnoutID] {
			key.WriteString(strconv.Quote(positionID))
			key.WriteByte(',')
		}
	}
	return key.String()
}

func edgeExcluded(graph *Graph, edge Edge, constraints PathConstraints) bool {
	switch edge.Kind {
	case EdgeKindTrackSection:
		if constraints.ExcludedTrackSections[edge.TrackSectionID] {
			return true
		}
		if blockID := graph.blockBySection[edge.TrackSectionID]; blockID != "" && constraints.ExcludedBlocks[blockID] {
			return true
		}
	case EdgeKindTurnout:
		if constraints.ExcludedTurnouts[edge.TurnoutID] {
			return true
		}
		if blockID := graph.blockByTurnout[edge.TurnoutID]; blockID != "" && constraints.ExcludedBlocks[blockID] {
			return true
		}
	}
	return false
}
