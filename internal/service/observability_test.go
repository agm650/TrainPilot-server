package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/observability"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/station/simulator"
	"github.com/agm650/TrainPilot-server/internal/store"
)

func TestThrottleAndStationErrorMetrics(t *testing.T) {
	ctx := context.Background()
	control, db, sim, _, user, session := newControlFixture(t)
	metrics := observability.New(":memory:")
	db.SetMetrics(metrics)
	control.SetMetrics(metrics)
	if err := control.SetTrackPower(ctx, user, true); err != nil {
		t.Fatal(err)
	}
	lease, err := control.Acquire(ctx, user, session, "loco-bb26001")
	if err != nil {
		t.Fatal(err)
	}
	if err := control.Throttle(ctx, user, session, "loco-bb26001", lease.ID, 40, station.Forward); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected station error")
	if err := sim.SetOperationFault(simulator.OpThrottle, simulator.OperationFault{Error: injected, Remaining: 1}); err != nil {
		t.Fatal(err)
	}
	if err := control.Throttle(ctx, user, session, "loco-bb26001", lease.ID, 45, station.Forward); !errors.Is(err, injected) {
		t.Fatalf("throttle error=%v", err)
	}

	body := scrapeServiceMetrics(t, metrics)
	for _, sample := range []string{
		`trainpilot_control_commands_total{command="throttle",result="success"} 1`,
		`trainpilot_control_commands_total{command="throttle",result="error"} 1`,
		`trainpilot_station_commands_total{operation="throttle",result="error"} 1`,
	} {
		if !strings.Contains(body, sample) {
			t.Fatalf("missing metric sample %q", sample)
		}
	}
}

func TestSimulatorFeedbackMetrics(t *testing.T) {
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
	metrics := observability.New(":memory:")
	db.SetMetrics(metrics)
	sim := simulator.New()
	if err := sim.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	railway := NewRailwayService(db, sim, events.New())
	railway.SetMetrics(metrics)
	railway.StartFeedback(ctx)
	if err := sim.SetFeedback(ctx, station.FeedbackEvent{Source: "simulator", Kind: "occupancy", Address: 1, Active: true}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(time.Second)
	for {
		body := scrapeServiceMetrics(t, metrics)
		if strings.Contains(body, `trainpilot_feedback_events_total{provider="simulator",result="mapped"} 1`) &&
			strings.Contains(body, `trainpilot_feedback_occupancy_updates_total{provider="simulator",result="changed"} 1`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("feedback metrics not observed:\n%s", body)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func scrapeServiceMetrics(t *testing.T, metrics *observability.Metrics) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("metrics status=%d", recorder.Code)
	}
	return recorder.Body.String()
}
