package topology_test

import (
	"reflect"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/model/topologyfixture"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/topology"
)

func TestGraphNeighborhoodQueriesAreDeterministic(t *testing.T) {
	layout := topologyfixture.PassingStation()
	graph := buildGraph(t, layout)

	neighbors := graph.NeighborNodes("station-west-stem")
	if got := nodeIDs(neighbors); !reflect.DeepEqual(got, []string{"station-west-diverging", "station-west-straight", "west-boundary"}) {
		t.Fatalf("NeighborNodes() = %+v", got)
	}
	sections := graph.TrackSectionsAtNode("station-west-stem")
	if got := trackSectionIDs(sections); !reflect.DeepEqual(got, []string{"approach-west"}) {
		t.Fatalf("TrackSectionsAtNode() = %+v", got)
	}
	resources := graph.ResourcesAtNode("station-west-stem")
	wantResources := []topology.Resource{
		{Kind: topology.EdgeKindTrackSection, TrackSectionID: "approach-west"},
		{Kind: topology.EdgeKindTurnout, TurnoutID: "station-west"},
	}
	if !reflect.DeepEqual(resources, wantResources) {
		t.Fatalf("ResourcesAtNode() = %+v, want %+v", resources, wantResources)
	}

	adjacent := graph.AdjacentResources(topology.Resource{Kind: topology.EdgeKindTurnout, TurnoutID: "station-west"})
	wantAdjacent := []topology.Resource{
		{Kind: topology.EdgeKindTrackSection, TrackSectionID: "approach-west"},
		{Kind: topology.EdgeKindTrackSection, TrackSectionID: "main-west"},
		{Kind: topology.EdgeKindTrackSection, TrackSectionID: "passing-loop"},
	}
	if !reflect.DeepEqual(adjacent, wantAdjacent) {
		t.Fatalf("AdjacentResources() = %+v, want %+v", adjacent, wantAdjacent)
	}
}

func TestAdjacentTrackSectionsShareAnEndpoint(t *testing.T) {
	layout := model.LayoutDefinition{
		TopologyNodes: []model.TopologyNode{
			node("a", model.TopologyNodeBoundary),
			node("b", model.TopologyNodeJoint),
			node("c", model.TopologyNodeBoundary),
		},
		TrackSections: []model.TrackSection{
			section("ab", "a", "b"),
			section("bc", "b", "c"),
		},
	}
	graph := buildGraph(t, layout)
	if got := trackSectionIDs(graph.AdjacentTrackSections("ab")); !reflect.DeepEqual(got, []string{"bc"}) {
		t.Fatalf("AdjacentTrackSections(ab) = %+v", got)
	}
	if got := graph.AdjacentTrackSections("missing"); got != nil {
		t.Fatalf("AdjacentTrackSections(missing) = %+v, want nil", got)
	}
}

func TestActiveNeighborhoodOmitsUnconfirmedTurnout(t *testing.T) {
	layout := topologyfixture.PassingStation()
	graph := buildGraph(t, layout)
	unknown := graph.ActiveView(nil)
	if got := unknown.ResourcesAtNode("station-west-stem"); !reflect.DeepEqual(got, []topology.Resource{{Kind: topology.EdgeKindTrackSection, TrackSectionID: "approach-west"}}) {
		t.Fatalf("unknown ResourcesAtNode() = %+v", got)
	}

	west := runtimeTurnout(layout.Turnouts[0], "straight", "straight", station.AccessoryReportKnown, false, station.AccessoryReportPhysical)
	active := graph.ActiveView(map[string]model.Turnout{west.ID: west})
	want := []topology.Resource{
		{Kind: topology.EdgeKindTrackSection, TrackSectionID: "approach-west"},
		{Kind: topology.EdgeKindTurnout, TurnoutID: "station-west"},
	}
	if got := active.ResourcesAtNode("station-west-stem"); !reflect.DeepEqual(got, want) {
		t.Fatalf("active ResourcesAtNode() = %+v, want %+v", got, want)
	}
}

func nodeIDs(nodes []model.TopologyNode) []string {
	ids := make([]string, 0, len(nodes))
	for _, node := range nodes {
		ids = append(ids, node.ID)
	}
	return ids
}

func trackSectionIDs(sections []model.TrackSection) []string {
	ids := make([]string, 0, len(sections))
	for _, section := range sections {
		ids = append(ids, section.ID)
	}
	return ids
}
