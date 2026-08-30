package simulator

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/station"
)

func TestConnectivityTransitionsPreserveLastSeenUntilOnline(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, time.August, 30, 10, 0, 0, 0, time.UTC)
	clk := clock.NewFake(start)
	sim := NewWithClock(clk)
	if err := sim.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	assertConnectivity(t, sim, station.Online, start)

	clk.Advance(5 * time.Second)
	if err := sim.SetConnectivity(station.Degraded); err != nil {
		t.Fatal(err)
	}
	assertConnectivity(t, sim, station.Degraded, start)
	if err := sim.SetConnectivity(station.Online); err != nil {
		t.Fatal(err)
	}
	assertConnectivity(t, sim, station.Online, start.Add(5*time.Second))

	clk.Advance(5 * time.Second)
	if err := sim.SetConnectivity(station.Degraded); err != nil {
		t.Fatal(err)
	}
	if err := sim.SetConnectivity(station.Offline); err != nil {
		t.Fatal(err)
	}
	assertConnectivity(t, sim, station.Offline, start.Add(5*time.Second))
	clk.Advance(5 * time.Second)
	if err := sim.SetConnectivity(station.Online); err != nil {
		t.Fatal(err)
	}
	assertConnectivity(t, sim, station.Online, start.Add(15*time.Second))
}

func TestOfflineRejectsEveryActiveOperationWithoutChangingState(t *testing.T) {
	tests := []struct {
		name  string
		setup func(context.Context, *Simulator) error
		call  func(context.Context, *Simulator) error
	}{
		{
			name:  "track power",
			setup: func(context.Context, *Simulator) error { return nil },
			call:  func(ctx context.Context, sim *Simulator) error { return sim.SetTrackPower(ctx, true) },
		},
		{
			name:  "emergency stop",
			setup: func(context.Context, *Simulator) error { return nil },
			call:  func(ctx context.Context, sim *Simulator) error { return sim.EmergencyStop(ctx) },
		},
		{
			name: "throttle",
			setup: func(ctx context.Context, sim *Simulator) error {
				return sim.SetLocoSpeed(ctx, 3, 0.25, station.Forward)
			},
			call: func(ctx context.Context, sim *Simulator) error {
				return sim.SetLocoSpeed(ctx, 3, 0.75, station.Reverse)
			},
		},
		{
			name: "function",
			setup: func(ctx context.Context, sim *Simulator) error {
				return sim.SetLocoFunction(ctx, 3, 1, false)
			},
			call: func(ctx context.Context, sim *Simulator) error {
				return sim.SetLocoFunction(ctx, 3, 1, true)
			},
		},
		{
			name: "accessory",
			setup: func(ctx context.Context, sim *Simulator) error {
				return sim.SetAccessory(ctx, 12, "straight")
			},
			call: func(ctx context.Context, sim *Simulator) error {
				return sim.SetAccessory(ctx, 12, "diverging")
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			sim := New()
			if err := sim.Connect(ctx); err != nil {
				t.Fatal(err)
			}
			if err := tc.setup(ctx, sim); err != nil {
				t.Fatal(err)
			}
			if err := sim.SetConnectivity(station.Offline); err != nil {
				t.Fatal(err)
			}
			before := sim.Snapshot()
			if err := tc.call(ctx, sim); !errors.Is(err, station.ErrOffline) {
				t.Fatalf("operation error=%v, want station.ErrOffline", err)
			}
			after := sim.Snapshot()
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("offline operation changed state\nbefore=%+v\nafter=%+v", before, after)
			}
		})
	}
}

func TestDegradedAllowsCommandsAndSuccessfulActivityRefreshesLastSeen(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, time.August, 30, 10, 0, 0, 0, time.UTC)
	clk := clock.NewFake(start)
	sim := NewWithClock(clk)
	if err := sim.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	clk.Advance(5 * time.Second)
	if err := sim.SetConnectivity(station.Degraded); err != nil {
		t.Fatal(err)
	}
	if err := sim.SetLocoSpeed(ctx, 3, 0.5, station.Forward); err != nil {
		t.Fatal(err)
	}
	if got := sim.Loco(3).Speed; got != 0.5 {
		t.Fatalf("speed=%v", got)
	}
	assertConnectivity(t, sim, station.Degraded, start.Add(5*time.Second))
}

