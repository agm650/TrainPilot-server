package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/sqlite"
)

func TestLocomotiveFunctionMigrationPersistenceAndLegacyMerge(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "locomotives.db")
	legacy, err := sqlite.OpenWithJournalMode(path, "wal")
	if err != nil {
		t.Fatal(err)
	}
	_, err = legacy.ExecContext(ctx, `CREATE TABLE locomotives (
		id TEXT PRIMARY KEY, name TEXT NOT NULL, dcc_address INTEGER NOT NULL,
		address_kind TEXT NOT NULL DEFAULT 'short', speed_steps INTEGER NOT NULL DEFAULT 128,
		manufacturer TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '')`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.ExecContext(ctx, `INSERT INTO locomotives(id,name,dcc_address) VALUES('loco','Legacy',3)`); err != nil {
		t.Fatal(err)
	}
	_ = legacy.Close()
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	loco, err := db.GetLocomotive(ctx, "loco")
	if err != nil || loco.Functions == nil || len(loco.Functions) != 0 || loco.IsMomentary(2) {
		t.Fatalf("migrated locomotive=%+v err=%v", loco, err)
	}
	loco.Functions = []model.LocomotiveFunction{{Number: 0}, {Number: 2, Momentary: true}}
	if err := db.UpdateLocomotive(ctx, loco); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	items, err := db.ListLocomotives(ctx)
	if err != nil || len(items) != 1 || !items[0].IsMomentary(2) || items[0].IsMomentary(0) {
		t.Fatalf("reopened locomotives=%+v err=%v", items, err)
	}
	loco = items[0]
	loco.Name, loco.Functions = "Legacy merge", nil
	if err := db.ReplaceLocomotives(ctx, []model.Locomotive{loco}, false); err != nil {
		t.Fatal(err)
	}
	loco, err = db.GetLocomotive(ctx, "loco")
	if err != nil || !loco.IsMomentary(2) || loco.Name != "Legacy merge" {
		t.Fatalf("legacy merge erased definitions: %+v err=%v", loco, err)
	}
	loco.Functions = []model.LocomotiveFunction{}
	if err := db.UpdateLocomotive(ctx, loco); err != nil {
		t.Fatal(err)
	}
	loco, err = db.GetLocomotive(ctx, "loco")
	if err != nil || len(loco.Functions) != 0 {
		t.Fatalf("explicit reset=%+v err=%v", loco, err)
	}
}
