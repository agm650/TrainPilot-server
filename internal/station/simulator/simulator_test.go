package simulator

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/station"
)

func TestOperationsFailWhileDisconnected(t *testing.T) {
	ctx := context.Background()
	sim := New()
	operations := []struct {
		name string
		call func() error
	}{
		{"track power", func() error { return sim.SetTrackPower(ctx, true) }},
		{"emergency stop", func() error { return sim.EmergencyStop(ctx) }},
		{"speed", func() error { return sim.SetLocoSpeed(ctx, 3, 0.5, station.Forward) }},
		{"function", func() error { return sim.SetLocoFunction(ctx, 3, 1, true) }},
		{"accessory", func() error { return sim.SetAccessory(ctx, 3, "straight") }},
	}
	for _, tc := range operations {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); err == nil {
				t.Fatal("expected disconnected error")
			}
		})
	}
}

func TestSimulatorLifecycleAndLocomotiveState(t *testing.T) {
	ctx := context.Background()
	sim := New()
	if err := sim.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if caps := sim.Capabilities(); caps.Driver != "simulator" || !caps.TrackPower || !caps.LocomotiveControl || !caps.AccessoryControl || !caps.Feedback || caps.Functions != 69 || caps.MaxFunctionNumber != 68 {
		t.Fatalf("capabilities=%+v", caps)
	}
	if err := sim.SetTrackPower(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := sim.SetLocoSpeed(ctx, 2601, -0.1, station.Forward); err == nil {
		t.Fatal("negative speed accepted")
	}
	if err := sim.SetLocoSpeed(ctx, 2601, 1.1, station.Forward); err == nil {
		t.Fatal("speed above one accepted")
	}
	if err := sim.SetLocoSpeed(ctx, 2601, 0.5, station.Direction("sideways")); err != station.ErrUnsupported {
		t.Fatalf("invalid direction error=%v", err)
	}
	if err := sim.SetLocoSpeed(ctx, 2601, 0.6, station.Reverse); err != nil {
		t.Fatal(err)
	}
	if err := sim.SetLocoFunction(ctx, 2601, 2, true); err != nil {
		t.Fatal(err)
	}
	if err := sim.SetLocoFunction(ctx, 2601, 69, true); err == nil {
		t.Fatal("unsupported function accepted")
	}
	if err := sim.SetAccessory(ctx, 12, "diverging"); err != nil {
		t.Fatal(err)
	}

	state := sim.Loco(2601)
	if state.Speed != 0.6 || state.Direction != station.Reverse || !state.Functions[2] {
		t.Fatalf("state=%+v", state)
	}
	state.Functions[2] = false
	if !sim.Loco(2601).Functions[2] {
		t.Fatal("Loco returned the simulator's mutable function map")
	}
	if empty := sim.Loco(9999); empty.Functions == nil || empty.Speed != 0 {
		t.Fatalf("empty state=%+v", empty)
	}

	if err := sim.EmergencyStop(ctx); err != nil {
		t.Fatal(err)
	}
	if got := sim.Loco(2601).Speed; got != 0 {
		t.Fatalf("speed after emergency stop=%v", got)
	}
	status, err := sim.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !status.EmergencyStop {
		t.Fatal("emergency stop is not reflected in simulator status")
	}
	if err := sim.SetTrackPower(ctx, true); err != nil {
		t.Fatal(err)
	}
	status, err = sim.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.EmergencyStop {
		t.Fatal("power-on did not clear the simulator emergency-stop latch")
	}
	if err := sim.Close(); err != nil {
		t.Fatal(err)
	}
	if err := sim.Close(); err != nil {
		t.Fatal(err)
	}
	if err := sim.SetTrackPower(ctx, false); err == nil {
		t.Fatal("operation succeeded after Close")
	}
}

func TestInjectFeedbackDropsWhenBufferIsFull(t *testing.T) {
	sim := New()
	for i := 0; i < 64; i++ {
		sim.InjectFeedback(station.FeedbackEvent{Address: i})
	}
	done := make(chan struct{})
	go func() {
		sim.InjectFeedback(station.FeedbackEvent{Address: 65})
		close(done)
	}()
	<-done

	for i := 0; i < 64; i++ {
		<-sim.Feedback()
	}
	select {
	case event := <-sim.Feedback():
		t.Fatalf("unexpected extra event: %+v", event)
	default:
	}
}

func TestSnapshotIsDeepCopy(t *testing.T) {
	ctx := context.Background()
	sim := New()
	if err := sim.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if err := sim.SetTrackPower(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := sim.SetLocoSpeed(ctx, 2601, 0.6, station.Reverse); err != nil {
		t.Fatal(err)
	}
	if err := sim.SetLocoFunction(ctx, 2601, 2, true); err != nil {
		t.Fatal(err)
	}
	if err := sim.SetAccessory(ctx, 12, "diverging"); err != nil {
		t.Fatal(err)
	}

	snapshot := sim.Snapshot()
	snapshot.Connected = false
	snapshot.TrackPower = false
	loco := snapshot.Locomotives[2601]
	loco.Speed = 0
	loco.Functions[2] = false
	snapshot.Locomotives[2601] = loco
	snapshot.Locomotives[99] = LocoState{Functions: map[int]bool{1: true}}
	snapshot.Accessories[12] = AccessoryState{State: "straight"}
	snapshot.Accessories[13] = AccessoryState{State: "diverging"}

	actual := sim.Snapshot()
	if !actual.Connected || !actual.TrackPower {
		t.Fatalf("simulator scalar state changed through snapshot: %+v", actual)
	}
	actualLoco := actual.Locomotives[2601]
	if actualLoco.Speed != 0.6 || !actualLoco.Functions[2] {
		t.Fatalf("simulator locomotive changed through snapshot: %+v", actualLoco)
	}
	if _, ok := actual.Locomotives[99]; ok {
		t.Fatal("locomotive added to snapshot leaked into simulator")
	}
	if got := actual.Accessories[12].State; got != "diverging" {
		t.Fatalf("simulator accessory changed through snapshot: %q", got)
	}
	if _, ok := actual.Accessories[13]; ok {
		t.Fatal("accessory added to snapshot leaked into simulator")
	}
}

func TestHealthUsesInjectedClock(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, time.August, 30, 12, 0, 0, 0, time.FixedZone("test", 2*60*60))
	clk := clock.NewFake(start)
	sim := NewWithClock(clk)
	if err := sim.Connect(ctx); err != nil {
		t.Fatal(err)
	}

	health := sim.Health()
	if health.LastSeen == nil || !health.LastSeen.Equal(start) {
		t.Fatalf("initial LastSeen=%v, want %v", health.LastSeen, start.UTC())
	}

	clk.Advance(5 * time.Second)
	if err := sim.SetTrackPower(ctx, true); err != nil {
		t.Fatal(err)
	}
	health = sim.Health()
	want := start.Add(5 * time.Second)
	if health.LastSeen == nil || !health.LastSeen.Equal(want) {
		t.Fatalf("advanced LastSeen=%v, want %v", health.LastSeen, want.UTC())
	}
}

func TestResetPreservesConnectionAndClearsState(t *testing.T) {
	ctx := context.Background()
	sim := New()
	if err := sim.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if err := sim.SetTrackPower(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := sim.SetLocoSpeed(ctx, 2601, 0.6, station.Reverse); err != nil {
		t.Fatal(err)
	}
	if err := sim.SetLocoFunction(ctx, 2601, 2, true); err != nil {
		t.Fatal(err)
	}
	if err := sim.SetAccessory(ctx, 12, "diverging"); err != nil {
		t.Fatal(err)
	}
	if err := sim.EmergencyStop(ctx); err != nil {
		t.Fatal(err)
	}
	sim.InjectFeedback(station.FeedbackEvent{Address: 4, Active: true})

	sim.Reset()

	snapshot := sim.Snapshot()
	if !snapshot.Connected {
		t.Fatal("reset disconnected the simulator")
	}
	if snapshot.TrackPower || snapshot.EmergencyStop {
		t.Fatalf("reset safety state=%+v", snapshot)
	}
	if len(snapshot.Locomotives) != 0 || len(snapshot.Accessories) != 0 {
		t.Fatalf("reset retained layout state: %+v", snapshot)
	}
	if got := sim.Loco(2601); len(got.Functions) != 0 || got.Speed != 0 {
		t.Fatalf("reset locomotive state=%+v", got)
	}
	if got := sim.Accessory(12); got != (AccessoryState{}) {
		t.Fatalf("reset accessory state=%+v", got)
	}
	select {
	case event := <-sim.Feedback():
		t.Fatalf("reset retained feedback event: %+v", event)
	default:
	}

	if err := sim.Close(); err != nil {
		t.Fatal(err)
	}
	sim.Reset()
	if sim.Snapshot().Connected {
		t.Fatal("reset reconnected a disconnected simulator")
	}
}

func TestSnapshotAndCommandsAreConcurrentSafe(t *testing.T) {
	ctx := context.Background()
	sim := New()
	if err := sim.Connect(ctx); err != nil {
		t.Fatal(err)
	}

	const iterations = 250
	errors := make(chan error, iterations*3)
	var wg sync.WaitGroup
	wg.Add(4)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			if err := sim.SetLocoSpeed(ctx, i%8, float64(i%100)/100, station.Forward); err != nil {
				errors <- err
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			if err := sim.SetLocoFunction(ctx, i%8, i%69, i%2 == 0); err != nil {
				errors <- err
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			if err := sim.SetAccessory(ctx, i%16, "straight"); err != nil {
				errors <- err
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			snapshot := sim.Snapshot()
			if loco, ok := snapshot.Locomotives[i%8]; ok {
				loco.Functions[i%69] = true
				snapshot.Locomotives[i%8] = loco
			}
			delete(snapshot.Accessories, i%16)
		}
	}()
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}

	snapshot := sim.Snapshot()
	if len(snapshot.Locomotives) == 0 || len(snapshot.Accessories) == 0 {
		t.Fatalf("concurrent commands did not produce state: %+v", snapshot)
	}
}
