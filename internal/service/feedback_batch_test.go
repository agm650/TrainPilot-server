package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"reflect"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/store"
)

func TestRBusFrameTransferIsAtomicAndLogsFinalOccupiedSet(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.ImportLayout(ctx, model.LayoutDefinition{Blocks: []model.BlockDefinition{
		{ID: "a", Name: "A"}, {ID: "b", Name: "B"}, {ID: "shadow", Name: "Shadow"},
	}}, false); err != nil {
		t.Fatal(err)
	}
	for _, mapping := range []struct {
		provider string
		address  int
		block    string
	}{
		{"z21-rbus", 1, "a"}, {"z21-rbus", 2, "a"}, {"*", 3, "b"}, {"*", 1, "shadow"},
	} {
		if err := db.SetFeedbackMapping(ctx, mapping.provider, mapping.address, mapping.block); err != nil {
			t.Fatal(err)
		}
	}
	bus := events.New()
	published, unsubscribe := bus.Subscribe(32)
	defer unsubscribe()
	r := NewRailwayService(db, newManualAccessoryStation(), bus)
	logs := make(feedbackLogWriter, 256)
	r.SetFeedbackLogger(slog.New(slog.NewJSONHandler(logs, nil)))
	frame := station.FeedbackEvent{Source: "z21-rbus", Kind: "occupancy", Inputs: make([]station.FeedbackInput, 80)}
	for i := range frame.Inputs {
		frame.Inputs[i].Address = i + 1
	}
	frame.Inputs[0].Active, frame.Inputs[2].Active = true, true
	r.handleFeedback(ctx, frame)
	if len(published) != 2 {
		t.Fatalf("initial events=%d want 2", len(published))
	}
	for len(published) > 0 {
		<-published
	}
	for len(logs) > 0 {
		<-logs
	}

	frame.Inputs[0].Active, frame.Inputs[1].Active, frame.Inputs[2].Active, frame.Inputs[4].Active = false, true, false, true
	r.handleFeedback(ctx, frame)
	if r.occupancy.BlockState("a").State != model.OccupancyOccupied || r.occupancy.BlockState("b").State != model.OccupancyFree || r.occupancy.BlockState("shadow").State != model.OccupancyUnknown {
		t.Fatal("source precedence, wildcard or same-block transfer failed")
	}
	if len(published) != 1 {
		t.Fatalf("transfer emitted intermediate state: events=%d", len(published))
	}
	changed := (<-published).Payload.(model.BlockOccupancyChanged)
	if changed.BlockID != "b" || changed.State != model.OccupancyFree {
		t.Fatalf("unexpected transfer event=%+v", changed)
	}
	if len(logs) != 4 {
		t.Fatalf("transition logs=%d want 4", len(logs))
	}
	for len(logs) > 0 {
		var record struct {
			Address  int   `json:"address"`
			Occupied []int `json:"occupied_addresses"`
		}
		if err := json.Unmarshal([]byte(<-logs), &record); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(record.Occupied, []int{2, 5}) {
			t.Fatalf("intermediate occupied set leaked: %+v", record)
		}
	}
	r.handleFeedback(ctx, frame)
	if len(published) != 0 || len(logs) != 0 {
		t.Fatal("identical frame emitted business events or transition logs")
	}
	sources, err := r.occupancy.ProviderStates(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range sources {
		if source.Sequence != 3 {
			t.Fatalf("frame was processed twice or not refreshed: %+v", source)
		}
	}
	before := bus.CurrentSequence()
	r.handleFeedback(ctx, station.FeedbackEvent{Source: "z21-rbus", Kind: "occupancy", Inputs: []station.FeedbackInput{}})
	unmapped := make([]station.FeedbackInput, 80)
	for i := range unmapped {
		unmapped[i].Address = i + 81
	}
	r.handleFeedback(ctx, station.FeedbackEvent{Source: "z21-rbus", Kind: "occupancy", Inputs: unmapped})
	if bus.CurrentSequence() != before {
		t.Fatal("empty or unmapped frame changed business state")
	}
}

func TestFeedbackBatchUpdatesAllBlocksBeforePublication(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SeedDemo(ctx); err != nil {
		t.Fatal(err)
	}
	bus := events.New()
	published, unsubscribe := bus.Subscribe(4)
	defer unsubscribe()
	r := NewRailwayService(db, newManualAccessoryStation(), bus)
	done := make(chan struct{})
	go func() {
		r.handleFeedback(ctx, station.FeedbackEvent{Source: "simulator", Kind: "occupancy", Inputs: []station.FeedbackInput{{Address: 1, Active: true}, {Address: 2, Active: true}, {Address: 3, Active: true}}})
		close(done)
	}()
	<-published
	for _, id := range []string{"block-a", "block-b", "block-c"} {
		if got := r.occupancy.BlockState(id).State; got != model.OccupancyOccupied {
			t.Fatalf("first publication preceded block %s mutation: %s", id, got)
		}
	}
	<-done
	if len(published) != 2 {
		t.Fatalf("expected one event per changed block, got %d more", len(published))
	}
}
