package transfer

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
)

func TestValidateLayoutArchiveRollsBackSuccessfulImport(t *testing.T) {
	ctx := context.Background()
	db := openArchiveStore(t)
	if err := db.SeedDemo(ctx); err != nil {
		t.Fatal(err)
	}
	before, err := db.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := BuildLayoutArchive(time.Now(), visualLayout())
	if err != nil {
		t.Fatal(err)
	}
	bus := events.New()
	stream, unsubscribe := bus.Subscribe(1)
	defer unsubscribe()
	result, err := New(db, bus, clock.Real{}).ValidateLayout(ctx, model.User{Role: model.RoleAdministrator}, archive, true)
	if err != nil || !result.Valid || len(result.Errors) != 0 || result.Warnings == nil {
		t.Fatalf("validation result=%+v error=%v", result, err)
	}
	after, err := db.ExportLayout(ctx)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("dry run changed layout: %v", err)
	}
	select {
	case event := <-stream:
		t.Fatalf("dry run published event: %+v", event)
	default:
	}
}

func TestValidateLayoutArchiveReportsPresentationAndFormatErrors(t *testing.T) {
	ctx := context.Background()
	db := openArchiveStore(t)
	svc := New(db, events.New(), clock.Real{})
	admin := model.User{Role: model.RoleAdministrator}
	layout := visualLayout()
	layout.Presentation.TrackSections[0].Segments[1].To = &model.LayoutPoint{X: 99, Y: 10}
	archive, err := writeArchive(Manifest{Format: FormatID, Version: LayoutFormatVersion, PackageType: "layout", CreatedAt: time.Now()}, "layout.json", LayoutDocument{Layout: layout})
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.ValidateLayout(ctx, admin, archive, true)
	if err != nil || result.Valid || len(result.Errors) != 1 || result.Errors[0].Code != "layout_track_path_invalid" || result.Errors[0].ResourceType != "trackSection" || result.Errors[0].ResourceID != "approach-west" {
		t.Fatalf("presentation diagnostic=%+v error=%v", result, err)
	}
	result, err = svc.ValidateLayout(ctx, admin, []byte("not a ZIP"), true)
	if err != nil || result.Valid || len(result.Errors) != 1 || result.Errors[0].Code != "invalid_archive" {
		t.Fatalf("archive diagnostic=%+v error=%v", result, err)
	}
}

func TestValidateLayoutArchiveChecksAccessoryAddressConflicts(t *testing.T) {
	layout := visualLayout()
	layout.Turnouts[1].Endpoints[0].LinearAddress = layout.Turnouts[0].Endpoints[0].LinearAddress
	archive, err := BuildLayoutArchive(time.Now(), layout)
	if err != nil {
		t.Fatal(err)
	}
	result, err := New(openArchiveStore(t), events.New(), clock.Real{}).ValidateLayout(context.Background(), model.User{Role: model.RoleAdministrator}, archive, true)
	if err != nil || result.Valid || len(result.Errors) != 1 || result.Errors[0].Code != "accessory_address_conflict" {
		t.Fatalf("address diagnostic=%+v error=%v", result, err)
	}
}

func TestValidateLayoutArchiveReturnsRouteWarnings(t *testing.T) {
	layout := model.LayoutDefinition{
		TopologyNodes: []model.TopologyNode{{ID: "a", Kind: model.TopologyNodeBoundary}, {ID: "b", Kind: model.TopologyNodeBoundary}},
		TrackSections: []model.TrackSection{{ID: "line", Name: "Line", NodeAID: "a", NodeBID: "b"}},
		Blocks:        []model.BlockDefinition{{ID: "block", Name: "Block", TrackSectionIDs: []string{"line"}}},
		Routes: []model.RouteDefinition{
			{ID: "first", Name: "First", EntryNodeID: "a", ExitNodeID: "b", BlockIDs: []string{"block"}, TurnoutStates: map[string]string{}},
			{ID: "second", Name: "Second", EntryNodeID: "b", ExitNodeID: "a", BlockIDs: []string{"block"}, TurnoutStates: map[string]string{}},
		},
	}
	archive, err := BuildLayoutArchive(time.Now(), layout)
	if err != nil {
		t.Fatal(err)
	}
	result, err := New(openArchiveStore(t), events.New(), clock.Real{}).ValidateLayout(context.Background(), model.User{Role: model.RoleAdministrator}, archive, true)
	if err != nil || !result.Valid || len(result.Errors) != 0 || len(result.Warnings) != 2 || result.Warnings[0].Code != "route_possible_undeclared_conflict" || result.Warnings[0].ResourceType != "route" {
		t.Fatalf("route warnings=%+v error=%v", result, err)
	}
}
