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
	"github.com/agm650/TrainPilot-server/internal/model/topologyfixture"
	"github.com/agm650/TrainPilot-server/internal/store"
)

func visualLayout() model.LayoutDefinition {
	layout := topologyfixture.PassingStation()
	p := model.EmptyLayoutPresentation()
	p.GridSpacing = 25
	p.Nodes = []model.LayoutNodePosition{
		{NodeID: "west-boundary", X: 0, Y: 10},
		{NodeID: "station-west-stem", X: 100, Y: 10},
		{NodeID: "east-boundary", X: 600, Y: 10},
	}
	p.TrackSections = []model.LayoutTrackPath{{TrackSectionID: "approach-west", Segments: []model.LayoutTrackSegment{
		{Type: "line", To: &model.LayoutPoint{X: 40, Y: 10}},
		{Type: "cubic", Control1: &model.LayoutPoint{X: 60, Y: 10}, Control2: &model.LayoutPoint{X: 80, Y: 10}, To: &model.LayoutPoint{X: 100, Y: 10}},
	}}}
	p.Turnouts = []model.LayoutTurnoutPosition{{TurnoutID: "station-west", X: 100, Y: 10, RotationDegrees: 90, Mirrored: true}}
	p.Blocks = []model.LayoutBlockStyle{
		{BlockID: "block-west", Color: "#33AADD", Opacity: 0.4},
		{BlockID: "block-east", Color: "#112233", Opacity: 0.8},
	}
	layout.Presentation = &p
	return layout
}

func openArchiveStore(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func archiveManifest(t *testing.T, data []byte) Manifest {
	t.Helper()
	var manifest Manifest
	if err := json.Unmarshal(archiveEntry(t, data, "manifest.json"), &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestLayoutPresentationArchiveRoundTrip(t *testing.T) {
	ctx := context.Background()
	admin := model.User{ID: "admin", Role: model.RoleAdministrator}
	source := openArchiveStore(t)
	if err := source.ImportLayout(ctx, visualLayout(), true); err != nil {
		t.Fatal(err)
	}
	want, err := source.GetLayoutPresentation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	first, err := New(source, events.New(), clock.NewFake(createdAt)).ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if manifest := archiveManifest(t, first); manifest.Version != LayoutFormatVersion {
		t.Fatalf("layout archive version=%d", manifest.Version)
	}
	var doc LayoutDocument
	if err := json.Unmarshal(archiveEntry(t, first, "layout.json"), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Layout.Presentation == nil || !reflect.DeepEqual(*doc.Layout.Presentation, want) {
		t.Fatalf("presentation missing or changed: %+v", doc.Layout.Presentation)
	}
	target := openArchiveStore(t)
	if err := New(target, events.New(), clock.Real{}).ImportLayout(ctx, admin, first, true); err != nil {
		t.Fatal(err)
	}
	got, err := target.GetLayoutPresentation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("imported presentation changed: %+v", got)
	}
	second, err := New(target, events.New(), clock.NewFake(createdAt)).ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("layout archive changed after export/import round trip")
	}
}

func TestLayoutPresentationArchiveMergeAndReplace(t *testing.T) {
	ctx := context.Background()
	admin := model.User{ID: "admin", Role: model.RoleAdministrator}
	db := openArchiveStore(t)
	layout := visualLayout()
	if err := db.ImportLayout(ctx, layout, true); err != nil {
		t.Fatal(err)
	}
	update := visualLayout()
	update.Presentation.GridSpacing = 30
	update.Presentation.Nodes = []model.LayoutNodePosition{{NodeID: "east-boundary", X: 650, Y: 20}}
	update.Presentation.TrackSections = nil
	update.Presentation.Turnouts = nil
	update.Presentation.Blocks = []model.LayoutBlockStyle{{BlockID: "block-west", Color: "#445566", Opacity: 0.5}}
	data, err := BuildLayoutArchive(time.Now(), update)
	if err != nil {
		t.Fatal(err)
	}
	svc := New(db, events.New(), clock.Real{})
	if err := svc.ImportLayout(ctx, admin, data, false); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetLayoutPresentation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.GridSpacing != 30 || len(got.Nodes) != 3 || got.Nodes[0].X != 650 || len(got.TrackSections) != 1 || len(got.Turnouts) != 1 || len(got.Blocks) != 2 || got.Blocks[1].Color != "#445566" {
		t.Fatalf("merge changed omitted graphics or missed updates: %+v", got)
	}
	if err := svc.ImportLayout(ctx, admin, data, true); err != nil {
		t.Fatal(err)
	}
	got, err = db.GetLayoutPresentation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, model.NormalizeLayoutPresentation(*update.Presentation)) {
		t.Fatalf("replace retained omitted graphics: %+v", got)
	}
	if err := db.ImportLayout(ctx, model.LayoutDefinition{}, true); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"layout_node_positions", "layout_track_paths", "layout_turnout_positions", "layout_block_styles"} {
		rows, err := db.DB.QueryContext(ctx, "SELECT COUNT(*) FROM "+table)
		if err != nil {
			t.Fatal(err)
		}
		var count int
		if !rows.Next() || rows.Scan(&count) != nil || count != 0 {
			rows.Close()
			t.Fatalf("orphan rows in %s: %d", table, count)
		}
		rows.Close()
	}
}

