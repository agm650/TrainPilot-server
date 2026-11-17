package transfer

import (
	"context"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
)

func TestLayoutPowerDependencyRoundTripAndLegacyMerge(t *testing.T) {
	ctx := context.Background()
	independent := false
	provider := model.OccupancyProvider{ID: "z21-rbus", Type: "current-detection", Priority: 100, Required: true, TrackPowerDependent: &independent}
	layout := model.LayoutDefinition{Blocks: []model.BlockDefinition{{ID: "a", Name: "A"}}, OccupancyProviders: []model.OccupancyProvider{provider}, OccupancySensorMappings: []model.OccupancySensorMapping{{ProviderID: "z21-rbus", SensorID: "1", BlockID: "a"}}}
	data, err := BuildLayoutArchive(time.Now(), layout)
	if err != nil {
		t.Fatal(err)
	}
	if version := archiveManifest(t, data).Version; version != 8 {
		t.Fatalf("layout version=%d want 8", version)
	}
	db := openArchiveStore(t)
	svc := New(db, events.New(), clock.Real{})
	admin := model.User{Role: model.RoleAdministrator}
	if err := svc.ImportLayout(ctx, admin, data, true); err != nil {
		t.Fatal(err)
	}
	exported, err := svc.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ImportLayout(ctx, admin, exported, true); err != nil {
		t.Fatal(err)
	}
	got, err := db.OccupancyProvider(ctx, provider.ID)
	if err != nil || got.TrackPowerDependent == nil || got.DependsOnTrackPower() {
		t.Fatalf("power declaration lost: %+v err=%v", got, err)
	}
	// v7 files lack this optional field. Merging one must retain an explicit
	// independence declaration already present on the server.
	layout.OccupancyProviders[0].TrackPowerDependent = nil
	legacy, err := writeArchive(Manifest{Format: FormatID, Version: 7, PackageType: "layout", CreatedAt: time.Now()}, "layout.json", LayoutDocument{Layout: layout})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ImportLayout(ctx, admin, legacy, false); err != nil {
		t.Fatal(err)
	}
	got, err = db.OccupancyProvider(ctx, provider.ID)
	if err != nil || got.DependsOnTrackPower() {
		t.Fatalf("legacy merge erased declaration: %+v err=%v", got, err)
	}
}
