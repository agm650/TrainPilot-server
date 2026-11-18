package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/station/simulator"
)

type recoveringSignalDriver struct {
	*simulator.Simulator
	attempts atomic.Int32
	fail     atomic.Bool
}

func (d *recoveringSignalDriver) SetExtendedAccessory(ctx context.Context, c station.ExtendedAccessoryCommand) error {
	d.attempts.Add(1)
	if d.fail.Load() {
		return errors.New("offline decoder")
	}
	return d.Simulator.SetExtendedAccessory(ctx, c)
}

func TestSignalRecoveryRetriesStopBeforeOpeningAndDoesNotConfirmFailure(t *testing.T) {
	db, sim, bus := signalServiceFixture(t)
	o := signalOccupancyFixture(t, db, bus)
	signalObserveBlock(t, o, model.OccupancyFree, 1)
	_ = sim.SetTrackPower(context.Background(), true)
	d := &recoveringSignalDriver{Simulator: sim}
	d.fail.Store(true)
	s := NewSignalService(db, d, bus, o)
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	state := awaitSignal(t, s, func(v model.SignalState) bool { return v.Error != nil })
	if state.CommandedAspect != nil || state.ReportedAspect != nil || state.Synchronized {
		t.Fatal(state)
	}
	for range 20 {
		s.reconcile()
	}
	if d.attempts.Load() != 1 {
		t.Fatal("retry storm", d.attempts.Load())
	}
	d.fail.Store(false)
	state = awaitSignal(t, s, func(v model.SignalState) bool { return v.CommandedAspect != nil && !v.Pending && v.Error == nil })
	if *state.CommandedAspect != "S" {
		t.Fatal(state)
	}
	if err := s.CommandAspect(context.Background(), signalDispatcher, "s1", "VL"); err != nil {
		t.Fatal(err)
	}
	commands := sim.ExtendedAccessoryCommands()
	if len(commands) != 2 || commands[0].Command.Aspect != 0 || commands[1].Command.Aspect != 16 {
		t.Fatal(commands)
	}
}

func TestSignalRefreshIsOptInAndDecoderResetRequiresStop(t *testing.T) {
	db, sim, bus := signalServiceFixture(t)
	o := signalOccupancyFixture(t, db, bus)
	signalObserveBlock(t, o, model.OccupancyFree, 1)
	_ = sim.SetTrackPower(context.Background(), true)
	s := NewSignalService(db, sim, bus, o)
	if err := s.SetRefreshInterval(100 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	awaitSignal(t, s, func(v model.SignalState) bool { return v.CommandedAspect != nil && !v.Pending })
	deadline := time.Now().Add(3 * time.Second)
	for len(sim.ExtendedAccessoryCommands()) < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(sim.ExtendedAccessoryCommands()) < 2 {
		t.Fatal("refresh was not sent")
	}
	if err := s.CommandAspect(context.Background(), signalDispatcher, "s1", "VL"); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, s, func(v model.SignalState) bool { return v.ReportedAspect != nil && !v.Pending })
	start := len(sim.ExtendedAccessoryCommands())
	s.observeReport(station.ExtendedAccessoryStateEvent{Address: 1, State: station.AccessoryReportUnknown, Quality: station.AccessoryReportStation, ObservedAt: time.Now()})
	state, _ := s.State("s1")
	if state.CommandedAspect != nil || state.ReportedAspect != nil || state.DesiredAspect != "S" {
		t.Fatal(state)
	}
	awaitSignal(t, s, func(v model.SignalState) bool {
		return v.CommandedAspect != nil && *v.CommandedAspect == "S" && !v.Pending
	})
	for _, c := range sim.ExtendedAccessoryCommands()[start:] {
		if c.Command.Aspect != 0 {
			t.Fatal("reset replayed opening", c)
		}
	}
}

func TestAutomaticSignalImportRequiresNewObservations(t *testing.T) {
	s, o, sim, _ := automaticSignalFixture(t, true)
	observeAutomaticBlocks(t, o, 1, model.OccupancyFree, model.OccupancyFree, model.OccupancyFree)
	awaitAutomaticSignals(t, s, map[string]model.SignalAspectID{"a": "VL", "b": "VL", "c": "VL"})
	start := len(sim.ExtendedAccessoryCommands())
	if err := s.ApplyLayout(context.Background(), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	awaitAutomaticSignals(t, s, map[string]model.SignalAspectID{"a": "S", "b": "S", "c": "S"})
	if o.BlockState("a").State != model.OccupancyUnknown {
		t.Fatal("import reused observations")
	}
	for _, c := range sim.ExtendedAccessoryCommands()[start:] {
		if c.Command.Aspect != 0 {
			t.Fatal(c)
		}
	}
	observeAutomaticBlocks(t, o, 1, model.OccupancyFree, model.OccupancyFree, model.OccupancyFree)
	awaitAutomaticSignals(t, s, map[string]model.SignalAspectID{"a": "VL", "b": "VL", "c": "VL"})
}

func TestAutomaticSignalPartialRecoveryNeverReusesOldOpening(t *testing.T) {
	s, o, _, _ := automaticSignalFixture(t, true)
	observeAutomaticBlocks(t, o, 1, model.OccupancyFree, model.OccupancyFree, model.OccupancyFree)
	allFree := map[string]model.SignalAspectID{"a": "VL", "b": "VL", "c": "VL"}
	awaitAutomaticSignals(t, s, allFree)
	for _, connectivity := range []station.Connectivity{station.Degraded, station.Offline} {
		s.ObserveStatus(station.Status{Connectivity: connectivity, TrackPower: "unknown"})
		if err := o.SetProviderAvailable(context.Background(), "external", false); err != nil {
			t.Fatal(err)
		}
		for _, v := range s.Snapshot() {
			if v.CommandedAspect != nil || v.ReportedAspect != nil {
				t.Fatal(v)
			}
		}
		if err := o.SetProviderAvailable(context.Background(), "external", true); err != nil {
			t.Fatal(err)
		}
		s.ObserveStatus(station.Status{Connectivity: station.Online, TrackPower: "on"})
		awaitAutomaticSignals(t, s, map[string]model.SignalAspectID{"a": "S", "b": "S", "c": "S"})
		observeAutomaticBlocks(t, o, 1, model.OccupancyFree)
		awaitAutomaticSignals(t, s, map[string]model.SignalAspectID{"a": "A", "b": "S", "c": "S"})
		observeAutomaticBlocks(t, o, 2, model.OccupancyFree, model.OccupancyFree, model.OccupancyFree)
		awaitAutomaticSignals(t, s, allFree)
	}
}
