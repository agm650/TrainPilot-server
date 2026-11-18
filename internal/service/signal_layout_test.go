package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/model"
)

func TestSignalLayoutBarrierCancelsOpeningAndReplacesEndpoint(t *testing.T) {
	db, sim, bus := signalServiceFixture(t)
	o := signalOccupancyFixture(t, db, bus)
	signalObserveBlock(t, o, model.OccupancyFree, 1)
	_ = sim.SetTrackPower(context.Background(), true)
	driver := &signalOpeningDriver{Simulator: sim, entered: make(chan struct{}, 1)}
	s := NewSignalService(db, driver, bus, o)
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	awaitSignal(t, s, func(v model.SignalState) bool { return v.CommandedAspect != nil && !v.Pending })
	result := make(chan error, 1)
	go func() { result <- s.CommandAspect(context.Background(), signalDispatcher, "s1", "VL") }()
	select {
	case <-driver.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("opening not started")
	}
	if err := s.ApplyLayout(context.Background(), func() error {
		d, err := db.Signal(context.Background(), "s1")
		if err != nil {
			return err
		}
		d.Endpoint.LinearAddress = 2
		if err := db.SetSignal(context.Background(), d); err != nil {
			return err
		}
		bus.Publish("layout.imported", nil)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, ErrSafetyPreempted) {
		t.Fatal(err)
	}
	views := s.Signals()
	if len(views) != 1 || views[0].Definition.Endpoint.LinearAddress != 2 || views[0].State.DesiredAspect != "S" {
		t.Fatal(views)
	}
	awaitSignal(t, s, func(v model.SignalState) bool { return v.CommandedAspect != nil && !v.Pending })
	commands := sim.ExtendedAccessoryCommands()
	if len(commands) != 2 || commands[1].Command.Address != 2 || commands[1].Command.Aspect != 0 {
		t.Fatal(commands)
	}
	failure := errors.New("transaction rolled back")
	if err := s.ApplyLayout(context.Background(), func() error { return failure }); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if views := s.Signals(); len(views) != 1 || views[0].Definition.Endpoint.LinearAddress != 2 || views[0].State.DesiredAspect != "S" {
		t.Fatal(views)
	}
}
