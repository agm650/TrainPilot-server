package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/model"
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
