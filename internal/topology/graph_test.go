package topology_test

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/model/topologyfixture"
	"github.com/agm650/TrainPilot-server/internal/topology"
)

func TestBuildSimpleBidirectionalLine(t *testing.T) {
	layout := model.LayoutDefinition{
		TopologyNodes: []model.TopologyNode{
			node("b", model.TopologyNodeBoundary),
			node("a", model.TopologyNodeBoundary),
		},
		TrackSections: []model.TrackSection{{
			ID: "line", Name: "Line", NodeAID: "a", NodeBID: "b", LengthMM: 1200,
		}},
	}
	graph := buildGraph(t, layout)
	if got := graph.Nodes(); len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
		t.Fatalf("nodes are not deterministically ordered: %+v", got)
	}
	if _, exists := graph.Node("a"); !exists {
		t.Fatal("node index does not contain a")
	}
	if _, exists := graph.TrackSection("line"); !exists {
		t.Fatal("track section index does not contain line")
	}
	edges := graph.Edges()
	if len(edges) != 1 {
		t.Fatalf("edges = %d, want 1", len(edges))
	}
	edge := edges[0]
	if edge.Kind != topology.EdgeKindTrackSection || edge.TrackSectionID != "line" || edge.LengthMM != 1200 {
		t.Fatalf("unexpected track section edge: %+v", edge)
	}
	if other, ok := edge.Other("a"); !ok || other != "b" {
		t.Fatalf("Other(a) = %q,%v, want b,true", other, ok)
	}
	if other, ok := edge.Other("b"); !ok || other != "a" {
		t.Fatalf("Other(b) = %q,%v, want a,true", other, ok)
	}
	if len(graph.IncidentEdges("a")) != 1 || len(graph.IncidentEdges("b")) != 1 {
		t.Fatal("track section is not indexed at both endpoints")
	}
}

func TestBuildSupportsDeadEndParallelTracksAndCycle(t *testing.T) {
	tests := []struct {
		name      string
		layout    model.LayoutDefinition
		edgeCount int
	}{
		{
			name: "dead end",
			layout: model.LayoutDefinition{
				TopologyNodes: []model.TopologyNode{node("joint", model.TopologyNodeJoint), node("buffer", model.TopologyNodeBuffer)},
				TrackSections: []model.TrackSection{section("siding", "joint", "buffer")},
			},
			edgeCount: 1,
		},
		{
			name: "parallel tracks",
			layout: model.LayoutDefinition{
				TopologyNodes: []model.TopologyNode{node("a", model.TopologyNodeJoint), node("b", model.TopologyNodeJoint)},
				TrackSections: []model.TrackSection{section("second", "a", "b"), section("first", "a", "b")},
			},
			edgeCount: 2,
		},
		{
			name: "cycle",
			layout: model.LayoutDefinition{
				TopologyNodes: []model.TopologyNode{
					node("a", model.TopologyNodeJoint), node("b", model.TopologyNodeJoint),
					node("c", model.TopologyNodeJoint), node("d", model.TopologyNodeJoint),
				},
				TrackSections: []model.TrackSection{
					section("ab", "a", "b"), section("bc", "b", "c"),
					section("cd", "c", "d"), section("da", "d", "a"),
				},
			},
			edgeCount: 4,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			graph := buildGraph(t, test.layout)
			if len(graph.Edges()) != test.edgeCount {
				t.Fatalf("edges = %d, want %d", len(graph.Edges()), test.edgeCount)
			}
			components := graph.ConnectedComponents()
			if len(components) != 1 {
				t.Fatalf("components = %+v, want one", components)
			}
		})
	}
}

func TestConnectedComponentsKeepFixedCrossingDisconnected(t *testing.T) {
	layout := model.LayoutDefinition{
		TopologyNodes: []model.TopologyNode{
			node("a", model.TopologyNodeJoint), node("b", model.TopologyNodeJoint),
			node("c", model.TopologyNodeJoint), node("d", model.TopologyNodeJoint),
			node("isolated", model.TopologyNodeJoint),
		},
		TrackSections: []model.TrackSection{
			section("ab", "a", "b"),
			section("cd", "c", "d"),
		},
	}
	components := buildGraph(t, layout).ConnectedComponents()
	want := [][]string{{"a", "b"}, {"c", "d"}, {"isolated"}}
	if !reflect.DeepEqual(components, want) {
		t.Fatalf("ConnectedComponents() = %+v, want %+v", components, want)
	}
}

