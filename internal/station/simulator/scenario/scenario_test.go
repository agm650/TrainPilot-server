package scenario

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/station/simulator"
)

const minimalScenario = `{
  "version": 2,
  "name": "minimal",
  "initial": {},
  "steps": []
}`

func TestParseMinimalScenario(t *testing.T) {
	definition, err := Parse([]byte(minimalScenario))
	if err != nil {
		t.Fatal(err)
	}
	if definition.Version != CurrentVersion || definition.Name != "minimal" || len(definition.Steps) != 0 {
		t.Fatalf("definition=%+v", definition)
	}
}

func TestParseRejectsInvalidScenarioBeforeExecution(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"missing version", `{"name":"x","initial":{},"steps":[]}`, "scenario.version"},
		{"unknown version", `{"version":3,"name":"x","initial":{},"steps":[]}`, "unsupported scenario.version"},
		{"missing initial", `{"version":1,"name":"x","steps":[]}`, "scenario.initial"},
		{"missing steps", `{"version":1,"name":"x","initial":{}}`, "scenario.steps"},
		{"unknown top field", `{"version":1,"name":"x","initial":{},"steps":[],"extra":true}`, "unknown field"},
		{"unknown action", scenarioWithSteps(`{"at":"0s","action":"unknown"}`), "unsupported action"},
		{"invalid duration", scenarioWithSteps(`{"at":"later","action":"fault.clear"}`), "invalid at duration"},
		{"missing required field", scenarioWithSteps(`{"at":"0s","action":"station.connectivity"}`), "connectivity is required"},
		{"unknown connectivity", scenarioWithSteps(`{"at":"0s","action":"station.connectivity","connectivity":"lost"}`), "unsupported connectivity"},
		{"negative address", scenarioWithSteps(`{"at":"0s","action":"feedback.emit","source":"simulator","kind":"occupancy","address":-1,"active":true}`), "address must not be negative"},
		{"unknown step field", scenarioWithSteps(`{"at":"0s","action":"fault.clear","extra":true}`), "unknown field"},
		{"unsorted", scenarioWithSteps(
			`{"at":"2s","action":"fault.clear"}`,
			`{"at":"1s","action":"fault.clear"}`,
		), "before previous step"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Parse([]byte(test.content)); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Parse error=%v, want substring %q", err, test.want)
			}
		})
	}
}

func TestSameTimestampStepsPreserveFileOrder(t *testing.T) {
	definition := mustParse(t, scenarioWithSteps(
		`{"at":"0s","action":"feedback.emit","source":"simulator","kind":"occupancy","address":1,"active":true}`,
		`{"at":"0s","action":"feedback.emit","source":"simulator","kind":"occupancy","address":1,"active":false}`,
	))
	_, sim, runner := newManualRunner(t, definition)
	if err := runner.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := receiveFeedback(t, sim.Feedback())
	second := receiveFeedback(t, sim.Feedback())
	if !first.Active || second.Active {
		t.Fatalf("events out of order: first=%+v second=%+v", first, second)
	}
	if snapshot := runner.Snapshot(); snapshot.State != StateCompleted || snapshot.NextStep != 2 {
		t.Fatalf("control snapshot=%+v", snapshot)
	}
}

