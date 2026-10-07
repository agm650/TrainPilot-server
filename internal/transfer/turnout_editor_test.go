package transfer

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/model/topologyfixture"
)

func TestTurnoutEditorArchivesRoundTripEveryKindWithoutLegacyAddress(t *testing.T) {
	custom := topologyfixture.ThreeWay()
	custom.Turnouts[0].Kind = model.TurnoutKindCustom
	custom.Turnouts[0].Endpoints = append(custom.Turnouts[0].Endpoints, model.AccessoryEndpoint{ID: "C", LinearAddress: 22})
	for i := range custom.Turnouts[0].Positions {
		custom.Turnouts[0].Positions[i].Endpoints["C"] = model.AccessoryPosition1
	}
	inverted := topologyfixture.Simple()
	inverted.Turnouts[0].Endpoints[0].Inverted = true
	for _, test := range []struct {
		name   string
		layout model.LayoutDefinition
	}{
		{"simple", topologyfixture.Simple()},
		{"inverted simple", inverted},
		{"three way", topologyfixture.ThreeWay()},
		{"double slip", topologyfixture.DoubleSlip()},
		{"single slip", topologyfixture.SingleSlip()},
		{"custom", custom},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			archive, err := BuildLayoutArchive(time.Now(), test.layout)
			if err != nil {
				t.Fatal(err)
			}
			contents := archiveEntry(t, archive, "layout.json")
			if bytes.Contains(contents, []byte(`"dccAddress"`)) || bytes.Contains(contents, []byte(`"desiredState"`)) {
				t.Fatal("editor archive depends on deprecated turnout fields")
			}
			db := openArchiveStore(t)
			svc := New(db, events.New(), clock.Real{})
			admin := model.User{Role: model.RoleAdministrator}
			result, err := svc.ValidateLayout(ctx, admin, archive, true)
			if err != nil || !result.Valid {
				t.Fatalf("validation=%+v error=%v", result, err)
			}
			if err := svc.ImportLayout(ctx, admin, archive, true); err != nil {
				t.Fatal(err)
			}
			got, err := db.ExportLayout(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Turnouts) != 1 || len(got.TurnoutTopologies) != 1 || got.Turnouts[0].Kind != test.layout.Turnouts[0].Kind || len(got.Turnouts[0].Endpoints) != len(test.layout.Turnouts[0].Endpoints) || len(got.Turnouts[0].Positions) != len(test.layout.Turnouts[0].Positions) || got.Turnouts[0].Endpoints[0].Inverted != test.layout.Turnouts[0].Endpoints[0].Inverted {
				t.Fatalf("editor turnout changed after import: %+v", got.Turnouts)
			}
		})
	}
}

func TestTurnoutEditorDryRunReportsSpecificCodes(t *testing.T) {
	for _, test := range []struct {
		name   string
		code   string
		layout model.LayoutDefinition
	}{
		{"missing vector", "turnout_position_vector_missing", func() model.LayoutDefinition {
			layout := topologyfixture.ThreeWay()
			delete(layout.Turnouts[0].Positions[0].Endpoints, "B")
			return layout
		}()},
		{"missing topology position", "turnout_topology_position_missing", func() model.LayoutDefinition {
			layout := topologyfixture.Simple()
			layout.TurnoutTopologies[0].Positions = layout.TurnoutTopologies[0].Positions[:1]
			return layout
		}()},
	} {
		t.Run(test.name, func(t *testing.T) {
			archive, err := writeArchive(Manifest{Format: FormatID, Version: LayoutFormatVersion, PackageType: "layout", CreatedAt: time.Now()}, "layout.json", LayoutDocument{Layout: test.layout})
			if err != nil {
				t.Fatal(err)
			}
			result, err := New(openArchiveStore(t), events.New(), clock.Real{}).ValidateLayout(context.Background(), model.User{Role: model.RoleAdministrator}, archive, true)
			if err != nil || result.Valid || len(result.Errors) != 1 || result.Errors[0].Code != test.code || result.Errors[0].ResourceType != "turnout" || result.Errors[0].ResourceID != test.layout.Turnouts[0].ID {
				t.Fatalf("diagnostic=%+v error=%v", result, err)
			}
		})
	}
}
