package transfer

import (
	"context"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/store"
)

func TestRollingStockArchiveRoundTrip(t *testing.T) {
	ctx := context.Background()
	source, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if err := source.SeedDemo(ctx); err != nil {
		t.Fatal(err)
	}
	svc := New(source, events.New(), clock.NewFake(time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)))
	data, err := svc.ExportRollingStock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	targetSvc := New(target, events.New(), clock.Real{})
	admin := model.User{ID: "admin", Role: model.RoleAdministrator}
	if err := targetSvc.ImportRollingStock(ctx, admin, data, true); err != nil {
		t.Fatal(err)
	}
	items, err := target.ListLocomotives(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("locomotives=%d", len(items))
	}
}
func TestLayoutArchiveRoundTrip(t *testing.T) {
	ctx := context.Background()
	source, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if err := source.SeedDemo(ctx); err != nil {
		t.Fatal(err)
	}
	svc := New(source, events.New(), clock.Real{})
	data, err := svc.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	admin := model.User{ID: "admin", Role: model.RoleAdministrator}
	if err := New(target, events.New(), clock.Real{}).ImportLayout(ctx, admin, data, true); err != nil {
		t.Fatal(err)
	}
	layout, err := target.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(layout.Blocks) != 3 || len(layout.Routes) != 1 {
		t.Fatalf("blocks=%d routes=%d", len(layout.Blocks), len(layout.Routes))
	}
}
func TestDriverCannotImport(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	svc := New(db, events.New(), clock.Real{})
	err = svc.ImportRollingStock(ctx, model.User{Role: model.RoleDriver}, []byte("bad"), false)
	if err == nil {
		t.Fatal("driver import unexpectedly accepted")
	}
}

func TestInvalidLayoutDoesNotModifyDatabase(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SeedDemo(ctx); err != nil {
		t.Fatal(err)
	}
	before, err := db.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	invalid := LayoutDocument{Layout: model.LayoutDefinition{
		Blocks: []model.Block{{ID: "new-block", Name: "New block"}},
		Routes: []model.RouteDefinition{{ID: "bad-route", Name: "Bad route", BlockIDs: []string{"missing-block"}, TurnoutStates: map[string]string{}}},
	}}
	data, err := writeArchive(Manifest{Format: FormatID, Version: FormatVersion, PackageType: "layout", CreatedAt: time.Now()}, "layout.json", invalid)
	if err != nil {
		t.Fatal(err)
	}
	err = New(db, events.New(), clock.Real{}).ImportLayout(ctx, model.User{ID: "admin", Role: model.RoleAdministrator}, data, true)
	if err == nil {
		t.Fatal("invalid layout unexpectedly imported")
	}
	after, err := db.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Blocks) != len(after.Blocks) || len(before.Routes) != len(after.Routes) {
		t.Fatalf("database changed after rejected import: before blocks/routes=%d/%d after=%d/%d", len(before.Blocks), len(before.Routes), len(after.Blocks), len(after.Routes))
	}
}