func TestBuildPreservesConditionalTurnoutEdges(t *testing.T) {
	tests := []struct {
		name      string
		layout    model.LayoutDefinition
		edgeCount int
	}{
		{name: "simple", layout: topologyfixture.Simple(), edgeCount: 2},
		{name: "three way", layout: topologyfixture.ThreeWay(), edgeCount: 3},
		{name: "double slip", layout: topologyfixture.DoubleSlip(), edgeCount: 4},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			graph := buildGraph(t, test.layout)
			if _, exists := graph.TurnoutTopology(test.layout.Turnouts[0].ID); !exists {
				t.Fatalf("turnout topology index does not contain %q", test.layout.Turnouts[0].ID)
			}
			edges := graph.Edges()
			if len(edges) != test.edgeCount {
				t.Fatalf("edges = %d, want %d: %+v", len(edges), test.edgeCount, edges)
			}
			for _, edge := range edges {
				if edge.Kind != topology.EdgeKindTurnout || edge.TurnoutID != test.layout.Turnouts[0].ID || len(edge.PositionIDs) == 0 {
					t.Fatalf("conditional metadata missing from edge: %+v", edge)
				}
			}
		})
	}
}

func TestDoubleSlipUnionRetainsEveryEnablingPosition(t *testing.T) {
	edges := buildGraph(t, topologyfixture.DoubleSlip()).Edges()
	for _, edge := range edges {
		if edge.PortAID == "a" && edge.PortBID == "c" {
			want := []string{"route_a", "route_b"}
			if !reflect.DeepEqual(edge.PositionIDs, want) {
				t.Fatalf("a-c positions = %+v, want %+v", edge.PositionIDs, want)
			}
			return
		}
	}
	t.Fatal("double-slip a-c edge not found")
}

func TestBuildRejectsInvalidNodeStructures(t *testing.T) {
	tests := []struct {
		name   string
		layout model.LayoutDefinition
	}{
		{
			name:   "unexplained fixed branch",
			layout: fixedBranchLayout(model.TopologyNodeJoint),
		},
		{
			name:   "buffer with two sections",
			layout: fixedBranchLayout(model.TopologyNodeBuffer),
		},
		{
			name:   "boundary with two sections",
			layout: fixedBranchLayout(model.TopologyNodeBoundary),
		},
		{
			name: "buffer used as turnout port",
			layout: func() model.LayoutDefinition {
				layout := topologyfixture.Simple()
				layout.TopologyNodes[0].Kind = model.TopologyNodeBuffer
				return layout
			}(),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := topology.Build(test.layout); !errors.Is(err, topology.ErrInvalidGraph) {
				t.Fatalf("Build() error = %v, want ErrInvalidGraph", err)
			}
		})
	}
}

func TestBuildAllowsFixedBranchExplainedByTurnout(t *testing.T) {
	layout := topologyfixture.Simple()
	stemNodeID := layout.TurnoutTopologies[0].Ports[0].NodeID
	for _, id := range []string{"outer-a", "outer-b", "outer-c"} {
		layout.TopologyNodes = append(layout.TopologyNodes, node(id, model.TopologyNodeBoundary))
		layout.TrackSections = append(layout.TrackSections, section("section-"+id, stemNodeID, id))
	}
	if _, err := topology.Build(layout); err != nil {
		t.Fatalf("turnout-explained branch rejected: %v", err)
	}
}

func TestBuildRejectsUnknownReference(t *testing.T) {
	layout := model.LayoutDefinition{
		TopologyNodes: []model.TopologyNode{node("a", model.TopologyNodeJoint)},
		TrackSections: []model.TrackSection{section("invalid", "a", "missing")},
	}
	_, err := topology.Build(layout)
	if !errors.Is(err, topology.ErrInvalidGraph) || !errors.Is(err, model.ErrInvalidTopology) {
		t.Fatalf("Build() error = %v, want graph and model validation errors", err)
	}
}

