package transfer

import (
	"context"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
)

func TestRollingStockFunctionsRoundTripAndLegacyImport(t *testing.T) {
	ctx := context.Background()
	db := openArchiveStore(t)
	svc := New(db, events.New(), clock.Real{})
	admin := model.User{Role: model.RoleAdministrator}
	loco := model.Locomotive{ID: "loco", Name: "Horn", DCCAddress: 3, AddressKind: "short", SpeedSteps: 128, Functions: []model.LocomotiveFunction{{Number: 2, Momentary: true}}}
	data, err := BuildRollingStockArchive(time.Now(), []model.Locomotive{loco})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ImportRollingStock(ctx, admin, data, true); err != nil {
		t.Fatal(err)
	}
	data, err = svc.ExportRollingStock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ImportRollingStock(ctx, admin, data, true); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetLocomotive(ctx, loco.ID)
	if err != nil || !got.IsMomentary(2) {
		t.Fatalf("round trip lost definitions: %+v err=%v", got, err)
	}
	// Old archives contain no definitions. Merging preserves existing settings;
	// importing into a new library defaults to normal functions.
	loco.Functions = nil
	legacy, err := writeArchive(Manifest{Format: FormatID, Version: 6, PackageType: "rolling-stock", CreatedAt: time.Now()}, "rolling-stock.json", map[string]any{"locomotives": []map[string]any{{"id": loco.ID, "name": loco.Name, "dccAddress": 3, "addressKind": "short", "speedSteps": 128}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ImportRollingStock(ctx, admin, legacy, false); err != nil {
		t.Fatal(err)
	}
	got, err = db.GetLocomotive(ctx, loco.ID)
	if err != nil || !got.IsMomentary(2) {
		t.Fatalf("legacy merge erased definitions: %+v err=%v", got, err)
	}
	if err := svc.ImportRollingStock(ctx, admin, legacy, true); err != nil {
		t.Fatal(err)
	}
	got, err = db.GetLocomotive(ctx, loco.ID)
	if err != nil || got.IsMomentary(2) || len(got.Functions) != 0 {
		t.Fatalf("legacy creation must default to normal: %+v err=%v", got, err)
	}
	for _, definitions := range [][]model.LocomotiveFunction{{{Number: -1}}, {{Number: 69}}, {{Number: 2}, {Number: 2, Momentary: true}}} {
		loco.Functions = definitions
		if _, err := BuildRollingStockArchive(time.Now(), []model.Locomotive{loco}); err == nil {
			t.Fatalf("invalid definitions accepted: %+v", definitions)
		}
	}
}
