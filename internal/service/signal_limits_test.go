package service

import (
	"context"
	"errors"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/station/simulator"
)

type limitedSignalDriver struct{ *simulator.Simulator }

func (d limitedSignalDriver) Capabilities() station.Capabilities {
	caps := d.Simulator.Capabilities()
	limits := station.PortableExtendedAccessoryLimits()
	limits.MaxAspect = 31
	caps.ExtendedAccessoryLimits = &limits
	return caps
}

func TestSignalCannotOpenWithoutACommandableFallback(t *testing.T) {
	db, sim, bus := signalServiceFixture(t)
	ctx := context.Background()
	o := signalOccupancyFixture(t, db, bus)
	signalObserveBlock(t, o, model.OccupancyFree, 1)
	d, err := db.Signal(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	d.Endpoint.AspectCodes["S"] = 255
	if err := db.SetSignal(ctx, d); err != nil {
		t.Fatal(err)
	}
	_ = sim.SetTrackPower(ctx, true)
	s := NewSignalService(db, limitedSignalDriver{sim}, bus, o)
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	state := awaitSignal(t, s, func(v model.SignalState) bool { return v.InhibitedReason == "unsupported_endpoint" })
	if state.Pending || state.Synchronized || state.CommandedAspect != nil {
		t.Fatal(state)
	}
	if err := s.CommandAspect(ctx, signalDispatcher, "s1", "VL"); !errors.Is(err, station.ErrUnsupported) {
		t.Fatal(err)
	}
	if len(sim.ExtendedAccessoryCommands()) != 0 {
		t.Fatal("opening sent without commandable stop")
	}
	if err := s.ApplyLayout(ctx, func() error {
		d.Endpoint.AspectCodes["S"] = 0
		d.Endpoint.AspectCodes["VL"] = 255
		return db.SetSignal(ctx, d)
	}); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, s, func(v model.SignalState) bool { return v.CommandedAspect != nil && !v.Pending })
	if err := s.CommandAspect(ctx, signalDispatcher, "s1", "VL"); !errors.Is(err, station.ErrInvalidExtendedAccessoryAspect) {
		t.Fatal(err)
	}
	state, _ = s.State("s1")
	if state.DesiredAspect != "S" || state.Pending || len(sim.ExtendedAccessoryCommands()) != 1 {
		t.Fatal(state, sim.ExtendedAccessoryCommands())
	}
}
