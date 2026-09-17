package store

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/model/topologyfixture"
	"github.com/agm650/TrainPilot-server/internal/sqlite"
)

func TestBlockDefinitionPersistenceAndInverseIndexes(t *testing.T) {
	ctx := context.Background()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	layout := topologyfixture.Simple()
	for _, port := range layout.TurnoutTopologies[0].Ports {
		outerID := "outer-" + port.ID
		layout.TopologyNodes = append(layout.TopologyNodes, model.TopologyNode{ID: outerID, Kind: model.TopologyNodeBoundary})
		layout.TrackSections = append(layout.TrackSections, model.TrackSection{
			ID: "section-" + port.ID, NodeAID: port.NodeID, NodeBID: outerID,
		})
	}
	layout.Blocks = []model.BlockDefinition{{
		ID: "detected", Name: "Detected",
		TrackSectionIDs: []string{"section-straight", "section-diverging", "section-stem"},
		TurnoutIDs:      []string{layout.Turnouts[0].ID},
	}}
	if err := db.ImportLayout(ctx, layout, false); err != nil {
		t.Fatal(err)
	}

	want := model.BlockDefinition{
		ID: "detected", Name: "Detected",
		TrackSectionIDs: []string{"section-diverging", "section-stem", "section-straight"},
		TurnoutIDs:      []string{layout.Turnouts[0].ID},
	}
	for name, get := range map[string]func() (model.BlockDefinition, error){
		"section": func() (model.BlockDefinition, error) { return db.BlockForTrackSection(ctx, "section-stem") },
		"turnout": func() (model.BlockDefinition, error) { return db.BlockForTurnout(ctx, layout.Turnouts[0].ID) },
		"block":   func() (model.BlockDefinition, error) { return db.ResourcesForBlock(ctx, "detected") },
	} {
		t.Run(name, func(t *testing.T) {
			got, err := get()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("definition = %#v, want %#v", got, want)
			}
		})
	}
	if _, err := db.BlockForTrackSection(ctx, "unassigned"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unassigned section error = %v, want ErrNotFound", err)
	}

	exported, err := db.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(exported.Blocks, []model.BlockDefinition{want}) {
		t.Fatalf("exported blocks = %#v, want %#v", exported.Blocks, []model.BlockDefinition{want})
	}
	if err := db.ReplaceTopologyDefinition(ctx, model.LayoutDefinition{
		TopologyNodes:     layout.TopologyNodes,
		TrackSections:     layout.TrackSections,
		TurnoutTopologies: layout.TurnoutTopologies,
	}); err != nil {
		t.Fatal(err)
	}
	afterReplace, err := db.ResourcesForBlock(ctx, "detected")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterReplace, want) {
		t.Fatalf("topology replacement lost membership: got %#v want %#v", afterReplace, want)
	}
}

func TestLayoutImportPreservesRuntimeBlockOccupancy(t *testing.T) {
	ctx := context.Background()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	layout := model.LayoutDefinition{Blocks: []model.BlockDefinition{{ID: "block", Name: "Block"}}}
	if err := db.ImportLayout(ctx, layout, false); err != nil {
		t.Fatal(err)
	}
	if err := db.SetBlockOccupied(ctx, "block", true); err != nil {
		t.Fatal(err)
	}
	layout.Blocks[0].Name = "Renamed"
	if err := db.ImportLayout(ctx, layout, false); err != nil {
		t.Fatal(err)
	}
	blocks, err := db.ListBlocks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 || blocks[0].Name != "Renamed" || !blocks[0].Occupied {
		t.Fatalf("runtime block changed by configuration import: %+v", blocks)
	}
}

func TestMergeCannotChangeResourceOwnedByOmittedBlock(t *testing.T) {
	ctx := context.Background()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	initial := model.LayoutDefinition{
		TopologyNodes: []model.TopologyNode{
			{ID: "a", Kind: model.TopologyNodeBoundary},
			{ID: "b", Kind: model.TopologyNodeBoundary},
		},
		TrackSections: []model.TrackSection{{ID: "section", Name: "Initial", NodeAID: "a", NodeBID: "b"}},
		Blocks:        []model.BlockDefinition{{ID: "block", Name: "Block", TrackSectionIDs: []string{"section"}}},
	}
	if err := db.ImportLayout(ctx, initial, false); err != nil {
		t.Fatal(err)
	}
	update := model.LayoutDefinition{
		TopologyNodes: initial.TopologyNodes,
		TrackSections: []model.TrackSection{{ID: "section", Name: "Changed", NodeAID: "a", NodeBID: "b"}},
	}
	if err := db.ImportLayout(ctx, update, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("ImportLayout() error = %v, want ErrConflict", err)
	}
	sections, err := db.ListTrackSections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sections) != 1 || sections[0].Name != "Initial" {
		t.Fatalf("rejected merge changed resource: %+v", sections)
	}
}

func TestImportCanTransferResourceBetweenIncludedBlocks(t *testing.T) {
	ctx := context.Background()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	layout := model.LayoutDefinition{
		TopologyNodes: []model.TopologyNode{
			{ID: "a", Kind: model.TopologyNodeBoundary},
			{ID: "b", Kind: model.TopologyNodeBoundary},
		},
		TrackSections: []model.TrackSection{{ID: "section", NodeAID: "a", NodeBID: "b"}},
		Blocks: []model.BlockDefinition{
			{ID: "first", Name: "First", TrackSectionIDs: []string{"section"}},
			{ID: "second", Name: "Second"},
		},
	}
	if err := db.ImportLayout(ctx, layout, false); err != nil {
		t.Fatal(err)
	}
	layout.Blocks = []model.BlockDefinition{
		{ID: "second", Name: "Second", TrackSectionIDs: []string{"section"}},
		{ID: "first", Name: "First"},
	}
	if err := db.ImportLayout(ctx, layout, false); err != nil {
		t.Fatal(err)
	}
	owner, err := db.BlockForTrackSection(ctx, "section")
	if err != nil {
		t.Fatal(err)
	}
	if owner.ID != "second" {
		t.Fatalf("track section owner = %q, want second", owner.ID)
	}
}

func TestBlockMembershipMigrationLeavesLegacyBlocksUnassigned(t *testing.T) {
	ctx := context.Background()
	raw, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.ExecContext(ctx, `CREATE TABLE blocks (id TEXT PRIMARY KEY,name TEXT NOT NULL,occupied INTEGER NOT NULL DEFAULT 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `INSERT INTO blocks(id,name,occupied) VALUES('legacy','Legacy',1)`); err != nil {
		t.Fatal(err)
	}
	db := &Store{DB: raw}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	definition, err := db.ResourcesForBlock(ctx, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if len(definition.TrackSectionIDs) != 0 || len(definition.TurnoutIDs) != 0 {
		t.Fatalf("legacy block acquired resources: %+v", definition)
	}
	blocks, err := db.ListBlocks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 || !blocks[0].Occupied {
		t.Fatalf("legacy runtime state was not preserved: %+v", blocks)
	}
}
