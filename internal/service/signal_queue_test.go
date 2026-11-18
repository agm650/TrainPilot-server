package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station"
)

func queueTestJob(id string, restrictive bool) *signalJob {
	ctx, cancel := context.WithCancel(context.Background())
	return &signalJob{id: id, ctx: ctx, cancel: cancel, restrictive: restrictive, done: make(chan error, 1)}
}

func TestSignalQueueBoundsCoalescesAndPrioritizesStops(t *testing.T) {
	q := newSignalCommandQueue()
	for i := range signalQueueCapacity {
		if _, err := q.enqueue(queueTestJob(fmt.Sprint(i), false)); err != nil {
			t.Fatal(err)
		}
	}
	extra := queueTestJob("overflow", false)
	defer extra.cancel()
	if _, err := q.enqueue(extra); !errors.Is(err, ErrSignalBusy) {
		t.Fatal(err)
	}
	latest := queueTestJob("0", false)
	old, err := q.enqueue(latest)
	if err != nil || old == nil {
		t.Fatal(old, err)
	}
	if err := <-old.done; !errors.Is(err, ErrSafetyPreempted) {
		t.Fatal(err)
	}
	stop := queueTestJob("stop", true)
	victim, err := q.enqueue(stop)
	if err != nil || victim == nil || len(q.pending) != signalQueueCapacity {
		t.Fatal(victim, err, len(q.pending))
	}
	if err := <-victim.done; !errors.Is(err, ErrSafetyPreempted) {
		t.Fatal(err)
	}
	if job := q.take(); job != stop {
		t.Fatal("stop did not take priority", job)
	}
	stop.cancel()
	seenLatest := false
	for job := q.take(); job != nil; job = q.take() {
		if job.id == "0" {
			seenLatest = job == latest
		}
		job.cancel()
	}
	if !seenLatest {
		t.Fatal("coalescence lost latest target")
	}
}

type signalSafetyDriver struct {
	*signalOpeningDriver
	called chan time.Time
}

func (d *signalSafetyDriver) EmergencyStop(ctx context.Context) error {
	d.called <- time.Now()
	return d.Simulator.EmergencyStop(ctx)
}
func (d *signalSafetyDriver) SetTrackPower(ctx context.Context, on bool) error {
	if !on {
		d.called <- time.Now()
	}
	return d.Simulator.SetTrackPower(ctx, on)
}

func TestSignalIOCannotDelayEmergencyOrPowerOff(t *testing.T) {
	for _, powerOff := range []bool{false, true} {
		t.Run(fmt.Sprint("powerOff=", powerOff), func(t *testing.T) {
			db, sim, bus := signalServiceFixture(t)
			o := signalOccupancyFixture(t, db, bus)
			signalObserveBlock(t, o, model.OccupancyFree, 1)
			_ = sim.SetTrackPower(context.Background(), true)
			d := &signalSafetyDriver{signalOpeningDriver: &signalOpeningDriver{Simulator: sim, entered: make(chan struct{}, 1)}, called: make(chan time.Time, 1)}
			s := NewSignalService(db, d, bus, o)
			control := NewControlService(db, d, bus, clock.Real{}, time.Minute, time.Second, time.Second)
			control.SetSignalService(s)
			if err := s.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			awaitSignal(t, s, func(v model.SignalState) bool { return v.CommandedAspect != nil && !v.Pending })
			result := make(chan error, 1)
			go func() { result <- s.CommandAspect(context.Background(), signalDispatcher, "s1", "VL") }()
			select {
			case <-d.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("opening did not start")
			}
			started := time.Now()
			var err error
			if powerOff {
				err = control.SetTrackPower(context.Background(), signalDispatcher, false)
			} else {
				err = control.EmergencyStop(context.Background(), signalDispatcher)
			}
			if err != nil {
				t.Fatal(err)
			}
			called := <-d.called
			if elapsed := called.Sub(started); elapsed > 100*time.Millisecond {
				t.Fatalf("driver call delayed by signal I/O: %v", elapsed)
			}
			select {
			case err := <-result:
				if err == nil {
					t.Fatal("opening succeeded")
				}
			case <-time.After(time.Second):
				t.Fatal("opening leaked")
			}
			if !powerOff {
				awaitSignal(t, s, func(v model.SignalState) bool {
					return v.CommandedAspect != nil && *v.CommandedAspect == "S" && !v.Pending
				})
				if err := s.CommandAspect(context.Background(), signalDispatcher, "s1", "VL"); !errors.Is(err, ErrSignalUnavailable) {
					t.Fatal("opening during emergency", err)
				}
			}
		})
	}
}

