package topology_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/model/topologyfixture"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/topology"
)

type fixtureExpectation struct {
	fromNodeID     string
	toNodeID       string
	componentCount int
	activePosition map[string]string
}

func TestReferenceTopologyFixtures(t *testing.T) {
	expectations := map[string]fixtureExpectation{
		"simple-line":                     {fromNodeID: "buffer", toNodeID: "boundary", componentCount: 1},
		"passing-loop":                    {fromNodeID: "west-boundary", toNodeID: "east-boundary", componentCount: 1, activePosition: map[string]string{"t1": "straight", "t2": "straight"}},
		"three-way-yard":                  {fromNodeID: "yard-boundary", toNodeID: "left-buffer", componentCount: 1, activePosition: map[string]string{"triple": "left"}},
		"double-slip-station":             {fromNodeID: "a-boundary", toNodeID: "c-boundary", componentCount: 1, activePosition: map[string]string{"double-slip": "route_a"}},
		"fixed-crossing":                  {fromNodeID: "west", toNodeID: "east", componentCount: 2},
		"multi-section-block":             {fromNodeID: "stem-boundary", toNodeID: "straight-boundary", componentCount: 1, activePosition: map[string]string{"simple": "straight"}},
		"undetected-section":              {fromNodeID: "buffer", toNodeID: "boundary", componentCount: 1},
		"loop":                            {fromNodeID: "a", toNodeID: "c", componentCount: 1},
		"conceptual-five-detection-zones": {fromNodeID: "outer-1-a", toNodeID: "outer-1-b", componentCount: 5},
	}
	fixtures := topologyfixture.ReferenceLayouts()
	if len(fixtures) != len(expectations) {
		t.Fatalf("reference fixtures = %d, want %d", len(fixtures), len(expectations))
	}
	seen := map[string]bool{}
	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.Name, func(t *testing.T) {
			if seen[fixture.Name] {
				t.Fatalf("duplicate fixture name %q", fixture.Name)
			}
			seen[fixture.Name] = true
			expectation, exists := expectations[fixture.Name]
			if !exists {
				t.Fatalf("unexpected fixture %q", fixture.Name)
			}
			if err := model.ValidateTopologyDefinition(fixture.Layout); err != nil {
				t.Fatalf("model validation: %v", err)
			}
			if err := model.ValidateBlockDefinitions(fixture.Layout); err != nil {
				t.Fatalf("block validation: %v", err)
			}
			graph := buildGraph(t, fixture.Layout)
			if got := len(graph.ConnectedComponents()); got != expectation.componentCount {
				t.Fatalf("components = %d, want %d", got, expectation.componentCount)
			}
			assertFixtureQueries(t, graph, fixture.Layout, expectation.fromNodeID)
			assertFixturePath(t, graph, fixture.Layout, expectation)
			assertFixtureBlockMembership(t, graph, fixture.Layout)
			if fixture.Name == "undetected-section" {
				if owner, exists := graph.BlockForTrackSection("s2"); exists {
					t.Fatalf("undetected section has block owner %+v", owner)
				}
			}
		})
	}
}

func TestReferenceTurnoutFixturesCoverEveryPositionAndSafeUnknownState(t *testing.T) {
	for _, fixture := range topologyfixture.ReferenceLayouts() {
		for _, turnoutTopology := range fixture.Layout.TurnoutTopologies {
			turnoutTopology := turnoutTopology
			t.Run(fixture.Name+"/"+turnoutTopology.TurnoutID, func(t *testing.T) {
				graph := buildGraph(t, fixture.Layout)
				turnout := turnoutByID(t, fixture.Layout.Turnouts, turnoutTopology.TurnoutID)
				portNodes := map[string]string{}
				for _, port := range turnoutTopology.Ports {
					portNodes[port.ID] = port.NodeID
				}
				for _, position := range turnoutTopology.Positions {
					state := runtimeTurnout(turnout, position.PositionID, position.PositionID, station.AccessoryReportKnown, false, station.AccessoryReportPhysical)
					active := graph.ActiveView(map[string]model.Turnout{turnout.ID: state})
					for _, connection := range position.Connections {
						if _, found := active.FindPath(portNodes[connection.PortAID], portNodes[connection.PortBID], topology.PathConstraints{}); !found {
							t.Errorf("position %q does not connect %s-%s", position.PositionID, connection.PortAID, connection.PortBID)
						}
					}
				}

				unknown := runtimeTurnout(turnout, turnout.DesiredPosition, "", station.AccessoryReportUnknown, false, "")
				if countActiveTurnoutEdges(graph.ActiveView(map[string]model.Turnout{turnout.ID: unknown}), turnout.ID) != 0 {
					t.Fatal("unknown turnout enabled an internal connection")
				}
				pendingPosition := turnout.Positions[0].ID
				pending := runtimeTurnout(turnout, pendingPosition, pendingPosition, station.AccessoryReportKnown, true, station.AccessoryReportPhysical)
				if countActiveTurnoutEdges(graph.ActiveView(map[string]model.Turnout{turnout.ID: pending}), turnout.ID) != 0 {
					t.Fatal("pending turnout enabled an internal connection")
				}
			})
		}
	}
}