func TestManualAdvanceAppliesExactDueSteps(t *testing.T) {
	definition := mustParse(t, `{
  "version": 1,
  "name": "manual",
  "initial": {"connectivity":"online"},
  "steps": [
    {"at":"5s","action":"station.track_power","on":true},
    {"at":"8s","action":"station.emergency_stop","active":true},
    {"at":"8s","action":"station.emergency_stop","active":false},
    {"at":"10s","action":"station.track_power","on":false}
  ]
}`)
	_, sim, runner := newManualRunner(t, definition)
	if err := runner.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := runner.Advance(context.Background(), 4*time.Second); err != nil {
		t.Fatal(err)
	}
	if sim.Snapshot().TrackPower {
		t.Fatal("step was applied before its timestamp")
	}
	if err := runner.Advance(context.Background(), time.Second); err != nil {
		t.Fatal(err)
	}
	if !sim.Snapshot().TrackPower {
		t.Fatal("track power step was not applied at its timestamp")
	}
	if err := runner.Advance(context.Background(), 5*time.Second); err != nil {
		t.Fatal(err)
	}
	snapshot := sim.Snapshot()
	if snapshot.TrackPower || snapshot.EmergencyStop {
		t.Fatalf("final simulator snapshot=%+v", snapshot)
	}
	control := runner.Snapshot()
	if control.State != StateCompleted || control.Elapsed != 10*time.Second || control.NextStep != 4 {
		t.Fatalf("control snapshot=%+v", control)
	}
}

func TestAllVersionOneActionsAreExecutable(t *testing.T) {
	definition := mustParse(t, `{
  "version": 1,
  "name": "all-actions",
  "initial": {
    "connectivity": "online",
    "trackPower": false,
    "emergencyStop": false,
    "electrical": {"temperatureCelsius": 25, "supplyVoltageMilliVolts": 18000}
  },
  "steps": [
    {"at":"0s","action":"station.track_power","on":true},
    {"at":"0s","action":"station.emergency_stop","active":true},
    {"at":"0s","action":"station.electrical","electrical":{"temperatureCelsius":42,"supplyVoltageMilliVolts":17950,"trackVoltageMilliVolts":17890}},
    {"at":"0s","action":"feedback.set","source":"simulator","kind":"occupancy","address":1,"active":true,"emit":false},
    {"at":"0s","action":"feedback.emit","source":"simulator","kind":"occupancy","address":1,"active":true},
    {"at":"0s","action":"accessory.behavior","address":12,"mode":"delayed","delay":"2s"},
    {"at":"0s","action":"accessory.report","address":12,"state":"straight"},
    {"at":"0s","action":"fault.operation","operation":"throttle","error":"injected","remaining":1},
    {"at":"0s","action":"fault.clear"},
    {"at":"1s","action":"simulator.reset"},
    {"at":"2s","action":"station.connectivity","connectivity":"degraded"}
  ]
}`)
	_, sim, runner := newManualRunner(t, definition)
	if err := runner.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if event := receiveFeedback(t, sim.Feedback()); !event.Active || event.Address != 1 {
		t.Fatalf("feedback=%+v", event)
	}
	if err := runner.Advance(context.Background(), 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if snapshot := runner.Snapshot(); snapshot.State != StateCompleted {
		t.Fatalf("control snapshot=%+v", snapshot)
	}
	if health := sim.Health(); health.Connectivity != station.Degraded {
		t.Fatalf("health=%+v", health)
	}
}

func TestScenarioReplayAfterResetIsIdentical(t *testing.T) {
	definition := mustParse(t, `{
  "version":1,
  "name":"replay",
  "initial":{"connectivity":"online","trackPower":true},
  "steps":[
    {"at":"1s","action":"feedback.set","source":"simulator","kind":"occupancy","address":1,"active":true,"emit":false},
    {"at":"2s","action":"station.electrical","electrical":{"temperatureCelsius":42,"supplyVoltageMilliVolts":18000,"trackVoltageMilliVolts":18000}}
  ]
}`)
	start := time.Date(2026, time.August, 30, 10, 0, 0, 0, time.UTC)
	clk := clock.NewFake(start)
	sim := simulator.NewWithClock(clk)
	if err := sim.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	run := func() simulator.Snapshot {
		runner, err := New(definition, sim, clk)
		if err != nil {
			t.Fatal(err)
		}
		if err := runner.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := runner.Advance(context.Background(), 2*time.Second); err != nil {
			t.Fatal(err)
		}
		return sim.Snapshot()
	}
	first := run()
	clk.Set(start)
	sim.Reset()
	second := run()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("scenario replay differs\nfirst=%+v\nsecond=%+v", first, second)
	}
}

