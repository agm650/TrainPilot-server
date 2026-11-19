package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/store"
)

// handleFeedback is called synchronously. Change the provider epoch exactly
// between its preliminary check and the atomic occupancy mutation.
type changingFeedbackStation struct {
	*manualAccessoryStation
	state           station.FeedbackState
	next            station.FeedbackState
	calls, changeAt int
}

func (d *changingFeedbackStation) FeedbackState() station.FeedbackState {
	d.calls++
	if d.changeAt > 0 && d.calls == d.changeAt {
		d.state = d.next
	}
	return d.state
}

func TestFeedbackGenerationChangeDuringApplyDoesNotPolluteTransitionLogs(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.ImportLayout(ctx, model.LayoutDefinition{Blocks: []model.BlockDefinition{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}}, false); err != nil {
		t.Fatal(err)
	}
	for address, id := range map[int]string{1: "a", 2: "b"} {
		if err := db.SetFeedbackMapping(ctx, "z21-rbus", address, id); err != nil {
			t.Fatal(err)
		}
	}
	d := &changingFeedbackStation{manualAccessoryStation: newManualAccessoryStation(), state: station.FeedbackState{Source: "z21-rbus", Generation: 3, PowerGeneration: 7, Available: true}}
	r := NewRailwayService(db, d, events.New())
	if err := r.occupancy.SetTrackPowerAvailable(ctx, true); err != nil {
		t.Fatal(err)
	}
	logs := make(feedbackLogWriter, 16)
	r.SetFeedbackLogger(slog.New(slog.NewJSONHandler(logs, nil)))
	r.handleFeedback(ctx, station.FeedbackEvent{Source: "z21-rbus", Kind: "occupancy", Address: 1, Active: true, Generation: 3, PowerGeneration: 7})
	if len(logs) != 1 {
		t.Fatal("initial observation not logged")
	}
	<-logs
	d.calls, d.changeAt = 0, 3
	d.state.Generation = 4
	d.next = station.FeedbackState{Source: "z21-rbus", Generation: 5, PowerGeneration: 8, Available: true}
	r.handleFeedback(ctx, station.FeedbackEvent{Source: "z21-rbus", Kind: "occupancy", Generation: 4, PowerGeneration: 7, Inputs: []station.FeedbackInput{{Address: 1, Active: false}, {Address: 2, Active: true}}})
	if r.occupancy.BlockState("a").State != model.OccupancyUnknown || r.occupancy.BlockState("b").State != model.OccupancyUnknown {
		t.Fatal("obsolete frame restored occupancy")
	}
	if !reflect.DeepEqual(r.rbusLogActive, map[int]bool{1: true}) {
		t.Fatal("discarded frame changed transition cache", r.rbusLogActive)
	}
	if r.feedbackLogGeneration != 3 {
		t.Fatal("discarded frame advanced log generation", r.feedbackLogGeneration)
	}
	if len(logs) != 1 {
		t.Fatal("expected one diagnostic for the complete frame", len(logs))
	}
	line := <-logs
	var got struct {
		Message       string `json:"msg"`
		Result        string `json:"result"`
		Received      uint64 `json:"received_generation"`
		Current       uint64 `json:"current_generation"`
		ReceivedPower uint64 `json:"received_power_generation"`
		CurrentPower  uint64 `json:"current_power_generation"`
		Count         int    `json:"input_count"`
		Available     bool   `json:"available"`
	}
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatal(err)
	}
	if got.Message != "station.feedback.discarded" || got.Result != "resynchronizing" || got.Received != 4 || got.Current != 5 || got.ReceivedPower != 7 || got.CurrentPower != 8 || got.Count != 2 || !got.Available {
		t.Fatal(line)
	}
	if strings.Contains(line, "update_error") || strings.Contains(line, "occupied_addresses") || strings.Contains(line, `"error"`) {
		t.Fatal("expected resynchronization reported as application error", line)
	}
	d.changeAt = 0
	r.handleFeedback(ctx, station.FeedbackEvent{Source: "z21-rbus", Kind: "occupancy", Address: 2, Active: true, Generation: 5, PowerGeneration: 8})
	if len(logs) != 1 || r.occupancy.BlockState("b").State != model.OccupancyOccupied {
		t.Fatal("current generation was not applied")
	}
	var accepted struct {
		Generation      uint64 `json:"generation"`
		PowerGeneration uint64 `json:"power_generation"`
		Occupied        []int  `json:"occupied_addresses"`
	}
	if err := json.Unmarshal([]byte(<-logs), &accepted); err != nil {
		t.Fatal(err)
	}
	if accepted.Generation != 5 || accepted.PowerGeneration != 8 || !reflect.DeepEqual(accepted.Occupied, []int{2}) {
		t.Fatal(accepted)
	}
	r.handleFeedback(ctx, station.FeedbackEvent{Source: "z21-rbus", Kind: "occupancy", Address: 2, Active: true, Generation: 5, PowerGeneration: 8})
	if len(logs) != 0 {
		t.Fatal("unchanged accepted observation logged again")
	}
}

func TestFeedbackEarlyDiscardIdentifiesUnavailableOrObsoleteStream(t *testing.T) {
	for _, tc := range []struct {
		name      string
		available bool
		received  uint64
		changeAt  int
	}{
		{"unavailable", false, 8, 0},
		{"old_epoch", true, 7, 0},
		{"unmapped_epoch_changes_during_mapping", true, 8, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := store.Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			d := &changingFeedbackStation{manualAccessoryStation: newManualAccessoryStation(), state: station.FeedbackState{Source: "z21-rbus", Generation: 8, PowerGeneration: 9, Available: tc.available}, changeAt: tc.changeAt, next: station.FeedbackState{Source: "z21-rbus", Generation: 9, PowerGeneration: 9, Available: true}}
			r := NewRailwayService(db, d, events.New())
			logs := make(feedbackLogWriter, 4)
			r.SetFeedbackLogger(slog.New(slog.NewJSONHandler(logs, nil)))
			r.handleFeedback(context.Background(), station.FeedbackEvent{Source: "z21-rbus", Kind: "occupancy", Generation: tc.received, PowerGeneration: 9, Address: 1, Active: true})
			if len(logs) != 1 {
				t.Fatal("missing discarded-frame diagnostic")
			}
			line := <-logs
			if !strings.Contains(line, `"result":"resynchronizing"`) {
				t.Fatal(line)
			}
			if len(r.rbusLogActive) != 0 {
				t.Fatal("discarded frame populated log cache")
			}
			r.handleFeedback(context.Background(), station.FeedbackEvent{Source: "z21-rbus", Kind: "synchronization", Generation: 8, PowerGeneration: 9})
			if len(logs) != 0 {
				t.Fatal("synchronization marker logged as rejected occupancy")
			}
		})
	}
}
