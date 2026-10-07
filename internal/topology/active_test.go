package topology_test

import (
	"reflect"
	"slices"
	"sort"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/model/topologyfixture"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/topology"
)

func TestActiveViewSimpleTurnoutUsesOnlyConfirmedReportedPosition(t *testing.T) {
	tests := []struct {
		name            string
		desired         string
		reported        string
		status          station.AccessoryReportState
		pending         bool
		wantConnections []string
	}{
		{name: "straight", desired: "straight", reported: "straight", status: station.AccessoryReportKnown, wantConnections: []string{"stem-straight"}},
		{name: "diverging", desired: "diverging", reported: "diverging", status: station.AccessoryReportKnown, wantConnections: []string{"diverging-stem"}},
		{name: "desired differs", desired: "diverging", reported: "straight", status: station.AccessoryReportKnown, wantConnections: []string{"stem-straight"}},
		{name: "pending", desired: "diverging", reported: "straight", status: station.AccessoryReportKnown, pending: true},
		{name: "unknown", reported: "straight", status: station.AccessoryReportUnknown},
		{name: "invalid", reported: "straight", status: station.AccessoryReportInvalid},
		{name: "missing status", reported: "straight"},
		{name: "missing position", status: station.AccessoryReportKnown},
		{name: "invalid position", reported: "sideways", status: station.AccessoryReportKnown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			layout := topologyfixture.Simple()
			turnout := runtimeTurnout(layout.Turnouts[0], test.desired, test.reported, test.status, test.pending, station.AccessoryReportStation)
			view := buildGraph(t, layout).ActiveView(map[string]model.Turnout{turnout.ID: turnout})
			if got := activeConnectionPairs(view); !slices.Equal(got, test.wantConnections) {
				t.Fatalf("active connections = %+v, want %+v", got, test.wantConnections)
			}
		})
	}
}

func TestActiveViewTripleFollowsReportedBranch(t *testing.T) {
	tests := []struct {
		name            string
		desired         string
		reported        string
		pending         bool
		wantConnections []string
	}{
		{name: "left", desired: "left", reported: "left", wantConnections: []string{"left-stem"}},
		{name: "straight", desired: "straight", reported: "straight", wantConnections: []string{"stem-straight"}},
		{name: "right", desired: "right", reported: "right", wantConnections: []string{"right-stem"}},
		{name: "desired right reported left", desired: "right", reported: "left", wantConnections: []string{"left-stem"}},
		{name: "pending", desired: "right", reported: "left", pending: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			layout := topologyfixture.ThreeWay()
			turnout := runtimeTurnout(layout.Turnouts[0], test.desired, test.reported, station.AccessoryReportKnown, test.pending, station.AccessoryReportPhysical)
			view := buildGraph(t, layout).ActiveView(map[string]model.Turnout{turnout.ID: turnout})
			if got := activeConnectionPairs(view); !slices.Equal(got, test.wantConnections) {
				t.Fatalf("active connections = %+v, want %+v", got, test.wantConnections)
			}
		})
	}
}

func TestActiveViewDoubleSlipEnablesEveryConnectionForReportedPosition(t *testing.T) {
	tests := []struct {
		position        string
		wantConnections []string
	}{
		{position: "route_a", wantConnections: []string{"a-c", "b-d"}},
		{position: "route_b", wantConnections: []string{"a-c"}},
		{position: "route_c", wantConnections: []string{"b-d"}},
		{position: "route_d", wantConnections: []string{"a-d", "b-c"}},
	}
	for _, test := range tests {
		t.Run(test.position, func(t *testing.T) {
			layout := topologyfixture.DoubleSlip()
			turnout := runtimeTurnout(layout.Turnouts[0], "route_a", test.position, station.AccessoryReportKnown, false, station.AccessoryReportStation)
			view := buildGraph(t, layout).ActiveView(map[string]model.Turnout{turnout.ID: turnout})
			if got := activeConnectionPairs(view); !slices.Equal(got, test.wantConnections) {
				t.Fatalf("active connections = %+v, want %+v", got, test.wantConnections)
			}
		})
	}
}

