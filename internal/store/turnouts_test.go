package store

import (
	"context"
	"reflect"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/sqlite"
)

func TestMigrateLegacyTurnoutSchema(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `CREATE TABLE turnouts (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		dcc_address INTEGER NOT NULL,
		desired_state TEXT NOT NULL DEFAULT 'straight',
		reported_state TEXT NOT NULL DEFAULT 'unknown'
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO turnouts(id,name,dcc_address,desired_state,reported_state) VALUES('legacy-12','Legacy turnout',12,'straight','diverging')`); err != nil {
		t.Fatal(err)
	}
	store := &Store{DB: db}
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	turnout, err := store.GetTurnout(ctx, "legacy-12")
	if err != nil {
		t.Fatal(err)
	}
	if turnout.Name != "Legacy turnout" || turnout.Kind != model.TurnoutKindSimple || turnout.DesiredPosition != "straight" || turnout.ReportedPosition != "diverging" {
		t.Fatalf("unexpected migrated turnout: %+v", turnout)
	}
	if turnout.ReportedStatus != "known" || turnout.Quality != "assumed" || turnout.CommandStatus != model.TurnoutCommandIdle {
		t.Fatalf("unexpected migrated runtime state: %+v", turnout)
	}
	if len(turnout.Endpoints) != 1 || turnout.Endpoints[0].ID != "main" || turnout.Endpoints[0].LinearAddress != 12 {
		t.Fatalf("unexpected migrated endpoints: %+v", turnout.Endpoints)
	}
	if len(turnout.Positions) != 2 {
		t.Fatalf("migrated positions = %+v", turnout.Positions)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("second migration: %v", err)
	}
	for table, want := range map[string]int{
		"turnout_endpoints":          1,
		"turnout_positions":          2,
		"turnout_position_endpoints": 2,
	} {
		var got int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE turnout_id='legacy-12'`).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s rows = %d, want %d", table, got, want)
		}
	}
}

func TestCompoundTurnoutPersistenceRoundTrip(t *testing.T) {
	ctx := context.Background()
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	want := persistedThreeWayTurnout()
	want, err = model.NormalizeTurnout(want)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ImportLayout(ctx, model.LayoutDefinition{Turnouts: []model.Turnout{want}}, false); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetTurnout(ctx, want.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip mismatch:\n got: %#v\nwant: %#v", got, want)
	}
	exported, err := store.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(exported.Turnouts) != 1 || !reflect.DeepEqual(exported.Turnouts[0], want) {
		t.Fatalf("unexpected export: %#v", exported.Turnouts)
	}
}

func TestSeedDemoDoesNotModifyExistingCompoundTurnout(t *testing.T) {
	ctx := context.Background()
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	want := persistedThreeWayTurnout()
	want.ID = "turnout-1"
	want, err = model.NormalizeTurnout(want)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ImportLayout(ctx, model.LayoutDefinition{Turnouts: []model.Turnout{want}}, false); err != nil {
		t.Fatal(err)
	}
	if err := store.SeedDemo(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetTurnout(ctx, want.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SeedDemo modified compound turnout:\n got: %#v\nwant: %#v", got, want)
	}
}

func persistedThreeWayTurnout() model.Turnout {
	return model.Turnout{
		ID: "three-way", Name: "Three way", Kind: model.TurnoutKindThreeWay,
		Endpoints: []model.AccessoryEndpoint{
			{ID: "A", LinearAddress: 20},
			{ID: "B", LinearAddress: 21, Inverted: true},
		},
		Positions: []model.TurnoutPositionDefinition{
			{ID: "left", Label: "Left", Endpoints: map[string]model.AccessoryPosition{"A": model.AccessoryPosition2, "B": model.AccessoryPosition1}},
			{ID: "straight", Endpoints: map[string]model.AccessoryPosition{"A": model.AccessoryPosition1, "B": model.AccessoryPosition1}},
			{ID: "right", Endpoints: map[string]model.AccessoryPosition{"A": model.AccessoryPosition1, "B": model.AccessoryPosition2}},
		},
		DesiredPosition: "straight", ReportedPosition: "", Pending: true,
	}
}
