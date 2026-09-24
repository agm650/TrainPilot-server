package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/model/topologyfixture"
	"github.com/agm650/TrainPilot-server/internal/topology"
)

func presentationFixture() model.LayoutPresentation {
	point := func(x, y float64) *model.LayoutPoint { return &model.LayoutPoint{X: x, Y: y} }
	return model.LayoutPresentation{
		CoordinateSystem: model.LayoutCoordinateSystem,
		GridSpacing:      20,
		Nodes: []model.LayoutNodePosition{
			{NodeID: "west-boundary", X: 0, Y: 20},
			{NodeID: "station-west-stem", X: 100, Y: 20},
			{NodeID: "station-east-stem", X: 300, Y: 20},
			{NodeID: "east-boundary", X: 400, Y: 20},
		},
		TrackSections: []model.LayoutTrackPath{
			{TrackSectionID: "approach-west", Segments: []model.LayoutTrackSegment{
				{Type: "line", To: point(40, 20)},
				{Type: "cubic", Control1: point(60, 20), Control2: point(80, 30), To: point(100, 20)},
			}},
			{TrackSectionID: "approach-east", Segments: []model.LayoutTrackSegment{{Type: "line", To: point(400, 20)}}},
		},
		Turnouts: []model.LayoutTurnoutPosition{
			{TurnoutID: "station-west", X: 100, Y: 20, RotationDegrees: 90, Mirrored: true},
			{TurnoutID: "station-east", X: 300, Y: 20, RotationDegrees: 180},
		},
		Blocks: []model.LayoutBlockStyle{
			{BlockID: "block-west", Color: "#33AADD", Opacity: 0.3},
			{BlockID: "block-east", Color: "#aabbcc", Opacity: 1},
		},
	}
}

func TestLayoutPresentationPersistsAcrossRestartAndStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "layout.sqlite")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	layout := topologyfixture.PassingStation()
	p := presentationFixture()
	layout.Presentation = &p
	if err := db.ImportLayout(ctx, layout, true); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	want := presentationFixture()
	sort.Slice(want.Nodes, func(i, j int) bool { return want.Nodes[i].NodeID < want.Nodes[j].NodeID })
	sort.Slice(want.TrackSections, func(i, j int) bool {
		return want.TrackSections[i].TrackSectionID < want.TrackSections[j].TrackSectionID
	})
	sort.Slice(want.Turnouts, func(i, j int) bool { return want.Turnouts[i].TurnoutID < want.Turnouts[j].TurnoutID })
	sort.Slice(want.Blocks, func(i, j int) bool { return want.Blocks[i].BlockID < want.Blocks[j].BlockID })
	got, err := db.GetLayoutPresentation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("presentation after restart = %#v, want %#v", got, want)
	}
	exported, err := db.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if exported.Presentation == nil || !reflect.DeepEqual(*exported.Presentation, want) {
		t.Fatalf("store export presentation = %#v", exported.Presentation)
	}

	other, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := other.ImportLayout(ctx, exported, true); err != nil {
		t.Fatal(err)
	}
	roundTrip, err := other.GetLayoutPresentation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(roundTrip, want) {
		t.Fatalf("store round trip presentation = %#v, want %#v", roundTrip, want)
	}
	firstJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := json.Marshal(roundTrip)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("non-deterministic presentation JSON: %s != %s", firstJSON, secondJSON)
	}
}

func TestLayoutPresentationDefaultsAndRuntimeIndependence(t *testing.T) {
	ctx := context.Background()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	empty := model.EmptyLayoutPresentation()
	got, err := db.GetLayoutPresentation(ctx)
	if err != nil || !reflect.DeepEqual(got, empty) {
		t.Fatalf("empty database presentation = %#v, error = %v", got, err)
	}
	layout := topologyfixture.PassingStation()
	if err := db.ImportLayout(ctx, layout, true); err != nil {
		t.Fatal(err)
	}
	got, err = db.GetLayoutPresentation(ctx)
	if err != nil || !reflect.DeepEqual(got, empty) {
		t.Fatalf("legacy layout presentation = %#v, error = %v", got, err)
	}
	before, err := db.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	beforeTopology, err := topology.DefinitionFromLayout(before)
	if err != nil {
		t.Fatal(err)
	}
	p := presentationFixture()
	if err := db.ReplaceLayoutPresentation(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := db.ImportLayout(ctx, model.LayoutDefinition{}, false); err != nil {
		t.Fatal(err)
	}
	merged, err := db.GetLayoutPresentation(ctx)
	if err != nil || len(merged.TrackSections) != len(p.TrackSections) {
		t.Fatalf("merge without presentation changed drawing: %#v, error = %v", merged, err)
	}
	if _, err := db.DB.ExecContext(ctx, `UPDATE blocks SET occupied=1 WHERE id='block-west'`); err != nil {
		t.Fatal(err)
	}
	after, err := db.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	afterTopology, err := topology.DefinitionFromLayout(after)
	if err != nil {
		t.Fatal(err)
	}
	if beforeTopology.Revision != afterTopology.Revision {
		t.Fatal("presentation or runtime change modified physical topology revision")
	}
	var occupied int
	if err := db.DB.QueryRowContext(ctx, `SELECT occupied FROM blocks WHERE id='block-west'`).Scan(&occupied); err != nil || occupied != 1 {
		t.Fatalf("runtime occupancy = %d, error = %v", occupied, err)
	}
	if err := db.ImportLayout(ctx, layout, true); err != nil {
		t.Fatal(err)
	}
	got, err = db.GetLayoutPresentation(ctx)
	if err != nil || !reflect.DeepEqual(got, empty) {
		t.Fatalf("replace with legacy layout presentation = %#v, error = %v", got, err)
	}
}

func TestInvalidLayoutPresentationRollsBackLayoutImport(t *testing.T) {
	ctx := context.Background()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	layout := topologyfixture.PassingStation()
	p := presentationFixture()
	layout.Presentation = &p
	if err := db.ImportLayout(ctx, layout, true); err != nil {
		t.Fatal(err)
	}
	before, err := db.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	invalid := topologyfixture.PassingStation()
	invalid.Blocks[0].Name = "Changed in failed import"
	bad := presentationFixture()
	bad.Blocks[0].Color = "bad"
	invalid.Presentation = &bad
	if err := db.ImportLayout(ctx, invalid, true); !errors.Is(err, model.ErrInvalidLayoutPresentation) {
		t.Fatalf("invalid import error = %v", err)
	}
	after, err := db.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("invalid presentation changed layout: before=%#v after=%#v", before, after)
	}
}
