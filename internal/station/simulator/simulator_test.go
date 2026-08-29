package simulator

import (
	"context"
	"testing"

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
