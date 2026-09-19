package transfer

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/model/topologyfixture"
	"github.com/agm650/TrainPilot-server/internal/store"
)

func TestRollingStockArchiveRoundTrip(t *testing.T) {
	ctx := context.Background()
	source, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if err := source.SeedDemo(ctx); err != nil {
		t.Fatal(err)
	}
	svc := New(source, events.New(), clock.NewFake(time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)))
	data, err := svc.ExportRollingStock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	targetSvc := New(target, events.New(), clock.Real{})
	admin := model.User{ID: "admin", Role: model.RoleAdministrator}
	if err := targetSvc.ImportRollingStock(ctx, admin, data, true); err != nil {
		t.Fatal(err)
	}
	items, err := target.ListLocomotives(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("locomotives=%d", len(items))
	}
}
func TestLayoutArchiveRoundTrip(t *testing.T) {
	ctx := context.Background()
	source, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if err := source.SeedDemo(ctx); err != nil {
		t.Fatal(err)
	}
	svc := New(source, events.New(), clock.Real{})
	data, err := svc.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	admin := model.User{ID: "admin", Role: model.RoleAdministrator}
	if err := New(target, events.New(), clock.Real{}).ImportLayout(ctx, admin, data, true); err != nil {
		t.Fatal(err)
	}
	layout, err := target.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(layout.Blocks) != 3 || len(layout.Routes) != 1 {
		t.Fatalf("blocks=%d routes=%d", len(layout.Blocks), len(layout.Routes))
	}
}

