package transfer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/presentation"
	"github.com/agm650/TrainPilot-server/internal/store"
	"github.com/agm650/TrainPilot-server/internal/topology"
)

func signallingLayout() model.LayoutDefinition {
	p := model.SNCFBAL3Profile()
	p.ID = "CUSTOM"
	p.Name = "Configurable profile"
	ids := []model.SignalAspectID{"halt", "warn", "run"}
	for i := range p.Aspects {
		p.Aspects[i].ID = ids[i]
	}
	p.FallbackAspect = "halt"
	view := model.EmptyLayoutPresentation()
	view.Signals = []model.LayoutSignalPosition{{SignalID: "s1", X: 10, Y: 20, RotationDegrees: 90, Mirrored: true}}
	return model.LayoutDefinition{Presentation: &view, SignalProfiles: []model.SignalProfile{p}, Signals: []model.SignalDefinition{{ID: "s1", Name: "Signal", ProfileID: p.ID, Placement: model.SignalPlacement{TrackSectionID: "ab", EntryNodeID: "a"}, ProtectedBlockID: "block", Downstream: model.SignalDownstream{Kind: model.SignalDownstreamTerminal, Aspect: "halt"}, Endpoint: model.SignalEndpoint{Kind: model.SignalEndpointDCCExtended, LinearAddress: 42, AspectCodes: map[model.SignalAspectID]int{"halt": 0, "warn": 7, "run": 255}}}}, TopologyNodes: []model.TopologyNode{{ID: "a", Kind: model.TopologyNodeBoundary}, {ID: "b", Kind: model.TopologyNodeBoundary}}, TrackSections: []model.TrackSection{{ID: "ab", Name: "AB", NodeAID: "a", NodeBID: "b"}}, Blocks: []model.BlockDefinition{{ID: "block", Name: "Block", TrackSectionIDs: []string{"ab"}}}}
}

func TestSignallingArchiveRoundTripAndPresentationIndependence(t *testing.T) {
	ctx := context.Background()
	source := openArchiveStore(t)
	admin := model.User{ID: "admin", Role: model.RoleAdministrator}
	clk := clock.NewFake(time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC))
	if err := source.ImportLayout(ctx, signallingLayout(), true); err != nil {
		t.Fatal(err)
	}
	first, err := New(source, events.New(), clk).ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if archiveManifest(t, first).Version != 9 || FormatVersion != 7 {
		t.Fatal("wrong archive version")
	}
	data := archiveEntry(t, first, "layout.json")
	for _, key := range []string{"reportedAspect", "commandedAspect", "reportQuality", "computedAspect", "pending", "synchronized"} {
		if bytes.Contains(data, []byte(`"`+key+`"`)) {
			t.Fatalf("runtime field %s exported", key)
		}
	}
	target := openArchiveStore(t)
	svc := New(target, events.New(), clk)
	if err := svc.ImportLayout(ctx, admin, first, true); err != nil {
		t.Fatal(err)
	}
	second, err := svc.ExportLayout(ctx)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatal("non-deterministic signalling round trip", err)
	}
	layout, err := target.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if layout.Signals[0].Endpoint.AspectCodes["run"] != 255 || layout.Signals[0].Placement.EntryNodeID != "a" || len(layout.SignalProfiles) != 1 {
		t.Fatal("signal definition lost")
	}
	state, err := model.NewSignalState(layout.Signals[0], layout.SignalProfiles[0])
	if err != nil || state.ReportedAspect != nil || state.CommandedAspect != nil {
		t.Fatal("runtime restored", err)
	}
	before, _ := topology.DefinitionFromLayout(layout)
	view, _ := target.GetLayoutPresentation(ctx)
	beforeView, _ := presentation.Definition(view)
	view.Signals[0].X = 999
	if err := target.ReplaceLayoutPresentation(ctx, view); err != nil {
		t.Fatal(err)
	}
	afterLayout, _ := target.ExportLayout(ctx)
	after, _ := topology.DefinitionFromLayout(afterLayout)
	afterView, _ := presentation.Definition(*afterLayout.Presentation)
	if before.Revision != after.Revision || beforeView.Revision == afterView.Revision {
		t.Fatal("presentation and topology revisions coupled")
	}
	// Replacing logical definitions with the same identifiers retains drawing data.
	if err := target.ReplaceSignalDefinitions(ctx, layout.SignalProfiles, layout.Signals, true); err != nil {
		t.Fatal(err)
	}
	retained, _ := target.GetLayoutPresentation(ctx)
	if !reflect.DeepEqual(retained.Signals, view.Signals) {
		t.Fatal("configuration replacement lost retained symbol")
	}
}

