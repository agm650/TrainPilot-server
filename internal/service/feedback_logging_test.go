package service

import (
	"context"
	"encoding/json"
	"log/slog"
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
