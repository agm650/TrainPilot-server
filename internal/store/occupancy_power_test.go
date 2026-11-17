package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/model"
)

func TestOccupancyPowerDependencyMigrationAndPersistence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "power.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	provider := model.OccupancyProvider{ID: "z21-rbus", Type: "current-detection", Priority: 100, Required: true}
	if err := db.SetOccupancyProvider(ctx, provider); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.ExecContext(ctx, `ALTER TABLE occupancy_providers DROP COLUMN track_power_dependent`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := db.OccupancyProvider(ctx, provider.ID)
	if err != nil || got.TrackPowerDependent != nil || !got.DependsOnTrackPower() {
		t.Fatalf("legacy migration=%+v err=%v", got, err)
	}
	independent := false
	provider.TrackPowerDependent = &independent
	if err := db.SetOccupancyProvider(ctx, provider); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err = db.OccupancyProvider(ctx, provider.ID)
	if err != nil || got.TrackPowerDependent == nil || got.DependsOnTrackPower() {
		t.Fatalf("explicit independence lost: %+v err=%v", got, err)
	}
	provider.TrackPowerDependent = nil
	if err := db.SetOccupancyProvider(ctx, provider); err != nil {
		t.Fatal(err)
	}
	got, err = db.OccupancyProvider(ctx, provider.ID)
	if err != nil || got.DependsOnTrackPower() {
		t.Fatalf("omission erased independence: %+v err=%v", got, err)
	}
}
