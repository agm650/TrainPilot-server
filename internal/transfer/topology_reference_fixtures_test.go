package transfer

import (
	"bytes"
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/model/topologyfixture"
	"github.com/agm650/TrainPilot-server/internal/store"
)

func TestReferenceTopologyFixturesArchiveRoundTrip(t *testing.T) {
	ctx := context.Background()
	admin := model.User{ID: "topology-fixture-admin", Role: model.RoleAdministrator}
	createdAt := time.Date(2026, time.September, 18, 12, 0, 0, 0, time.UTC)
	for _, fixture := range topologyfixture.ReferenceLayouts() {
		fixture := fixture
		t.Run(fixture.Name, func(t *testing.T) {
			input, err := BuildLayoutArchive(createdAt, fixture.Layout)
			if err != nil {
				t.Fatal(err)
			}
			firstStore := openFixtureStore(t)
			firstService := New(firstStore, events.New(), clock.NewFake(createdAt))
			if err := firstService.ImportLayout(ctx, admin, input, true); err != nil {
				t.Fatal(err)
			}
			firstLayout, err := firstStore.ExportLayout(ctx)
			if err != nil {
				t.Fatal(err)
			}
			firstArchive, err := firstService.ExportLayout(ctx)
			if err != nil {
				t.Fatal(err)
			}

			secondStore := openFixtureStore(t)
			secondService := New(secondStore, events.New(), clock.NewFake(createdAt))
			if err := secondService.ImportLayout(ctx, admin, firstArchive, true); err != nil {
				t.Fatal(err)
			}
			secondLayout, err := secondStore.ExportLayout(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(secondLayout, firstLayout) {
				t.Fatalf("layout changed after archive round trip:\nfirst=%#v\nsecond=%#v", firstLayout, secondLayout)
			}
			secondArchive, err := secondService.ExportLayout(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(secondArchive, firstArchive) {
				t.Fatal("canonical archive changed after round trip")
			}
		})
	}
}

func openFixtureStore(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