func TestReferenceTopologyBuildPathAndExportAreDeterministic(t *testing.T) {
	expectations := map[string]fixtureExpectation{
		"simple-line":                     {fromNodeID: "buffer", toNodeID: "boundary"},
		"passing-loop":                    {fromNodeID: "west-boundary", toNodeID: "east-boundary"},
		"three-way-yard":                  {fromNodeID: "yard-boundary", toNodeID: "left-buffer"},
		"double-slip-station":             {fromNodeID: "a-boundary", toNodeID: "c-boundary"},
		"fixed-crossing":                  {fromNodeID: "west", toNodeID: "east"},
		"multi-section-block":             {fromNodeID: "stem-boundary", toNodeID: "straight-boundary"},
		"undetected-section":              {fromNodeID: "buffer", toNodeID: "boundary"},
		"loop":                            {fromNodeID: "a", toNodeID: "c"},
		"conceptual-five-detection-zones": {fromNodeID: "outer-1-a", toNodeID: "outer-1-b"},
	}
	for _, fixture := range topologyfixture.ReferenceLayouts() {
		fixture := fixture
		t.Run(fixture.Name, func(t *testing.T) {
			expectation := expectations[fixture.Name]
			var firstBuild, firstPath, firstExport []byte
			for iteration := 0; iteration < 100; iteration++ {
				graph := buildGraph(t, fixture.Layout)
				path, found := graph.FindPath(expectation.fromNodeID, expectation.toNodeID, topology.PathConstraints{})
				if !found {
					t.Fatalf("iteration %d: path not found", iteration)
				}
				definition, err := topology.DefinitionFromLayout(fixture.Layout)
				if err != nil {
					t.Fatalf("iteration %d: export: %v", iteration, err)
				}
				buildJSON := mustJSON(t, struct {
					Nodes      []model.TopologyNode
					Edges      []topology.Edge
					Components [][]string
				}{Nodes: graph.Nodes(), Edges: graph.Edges(), Components: graph.ConnectedComponents()})
				pathJSON := mustJSON(t, path)
				exportJSON := mustJSON(t, definition)
				if iteration == 0 {
					firstBuild, firstPath, firstExport = buildJSON, pathJSON, exportJSON
					continue
				}
				if !reflect.DeepEqual(buildJSON, firstBuild) || !reflect.DeepEqual(pathJSON, firstPath) || !reflect.DeepEqual(exportJSON, firstExport) {
					t.Fatalf("iteration %d produced non-deterministic topology output", iteration)
				}
			}
		})
	}
}

func assertFixtureQueries(t *testing.T, graph *topology.Graph, layout model.LayoutDefinition, fromNodeID string) {
	t.Helper()
	if len(graph.NeighborNodes(fromNodeID)) == 0 {
		t.Fatalf("NeighborNodes(%q) is empty", fromNodeID)
	}
	if len(graph.ResourcesAtNode(fromNodeID)) == 0 {
		t.Fatalf("ResourcesAtNode(%q) is empty", fromNodeID)
	}
	for _, section := range layout.TrackSections {
		if len(graph.TrackSectionsAtNode(section.NodeAID)) == 0 {
			t.Fatalf("section %q is absent from node %q query", section.ID, section.NodeAID)
		}
	}
}

func assertFixturePath(t *testing.T, graph *topology.Graph, layout model.LayoutDefinition, expectation fixtureExpectation) {
	t.Helper()
	if _, found := graph.FindPath(expectation.fromNodeID, expectation.toNodeID, topology.PathConstraints{}); !found {
		t.Fatalf("static path %s -> %s not found", expectation.fromNodeID, expectation.toNodeID)
	}
	states := make(map[string]model.Turnout, len(layout.Turnouts))
	for _, turnout := range layout.Turnouts {
		position := expectation.activePosition[turnout.ID]
		if position == "" {
			position = turnout.Positions[0].ID
		}
		states[turnout.ID] = runtimeTurnout(turnout, position, position, station.AccessoryReportKnown, false, station.AccessoryReportPhysical)
	}
	if _, found := graph.ActiveView(states).FindPath(expectation.fromNodeID, expectation.toNodeID, topology.PathConstraints{}); !found {
		t.Fatalf("active path %s -> %s not found", expectation.fromNodeID, expectation.toNodeID)
	}
}

func assertFixtureBlockMembership(t *testing.T, graph *topology.Graph, layout model.LayoutDefinition) {
	t.Helper()
	for _, block := range layout.Blocks {
		resources, exists := graph.ResourcesForBlock(block.ID)
		if !exists {
			t.Fatalf("block %q is absent from resource index", block.ID)
		}
		for _, sectionID := range resources.TrackSectionIDs {
			owner, exists := graph.BlockForTrackSection(sectionID)
			if !exists || owner.ID != block.ID {
				t.Fatalf("section %q owner = %+v,%v", sectionID, owner, exists)
			}
		}
		for _, turnoutID := range resources.TurnoutIDs {
			owner, exists := graph.BlockForTurnout(turnoutID)
			if !exists || owner.ID != block.ID {
				t.Fatalf("turnout %q owner = %+v,%v", turnoutID, owner, exists)
			}
		}
	}
	if len(layout.TrackSections) > 0 && len(layout.Blocks) == 0 {
		t.Fatal("fixture has no block membership case")
	}
}

func turnoutByID(t *testing.T, turnouts []model.Turnout, turnoutID string) model.Turnout {
	t.Helper()
	for _, turnout := range turnouts {
		if turnout.ID == turnoutID {
			return turnout
		}
	}
	t.Fatalf("turnout %q not found", turnoutID)
	return model.Turnout{}
}

func countActiveTurnoutEdges(graph *topology.ActiveGraph, turnoutID string) int {
	count := 0
	for _, edge := range graph.Edges() {
		if edge.TurnoutID == turnoutID {
			count++
		}
	}
	return count
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