func TestTopologyLayoutArchiveRoundTripIsDeterministic(t *testing.T) {
	ctx := context.Background()
	source, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	layout := topologyfixture.DoubleSlip()
	layout.Routes = []model.RouteDefinition{{
		ID: "double-slip-route", Name: "Double slip route",
		EntryNodeID: "double-slip-a", ExitNodeID: "double-slip-c",
		TurnoutStates: map[string]string{"double-slip": "route_b"},
	}}
	if err := source.ImportLayout(ctx, layout, false); err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	first, err := New(source, events.New(), clock.NewFake(createdAt)).ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertTopologyArchiveFields(t, first)

	target, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	admin := model.User{ID: "admin", Role: model.RoleAdministrator}
	if err := New(target, events.New(), clock.Real{}).ImportLayout(ctx, admin, first, true); err != nil {
		t.Fatal(err)
	}
	want, err := source.GetTopologyDefinition(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, err := target.GetTopologyDefinition(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("topology archive round trip mismatch:\n got: %#v\nwant: %#v", got, want)
	}
	exported, err := target.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(exported.Routes) != 1 || exported.Routes[0].EntryNodeID != "double-slip-a" || exported.Routes[0].ExitNodeID != "double-slip-c" {
		t.Fatalf("route endpoints were not preserved: %+v", exported.Routes)
	}
	second, err := New(target, events.New(), clock.NewFake(createdAt)).ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(second, first) {
		t.Fatal("topology archive changed after import and export")
	}
}

func TestVersionThreeLayoutArchiveImportsEmptyTopology(t *testing.T) {
	ctx := context.Background()
	legacyDocument := map[string]any{
		"layout": map[string]any{
			"blocks":           []any{},
			"turnouts":         []any{},
			"routes":           []any{},
			"feedbackMappings": []any{},
		},
	}
	data, err := writeArchive(Manifest{Format: FormatID, Version: 3, PackageType: "layout", CreatedAt: time.Now()}, "layout.json", legacyDocument)
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	admin := model.User{ID: "admin", Role: model.RoleAdministrator}
	if err := New(target, events.New(), clock.Real{}).ImportLayout(ctx, admin, data, true); err != nil {
		t.Fatal(err)
	}
	topology, err := target.GetTopologyDefinition(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(topology.TopologyNodes) != 0 || len(topology.TrackSections) != 0 || len(topology.TurnoutTopologies) != 0 {
		t.Fatalf("version 3 archive invented topology: %#v", topology)
	}
}

func TestVersionFourBlockImportsWithoutMembershipOrRuntimeState(t *testing.T) {
	ctx := context.Background()
	legacyDocument := map[string]any{
		"layout": map[string]any{
			"nodes":             []any{},
			"trackSections":     []any{},
			"turnoutTopologies": []any{},
			"blocks": []any{map[string]any{
				"id": "legacy", "name": "Legacy", "occupied": true,
			}},
			"turnouts": []any{},
			"routes": []any{map[string]any{
				"id": "route", "name": "Route", "blockIds": []any{"legacy"}, "turnoutStates": map[string]any{},
			}},
			"feedbackMappings": []any{},
		},
	}
	data, err := writeArchive(Manifest{Format: FormatID, Version: 4, PackageType: "layout", CreatedAt: time.Now()}, "layout.json", legacyDocument)
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	admin := model.User{ID: "admin", Role: model.RoleAdministrator}
	if err := New(target, events.New(), clock.Real{}).ImportLayout(ctx, admin, data, true); err != nil {
		t.Fatal(err)
	}
	definition, err := target.ResourcesForBlock(ctx, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if len(definition.TrackSectionIDs) != 0 || len(definition.TurnoutIDs) != 0 {
		t.Fatalf("legacy route inferred block resources: %+v", definition)
	}
	blocks, err := target.ListBlocks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 || blocks[0].Occupied {
		t.Fatalf("legacy runtime occupancy was restored: %+v", blocks)
	}
}

func TestBlockMembershipArchiveRoundTripOmitsOccupancy(t *testing.T) {
	ctx := context.Background()
	layout := model.LayoutDefinition{
		TopologyNodes: []model.TopologyNode{
			{ID: "a", Kind: model.TopologyNodeBoundary},
			{ID: "b", Kind: model.TopologyNodeBoundary},
		},
		TrackSections: []model.TrackSection{{ID: "section", Name: "Section", NodeAID: "a", NodeBID: "b"}},
		Blocks: []model.BlockDefinition{{
			ID: "block", Name: "Block", TrackSectionIDs: []string{"section"},
		}},
	}
	createdAt := time.Date(2026, 9, 17, 13, 0, 0, 0, time.UTC)
	data, err := BuildLayoutArchive(createdAt, layout)
	if err != nil {
		t.Fatal(err)
	}
	contents := archiveEntry(t, data, "layout.json")
	if bytes.Contains(contents, []byte(`"occupied"`)) {
		t.Fatalf("layout archive contains runtime occupancy: %s", contents)
	}
	if !bytes.Contains(contents, []byte(`"trackSectionIds"`)) {
		t.Fatalf("layout archive omits block membership: %s", contents)
	}

	target, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	admin := model.User{ID: "admin", Role: model.RoleAdministrator}
	if err := New(target, events.New(), clock.Real{}).ImportLayout(ctx, admin, data, true); err != nil {
		t.Fatal(err)
	}
	exported, err := target.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(exported.Blocks, layout.Blocks) {
		t.Fatalf("block membership round trip mismatch: got %#v want %#v", exported.Blocks, layout.Blocks)
	}
}

func TestOccupancyConfigurationArchiveRoundTrip(t *testing.T) {
	ctx := context.Background()
	required, priority := true, 95
	layout := model.LayoutDefinition{
		Blocks: []model.BlockDefinition{{ID: "block", Name: "Block"}},
		OccupancyProviders: []model.OccupancyProvider{{
			ID: "camera-yard", Type: "vision", Priority: 90, Required: false,
			StaleAfter: 3 * time.Minute, FreshnessRequired: true,
		}},
		OccupancySensorMappings: []model.OccupancySensorMapping{{
			ProviderID: "camera-yard", SensorID: "zone-12", BlockID: "block",
			Required: &required, Priority: &priority,
		}},
	}
	data, err := BuildLayoutArchive(time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC), layout)
	if err != nil {
		t.Fatal(err)
	}
	if contents := archiveEntry(t, data, "layout.json"); !bytes.Contains(contents, []byte(`"staleAfter": "3m0s"`)) {
		t.Fatalf("archive does not contain readable occupancy freshness: %s", contents)
	}
	target, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	admin := model.User{ID: "admin", Role: model.RoleAdministrator}
	if err := New(target, events.New(), clock.Real{}).ImportLayout(ctx, admin, data, true); err != nil {
		t.Fatal(err)
	}
	exported, err := target.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(exported.OccupancyProviders, layout.OccupancyProviders) ||
		!reflect.DeepEqual(exported.OccupancySensorMappings, layout.OccupancySensorMappings) {
		t.Fatalf("occupancy configuration mismatch: got %#v %#v", exported.OccupancyProviders, exported.OccupancySensorMappings)
	}
}

func TestLegacyLayoutArchiveImportsAsSimpleTurnout(t *testing.T) {
	ctx := context.Background()
	legacyDocument := map[string]any{
		"layout": map[string]any{
			"blocks": []any{},
			"turnouts": []any{map[string]any{
				"id": "legacy-12", "name": "Legacy", "dccAddress": 12,
				"desiredState": "straight", "reportedState": "diverging",
			}},
			"routes":           []any{},
			"feedbackMappings": []any{},
		},
	}
	data, err := writeArchive(Manifest{Format: FormatID, Version: 1, PackageType: "layout", CreatedAt: time.Now()}, "layout.json", legacyDocument)
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	admin := model.User{ID: "admin", Role: model.RoleAdministrator}
	if err := New(target, events.New(), clock.Real{}).ImportLayout(ctx, admin, data, true); err != nil {
		t.Fatal(err)
	}
	turnout, err := target.GetTurnout(ctx, "legacy-12")
	if err != nil {
		t.Fatal(err)
	}
	if turnout.Kind != model.TurnoutKindSimple || turnout.DesiredPosition != "straight" || turnout.ReportedPosition != "diverging" || len(turnout.Endpoints) != 1 || turnout.Endpoints[0].LinearAddress != 12 {
		t.Fatalf("unexpected legacy conversion: %+v", turnout)
	}
}

func TestCompoundLayoutArchiveRoundTripIsDeterministic(t *testing.T) {
	ctx := context.Background()
	source, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	want := archiveThreeWayTurnout()
	want, err = model.NormalizeTurnout(want)
	if err != nil {
		t.Fatal(err)
	}
	layout := model.LayoutDefinition{
		Blocks:   []model.BlockDefinition{{ID: "block-a", Name: "Block A"}},
		Turnouts: []model.Turnout{want},
		Routes: []model.RouteDefinition{{
			ID: "route-left", Name: "Route left", BlockIDs: []string{"block-a"},
			TurnoutStates: map[string]string{want.ID: "left"},
		}},
	}
	if err := source.ImportLayout(ctx, layout, false); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC))
	svc := New(source, events.New(), clk)
	first, err := svc.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("two exports of unchanged layout differ")
	}
	target, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	admin := model.User{ID: "admin", Role: model.RoleAdministrator}
	if err := New(target, events.New(), clock.Real{}).ImportLayout(ctx, admin, first, true); err != nil {
		t.Fatal(err)
	}
	got, err := target.GetTurnout(ctx, want.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !sameTurnoutConfiguration(got, want) {
		t.Fatalf("configuration round trip mismatch:\n got: %#v\nwant: %#v", got, want)
	}
	if got.Pending || got.DesiredPosition != "" || got.ReportedPosition != "" || got.CommandStatus != model.TurnoutCommandIdle {
		t.Fatalf("runtime state was restored from layout archive: %#v", got)
	}
	exported, err := target.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(exported.Routes) != 1 || exported.Routes[0].TurnoutStates[want.ID] != "left" {
		t.Fatalf("compound route was not preserved: %#v", exported.Routes)
	}
}

