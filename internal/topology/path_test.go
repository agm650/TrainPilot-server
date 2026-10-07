package topology_test

import (
	"reflect"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/model/topologyfixture"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/topology"
)

func TestFindPathOrientsBidirectionalSection(t *testing.T) {
	layout := lineLayout()
	graph := buildGraph(t, layout)

	forward, found := graph.FindPath("a", "b", topology.PathConstraints{})
	if !found || forward.CostModel != topology.PathCostTraversalCount || forward.Cost != 1 {
		t.Fatalf("forward path = %+v,%v", forward, found)
	}
	assertSectionTraversal(t, forward.Traversals[0], "line", "a", "b")

	reverse, found := graph.FindPhysicalPath("b", "a", topology.PathConstraints{})
	if !found || reverse.Cost != 1 {
		t.Fatalf("reverse path = %+v,%v", reverse, found)
	}
	assertSectionTraversal(t, reverse.Traversals[0], "line", "b", "a")
	if !graph.Reachable("a", "b", topology.PathConstraints{}) {
		t.Fatal("Reachable(a,b) = false")
	}
}

func TestFindPathHandlesDeadEndDisconnectedGraphAndCycle(t *testing.T) {
	deadEnd := model.LayoutDefinition{
		TopologyNodes: []model.TopologyNode{node("joint", model.TopologyNodeJoint), node("buffer", model.TopologyNodeBuffer)},
		TrackSections: []model.TrackSection{section("siding", "joint", "buffer")},
	}
	if path, found := buildGraph(t, deadEnd).FindPath("joint", "buffer", topology.PathConstraints{}); !found || path.Cost != 1 {
		t.Fatalf("dead-end path = %+v,%v", path, found)
	}

	disconnected := model.LayoutDefinition{
		TopologyNodes: []model.TopologyNode{
			node("a", model.TopologyNodeBoundary), node("b", model.TopologyNodeBoundary),
			node("c", model.TopologyNodeBoundary), node("d", model.TopologyNodeBoundary),
		},
		TrackSections: []model.TrackSection{section("ab", "a", "b"), section("cd", "c", "d")},
	}
	if path, found := buildGraph(t, disconnected).FindPath("a", "d", topology.PathConstraints{}); found {
		t.Fatalf("disconnected path = %+v", path)
	}

	cycle := model.LayoutDefinition{
		TopologyNodes: []model.TopologyNode{
			node("a", model.TopologyNodeJoint), node("b", model.TopologyNodeJoint), node("c", model.TopologyNodeJoint),
		},
		TrackSections: []model.TrackSection{
			section("ab", "a", "b"), section("bc", "b", "c"), section("ca", "c", "a"),
		},
	}
	path, found := buildGraph(t, cycle).FindPath("a", "c", topology.PathConstraints{})
	if !found || path.Cost != 1 {
		t.Fatalf("cycle path = %+v,%v", path, found)
	}
	assertSectionTraversal(t, path.Traversals[0], "ca", "a", "c")
}

func TestFindPathChoosesDeterministicAlternative(t *testing.T) {
	layout := alternativePathsLayout()
	graph := buildGraph(t, layout)
	var first topology.Path
	for iteration := 0; iteration < 100; iteration++ {
		path, found := graph.FindPath("a", "d", topology.PathConstraints{})
		if !found || path.Cost != 2 {
			t.Fatalf("iteration %d path = %+v,%v", iteration, path, found)
		}
		if iteration == 0 {
			first = path
			continue
		}
		if !reflect.DeepEqual(path, first) {
			t.Fatalf("iteration %d changed path:\nfirst=%+v\n got=%+v", iteration, first, path)
		}
	}
	if got := traversedSectionIDs(first); !reflect.DeepEqual(got, []string{"a-via-b", "b-to-d"}) {
		t.Fatalf("selected sections = %+v", got)
	}
}

