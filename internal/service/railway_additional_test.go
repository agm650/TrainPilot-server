package service

import (
	"context"
	"errors"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station/simulator"
	"github.com/agm650/TrainPilot-server/internal/store"
)

func TestRailwayListsAndMutations(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SeedDemo(ctx); err != nil {
		t.Fatal(err)
	}
	sim := simulator.New()
	if err := sim.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	bus := events.New()
	ch, unsubscribe := bus.Subscribe(4)
	defer unsubscribe()
	svc := NewRailwayService(db, sim, bus)

	if items, err := svc.Locomotives(ctx); err != nil || len(items) != 2 {
		t.Fatalf("locomotives len=%d err=%v", len(items), err)
	}
	if items, err := svc.Blocks(ctx); err != nil || len(items) != 3 {
		t.Fatalf("blocks len=%d err=%v", len(items), err)
	}
	if items, err := svc.Turnouts(ctx); err != nil || len(items) != 1 {
		t.Fatalf("turnouts len=%d err=%v", len(items), err)
	}

	viewer := model.User{Role: model.RoleViewer}
	dispatcher := model.User{Role: model.RoleDispatcher}
	if err := svc.SetTurnout(ctx, viewer, "turnout-1", "straight"); err == nil {
		t.Fatal("viewer changed turnout")
	}
	if err := svc.SetTurnout(ctx, dispatcher, "turnout-1", "invalid"); err == nil {
		t.Fatal("invalid turnout state accepted")
	}
	if err := svc.SetTurnout(ctx, dispatcher, "missing", "straight"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing turnout error=%v", err)
	}
	if err := sim.Close(); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetTurnout(ctx, dispatcher, "turnout-1", "diverging"); err == nil {
		t.Fatal("turnout command succeeded while station disconnected")
	}
	if err := sim.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetTurnout(ctx, dispatcher, "turnout-1", "diverging"); err != nil {
		t.Fatal(err)
	}
	turnout, err := db.GetTurnout(ctx, "turnout-1")
	if err != nil || turnout.DesiredState != "diverging" || turnout.ReportedState != "diverging" {
		t.Fatalf("turnout=%+v err=%v", turnout, err)
	}
	if event := <-ch; event.Type != "turnout.state.changed" {
		t.Fatalf("event=%+v", event)
	}

	if err := svc.SetBlockFeedback(ctx, "missing", true); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing block error=%v", err)
	}
	if err := svc.SetBlockFeedback(ctx, "block-a", true); err != nil {
		t.Fatal(err)
	}
	blocks, err := db.ListBlocks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, block := range blocks {
		if block.ID == "block-a" {
			found = block.Occupied
		}
	}
	if !found {
		t.Fatal("block-a was not marked occupied")
	}
	if event := <-ch; event.Type != "block.occupancy.changed" {
		t.Fatalf("event=%+v", event)
	}
}