func TestLayoutArchiveRoundTripsSimpleTripleAndDoubleSlipConfiguration(t *testing.T) {
	ctx := context.Background()
	source, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	turnouts := []model.Turnout{
		model.NewSimpleTurnout("simple", "Simple", 12, "straight", "diverging"),
		archiveThreeWayTurnout(),
		{
			ID: "double-slip", Name: "Double slip", Kind: model.TurnoutKindDoubleSlip,
			Endpoints: []model.AccessoryEndpoint{{ID: "A", LinearAddress: 30}, {ID: "B", LinearAddress: 31, Inverted: true}},
			Positions: []model.TurnoutPositionDefinition{
				{ID: "route_a", Label: "Route A", Endpoints: map[string]model.AccessoryPosition{"A": model.AccessoryPosition1, "B": model.AccessoryPosition1}},
				{ID: "route_b", Label: "Route B", Endpoints: map[string]model.AccessoryPosition{"A": model.AccessoryPosition1, "B": model.AccessoryPosition2}},
				{ID: "route_c", Label: "Route C", Endpoints: map[string]model.AccessoryPosition{"A": model.AccessoryPosition2, "B": model.AccessoryPosition1}},
				{ID: "route_d", Label: "Route D", Endpoints: map[string]model.AccessoryPosition{"A": model.AccessoryPosition2, "B": model.AccessoryPosition2}},
			},
		},
	}
	for i := range turnouts {
		turnouts[i], err = model.NormalizeTurnout(turnouts[i])
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := source.ImportLayout(ctx, model.LayoutDefinition{Turnouts: turnouts}, false); err != nil {
		t.Fatal(err)
	}
	data, err := New(source, events.New(), clock.NewFake(time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC))).ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if err := New(target, events.New(), clock.Real{}).ImportLayout(ctx, model.User{ID: "admin", Role: model.RoleAdministrator}, data, true); err != nil {
		t.Fatal(err)
	}
	for _, want := range turnouts {
		got, err := target.GetTurnout(ctx, want.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !sameTurnoutConfiguration(got, want) {
			t.Errorf("turnout %s configuration mismatch:\n got: %#v\nwant: %#v", want.ID, got, want)
		}
	}
}

func sameTurnoutConfiguration(a, b model.Turnout) bool {
	return a.ID == b.ID && a.Name == b.Name && a.Kind == b.Kind &&
		reflect.DeepEqual(a.Endpoints, b.Endpoints) && reflect.DeepEqual(a.Positions, b.Positions)
}
func TestDriverCannotImport(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	svc := New(db, events.New(), clock.Real{})
	err = svc.ImportRollingStock(ctx, model.User{Role: model.RoleDriver}, []byte("bad"), false)
	if err == nil {
		t.Fatal("driver import unexpectedly accepted")
	}
}

func TestInvalidLayoutDoesNotModifyDatabase(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SeedDemo(ctx); err != nil {
		t.Fatal(err)
	}
	before, err := db.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	invalid := LayoutDocument{Layout: model.LayoutDefinition{
		Blocks: []model.BlockDefinition{{ID: "new-block", Name: "New block"}},
		Routes: []model.RouteDefinition{{ID: "bad-route", Name: "Bad route", BlockIDs: []string{"missing-block"}, TurnoutStates: map[string]string{}}},
	}}
	data, err := writeArchive(Manifest{Format: FormatID, Version: FormatVersion, PackageType: "layout", CreatedAt: time.Now()}, "layout.json", invalid)
	if err != nil {
		t.Fatal(err)
	}
	err = New(db, events.New(), clock.Real{}).ImportLayout(ctx, model.User{ID: "admin", Role: model.RoleAdministrator}, data, true)
	if err == nil {
		t.Fatal("invalid layout unexpectedly imported")
	}
	after, err := db.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Blocks) != len(after.Blocks) || len(before.Routes) != len(after.Routes) {
		t.Fatalf("database changed after rejected import: before blocks/routes=%d/%d after=%d/%d", len(before.Blocks), len(before.Routes), len(after.Blocks), len(after.Routes))
	}
}