func TestActiveViewPreservesReportQuality(t *testing.T) {
	for _, quality := range []station.AccessoryReportQuality{
		station.AccessoryReportAssumed,
		station.AccessoryReportStation,
		station.AccessoryReportPhysical,
	} {
		t.Run(string(quality), func(t *testing.T) {
			layout := topologyfixture.Simple()
			turnout := runtimeTurnout(layout.Turnouts[0], "straight", "straight", station.AccessoryReportKnown, false, quality)
			edges := buildGraph(t, layout).ActiveView(map[string]model.Turnout{turnout.ID: turnout}).Edges()
			if len(edges) != 1 || edges[0].ReportedPosition != "straight" || edges[0].Quality != quality {
				t.Fatalf("active traversal metadata = %+v", edges)
			}
		})
	}
}

func TestActiveViewExternalReportChangeDoesNotMutateStaticGraph(t *testing.T) {
	layout := topologyfixture.Simple()
	graph := buildGraph(t, layout)
	staticBefore := graph.Edges()
	turnout := runtimeTurnout(layout.Turnouts[0], "straight", "straight", station.AccessoryReportKnown, false, station.AccessoryReportPhysical)
	states := map[string]model.Turnout{turnout.ID: turnout}
	straightView := graph.ActiveView(states)

	turnout.ReportedPosition = "diverging"
	states[turnout.ID] = turnout
	divergingView := graph.ActiveView(states)
	if got := activeConnectionPairs(straightView); !reflect.DeepEqual(got, []string{"stem-straight"}) {
		t.Fatalf("existing active snapshot changed: %+v", got)
	}
	if got := activeConnectionPairs(divergingView); !reflect.DeepEqual(got, []string{"diverging-stem"}) {
		t.Fatalf("external report was not applied: %+v", got)
	}
	if !reflect.DeepEqual(graph.Edges(), staticBefore) {
		t.Fatal("active views mutated the static graph")
	}
}

func TestActiveViewKeepsFixedEdgesAndIndexesActiveNeighbors(t *testing.T) {
	layout := topologyfixture.Simple()
	layout.TopologyNodes = append(layout.TopologyNodes,
		node("boundary", model.TopologyNodeBoundary),
	)
	layout.TrackSections = append(layout.TrackSections,
		section("approach", "boundary", "simple-stem"),
	)
	turnout := runtimeTurnout(layout.Turnouts[0], "straight", "straight", station.AccessoryReportKnown, false, station.AccessoryReportStation)
	view := buildGraph(t, layout).ActiveView(map[string]model.Turnout{turnout.ID: turnout})
	if edges := view.Edges(); len(edges) != 2 || edges[0].Kind != topology.EdgeKindTrackSection || edges[1].Kind != topology.EdgeKindTurnout {
		t.Fatalf("unexpected active edges: %+v", edges)
	}
	if len(view.IncidentEdges("simple-stem")) != 2 || len(view.IncidentEdges("simple-diverging")) != 0 {
		t.Fatal("active incident-edge index includes an inactive branch")
	}
	wantComponents := [][]string{{"boundary", "simple-stem", "simple-straight"}, {"simple-diverging"}}
	if got := view.ConnectedComponents(); !reflect.DeepEqual(got, wantComponents) {
		t.Fatalf("active components = %+v, want %+v", got, wantComponents)
	}
}

func TestActiveViewWithoutTurnoutStateHasNoInternalConnection(t *testing.T) {
	layout := topologyfixture.Simple()
	view := buildGraph(t, layout).ActiveView(nil)
	if len(view.Edges()) != 0 {
		t.Fatalf("missing turnout state enabled edges: %+v", view.Edges())
	}
	if got := view.ConnectedComponents(); len(got) != 3 {
		t.Fatalf("components = %+v, want three isolated ports", got)
	}
}

func runtimeTurnout(
	turnout model.Turnout,
	desired string,
	reported string,
	status station.AccessoryReportState,
	pending bool,
	quality station.AccessoryReportQuality,
) model.Turnout {
	turnout.DesiredPosition = desired
	turnout.ReportedPosition = reported
	turnout.ReportedStatus = status
	turnout.Pending = pending
	turnout.Quality = quality
	return turnout
}

func activeConnectionPairs(view *topology.ActiveGraph) []string {
	pairs := make([]string, 0)
	for _, edge := range view.Edges() {
		if edge.Kind == topology.EdgeKindTurnout {
			pairs = append(pairs, edge.PortAID+"-"+edge.PortBID)
		}
	}
	sort.Strings(pairs)
	return pairs
}