func TestStaticPathReturnsTurnoutRequirements(t *testing.T) {
	tests := []struct {
		name            string
		layout          model.LayoutDefinition
		fromNodeID      string
		toNodeID        string
		wantTurnoutID   string
		wantRequired    string
		wantPossible    []string
		wantEntryPortID string
		wantExitPortID  string
	}{
		{
			name: "simple", layout: topologyfixture.Simple(),
			fromNodeID: "simple-stem", toNodeID: "simple-diverging",
			wantTurnoutID: "simple", wantRequired: "diverging", wantPossible: []string{"diverging"},
			wantEntryPortID: "stem", wantExitPortID: "diverging",
		},
		{
			name: "triple", layout: topologyfixture.ThreeWay(),
			fromNodeID: "triple-stem", toNodeID: "triple-right",
			wantTurnoutID: "triple", wantRequired: "right", wantPossible: []string{"right"},
			wantEntryPortID: "stem", wantExitPortID: "right",
		},
		{
			name: "double slip", layout: topologyfixture.DoubleSlip(),
			fromNodeID: "double-slip-a", toNodeID: "double-slip-c",
			wantTurnoutID: "double-slip", wantRequired: "route_a", wantPossible: []string{"route_a", "route_b"},
			wantEntryPortID: "a", wantExitPortID: "c",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path, found := buildGraph(t, test.layout).FindPath(test.fromNodeID, test.toNodeID, topology.PathConstraints{})
			if !found || len(path.Traversals) != 1 || path.Traversals[0].Turnout == nil {
				t.Fatalf("path = %+v,%v", path, found)
			}
			traversal := path.Traversals[0].Turnout
			if traversal.TurnoutID != test.wantTurnoutID || traversal.RequiredPositionID != test.wantRequired ||
				traversal.EntryPortID != test.wantEntryPortID || traversal.ExitPortID != test.wantExitPortID ||
				!reflect.DeepEqual(traversal.PossiblePositionIDs, test.wantPossible) {
				t.Fatalf("turnout traversal = %+v", traversal)
			}
		})
	}
}

func TestStaticPathRejectsConflictingPositionsOnSameTurnout(t *testing.T) {
	layout := topologyfixture.Simple()
	if path, found := buildGraph(t, layout).FindPath("simple-diverging", "simple-straight", topology.PathConstraints{}); found {
		t.Fatalf("conflicting turnout path = %+v", path)
	}
}

func TestTurnoutTraversalIsOrientedInReverse(t *testing.T) {
	layout := topologyfixture.Simple()
	path, found := buildGraph(t, layout).FindPath("simple-diverging", "simple-stem", topology.PathConstraints{})
	if !found || len(path.Traversals) != 1 || path.Traversals[0].Turnout == nil {
		t.Fatalf("reverse turnout path = %+v,%v", path, found)
	}
	traversal := path.Traversals[0].Turnout
	if traversal.FromNodeID != "simple-diverging" || traversal.ToNodeID != "simple-stem" ||
		traversal.EntryPortID != "diverging" || traversal.ExitPortID != "stem" {
		t.Fatalf("reverse turnout traversal = %+v", traversal)
	}
}

func TestActivePathUsesOnlyConfirmedConnections(t *testing.T) {
	layout := topologyfixture.Simple()
	graph := buildGraph(t, layout)
	constraints := topology.PathConstraints{}
	if _, found := graph.FindPath("simple-stem", "simple-diverging", constraints); !found {
		t.Fatal("static path not found")
	}
	if path, found := graph.ActiveView(nil).FindActivePath("simple-stem", "simple-diverging", constraints); found {
		t.Fatalf("unknown turnout active path = %+v", path)
	}

	turnout := runtimeTurnout(layout.Turnouts[0], "straight", "diverging", station.AccessoryReportKnown, false, station.AccessoryReportPhysical)
	active := graph.ActiveView(map[string]model.Turnout{turnout.ID: turnout})
	path, found := active.FindPath("simple-stem", "simple-diverging", constraints)
	if !found || !active.Reachable("simple-stem", "simple-diverging", constraints) {
		t.Fatalf("confirmed active path = %+v,%v", path, found)
	}
	traversal := path.Traversals[0].Turnout
	if traversal.ActivePositionID != "diverging" || traversal.RequiredPositionID != "diverging" || traversal.Quality != station.AccessoryReportPhysical {
		t.Fatalf("active turnout traversal = %+v", traversal)
	}
}

