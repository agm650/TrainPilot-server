package service

import (
	"context"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/events"
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
	blocks, err := db.ListBlocks(ctx)
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