func TestSignallingArchivesLegacyMergeAndReplace(t *testing.T) {
	ctx := context.Background()
	admin := model.User{Role: model.RoleAdministrator}
	for _, version := range []int{7, 8} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			db := openArchiveStore(t)
			full := signallingLayout()
			if err := db.ImportLayout(ctx, full, true); err != nil {
				t.Fatal(err)
			}
			legacy := full
			legacy.Signals = nil
			legacy.SignalProfiles = nil
			empty := model.EmptyLayoutPresentation()
			legacy.Presentation = &empty
			data, err := writeArchive(Manifest{Format: FormatID, Version: version, PackageType: "layout", CreatedAt: time.Now()}, "layout.json", LayoutDocument{Layout: legacy})
			if err != nil {
				t.Fatal(err)
			}
			svc := New(db, events.New(), clock.Real{})
			if err := svc.ImportLayout(ctx, admin, data, false); err != nil {
				t.Fatal(err)
			}
			signals, _ := db.ListSignals(ctx)
			view, _ := db.GetLayoutPresentation(ctx)
			if len(signals) != 1 || len(view.Signals) != 1 {
				t.Fatal("legacy merge lost signalling")
			}
			if err := svc.ImportLayout(ctx, admin, data, true); err != nil {
				t.Fatal(err)
			}
			signals, _ = db.ListSignals(ctx)
			view, _ = db.GetLayoutPresentation(ctx)
			if signals == nil || len(signals) != 0 || len(view.Signals) != 0 {
				t.Fatal("legacy replacement retained signalling")
			}
		})
	}
}

func TestSignallingArchiveRejectsInvalidReferencesAndRuntime(t *testing.T) {
	ctx := context.Background()
	admin := model.User{Role: model.RoleAdministrator}
	db := openArchiveStore(t)
	bus := events.New()
	clk := clock.NewFake(time.Now())
	svc := New(db, bus, clk)
	if err := db.ImportLayout(ctx, signallingLayout(), true); err != nil {
		t.Fatal(err)
	}
	before, _ := svc.ExportLayout(ctx)
	sequence := bus.CurrentSequence()
	for _, kind := range []string{"orphan-symbol", "duplicate-symbol", "missing-profile", "wrong-orientation", "runtime", "old-manifest"} {
		t.Run(kind, func(t *testing.T) {
			var document map[string]any
			if err := json.Unmarshal(archiveEntry(t, before, "layout.json"), &document); err != nil {
				t.Fatal(err)
			}
			layout := document["layout"].(map[string]any)
			version := 9
			switch kind {
			case "orphan-symbol":
				layout["presentation"].(map[string]any)["signals"].([]any)[0].(map[string]any)["signalId"] = "missing"
			case "duplicate-symbol":
				p := layout["presentation"].(map[string]any)
				values := p["signals"].([]any)
				p["signals"] = append(values, values[0])
			case "missing-profile":
				layout["signalProfiles"] = []any{}
			case "wrong-orientation":
				layout["signals"].([]any)[0].(map[string]any)["placement"].(map[string]any)["entryNodeId"] = "missing"
			case "runtime":
				layout["signals"].([]any)[0].(map[string]any)["reportedAspect"] = "run"
			case "old-manifest":
				version = 8
			}
			data, err := writeArchive(Manifest{Format: FormatID, Version: version, PackageType: "layout", CreatedAt: clk.Now()}, "layout.json", document)
			if err != nil {
				t.Fatal(err)
			}
			if err := svc.ImportLayout(ctx, admin, data, true); !errors.Is(err, ErrInvalidArchive) {
				t.Fatal("invalid archive accepted", err)
			}
			after, _ := svc.ExportLayout(ctx)
			if !bytes.Equal(before, after) || bus.CurrentSequence() != sequence {
				t.Fatal("refused import mutated layout or emitted an event")
			}
		})
	}
}

func TestSignalMergeEndpointConflictRollsBack(t *testing.T) {
	ctx := context.Background()
	db := openArchiveStore(t)
	full := signallingLayout()
	if err := db.ImportLayout(ctx, full, true); err != nil {
		t.Fatal(err)
	}
	bus := events.New()
	svc := New(db, bus, clock.Real{})
	incoming := signallingLayout()
	incoming.Signals[0].ID = "s2"
	incoming.Presentation = nil
	data, err := BuildLayoutArchive(time.Now(), incoming)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ImportLayout(ctx, model.User{Role: model.RoleAdministrator}, data, false); !errors.Is(err, store.ErrConflict) {
		t.Fatal("endpoint conflict not classified", err)
	}
	signals, _ := db.ListSignals(ctx)
	if len(signals) != 1 || signals[0].ID != "s1" || bus.CurrentSequence() != 0 {
		t.Fatal("conflicting merge mutated configuration")
	}
}
