package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/sqlite"
)

func TestOccupancyConfigurationPersistence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "occupancy.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ImportLayout(ctx, model.LayoutDefinition{
		Blocks: []model.BlockDefinition{{ID: "B12", Name: "Yard"}},
	}, false); err != nil {
		db.Close()
		t.Fatal(err)
	}
	provider := model.OccupancyProvider{
		ID:                "camera-yard",
		Type:              "vision",
		Priority:          90,
		Required:          false,
		StaleAfter:        3 * time.Minute,
		FreshnessRequired: true,
	}
	if err := db.SetOccupancyProvider(ctx, provider); err != nil {
		db.Close()
		t.Fatal(err)
	}
	required, priority := true, 95
	mapping := model.OccupancySensorMapping{
		ProviderID: provider.ID,
		SensorID:   "zone-12",
		BlockID:    "B12",
		Required:   &required,
		Priority:   &priority,
	}
	if err := db.SetOccupancySensorMapping(ctx, mapping); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	gotProvider, err := db.OccupancyProvider(ctx, provider.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotProvider, provider) {
		t.Fatalf("provider = %#v, want %#v", gotProvider, provider)
	}
	gotMapping, err := db.OccupancySensorMapping(ctx, provider.ID, mapping.SensorID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotMapping, mapping) {
		t.Fatalf("mapping = %#v, want %#v", gotMapping, mapping)
	}
	resolved, err := db.ResolveOccupancySensorMapping(ctx, provider.ID, mapping.SensorID)
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.Required || resolved.Priority != priority || resolved.StaleAfter != provider.StaleAfter {
		t.Fatalf("resolved mapping = %+v", resolved)
	}
	initial := model.NewUnknownBlockOccupancy(mapping.BlockID, time.Now())
	if initial.State != model.OccupancyUnknown {
		t.Fatalf("occupancy after restart = %q", initial.State)
	}
}

func TestOccupancyMappingPersistsInheritedAndFalseOverrides(t *testing.T) {
	ctx := context.Background()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.ImportLayout(ctx, model.LayoutDefinition{
		Blocks: []model.BlockDefinition{{ID: "block", Name: "Block"}},
	}, false); err != nil {
		t.Fatal(err)
	}
	provider := model.OccupancyProvider{ID: "rbus", Type: "current-detection", Priority: 100, Required: true}
	if err := db.SetOccupancyProvider(ctx, provider); err != nil {
		t.Fatal(err)
	}
	if err := db.SetOccupancySensorMapping(ctx, model.OccupancySensorMapping{
		ProviderID: provider.ID, SensorID: "1", BlockID: "block",
	}); err != nil {
		t.Fatal(err)
	}
	required, priority := false, 0
	if err := db.SetOccupancySensorMapping(ctx, model.OccupancySensorMapping{
		ProviderID: provider.ID, SensorID: "2", BlockID: "block", Required: &required, Priority: &priority,
	}); err != nil {
		t.Fatal(err)
	}
	mappings, err := db.ListOccupancySensorMappings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(mappings) != 2 || mappings[0].Required != nil || mappings[0].Priority != nil {
		t.Fatalf("inherited mapping = %#v", mappings)
	}
	if mappings[1].Required == nil || *mappings[1].Required || mappings[1].Priority == nil || *mappings[1].Priority != 0 {
		t.Fatalf("override mapping = %#v", mappings[1])
	}
	resolved, err := db.ResolveOccupancySensorMapping(ctx, provider.ID, "1")
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.Required || resolved.Priority != 100 {
		t.Fatalf("inherited resolution = %+v", resolved)
	}
}

func TestOccupancyConfigurationRejectsInvalidValuesAndReferences(t *testing.T) {
	ctx := context.Background()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	invalidProvider := model.OccupancyProvider{ID: "invalid", Type: "vision", Priority: 101}
	if err := db.SetOccupancyProvider(ctx, invalidProvider); !errors.Is(err, model.ErrInvalidOccupancy) {
		t.Fatalf("invalid provider error = %v", err)
	}
	if err := db.SetOccupancySensorMapping(ctx, model.OccupancySensorMapping{
		ProviderID: "", SensorID: "sensor", BlockID: "block",
	}); !errors.Is(err, model.ErrInvalidOccupancy) {
		t.Fatalf("invalid mapping error = %v", err)
	}
	if _, err := db.OccupancyProvider(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing provider error = %v", err)
	}
	if _, err := db.OccupancySensorMapping(ctx, "missing", "sensor"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing mapping error = %v", err)
	}
}

func TestMigrateLegacyFeedbackMappingToOccupancyMapping(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `CREATE TABLE blocks (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		occupied INTEGER NOT NULL DEFAULT 0
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO blocks(id,name,occupied) VALUES('block','Block',0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE feedback_mappings (
		provider TEXT NOT NULL,
		address INTEGER NOT NULL,
		block_id TEXT NOT NULL REFERENCES blocks(id) ON DELETE CASCADE,
		PRIMARY KEY(provider,address)
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO feedback_mappings(provider,address,block_id) VALUES('z21-rbus',12,'block')`); err != nil {
		t.Fatal(err)
	}
	store := &Store{DB: db}
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	provider, err := store.OccupancyProvider(ctx, "z21-rbus")
	if err != nil {
		t.Fatal(err)
	}
	if provider.Type != "current-detection" || provider.Priority != 100 || !provider.Required || provider.StaleAfter != 0 {
		t.Fatalf("migrated provider = %+v", provider)
	}
	mapping, err := store.OccupancySensorMapping(ctx, "z21-rbus", "12")
	if err != nil {
		t.Fatal(err)
	}
	if mapping.BlockID != "block" {
		t.Fatalf("migrated mapping = %+v", mapping)
	}
	var legacyTable int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='feedback_mappings'`).Scan(&legacyTable); err != nil {
		t.Fatal(err)
	}
	if legacyTable != 0 {
		t.Fatal("legacy feedback_mappings table still exists")
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("second migration: %v", err)
	}
	if mappings, err := store.ListOccupancySensorMappings(ctx); err != nil || len(mappings) != 1 {
		t.Fatalf("mappings after second migration = %+v, err=%v", mappings, err)
	}
}