func TestStopPreventsFutureSteps(t *testing.T) {
	definition := mustParse(t, scenarioWithSteps(`{"at":"1h","action":"station.track_power","on":true}`))
	_, sim, runner := newManualRunner(t, definition)
	if err := runner.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	runner.Stop()
	if err := runner.Advance(context.Background(), time.Hour); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Advance error=%v", err)
	}
	if sim.Snapshot().TrackPower {
		t.Fatal("stopped scenario applied a future step")
	}
	if snapshot := runner.Snapshot(); snapshot.State != StateStopped || snapshot.NextStep != 0 {
		t.Fatalf("control snapshot=%+v", snapshot)
	}
}

func TestStepFailureIsObservableAndAtomic(t *testing.T) {
	definition := mustParse(t, scenarioWithSteps(`{"at":"1s","action":"feedback.emit","source":"simulator","kind":"occupancy","address":999,"active":true}`))
	_, sim, runner := newManualRunner(t, definition)
	for address := 0; address < 64; address++ {
		if err := sim.SetFeedback(context.Background(), station.FeedbackEvent{Source: "fill", Kind: "occupancy", Address: address, Active: true}); err != nil {
			t.Fatal(err)
		}
	}
	if err := runner.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	err := runner.Advance(context.Background(), time.Second)
	if !errors.Is(err, simulator.ErrFeedbackBufferFull) {
		t.Fatalf("Advance error=%v", err)
	}
	key := simulator.FeedbackKey{Source: "simulator", Kind: "occupancy", Address: 999}
	if _, exists := sim.Snapshot().FeedbackStates[key]; exists {
		t.Fatal("failed feedback step partially changed physical state")
	}
	control := runner.Snapshot()
	if control.State != StateFailed || control.Error == "" || control.NextStep != 0 {
		t.Fatalf("control snapshot=%+v", control)
	}
	if err := runner.Advance(context.Background(), time.Second); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("second Advance error=%v", err)
	}
}

func TestRealtimeExecutionAndCancellation(t *testing.T) {
	t.Run("completes", func(t *testing.T) {
		definition := mustParse(t, scenarioWithSteps(`{"at":"5ms","action":"station.track_power","on":true}`))
		sim := simulator.New()
		if err := sim.Connect(context.Background()); err != nil {
			t.Fatal(err)
		}
		runner, err := New(definition, sim, clock.Real{})
		if err != nil {
			t.Fatal(err)
		}
		if err := runner.StartRealtime(context.Background()); err != nil {
			t.Fatal(err)
		}
		waitDone(t, runner.Done())
		if !sim.Snapshot().TrackPower || runner.Snapshot().State != StateCompleted {
			t.Fatalf("simulator=%+v control=%+v", sim.Snapshot(), runner.Snapshot())
		}
	})

	t.Run("context", func(t *testing.T) {
		definition := mustParse(t, scenarioWithSteps(`{"at":"1h","action":"station.track_power","on":true}`))
		sim := simulator.New()
		if err := sim.Connect(context.Background()); err != nil {
			t.Fatal(err)
		}
		runner, err := New(definition, sim, clock.Real{})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		if err := runner.StartRealtime(ctx); err != nil {
			t.Fatal(err)
		}
		cancel()
		waitDone(t, runner.Done())
		if snapshot := runner.Snapshot(); snapshot.State != StateStopped || snapshot.NextStep != 0 {
			t.Fatalf("control snapshot=%+v", snapshot)
		}
	})
}