func TestBuildIsDeterministicAcrossDefinitionOrder(t *testing.T) {
	first := topologyfixture.DoubleSlip()
	second := topologyfixture.DoubleSlip()
	fixedNodes := []model.TopologyNode{
		node("fixed-a", model.TopologyNodeBoundary), node("fixed-b", model.TopologyNodeBoundary),
		node("fixed-c", model.TopologyNodeBoundary), node("fixed-d", model.TopologyNodeBoundary),
	}
	first.TopologyNodes = append(first.TopologyNodes, fixedNodes...)
	second.TopologyNodes = append(second.TopologyNodes, fixedNodes...)
	first.TrackSections = []model.TrackSection{
		section("fixed-z", "fixed-c", "fixed-d"),
		section("fixed-a", "fixed-a", "fixed-b"),
	}
	second.TrackSections = []model.TrackSection{
		section("fixed-a", "fixed-a", "fixed-b"),
		section("fixed-z", "fixed-c", "fixed-d"),
	}
	slices.Reverse(second.TopologyNodes)
	slices.Reverse(second.TurnoutTopologies[0].Ports)
	slices.Reverse(second.TurnoutTopologies[0].Positions)
	for index := range second.TurnoutTopologies[0].Positions {
		slices.Reverse(second.TurnoutTopologies[0].Positions[index].Connections)
		for connectionIndex := range second.TurnoutTopologies[0].Positions[index].Connections {
			connection := &second.TurnoutTopologies[0].Positions[index].Connections[connectionIndex]
			connection.PortAID, connection.PortBID = connection.PortBID, connection.PortAID
		}
	}

	firstGraph := buildGraph(t, first)
	secondGraph := buildGraph(t, second)
	if !reflect.DeepEqual(firstGraph.Nodes(), secondGraph.Nodes()) ||
		!reflect.DeepEqual(firstGraph.TrackSections(), secondGraph.TrackSections()) ||
		!reflect.DeepEqual(firstGraph.TurnoutTopologies(), secondGraph.TurnoutTopologies()) ||
		!reflect.DeepEqual(firstGraph.Edges(), secondGraph.Edges()) ||
		!reflect.DeepEqual(firstGraph.ConnectedComponents(), secondGraph.ConnectedComponents()) {
		t.Fatal("equivalent definitions produced different public graphs")
	}
}

func TestBuildThousandsOfSections(t *testing.T) {
	const sectionCount = 2000
	layout := model.LayoutDefinition{
		TopologyNodes: make([]model.TopologyNode, 0, sectionCount+1),
		TrackSections: make([]model.TrackSection, 0, sectionCount),
	}
	for index := 0; index <= sectionCount; index++ {
		kind := model.TopologyNodeJoint
		if index == 0 || index == sectionCount {
			kind = model.TopologyNodeBoundary
		}
		layout.TopologyNodes = append(layout.TopologyNodes, node(fmt.Sprintf("node-%04d", index), kind))
		if index > 0 {
			layout.TrackSections = append(layout.TrackSections, section(
				fmt.Sprintf("section-%04d", index),
				fmt.Sprintf("node-%04d", index-1),
				fmt.Sprintf("node-%04d", index),
			))
		}
	}
	graph := buildGraph(t, layout)
	if len(graph.Edges()) != sectionCount {
		t.Fatalf("edges = %d, want %d", len(graph.Edges()), sectionCount)
	}
	components := graph.ConnectedComponents()
	if len(components) != 1 || len(components[0]) != sectionCount+1 {
		t.Fatalf("unexpected components: count=%d first-size=%d", len(components), len(components[0]))
	}
}

func TestBlockMembershipSupportsSingleAndContinuousSections(t *testing.T) {
	layout := model.LayoutDefinition{
		TopologyNodes: []model.TopologyNode{
			node("a", model.TopologyNodeBoundary),
			node("b", model.TopologyNodeJoint),
			node("c", model.TopologyNodeBoundary),
		},
		TrackSections: []model.TrackSection{
			section("first", "a", "b"),
			section("second", "b", "c"),
		},
		Blocks: []model.BlockDefinition{
			{ID: "single", Name: "Single", TrackSectionIDs: []string{"first"}},
		},
	}
	graph := buildGraph(t, layout)
	block, exists := graph.BlockForTrackSection("first")
	if !exists || block.ID != "single" {
		t.Fatalf("BlockForTrackSection(first) = %+v,%v", block, exists)
	}
	if _, exists := graph.BlockForTrackSection("second"); exists {
		t.Fatal("unassigned track section unexpectedly has a block")
	}
	if _, exists := graph.BlockForTurnout("missing"); exists {
		t.Fatal("unassigned turnout unexpectedly has a block")
	}

	layout.Blocks[0].TrackSectionIDs = []string{"second", "first"}
	graph = buildGraph(t, layout)
	resources, exists := graph.ResourcesForBlock("single")
	if !exists || !reflect.DeepEqual(resources.TrackSectionIDs, []string{"first", "second"}) {
		t.Fatalf("ResourcesForBlock(single) = %+v,%v", resources, exists)
	}
	resources.TrackSectionIDs[0] = "mutated"
	resources, _ = graph.ResourcesForBlock("single")
	if resources.TrackSectionIDs[0] != "first" {
		t.Fatal("block resource index exposed mutable internal state")
	}
}

func TestBlockMembershipSupportsTurnoutBranchesAndDoubleSlip(t *testing.T) {
	tests := []struct {
		name   string
		layout model.LayoutDefinition
	}{
		{name: "simple turnout branches", layout: layoutWithAttachedSections(topologyfixture.Simple())},
		{name: "double slip", layout: layoutWithAttachedSections(topologyfixture.DoubleSlip())},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			turnoutID := test.layout.Turnouts[0].ID
			sectionIDs := make([]string, 0, len(test.layout.TrackSections))
			for _, trackSection := range test.layout.TrackSections {
				sectionIDs = append(sectionIDs, trackSection.ID)
			}
			test.layout.Blocks = []model.BlockDefinition{{
				ID: "block", Name: "Block", TrackSectionIDs: sectionIDs, TurnoutIDs: []string{turnoutID},
			}}
			graph := buildGraph(t, test.layout)
			block, exists := graph.BlockForTurnout(turnoutID)
			if !exists || block.ID != "block" {
				t.Fatalf("BlockForTurnout(%q) = %+v,%v", turnoutID, block, exists)
			}
		})
	}
}

