package store

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/model/topologyfixture"
)

func TestTopologyPersistenceRoundTripIsDeterministic(t *testing.T) {
	ctx := context.Background()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	want := persistedTopologyLayout()
	if err := db.ImportLayout(ctx, want, false); err != nil {
		t.Fatal(err)
	}

	nodes, err := db.ListTopologyNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 13 {
		t.Fatalf("topology nodes = %d, want 13", len(nodes))
	}
	for index := 1; index < len(nodes); index++ {
		if nodes[index-1].ID >= nodes[index].ID {
			t.Fatalf("topology nodes are not ordered by id: %+v", nodes)
		}
	}
	sections, err := db.ListTrackSections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for index, id := range []string{"cycle-ab", "cycle-bc", "cycle-ca", "line"} {
		if sections[index].ID != id {
			t.Fatalf("track section %d = %q, want %q", index, sections[index].ID, id)
		}
	}
	topologies, err := db.ListTurnoutTopologies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(topologies) != 2 || topologies[0].TurnoutID != "double-slip" || topologies[1].TurnoutID != "triple" {
		t.Fatalf("turnout topology order = %+v", topologies)
	}
	assertStoredTurnoutTopology(t, topologies, want.TurnoutTopologies)

	exported, err := db.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(exported.TopologyNodes, nodes) ||
		!reflect.DeepEqual(exported.TrackSections, sections) ||
		!reflect.DeepEqual(exported.TurnoutTopologies, topologies) {
		t.Fatalf("exported topology differs from store reads: %#v", exported)
	}
}

func TestReplaceTopologyDefinitionUsesPersistedTurnouts(t *testing.T) {
	ctx := context.Background()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	layout := topologyfixture.Simple()
	if err := db.ImportLayout(ctx, model.LayoutDefinition{Turnouts: layout.Turnouts}, false); err != nil {
		t.Fatal(err)
	}
	definition := model.LayoutDefinition{
		TopologyNodes:     layout.TopologyNodes,
		TrackSections:     layout.TrackSections,
		TurnoutTopologies: layout.TurnoutTopologies,
	}
	if err := db.ReplaceTopologyDefinition(ctx, definition); err != nil {
		t.Fatal(err)
	}
	sort.Slice(definition.TopologyNodes, func(i, j int) bool {
		return definition.TopologyNodes[i].ID < definition.TopologyNodes[j].ID
	})
	got, err := db.GetTopologyDefinition(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, definition) {
		t.Fatalf("topology round trip mismatch:\n got: %#v\nwant: %#v", got, definition)
	}
	if err := db.ReplaceTopologyDefinition(ctx, model.LayoutDefinition{}); err != nil {
		t.Fatal(err)
	}
	got, err = db.GetTopologyDefinition(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.TopologyNodes) != 0 || len(got.TrackSections) != 0 || len(got.TurnoutTopologies) != 0 {
		t.Fatalf("topology was not cleared: %#v", got)
	}
}

func TestTopologyReferencesRestrictNodeAndTurnoutDeletion(t *testing.T) {
	ctx := context.Background()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	layout := topologyfixture.Simple()
	if err := db.ImportLayout(ctx, layout, false); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.ExecContext(ctx, `DELETE FROM topology_nodes WHERE id=?`, layout.TopologyNodes[0].ID); err == nil {
		t.Fatal("referenced topology node deletion unexpectedly succeeded")
	}
	if _, err := db.DB.ExecContext(ctx, `DELETE FROM turnouts WHERE id=?`, layout.Turnouts[0].ID); err == nil {
		t.Fatal("turnout with topology deletion unexpectedly succeeded")
	}
}

func TestImportLayoutRollsBackTopologyAfterLateFailure(t *testing.T) {
	ctx := context.Background()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.ImportLayout(ctx, topologyfixture.Simple(), true); err != nil {
		t.Fatal(err)
	}
	before, err := db.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}

	invalid := topologyfixture.ThreeWay()
	invalid.Routes = []model.RouteDefinition{{
		ID: "late-failure", Name: "Late failure", TurnoutStates: map[string]string{},
		ConflictRouteIDs: []string{"missing-route"},
	}}
	if err := db.ImportLayout(ctx, invalid, true); err == nil {
		t.Fatal("invalid layout import unexpectedly succeeded")
	}
	after, err := db.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("database changed after rolled-back import:\n before: %#v\n after: %#v", before, after)
	}
}

func persistedTopologyLayout() model.LayoutDefinition {
	triple := topologyfixture.ThreeWay()
	doubleSlip := topologyfixture.DoubleSlip()
	return model.LayoutDefinition{
		TopologyNodes: append([]model.TopologyNode{
			{ID: "line-b", Kind: model.TopologyNodeBoundary},
			{ID: "line-a", Kind: model.TopologyNodeBuffer},
			{ID: "cycle-c", Kind: model.TopologyNodeJoint},
			{ID: "cycle-a", Kind: model.TopologyNodeJoint},
			{ID: "cycle-b", Kind: model.TopologyNodeJoint},
		}, append(triple.TopologyNodes, doubleSlip.TopologyNodes...)...),
		TrackSections: []model.TrackSection{
			{ID: "line", Name: "Line", NodeAID: "line-a", NodeBID: "line-b", LengthMM: 1200},
			{ID: "cycle-ca", NodeAID: "cycle-c", NodeBID: "cycle-a"},
			{ID: "cycle-ab", NodeAID: "cycle-a", NodeBID: "cycle-b", LengthMM: 300},
			{ID: "cycle-bc", NodeAID: "cycle-b", NodeBID: "cycle-c", LengthMM: 400},
		},
		Turnouts:          append(triple.Turnouts, doubleSlip.Turnouts...),
		TurnoutTopologies: append(triple.TurnoutTopologies, doubleSlip.TurnoutTopologies...),
	}
}

func assertStoredTurnoutTopology(t *testing.T, got, want []model.TurnoutTopology) {
	t.Helper()
	wantByID := make(map[string]model.TurnoutTopology, len(want))
	for _, topology := range want {
		wantByID[topology.TurnoutID] = topology
	}
	for _, topology := range got {
		if !reflect.DeepEqual(topology, wantByID[topology.TurnoutID]) {
			t.Fatalf("turnout topology %q mismatch:\n got: %#v\nwant: %#v", topology.TurnoutID, topology, wantByID[topology.TurnoutID])
		}
	}
}
