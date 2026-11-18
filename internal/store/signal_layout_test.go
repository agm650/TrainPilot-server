package store

import (
	"context"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/topology"
)

func TestSignalLayoutImportAndRevisionAreAtomic(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	l := signalTestLayout()
	l.SignalProfiles = []model.SignalProfile{model.SNCFBAL3Profile()}
	l.Signals = []model.SignalDefinition{testSignalDefinition()}
	if err := s.ImportLayout(ctx, l, true); err != nil {
		t.Fatal(err)
	}
	before, _ := s.ExportLayout(ctx)
	definition, err := topology.DefinitionFromLayout(before)
	if err != nil {
		t.Fatal(err)
	}
	l.Signals[0].Placement.EntryNodeID = "c"
	if err := s.ImportLayout(ctx, l, true); err == nil {
		t.Fatal("invalid import accepted")
	}
	after, _ := s.ExportLayout(ctx)
	retained, err := topology.DefinitionFromLayout(after)
	if err != nil || definition.Revision != retained.Revision {
		t.Fatal("failed import changed revision", err)
	}
	profile := l.SignalProfiles[0]
	profile.Name = "New configured name"
	if err := s.SetSignalProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	after, _ = s.ExportLayout(ctx)
	changed, err := topology.DefinitionFromLayout(after)
	if err != nil || changed.Revision == retained.Revision {
		t.Fatal("profile edit did not change topology revision", err)
	}
	if _, err := topology.BuildDefinition(changed, nil); err != nil {
		t.Fatal("published definition cannot be consumed", err)
	}
	legacy := signalTestLayout()
	if err := s.ImportLayout(ctx, legacy, false); err != nil {
		t.Fatal(err)
	}
	signals, _ := s.ListSignals(ctx)
	if len(signals) != 1 {
		t.Fatal("legacy merge lost signals")
	}
	if err := s.ImportLayout(ctx, legacy, true); err != nil {
		t.Fatal(err)
	}
	signals, _ = s.ListSignals(ctx)
	if signals == nil || len(signals) != 0 {
		t.Fatal("legacy replacement retained signals")
	}
}