func TestSingleOperationFaultDoesNotModifyState(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, time.August, 30, 10, 0, 0, 0, time.UTC)
	clk := clock.NewFake(start)
	sim := NewWithClock(clk)
	if err := sim.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	errInjected := errors.New("injected throttle failure")
	if err := sim.SetOperationFault(OpThrottle, OperationFault{Error: errInjected, Remaining: 1}); err != nil {
		t.Fatal(err)
	}
	if err := sim.SetLocoFunction(ctx, 3, 1, true); err != nil {
		t.Fatalf("fault leaked to another operation: %v", err)
	}
	clk.Advance(5 * time.Second)
	lastSeen := sim.Health().LastSeen
	if err := sim.SetLocoSpeed(ctx, 3, 0.75, station.Reverse); !errors.Is(err, errInjected) {
		t.Fatalf("first throttle error=%v", err)
	}
	if got := sim.Loco(3); got.Speed != 0 || got.Direction != "" {
		t.Fatalf("failed throttle modified state: %+v", got)
	}
	if health := sim.Health(); health.LastSeen == nil || lastSeen == nil || !health.LastSeen.Equal(*lastSeen) {
		t.Fatalf("failed throttle refreshed LastSeen: before=%v after=%v", lastSeen, health.LastSeen)
	}
	if err := sim.SetLocoSpeed(ctx, 3, 0.5, station.Forward); err != nil {
		t.Fatalf("second throttle error=%v", err)
	}
	if got := sim.Loco(3).Speed; got != 0.5 {
		t.Fatalf("successful throttle speed=%v", got)
	}
}

func TestOperationFaultRemainingCountIsExact(t *testing.T) {
	ctx := context.Background()
	sim := New()
	if err := sim.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	errInjected := errors.New("injected throttle failure")
	if err := sim.SetOperationFault(OpThrottle, OperationFault{Error: errInjected, Remaining: 3}); err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 4; attempt++ {
		err := sim.SetLocoSpeed(ctx, 3, float64(attempt)/10, station.Forward)
		if attempt <= 3 && !errors.Is(err, errInjected) {
			t.Fatalf("attempt %d error=%v", attempt, err)
		}
		if attempt == 4 && err != nil {
			t.Fatalf("fourth attempt error=%v", err)
		}
	}
	if got := sim.Loco(3).Speed; got != 0.4 {
		t.Fatalf("speed=%v", got)
	}
}

