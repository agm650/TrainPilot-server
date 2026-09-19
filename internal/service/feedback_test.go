package service

import (
	"context"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/station/simulator"
	"github.com/agm650/TrainPilot-server/internal/store"
)

func TestFeedbackUpdatesMappedBlock(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SeedDemo(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.ImportLayout(ctx, model.LayoutDefinition{
		TopologyNodes: []model.TopologyNode{
			{ID: "feedback-a", Kind: model.TopologyNodeBoundary},
			{ID: "feedback-b", Kind: model.TopologyNodeBoundary},
		},
		TrackSections: []model.TrackSection{{ID: "feedback-section", NodeAID: "feedback-a", NodeBID: "feedback-b"}},
		Blocks:        []model.BlockDefinition{{ID: "block-a", Name: "Block A", TrackSectionIDs: []string{"feedback-section"}}},
	}, false); err != nil {
		t.Fatal(err)
	}
	if err := db.SetFeedbackMapping(ctx, "simulator", 2, "block-b"); err != nil {
		t.Fatal(err)
	}
	sim := simulator.New()
	if err := sim.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	bus := events.New()
	published, unsubscribe := bus.Subscribe(4)
	defer unsubscribe()
	railway := NewRailwayService(db, sim, bus)
	railway.StartFeedback(ctx)
	for _, address := range []int{1, 2} {
		if err := sim.SetFeedback(ctx, station.FeedbackEvent{Source: "simulator", Kind: "occupancy", Address: address, Active: true}); err != nil {
			t.Fatal(err)
		}
	}
	for received := 0; received < 2; received++ {
		select {
		case event := <-published:
			if event.Type != "block.occupancy.changed" {
				t.Fatalf("event=%+v", event)
			}
		case <-time.After(time.Second):
			t.Fatal("mapped block event was not published")
		}
	}
	blocks, err := railway.Blocks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	occupied := map[string]bool{}
	for _, block := range blocks {
		occupied[block.ID] = block.Occupied
	}
	if !occupied["block-a"] || !occupied["block-b"] {
		t.Fatalf("occupied blocks=%+v", occupied)
	}
}

func TestFeedbackUsesOccupancyServiceAndStationHealth(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
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
	defer sim.Close()
	bus := events.New()
	railway := NewRailwayService(db, sim, bus)
	control := NewControlService(db, sim, bus, clock.Real{}, time.Minute, time.Second, 10*time.Millisecond)
	control.SetOccupancyProvider(railway.OccupancyService(), "simulator")
	railway.StartFeedback(ctx)
	control.Start()
	defer control.Close()

	if err := sim.SetFeedback(ctx, station.FeedbackEvent{Source: "simulator", Kind: "occupancy", Address: 1, Active: true}); err != nil {
		t.Fatal(err)
	}
	waitForBlockOccupancyState(t, railway.OccupancyService(), "block-a", model.OccupancyOccupied)
	if err := sim.SetFeedback(ctx, station.FeedbackEvent{Source: "simulator", Kind: "occupancy", Address: 1, Active: false}); err != nil {
		t.Fatal(err)
	}
	waitForBlockOccupancyState(t, railway.OccupancyService(), "block-a", model.OccupancyFree)

	if err := sim.SetConnectivity(station.Offline); err != nil {
		t.Fatal(err)
	}
	waitForBlockOccupancyState(t, railway.OccupancyService(), "block-a", model.OccupancyUnknown)
	if err := sim.SetConnectivity(station.Online); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if got := railway.OccupancyService().BlockState("block-a").State; got != model.OccupancyUnknown {
		t.Fatalf("state after reconnect without feedback = %q", got)
	}
	if err := sim.SetFeedback(ctx, station.FeedbackEvent{Source: "simulator", Kind: "occupancy", Address: 1, Active: true}); err != nil {
		t.Fatal(err)
	}
	waitForBlockOccupancyState(t, railway.OccupancyService(), "block-a", model.OccupancyOccupied)
}

func waitForBlockOccupancyState(t *testing.T, service *OccupancyService, blockID string, want model.OccupancyState) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if service.BlockState(blockID).State == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("block %q state = %q, want %q", blockID, service.BlockState(blockID).State, want)
}
