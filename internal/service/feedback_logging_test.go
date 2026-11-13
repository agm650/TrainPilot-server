package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/store"
)

type feedbackLogWriter chan string

func (w feedbackLogWriter) Write(p []byte) (int, error) {
	w <- string(p)
	return len(p), nil
}

func TestFeedbackLoggingIncludesMappedAndUnmappedRBusInputs(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.ImportLayout(ctx, model.LayoutDefinition{
		Blocks: []model.BlockDefinition{{ID: "block-a", Name: "Block A"}},
	}, false); err != nil {
		t.Fatal(err)
	}
	if err := db.SetFeedbackMapping(ctx, "z21-rbus", 1, "block-a"); err != nil {
		t.Fatal(err)
	}
	st := newManualAccessoryStation()
	railway := NewRailwayService(db, st, events.New())
	logs := make(feedbackLogWriter, 4)
	railway.SetFeedbackLogger(slog.New(slog.NewJSONHandler(logs, nil)))
	railway.StartFeedback(ctx)

	for _, tc := range []struct {
		name    string
		address int
		active  bool
		result  string
		state   model.OccupancyState
	}{
		{"occupied", 1, true, "mapped", model.OccupancyOccupied},
		{"free", 1, false, "mapped", model.OccupancyFree},
		{"unmapped", 999, true, "unmapped", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			select {
			case st.feedback <- station.FeedbackEvent{Source: "z21-rbus", Kind: "occupancy", Address: tc.address, Active: tc.active}:
			case <-ctx.Done():
				t.Fatal("feedback was not consumed")
			}
			var line string
			select {
			case line = <-logs:
			case <-ctx.Done():
				t.Fatal("feedback was not logged")
			}
			var got struct {
				Time    string `json:"time"`
				Message string `json:"msg"`
				Source  string `json:"source"`
				Kind    string `json:"kind"`
				Address int    `json:"address"`
				Active  bool   `json:"active"`
				Result  string `json:"result"`
				BlockID string `json:"block_id"`
				Error   string `json:"error"`
			}
			if err := json.Unmarshal([]byte(line), &got); err != nil {
				t.Fatal(err)
			}
			if got.Time == "" || got.Message != "station.feedback" || got.Source != "z21-rbus" || got.Kind != "occupancy" || got.Address != tc.address || got.Active != tc.active || got.Result != tc.result {
				t.Fatalf("unexpected feedback log: %s", line)
			}
			if tc.result == "mapped" {
				if got.BlockID != "block-a" || got.Error != "" {
					t.Fatalf("unexpected mapping log: %s", line)
				}
				if state := railway.OccupancyService().BlockState("block-a").State; state != tc.state {
					t.Fatalf("block state=%s, want %s", state, tc.state)
				}
			} else if got.BlockID != "" || got.Error == "" {
				t.Fatalf("unexpected unmapped log: %s", line)
			}
		})
	}
}

func TestRBusFeedbackLogsOccupiedAddressesOnStateChanges(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.ImportLayout(ctx, model.LayoutDefinition{
		Blocks: []model.BlockDefinition{{ID: "block-a", Name: "Block A"}},
	}, false); err != nil {
		t.Fatal(err)
	}
	if err := db.SetFeedbackMapping(ctx, "z21-rbus", 1, "block-a"); err != nil {
		t.Fatal(err)
	}
	st := newManualAccessoryStation()
	railway := NewRailwayService(db, st, events.New())
	logs := make(feedbackLogWriter, 32)
	railway.SetFeedbackLogger(slog.New(slog.NewJSONHandler(logs, nil)))
	railway.StartFeedback(ctx)

	send := func(t *testing.T, event station.FeedbackEvent) {
		t.Helper()
		select {
		case st.feedback <- event:
		case <-ctx.Done():
			t.Fatal("feedback was not consumed")
		}
	}

	type feedbackLogRecord struct {
		station.FeedbackEvent
		OccupiedAddresses []int `json:"occupied_addresses"`
	}

	for _, tc := range []struct {
		name     string
		source   string
		address  int
		active   bool
		logged   bool
		occupied []int
	}{
		{"initial free", "z21-rbus", 1, false, false, nil},
		{"repeated free", "z21-rbus", 1, false, false, nil},
		{"unmapped initial free", "z21-rbus", 999, false, false, nil},
		{"first occupied", "z21-rbus", 1, true, true, []int{1}},
		{"repeated occupied", "z21-rbus", 1, true, false, nil},
		{"unmapped occupied", "z21-rbus", 999, true, true, []int{1, 999}},
		{"unmapped repeated occupied", "z21-rbus", 999, true, false, nil},
		{"occupied addresses are sorted", "z21-rbus", 7, true, true, []int{1, 7, 999}},
		{"back to free", "z21-rbus", 1, false, true, []int{7, 999}},
		{"repeated free after change", "z21-rbus", 1, false, false, nil},
		{"unmapped back to free", "z21-rbus", 999, false, true, []int{7}},
		{"all addresses free", "z21-rbus", 7, false, true, []int{}},
		{"occupied again", "z21-rbus", 1, true, true, []int{1}},
		{"other source free", "simulator", 1, false, true, nil},
		{"other source repeated free", "simulator", 1, false, true, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			send(t, station.FeedbackEvent{Source: tc.source, Kind: "occupancy", Address: tc.address, Active: tc.active})
			// This unfiltered event marks completion of the preceding event.
			send(t, station.FeedbackEvent{Source: "log-barrier", Kind: "occupancy"})
			var received []feedbackLogRecord
			for {
				var line string
				select {
				case line = <-logs:
				case <-ctx.Done():
					t.Fatal("feedback processing did not complete")
				}
				var got feedbackLogRecord
				if err := json.Unmarshal([]byte(line), &got); err != nil {
					t.Fatal(err)
				}
				if got.Source == "log-barrier" {
					break
				}
				received = append(received, got)
			}
			if tc.logged {
				if len(received) != 1 || received[0].Source != tc.source || received[0].Address != tc.address || received[0].Active != tc.active {
					t.Fatalf("unexpected feedback logs: %+v", received)
				}
				if !reflect.DeepEqual(received[0].OccupiedAddresses, tc.occupied) {
					t.Fatalf("occupied addresses=%v, want %v", received[0].OccupiedAddresses, tc.occupied)
				}
			} else if len(received) != 0 {
				t.Fatalf("unchanged state was logged: %+v", received)
			}
			if tc.source == "z21-rbus" && tc.address == 1 {
				want := model.OccupancyFree
				if tc.active {
					want = model.OccupancyOccupied
				}
				if got := railway.OccupancyService().BlockState("block-a").State; got != want {
					t.Fatalf("block state=%s, want %s", got, want)
				}
			}
		})
	}
}