func TestRealtimeStopsOnSimulatorCloseOrReset(t *testing.T) {
	for _, action := range []string{"close", "reset"} {
		t.Run(action, func(t *testing.T) {
			definition := mustParse(t, scenarioWithSteps(`{"at":"1h","action":"station.track_power","on":true}`))
			sim := simulator.New()
			if err := sim.Connect(context.Background()); err != nil {
				t.Fatal(err)
			}
			runner, err := New(definition, sim, clock.Real{})
			if err != nil {
				t.Fatal(err)
			}
			if err := runner.StartRealtime(context.Background()); err != nil {
				t.Fatal(err)
			}
			if action == "close" {
				if err := sim.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				sim.Reset()
			}
			waitDone(t, runner.Done())
			if snapshot := runner.Snapshot(); snapshot.State != StateStopped || snapshot.NextStep != 0 {
				t.Fatalf("control snapshot=%+v", snapshot)
			}
		})
	}
}

func TestScenarioExecutionIsRaceSafeWithStatusReaders(t *testing.T) {
	var steps []string
	for index := 1; index <= 100; index++ {
		connectivity := "online"
		if index%2 == 0 {
			connectivity = "degraded"
		}
		steps = append(steps, fmt.Sprintf(`{"at":"%dms","action":"station.connectivity","connectivity":"%s"}`, index, connectivity))
	}
	definition := mustParse(t, scenarioWithSteps(steps...))
	_, sim, runner := newManualRunner(t, definition)
	if err := runner.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := runner.Advance(context.Background(), 100*time.Millisecond); err != nil {
			t.Errorf("Advance: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		for index := 0; index < 500; index++ {
			if _, err := sim.Status(context.Background()); err != nil && !errors.Is(err, simulator.ErrOperationCanceled) {
				t.Errorf("Status: %v", err)
				return
			}
		}
	}()
	wg.Wait()
	if snapshot := runner.Snapshot(); snapshot.State != StateCompleted {
		t.Fatalf("control snapshot=%+v", snapshot)
	}
}

func TestReferenceScenariosLoad(t *testing.T) {
	paths := []string{
		"station-offline-recovery.json",
		"feedback-a-to-b.json",
		"accessory-electrical-fault.json",
	}
	for _, name := range paths {
		t.Run(name, func(t *testing.T) {
			definition, err := LoadFile(filepath.Join("..", "..", "..", "..", "tests", "simulator", "scenarios", name))
			if err != nil {
				t.Fatal(err)
			}
			if definition.Name == "" {
				t.Fatal("loaded scenario has no name")
			}
			_, _, runner := newManualRunner(t, definition)
			if err := runner.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(definition.Steps) > 0 && runner.Snapshot().State == StateRunning {
				if err := runner.Advance(context.Background(), definition.Steps[len(definition.Steps)-1].At); err != nil {
					t.Fatal(err)
				}
			}
			if snapshot := runner.Snapshot(); snapshot.State != StateCompleted {
				t.Fatalf("control snapshot=%+v", snapshot)
			}
		})
	}
}

func scenarioWithSteps(steps ...string) string {
	return fmt.Sprintf(`{"version":1,"name":"test","initial":{},"steps":[%s]}`, strings.Join(steps, ","))
}

func mustParse(t *testing.T, content string) *Definition {
	t.Helper()
	definition, err := Load(bytes.NewBufferString(content))
	if err != nil {
		t.Fatal(err)
	}
	return definition
}

func newManualRunner(t *testing.T, definition *Definition) (time.Time, *simulator.Simulator, *Runner) {
	t.Helper()
	start := time.Date(2026, time.August, 30, 10, 0, 0, 0, time.UTC)
	clk := clock.NewFake(start)
	sim := simulator.NewWithClock(clk)
	if err := sim.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	runner, err := New(definition, sim, clk)
	if err != nil {
		t.Fatal(err)
	}
	return start, sim, runner
}

func receiveFeedback(t *testing.T, feedback <-chan station.FeedbackEvent) station.FeedbackEvent {
	t.Helper()
	select {
	case event := <-feedback:
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for feedback")
		return station.FeedbackEvent{}
	}
}

func waitDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for scenario completion")
	}
}