func TestLayoutImportedEventIsPublishedOnlyAfterSuccessfulCommit(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	bus := events.New()
	stream, unsubscribe := bus.Subscribe(2)
	defer unsubscribe()
	svc := New(db, bus, clock.Real{})
	admin := model.User{ID: "admin", Role: model.RoleAdministrator}

	invalid := topologyfixture.ThreeWay()
	invalid.TrackSections = []model.TrackSection{{ID: "broken", NodeAID: "missing", NodeBID: invalid.TopologyNodes[0].ID}}
	invalidData, err := writeArchive(Manifest{Format: FormatID, Version: FormatVersion, PackageType: "layout", CreatedAt: time.Now()}, "layout.json", LayoutDocument{Layout: invalid})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ImportLayout(ctx, admin, invalidData, true); err == nil {
		t.Fatal("invalid layout unexpectedly imported")
	}
	select {
	case event := <-stream:
		t.Fatalf("event published after rejected import: %+v", event)
	default:
	}

	valid := topologyfixture.PassingStation()
	validData, err := BuildLayoutArchive(time.Now(), valid)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ImportLayout(ctx, admin, validData, true); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-stream:
		if event.Type != "layout.imported" {
			t.Fatalf("event=%+v", event)
		}
	default:
		t.Fatal("layout.imported event is missing after successful commit")
	}
	persisted, err := db.GetTopologyDefinition(ctx)
	if err != nil || len(persisted.TopologyNodes) != len(valid.TopologyNodes) {
		t.Fatalf("persisted topology nodes=%d err=%v", len(persisted.TopologyNodes), err)
	}
}