func TestPathConstraintsExcludeSectionsTurnoutsAndBlocks(t *testing.T) {
	line := lineLayout()
	line.Blocks = []model.BlockDefinition{{ID: "detected", Name: "Detected", TrackSectionIDs: []string{"line"}}}
	graph := buildGraph(t, line)
	for name, constraints := range map[string]topology.PathConstraints{
		"section": {ExcludedTrackSections: map[string]bool{"line": true}},
		"block":   {ExcludedBlocks: map[string]bool{"detected": true}},
	} {
		t.Run(name, func(t *testing.T) {
			if path, found := graph.FindPath("a", "b", constraints); found {
				t.Fatalf("excluded path = %+v", path)
			}
		})
	}
	if _, found := graph.FindPath("a", "b", topology.PathConstraints{}); !found {
		t.Fatal("block membership was treated as an automatic exclusion")
	}

	turnoutLayout := topologyfixture.Simple()
	turnoutGraph := buildGraph(t, turnoutLayout)
	constraints := topology.PathConstraints{ExcludedTurnouts: map[string]bool{"simple": true}}
	if path, found := turnoutGraph.FindPath("simple-stem", "simple-straight", constraints); found {
		t.Fatalf("excluded turnout path = %+v", path)
	}
}

func TestPassingStationFixtureSupportsLoopAndSiding(t *testing.T) {
	layout := topologyfixture.PassingStation()
	graph := buildGraph(t, layout)
	path, found := graph.FindPath("west-boundary", "east-boundary", topology.PathConstraints{})
	if !found || path.Cost != 5 {
		t.Fatalf("station through path = %+v,%v", path, found)
	}
	if got := traversedSectionIDs(path); !reflect.DeepEqual(got, []string{"approach-west", "passing-loop", "approach-east"}) {
		t.Fatalf("station through sections = %+v", got)
	}
	requirements := turnoutRequirements(path)
	wantRequirements := map[string]string{"station-west": "diverging", "station-east": "diverging"}
	if !reflect.DeepEqual(requirements, wantRequirements) {
		t.Fatalf("station turnout requirements = %+v", requirements)
	}

	siding, found := graph.FindPath("west-boundary", "siding-buffer", topology.PathConstraints{})
	if !found || len(siding.Traversals) == 0 {
		t.Fatalf("station siding path = %+v,%v", siding, found)
	}
	last := siding.Traversals[len(siding.Traversals)-1]
	assertSectionTraversal(t, last, "siding", "station-branch-diverging", "siding-buffer")
}

func lineLayout() model.LayoutDefinition {
	return model.LayoutDefinition{
		TopologyNodes: []model.TopologyNode{node("a", model.TopologyNodeBoundary), node("b", model.TopologyNodeBoundary)},
		TrackSections: []model.TrackSection{section("line", "a", "b")},
	}
}

func alternativePathsLayout() model.LayoutDefinition {
	return model.LayoutDefinition{
		TopologyNodes: []model.TopologyNode{
			node("a", model.TopologyNodeJoint), node("b", model.TopologyNodeJoint),
			node("c", model.TopologyNodeJoint), node("d", model.TopologyNodeJoint),
		},
		TrackSections: []model.TrackSection{
			section("a-via-b", "a", "b"), section("b-to-d", "b", "d"),
			section("c-to-d", "c", "d"), section("z-via-c", "a", "c"),
		},
	}
}

func assertSectionTraversal(t *testing.T, traversal topology.Traversal, sectionID, fromNodeID, toNodeID string) {
	t.Helper()
	if traversal.Kind != topology.EdgeKindTrackSection || traversal.TrackSection == nil || traversal.Turnout != nil {
		t.Fatalf("traversal = %+v, want track section", traversal)
	}
	got := traversal.TrackSection
	if got.TrackSectionID != sectionID || got.FromNodeID != fromNodeID || got.ToNodeID != toNodeID {
		t.Fatalf("section traversal = %+v, want %s %s->%s", got, sectionID, fromNodeID, toNodeID)
	}
}

func traversedSectionIDs(path topology.Path) []string {
	var ids []string
	for _, traversal := range path.Traversals {
		if traversal.TrackSection != nil {
			ids = append(ids, traversal.TrackSection.TrackSectionID)
		}
	}
	return ids
}

func turnoutRequirements(path topology.Path) map[string]string {
	requirements := map[string]string{}
	for _, traversal := range path.Traversals {
		if traversal.Turnout != nil {
			requirements[traversal.Turnout.TurnoutID] = traversal.Turnout.RequiredPositionID
		}
	}
	return requirements
}
