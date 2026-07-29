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
	sim := simulator.New()
	if err := sim.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	railway := NewRailwayService(db, sim, events.New())
	railway.StartFeedback(ctx)
	sim.InjectFeedback(station.FeedbackEvent{Source: "simulator", Kind: "occupancy", Address: 1, Active: true})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		blocks, err := db.ListBlocks(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, block := range blocks {
			if block.ID == "block-a" && block.Occupied {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("mapped block was not marked occupied")
}
