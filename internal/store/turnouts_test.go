package store

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/sqlite"
	"github.com/agm650/TrainPilot-server/internal/station"
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

func TestTurnoutObservationDoesNotOverwriteTerminalCommandState(t *testing.T) {
	ctx := context.Background()
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	turnout := model.NewSimpleTurnout("turnout", "Turnout", 12, "straight", "straight")
	if err := store.ImportLayout(ctx, model.LayoutDefinition{Turnouts: []model.Turnout{turnout}}, false); err != nil {
		t.Fatal(err)
	}
	if err := store.SetTurnoutDesiredPosition(ctx, turnout.ID, "diverging", true); err != nil {
		t.Fatal(err)
	}
	if err := store.SetTurnoutCommandResult(ctx, turnout.ID, false, model.TurnoutCommandTimeout); err != nil {
		t.Fatal(err)
	}
	if err := store.SetTurnoutObservation(ctx, turnout.ID, "diverging", station.AccessoryReportKnown, station.AccessoryReportPhysical); err != nil {
		t.Fatal(err)
	}

	stored, err := store.GetTurnout(ctx, turnout.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Pending || stored.CommandStatus != model.TurnoutCommandTimeout {
		t.Fatalf("observation overwrote terminal command state: %+v", stored)
	}
	if stored.ReportedPosition != "diverging" || stored.ReportedStatus != station.AccessoryReportKnown || stored.Quality != station.AccessoryReportPhysical {
		t.Fatalf("observation was not stored: %+v", stored)
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

func TestImportLayoutRejectsAccessoryAddressSharedByDifferentTurnouts(t *testing.T) {
	ctx := context.Background()

	t.Run("same import", func(t *testing.T) {
		store, err := Open(":memory:")
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		first := model.NewSimpleTurnout("first", "First", 12, "", "")
		second := model.NewSimpleTurnout("second", "Second", 12, "", "")
		err = store.ImportLayout(ctx, model.LayoutDefinition{Turnouts: []model.Turnout{first, second}}, false)
		if !errors.Is(err, ErrAccessoryAddressConflict) || !errors.Is(err, ErrConflict) {
			t.Fatalf("ImportLayout error=%v", err)
		}
		turnouts, listErr := store.ListTurnouts(ctx)
		if listErr != nil {
			t.Fatal(listErr)
		}
		if len(turnouts) != 0 {
			t.Fatalf("conflicting import modified database: %+v", turnouts)
		}
	})

	t.Run("existing turnout", func(t *testing.T) {
		store, err := Open(":memory:")
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		first := model.NewSimpleTurnout("first", "First", 12, "", "")
		if err := store.ImportLayout(ctx, model.LayoutDefinition{Turnouts: []model.Turnout{first}}, false); err != nil {
			t.Fatal(err)
		}
		second := model.NewSimpleTurnout("second", "Second", 12, "", "")
		err = store.ImportLayout(ctx, model.LayoutDefinition{Turnouts: []model.Turnout{second}}, false)
		if !errors.Is(err, ErrAccessoryAddressConflict) {
			t.Fatalf("ImportLayout error=%v", err)
		}
		turnouts, listErr := store.ListTurnouts(ctx)
		if listErr != nil {
			t.Fatal(listErr)
		}
		if len(turnouts) != 1 || turnouts[0].ID != first.ID {
			t.Fatalf("conflicting merge modified database: %+v", turnouts)
		}
	})
}

func TestImportLayoutRejectsPendingTurnoutModificationOrDeletion(t *testing.T) {
	ctx := context.Background()
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	turnout := model.NewSimpleTurnout("pending", "Pending", 12, "straight", "straight")
	if err := store.ImportLayout(ctx, model.LayoutDefinition{Turnouts: []model.Turnout{turnout}}, false); err != nil {
		t.Fatal(err)
	}
	if err := store.SetTurnoutDesiredPosition(ctx, turnout.ID, "diverging", true); err != nil {
		t.Fatal(err)
	}

	modified := turnout
	modified.Name = "Modified while moving"
	if err := store.ImportLayout(ctx, model.LayoutDefinition{Turnouts: []model.Turnout{modified}}, false); !errors.Is(err, ErrTurnoutConfigurationPending) || !errors.Is(err, ErrConflict) {
		t.Fatalf("pending merge error=%v", err)
	}
	if err := store.ImportLayout(ctx, model.LayoutDefinition{}, true); !errors.Is(err, ErrTurnoutConfigurationPending) {
		t.Fatalf("pending replacement error=%v", err)
	}

	stored, err := store.GetTurnout(ctx, turnout.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != turnout.Name || !stored.Pending || stored.DesiredPosition != "diverging" {
		t.Fatalf("pending turnout changed by rejected import: %+v", stored)
	}

	unrelated := model.NewSimpleTurnout("unrelated", "Unrelated", 13, "", "")
	if err := store.ImportLayout(ctx, model.LayoutDefinition{Turnouts: []model.Turnout{unrelated}}, false); err != nil {
		t.Fatalf("unrelated merge while another turnout is pending: %v", err)
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