func TestLegacyLayoutArchiveDefaultsPresentation(t *testing.T) {
	ctx := context.Background()
	admin := model.User{ID: "admin", Role: model.RoleAdministrator}
	layout := visualLayout()
	encoded, err := json.Marshal(LayoutDocument{Layout: layout})
	if err != nil {
		t.Fatal(err)
	}
	var legacy map[string]map[string]any
	if err := json.Unmarshal(encoded, &legacy); err != nil {
		t.Fatal(err)
	}
	delete(legacy["layout"], "presentation")
	data, err := writeArchive(Manifest{Format: FormatID, Version: 6, PackageType: "layout", CreatedAt: time.Now()}, "layout.json", legacy)
	if err != nil {
		t.Fatal(err)
	}
	db := openArchiveStore(t)
	if err := db.ImportLayout(ctx, layout, true); err != nil {
		t.Fatal(err)
	}
	want, err := db.GetLayoutPresentation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	svc := New(db, events.New(), clock.Real{})
	if err := svc.ImportLayout(ctx, admin, data, false); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetLayoutPresentation(ctx)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("legacy merge lost presentation: %+v, %v", got, err)
	}
	if err := svc.ImportLayout(ctx, admin, data, true); err != nil {
		t.Fatal(err)
	}
	got, err = db.GetLayoutPresentation(ctx)
	if err != nil || !reflect.DeepEqual(got, model.EmptyLayoutPresentation()) {
		t.Fatalf("legacy replace did not default presentation: %+v, %v", got, err)
	}
}

func TestLayoutPresentationInvalidArchiveRollsBack(t *testing.T) {
	ctx := context.Background()
	admin := model.User{ID: "admin", Role: model.RoleAdministrator}
	db := openArchiveStore(t)
	layout := visualLayout()
	if err := db.ImportLayout(ctx, layout, true); err != nil {
		t.Fatal(err)
	}
	before, err := db.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	layout.Blocks[0].Name = "Modified"
	layout.Presentation.Blocks[0].Color = "invalid"
	data, err := writeArchive(Manifest{Format: FormatID, Version: LayoutFormatVersion, PackageType: "layout", CreatedAt: time.Now()}, "layout.json", LayoutDocument{Layout: layout})
	if err != nil {
		t.Fatal(err)
	}
	bus := events.New()
	stream, unsubscribe := bus.Subscribe(1)
	defer unsubscribe()
	if err := New(db, bus, clock.Real{}).ImportLayout(ctx, admin, data, true); !errors.Is(err, ErrInvalidArchive) {
		t.Fatalf("invalid presentation error=%v", err)
	}
	after, err := db.ExportLayout(ctx)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("invalid presentation changed layout: %v", err)
	}
	select {
	case event := <-stream:
		t.Fatalf("rejected import published event: %+v", event)
	default:
	}
}

func TestLayoutEditorArchivePreservesRoutesAndFeedback(t *testing.T) {
	ctx := context.Background()
	admin := model.User{ID: "admin", Role: model.RoleAdministrator}
	db := openArchiveStore(t)
	if err := db.SeedDemo(ctx); err != nil {
		t.Fatal(err)
	}
	before, err := db.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Routes) == 0 || len(before.FeedbackMappings) == 0 {
		t.Fatal("demo layout lacks routes or feedback")
	}
	before.Blocks[0].Name = "Edited block"
	before.Presentation.Blocks = []model.LayoutBlockStyle{{BlockID: before.Blocks[0].ID, Color: "#123456", Opacity: 1}}
	data, err := BuildLayoutArchive(time.Now(), before)
	if err != nil {
		t.Fatal(err)
	}
	if err := New(db, events.New(), clock.Real{}).ImportLayout(ctx, admin, data, true); err != nil {
		t.Fatal(err)
	}
	after, err := db.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after.Routes, before.Routes) || !reflect.DeepEqual(after.FeedbackMappings, before.FeedbackMappings) {
		t.Fatalf("editor reimport lost route or feedback: routes=%+v mappings=%+v", after.Routes, after.FeedbackMappings)
	}
	if after.Blocks[0].Name != "Edited block" || !reflect.DeepEqual(after.Presentation.Blocks, before.Presentation.Blocks) {
		t.Fatalf("editor changes missing: %+v", after)
	}
}

func TestRollingStockArchiveVersionRemainsSix(t *testing.T) {
	data, err := BuildRollingStockArchive(time.Now(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if manifest := archiveManifest(t, data); manifest.Version != 6 {
		t.Fatalf("rolling stock archive version=%d", manifest.Version)
	}
	unsupported, err := writeArchive(Manifest{Format: FormatID, Version: LayoutFormatVersion, PackageType: "rolling-stock", CreatedAt: time.Now()}, "rolling-stock.json", RollingStockDocument{})
	if err != nil {
		t.Fatal(err)
	}
	if err := New(openArchiveStore(t), events.New(), clock.Real{}).ImportRollingStock(context.Background(), model.User{Role: model.RoleAdministrator}, unsupported, true); !errors.Is(err, ErrInvalidArchive) {
		t.Fatalf("rolling stock version 7 error=%v", err)
	}
}

func TestLayoutArchiveImportRequiresAdministrator(t *testing.T) {
	data, err := BuildLayoutArchive(time.Now(), visualLayout())
	if err != nil {
		t.Fatal(err)
	}
	if err := New(openArchiveStore(t), events.New(), clock.Real{}).ImportLayout(context.Background(), model.User{Role: model.RoleDriver}, data, true); err == nil {
		t.Fatal("driver imported layout archive")
	}
}