func archiveThreeWayTurnout() model.Turnout {
	return model.Turnout{
		ID: "three-way", Name: "Three way", Kind: model.TurnoutKindThreeWay,
		Endpoints: []model.AccessoryEndpoint{{ID: "A", LinearAddress: 20}, {ID: "B", LinearAddress: 21}},
		Positions: []model.TurnoutPositionDefinition{
			{ID: "left", Endpoints: map[string]model.AccessoryPosition{"A": model.AccessoryPosition2, "B": model.AccessoryPosition1}},
			{ID: "straight", Endpoints: map[string]model.AccessoryPosition{"A": model.AccessoryPosition1, "B": model.AccessoryPosition1}},
			{ID: "right", Endpoints: map[string]model.AccessoryPosition{"A": model.AccessoryPosition1, "B": model.AccessoryPosition2}},
		},
		DesiredPosition: "straight",
	}
}

func assertTopologyArchiveFields(t *testing.T, data []byte) {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range reader.File {
		if file.Name != "layout.json" {
			continue
		}
		entry, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		contents, err := io.ReadAll(entry)
		entry.Close()
		if err != nil {
			t.Fatal(err)
		}
		var document struct {
			Layout map[string]json.RawMessage `json:"layout"`
		}
		if err := json.Unmarshal(contents, &document); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"nodes", "trackSections", "turnoutTopologies"} {
			if _, exists := document.Layout[field]; !exists {
				t.Fatalf("layout archive does not contain %q", field)
			}
		}
		return
	}
	t.Fatal("layout.json not found")
}

func archiveEntry(t *testing.T, data []byte, name string) []byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range reader.File {
		if file.Name != name {
			continue
		}
		entry, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		contents, err := io.ReadAll(entry)
		entry.Close()
		if err != nil {
			t.Fatal(err)
		}
		return contents
	}
	t.Fatalf("archive entry %q not found", name)
	return nil
}
