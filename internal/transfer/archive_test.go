package transfer

import (
	"bytes"
	"context"
	"reflect"
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

func TestLegacyLayoutArchiveImportsAsSimpleTurnout(t *testing.T) {
	ctx := context.Background()
	legacyDocument := map[string]any{
		"layout": map[string]any{
			"blocks": []any{},
			"turnouts": []any{map[string]any{
				"id": "legacy-12", "name": "Legacy", "dccAddress": 12,
				"desiredState": "straight", "reportedState": "diverging",
			}},
			"routes":           []any{},
			"feedbackMappings": []any{},
		},
	}
	data, err := writeArchive(Manifest{Format: FormatID, Version: 1, PackageType: "layout", CreatedAt: time.Now()}, "layout.json", legacyDocument)
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
	turnout, err := target.GetTurnout(ctx, "legacy-12")
	if err != nil {
		t.Fatal(err)
	}
	if turnout.Kind != model.TurnoutKindSimple || turnout.DesiredPosition != "straight" || turnout.ReportedPosition != "diverging" || len(turnout.Endpoints) != 1 || turnout.Endpoints[0].LinearAddress != 12 {
		t.Fatalf("unexpected legacy conversion: %+v", turnout)
	}
}

func TestCompoundLayoutArchiveRoundTripIsDeterministic(t *testing.T) {
	ctx := context.Background()
	source, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	want := archiveThreeWayTurnout()
	want, err = model.NormalizeTurnout(want)
	if err != nil {
		t.Fatal(err)
	}
	layout := model.LayoutDefinition{
		Blocks:   []model.Block{{ID: "block-a", Name: "Block A"}},
		Turnouts: []model.Turnout{want},
		Routes: []model.RouteDefinition{{
			ID: "route-left", Name: "Route left", BlockIDs: []string{"block-a"},
			TurnoutStates: map[string]string{want.ID: "left"},
		}},
	}
	if err := source.ImportLayout(ctx, layout, false); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC))
	svc := New(source, events.New(), clk)
	first, err := svc.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("two exports of unchanged layout differ")
	}
	target, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	admin := model.User{ID: "admin", Role: model.RoleAdministrator}
	if err := New(target, events.New(), clock.Real{}).ImportLayout(ctx, admin, first, true); err != nil {
		t.Fatal(err)
	}
	got, err := target.GetTurnout(ctx, want.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip mismatch:\n got: %#v\nwant: %#v", got, want)
	}
	exported, err := target.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(exported.Routes) != 1 || exported.Routes[0].TurnoutStates[want.ID] != "left" {
		t.Fatalf("compound route was not preserved: %#v", exported.Routes)
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

func archiveThreeWayTurnout() model.Turnout {
	return model.Turnout{
		ID: "three-way", Name: "Three way", Kind: model.TurnoutKindThreeWay,
		Endpoints: []model.AccessoryEndpoint{{ID: "A", LinearAddress: 20}, {ID: "B", LinearAddress: 21}},
		Positions: []model.TurnoutPositionDefinition{
			{ID: "left", Endpoints: map[string]model.AccessoryPosition{"A": model.AccessoryPosition2, "B": model.AccessoryPosition1}},
			{ID: "straight", Endpoints: map[string]model.AccessoryPosition{"A": model.AccessoryPosition1, "B": model.AccessoryPosition1}},
			{ID: "right", Endpoints: map[string]model.AccessoryPosition{"A": model.AccessoryPosition1, "B": model.AccessoryPosition2}},
		},
		DesiredPosition: "straight",
	}
}
