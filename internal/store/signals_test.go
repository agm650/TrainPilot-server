package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/model"
)

func signalTestLayout() model.LayoutDefinition {
	return model.LayoutDefinition{
		TopologyNodes: []model.TopologyNode{{ID: "a", Kind: model.TopologyNodeBoundary}, {ID: "b", Kind: model.TopologyNodeJoint}, {ID: "c", Kind: model.TopologyNodeBoundary}},
		TrackSections: []model.TrackSection{{ID: "ab", Name: "AB", NodeAID: "a", NodeBID: "b"}, {ID: "bc", Name: "BC", NodeAID: "b", NodeBID: "c"}},
		Blocks:        []model.BlockDefinition{{ID: "block", Name: "Block", TrackSectionIDs: []string{"ab"}}, {ID: "block-b", Name: "Block B", TrackSectionIDs: []string{"bc"}}},
	}
}
func testSignalDefinition() model.SignalDefinition {
	return model.SignalDefinition{ID: "s1", Name: "Signal", ProfileID: model.SignalProfileSNCFBAL3, Placement: model.SignalPlacement{TrackSectionID: "ab", EntryNodeID: "a"}, ProtectedBlockID: "block", Downstream: model.SignalDownstream{Kind: model.SignalDownstreamTerminal, Aspect: "S"}, Endpoint: model.SignalEndpoint{Kind: model.SignalEndpointDCCExtended, LinearAddress: 1, AspectCodes: map[model.SignalAspectID]int{"S": 0, "A": 7, "VL": 16}}}
}

func TestSignalPersistenceMigrationAndRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "layout.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ImportLayout(ctx, signalTestLayout(), true); err != nil {
		t.Fatal(err)
	}
	// Simulate the existing pre-signalling database, preserving its layout.
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE signals`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `DROP TABLE signal_profiles`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := s.ListSignalProfiles(ctx)
	if err != nil || profiles == nil || len(profiles) != 0 {
		t.Fatal(profiles, err)
	}
	signals, err := s.ListSignals(ctx)
	if err != nil || signals == nil || len(signals) != 0 {
		t.Fatal(signals, err)
	}
	p := model.SNCFBAL3Profile()
	value := testSignalDefinition()
	if err := s.ReplaceSignalDefinitions(ctx, []model.SignalProfile{p}, []model.SignalDefinition{value}, true); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	loaded, err := s.Signal(ctx, value.ID)
	if err != nil {
		t.Fatal(err)
	}
	normalized, _ := model.NormalizeSignalDefinition(value, p)
	if !reflect.DeepEqual(loaded, normalized) {
		t.Fatal(loaded)
	}
	state, err := model.NewSignalState(loaded, p)
	if err != nil || state.ReportedAspect != nil || state.CommandedAspect != nil || state.Pending || state.Synchronized {
		t.Fatal(state, err)
	}
	blocks, err := s.ListBlockDefinitions(ctx)
	if err != nil || len(blocks) != 2 {
		t.Fatal("migration lost layout", err)
	}
}

func TestSignalsConstraintsRollbackAndProfileUpdate(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ImportLayout(ctx, signalTestLayout(), true); err != nil {
		t.Fatal(err)
	}
	p := model.SNCFBAL3Profile()
	if err := s.SetSignalProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	one := testSignalDefinition()
	one.Placement = model.SignalPlacement{TrackSectionID: "bc", EntryNodeID: "b"}
	one.ProtectedBlockID = "block-b"
	if err := s.SetSignal(ctx, one); err != nil {
		t.Fatal(err)
	}
	two := testSignalDefinition()
	two.ID = "s2"
	if err := s.SetSignal(ctx, two); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate endpoint accepted", err)
	}
	two.Endpoint.LinearAddress = 2
	two.Downstream = model.SignalDownstream{Kind: model.SignalDownstreamSignal, SignalID: "s1"}
	if err := s.SetSignal(ctx, two); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSignal(ctx, "s1"); !errors.Is(err, ErrConflict) {
		t.Fatal("referenced signal deleted", err)
	}
	if err := s.DeleteSignalProfile(ctx, p.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("referenced profile deleted", err)
	}
	p.Aspects[2].ID = "changed"
	if err := s.SetSignalProfile(ctx, p); !errors.Is(err, ErrConflict) {
		t.Fatal("profile change invalidated signals", err)
	}
	retained, err := s.SignalProfile(ctx, p.ID)
	if err != nil || retained.Aspects[2].ID != "VL" {
		t.Fatal("profile update did not roll back", err)
	}
	two.Endpoint.AspectCodes["A"] = -1
	if err := s.ReplaceSignalDefinitions(ctx, []model.SignalProfile{model.SNCFBAL3Profile()}, []model.SignalDefinition{one, two}, true); err == nil {
		t.Fatal("invalid bulk accepted")
	}
	values, _ := s.ListSignals(ctx)
	if len(values) != 2 {
		t.Fatal("bulk did not roll back")
	}
	if err := s.DeleteSignal(ctx, "s2"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSignal(ctx, "s1"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSignalProfile(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSignal(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}