func TestSignalFeedbackCancelsSlowOpeningWithoutBlockingReception(t *testing.T) {
	db, sim, bus := signalServiceFixture(t)
	o := signalOccupancyFixture(t, db, bus)
	signalObserveBlock(t, o, model.OccupancyFree, 1)
	_ = sim.SetTrackPower(context.Background(), true)
	d := &signalOpeningDriver{Simulator: sim, entered: make(chan struct{}, 1)}
	s := NewSignalService(db, d, bus, o)
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	awaitSignal(t, s, func(v model.SignalState) bool { return v.CommandedAspect != nil && !v.Pending })
	result := make(chan error, 1)
	go func() { result <- s.CommandAspect(context.Background(), signalDispatcher, "s1", "VL") }()
	select {
	case <-d.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("opening did not start")
	}
	for seq := uint64(2); seq < 52; seq++ {
		state := model.OccupancyFree
		if seq%2 == 1 {
			state = model.OccupancyOccupied
		}
		signalObserveBlock(t, o, state, seq)
	}
	awaitSignal(t, s, func(v model.SignalState) bool {
		return v.CommandedAspect != nil && *v.CommandedAspect == "S" && !v.Pending
	})
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("obsolete opening succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("blocked result")
	}
	for _, c := range sim.ExtendedAccessoryCommands() {
		if c.Command.Aspect != 0 {
			t.Fatal("stale opening reached transport", c)
		}
	}
}

// The packet has already crossed cancellation. Its next actual write must stop.
type lateSignalDriver struct {
	*signalOpeningDriver
	release chan struct{}
}

func (d *lateSignalDriver) SetExtendedAccessory(ctx context.Context, c station.ExtendedAccessoryCommand) error {
	if c.Aspect == 16 {
		d.entered <- struct{}{}
		<-d.release
		return d.Simulator.SetExtendedAccessory(context.WithoutCancel(ctx), c)
	}
	return d.Simulator.SetExtendedAccessory(ctx, c)
}

func TestSignalCorrectsPacketAlreadyInFlight(t *testing.T) {
	db, sim, bus := signalServiceFixture(t)
	o := signalOccupancyFixture(t, db, bus)
	signalObserveBlock(t, o, model.OccupancyFree, 1)
	_ = sim.SetTrackPower(context.Background(), true)
	d := &lateSignalDriver{signalOpeningDriver: &signalOpeningDriver{Simulator: sim, entered: make(chan struct{}, 1)}, release: make(chan struct{})}
	s := NewSignalService(db, d, bus, o)
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	awaitSignal(t, s, func(v model.SignalState) bool { return v.CommandedAspect != nil && !v.Pending })
	result := make(chan error, 1)
	go func() { result <- s.CommandAspect(context.Background(), signalDispatcher, "s1", "VL") }()
	select {
	case <-d.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("opening did not start")
	}
	signalObserveBlock(t, o, model.OccupancyOccupied, 2)
	awaitSignal(t, s, func(v model.SignalState) bool { return v.DesiredAspect == "S" })
	close(d.release)
	if err := <-result; !errors.Is(err, ErrSafetyPreempted) {
		t.Fatal(err)
	}
	awaitSignal(t, s, func(v model.SignalState) bool {
		return v.CommandedAspect != nil && *v.CommandedAspect == "S" && !v.Pending
	})
	commands := sim.ExtendedAccessoryCommands()
	if len(commands) < 3 || commands[1].Command.Aspect != 16 {
		t.Fatal(commands)
	}
	for _, command := range commands[2:] {
		if command.Command.Aspect != 0 {
			t.Fatal("correction reopened signal", commands)
		}
	}
}