func TestDelayedOperationCompletesOnlyWhenClockReleasesIt(t *testing.T) {
	ctx := context.Background()
	clk := newControlledWaitClock()
	sim := NewWithClock(clk)
	if err := sim.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if err := sim.SetOperationFault(OpStatus, OperationFault{Delay: 500 * time.Millisecond, Remaining: 1}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := sim.Status(ctx)
		done <- err
	}()
	waitForControlledClock(t, clk)
	select {
	case err := <-done:
		t.Fatalf("status completed before clock release: %v", err)
	default:
	}
	close(clk.release)
	if err := waitForOperation(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestDelayedOperationRespectsContextWithoutApplying(t *testing.T) {
	clk := newControlledWaitClock()
	sim := NewWithClock(clk)
	if err := sim.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := sim.SetOperationFault(OpThrottle, OperationFault{Delay: time.Hour, Remaining: 1}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- sim.SetLocoSpeed(ctx, 3, 0.75, station.Forward)
	}()
	waitForControlledClock(t, clk)
	cancel()
	if err := waitForOperation(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("delayed throttle error=%v", err)
	}
	if got := sim.Loco(3).Speed; got != 0 {
		t.Fatalf("canceled throttle speed=%v", got)
	}
}

func TestResetCancelsDelayedOperationAndClearsFaults(t *testing.T) {
	clk := newControlledWaitClock()
	sim := NewWithClock(clk)
	ctx := context.Background()
	if err := sim.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if err := sim.SetOperationFault(OpThrottle, OperationFault{Delay: time.Hour}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- sim.SetLocoSpeed(ctx, 3, 0.75, station.Forward)
	}()
	waitForControlledClock(t, clk)
	sim.Reset()
	close(clk.release)
	if err := waitForOperation(t, done); !errors.Is(err, ErrOperationCanceled) {
		t.Fatalf("delayed throttle error=%v", err)
	}
	if got := sim.Loco(3).Speed; got != 0 {
		t.Fatalf("reset operation was applied: speed=%v", got)
	}
}

func TestFailedCommandIsNeverReplayed(t *testing.T) {
	ctx := context.Background()
	clk := clock.NewFake(time.Date(2026, time.August, 30, 10, 0, 0, 0, time.UTC))
	sim := NewWithClock(clk)
	if err := sim.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	errInjected := errors.New("injected throttle failure")
	if err := sim.SetOperationFault(OpThrottle, OperationFault{Error: errInjected}); err != nil {
		t.Fatal(err)
	}
	if err := sim.SetLocoSpeed(ctx, 3, 0.75, station.Forward); !errors.Is(err, errInjected) {
		t.Fatalf("failed throttle error=%v", err)
	}
	sim.ClearFaults()
	clk.Advance(24 * time.Hour)
	if got := sim.Loco(3).Speed; got != 0 {
		t.Fatalf("failed throttle was replayed: speed=%v", got)
	}
	if err := sim.SetLocoSpeed(ctx, 3, 0.25, station.Forward); err != nil {
		t.Fatal(err)
	}
	if got := sim.Loco(3).Speed; got != 0.25 {
		t.Fatalf("new throttle speed=%v", got)
	}
}

func TestResetClearsUnlimitedFault(t *testing.T) {
	ctx := context.Background()
	sim := New()
	if err := sim.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	errInjected := errors.New("injected accessory failure")
	if err := sim.SetOperationFault(OpAccessory, OperationFault{Error: errInjected}); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := sim.SetAccessory(ctx, 12, "straight"); !errors.Is(err, errInjected) {
			t.Fatalf("attempt %d error=%v", attempt, err)
		}
	}
	sim.Reset()
	if err := sim.SetAccessory(ctx, 12, "straight"); err != nil {
		t.Fatalf("accessory after reset error=%v", err)
	}
}

func TestConcurrentFaultRulesAreIndependent(t *testing.T) {
	ctx := context.Background()
	sim := New()
	if err := sim.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	errInjected := errors.New("injected operation failure")
	for _, operation := range []Operation{OpThrottle, OpFunction, OpAccessory} {
		if err := sim.SetOperationFault(operation, OperationFault{Error: errInjected, Remaining: 50}); err != nil {
			t.Fatal(err)
		}
	}
	type result struct {
		operation Operation
		failures  int
		err       error
	}
	results := make(chan result, 3)
	var wg sync.WaitGroup
	for _, operation := range []Operation{OpThrottle, OpFunction, OpAccessory} {
		operation := operation
		wg.Add(1)
		go func() {
			defer wg.Done()
			failures := 0
			for attempt := 0; attempt < 100; attempt++ {
				var err error
				switch operation {
				case OpThrottle:
					err = sim.SetLocoSpeed(ctx, 3, 0.5, station.Forward)
				case OpFunction:
					err = sim.SetLocoFunction(ctx, 3, attempt%69, true)
				case OpAccessory:
					err = sim.SetAccessory(ctx, 12, "straight")
				}
				if errors.Is(err, errInjected) {
					failures++
				} else if err != nil {
					results <- result{operation: operation, err: err}
					return
				}
			}
			results <- result{operation: operation, failures: failures}
		}()
	}
	wg.Wait()
	close(results)
	for result := range results {
		if result.err != nil {
			t.Fatalf("%s error=%v", result.operation, result.err)
		}
		if result.failures != 50 {
			t.Fatalf("%s failures=%d, want 50", result.operation, result.failures)
		}
	}
}

func TestOperationFaultValidation(t *testing.T) {
	sim := New()
	if err := sim.SetOperationFault("invalid", OperationFault{}); err == nil {
		t.Fatal("invalid operation accepted")
	}
	if err := sim.SetOperationFault(OpStatus, OperationFault{Delay: -time.Second}); err == nil {
		t.Fatal("negative delay accepted")
	}
	if err := sim.SetOperationFault(OpStatus, OperationFault{Remaining: -1}); err == nil {
		t.Fatal("negative remaining count accepted")
	}
}

func assertConnectivity(t *testing.T, sim *Simulator, want station.Connectivity, lastSeen time.Time) {
	t.Helper()
	health := sim.Health()
	if health.Connectivity != want || health.LastSeen == nil || !health.LastSeen.Equal(lastSeen) {
		t.Fatalf("health=%+v, want connectivity=%s LastSeen=%v", health, want, lastSeen)
	}
	status, err := sim.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Connectivity != want || status.LastSeen == nil || !status.LastSeen.Equal(lastSeen) {
		t.Fatalf("status=%+v, want connectivity=%s LastSeen=%v", status, want, lastSeen)
	}
}

type controlledWaitClock struct {
	now     time.Time
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newControlledWaitClock() *controlledWaitClock {
	return &controlledWaitClock{
		now:     time.Date(2026, time.August, 30, 10, 0, 0, 0, time.UTC),
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (c *controlledWaitClock) Now() time.Time { return c.now }

func (c *controlledWaitClock) WaitUntil(ctx context.Context, _ time.Time) error {
	c.once.Do(func() { close(c.started) })
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.release:
		return nil
	}
}

func waitForControlledClock(t *testing.T, clk *controlledWaitClock) {
	t.Helper()
	select {
	case <-clk.started:
	case <-time.After(time.Second):
		t.Fatal("operation did not enter controlled clock wait")
	}
}

func waitForOperation(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(time.Second):
		t.Fatal("operation did not complete")
		return nil
	}
}