func TestBlockMembershipRejectsDuplicateAndDisconnectedResources(t *testing.T) {
	continuous := model.LayoutDefinition{
		TopologyNodes: []model.TopologyNode{
			node("a", model.TopologyNodeBoundary), node("b", model.TopologyNodeJoint), node("c", model.TopologyNodeBoundary),
		},
		TrackSections: []model.TrackSection{section("ab", "a", "b"), section("bc", "b", "c")},
	}
	disconnected := model.LayoutDefinition{
		TopologyNodes: []model.TopologyNode{
			node("a", model.TopologyNodeBoundary), node("b", model.TopologyNodeBoundary),
			node("c", model.TopologyNodeBoundary), node("d", model.TopologyNodeBoundary),
		},
		TrackSections: []model.TrackSection{section("ab", "a", "b"), section("cd", "c", "d")},
		Blocks:        []model.BlockDefinition{{ID: "block", Name: "Block", TrackSectionIDs: []string{"ab", "cd"}}},
	}
	doubleSection := continuous
	doubleSection.Blocks = []model.BlockDefinition{
		{ID: "first", Name: "First", TrackSectionIDs: []string{"ab"}},
		{ID: "second", Name: "Second", TrackSectionIDs: []string{"ab"}},
	}
	doubleTurnout := topologyfixture.Simple()
	doubleTurnout.Blocks = []model.BlockDefinition{
		{ID: "first", Name: "First", TurnoutIDs: []string{doubleTurnout.Turnouts[0].ID}},
		{ID: "second", Name: "Second", TurnoutIDs: []string{doubleTurnout.Turnouts[0].ID}},
	}
	for _, test := range []struct {
		name   string
		layout model.LayoutDefinition
	}{
		{name: "duplicate section", layout: doubleSection},
		{name: "duplicate turnout", layout: doubleTurnout},
		{name: "disconnected islands", layout: disconnected},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := topology.Build(test.layout)
			if !errors.Is(err, topology.ErrInvalidGraph) || !errors.Is(err, model.ErrInvalidBlockDefinition) {
				t.Fatalf("Build() error = %v, want graph and block definition errors", err)
			}
		})
	}
}

func TestBlockWithoutMembershipRemainsValid(t *testing.T) {
	layout := model.LayoutDefinition{Blocks: []model.BlockDefinition{{ID: "legacy", Name: "Legacy"}}}
	graph := buildGraph(t, layout)
	resources, exists := graph.ResourcesForBlock("legacy")
	if !exists || len(resources.TrackSectionIDs) != 0 || len(resources.TurnoutIDs) != 0 {
		t.Fatalf("legacy block resources = %+v,%v", resources, exists)
	}
}

func buildGraph(t *testing.T, layout model.LayoutDefinition) *topology.Graph {
	t.Helper()
	graph, err := topology.Build(layout)
	if err != nil {
		t.Fatal(err)
	}
	return graph
}

func node(id string, kind model.TopologyNodeKind) model.TopologyNode {
	return model.TopologyNode{ID: id, Kind: kind}
}

func section(id, nodeAID, nodeBID string) model.TrackSection {
	return model.TrackSection{ID: id, NodeAID: nodeAID, NodeBID: nodeBID}
}

func fixedBranchLayout(kind model.TopologyNodeKind) model.LayoutDefinition {
	return model.LayoutDefinition{
		TopologyNodes: []model.TopologyNode{
			node("center", kind),
			node("a", model.TopologyNodeBoundary),
			node("b", model.TopologyNodeBoundary),
			node("c", model.TopologyNodeBoundary),
		},
		TrackSections: []model.TrackSection{
			section("center-a", "center", "a"),
			section("center-b", "center", "b"),
			section("center-c", "center", "c"),
		},
	}
}

func layoutWithAttachedSections(layout model.LayoutDefinition) model.LayoutDefinition {
	for _, port := range layout.TurnoutTopologies[0].Ports {
		outerID := "outer-" + port.ID
		layout.TopologyNodes = append(layout.TopologyNodes, node(outerID, model.TopologyNodeBoundary))
		layout.TrackSections = append(layout.TrackSections, section("section-"+port.ID, port.NodeID, outerID))
	}
	return layout
}
