package store

import (
	"context"
	"errors"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/model/topologyfixture"
	"github.com/agm650/TrainPilot-server/internal/sqlite"
	"github.com/agm650/TrainPilot-server/internal/topology"
)

func TestTopologicalRoutePersistenceRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	layout := topologyfixture.PassingStation()
	layout.Routes = []model.RouteDefinition{{
		ID: "through-loop", Name: "Through loop",
		EntryNodeID: "west-boundary", ExitNodeID: "east-boundary",
		BlockIDs: []string{"block-west", "block-loop", "block-east"},
		TurnoutStates: map[string]string{
			"station-west": "diverging", "station-east": "diverging",
		},
	}}
	if err := db.ImportLayout(ctx, layout, true); err != nil {
		t.Fatal(err)
	}
	exported, err := db.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(exported.Routes) != 1 || exported.Routes[0].EntryNodeID != "west-boundary" || exported.Routes[0].ExitNodeID != "east-boundary" {
		t.Fatalf("exported route=%+v", exported.Routes)
	}
}

func TestImportLayoutRejectsInvalidTopologicalRouteBeforeMutation(t *testing.T) {
	ctx := context.Background()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.ImportLayout(ctx, topologyfixture.PassingStation(), true); err != nil {
		t.Fatal(err)
	}
	before, err := db.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	invalid := topologyfixture.PassingStation()
	invalid.Routes = []model.RouteDefinition{{
		ID: "unsafe", Name: "Unsafe", EntryNodeID: "west-boundary", ExitNodeID: "east-boundary",
		TurnoutStates: map[string]string{"station-west": "diverging", "station-east": "diverging"},
	}}
	if err := db.ImportLayout(ctx, invalid, true); !errors.Is(err, topology.ErrInvalidRouteDefinition) {
		t.Fatalf("error=%v", err)
	}
	exported, err := db.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(exported.Blocks) != len(before.Blocks) || len(exported.Routes) != len(before.Routes) || len(exported.TopologyNodes) != len(before.TopologyNodes) {
		t.Fatalf("layout changed after rejection: before=%+v after=%+v", before, exported)
	}
}

func TestImportLayoutAllowsAdditionalRouteProtectionWarnings(t *testing.T) {
	ctx := context.Background()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	layout := topologyfixture.PassingStation()
	layout.Routes = []model.RouteDefinition{{
		ID: "protected-loop", Name: "Protected loop",
		EntryNodeID: "west-boundary", ExitNodeID: "east-boundary",
		BlockIDs: []string{"block-west", "block-loop", "block-east", "block-siding"},
		TurnoutStates: map[string]string{
			"station-west": "diverging", "station-east": "diverging", "station-branch": "straight",
		},
	}}
	graph, err := topology.Build(layout)
	if err != nil {
		t.Fatal(err)
	}
	issues := topology.ValidateRouteDefinitions(graph, layout.Routes, layout.Turnouts)
	if err := topology.RouteValidationErrors(issues); err != nil {
		t.Fatalf("warnings became blocking: %v", err)
	}
	if err := db.ImportLayout(ctx, layout, true); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateLegacyRouteEndpoints(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `CREATE TABLE routes (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		state TEXT NOT NULL DEFAULT 'idle',
		reserved_by_session TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO routes(id,name) VALUES('legacy','Legacy')`); err != nil {
		t.Fatal(err)
	}
	store := &Store{DB: db}
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var entry, exit string
	if err := db.QueryRowContext(ctx, `SELECT entry_node_id,exit_node_id FROM routes WHERE id='legacy'`).Scan(&entry, &exit); err != nil {
		t.Fatal(err)
	}
	if entry != "" || exit != "" {
		t.Fatalf("legacy endpoints=%q,%q", entry, exit)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("second migration: %v", err)
	}
}
