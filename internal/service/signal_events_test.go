package service

import (
	"context"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station"
)

func TestSignalEventsDeduplicateStatesWithoutReloadingOnTheirOwnTraffic(t *testing.T) {
	db, sim, bus := signalServiceFixture(t)
	_ = sim.SetTrackPower(context.Background(), true)
	s := NewSignalService(db, sim, bus, nil)
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	awaitSignal(t, s, func(v model.SignalState) bool {
		return v.ReportedAspect != nil && v.CommandedAspect != nil && !v.Pending
	})
	ch, _, cancel := bus.SubscribeTypesWithOverflow(8, "signal.aspect.changed")
	defer cancel()
	status, _ := sim.Status(context.Background())
	code := 0
	for range 100 {
		s.ObserveStatus(status)
		s.observeReport(station.ExtendedAccessoryStateEvent{Address: 1, Aspect: &code, State: station.AccessoryReportKnown, Quality: station.AccessoryReportAssumed, ObservedAt: time.Now()})
		s.reconcile()
	}
	select {
	case e := <-ch:
		t.Fatal("identical state republished", e)
	default:
	}
	if len(sim.ExtendedAccessoryCommands()) != 1 {
		t.Fatal("own traffic caused configuration reload")
	}
	for range 100 {
		bus.Publish("signal.commanded", model.SignalCommanded{SignalID: "other", Aspect: "S"})
	}
	s.Invalidate("test_boundary")
	e := <-ch
	state, ok := e.Payload.(model.SignalState)
	if !ok || state.InhibitedReason != "test_boundary" || state.ReportedAspect != nil {
		t.Fatal(e)
	}
}

func TestSignalModeEventIsPublishedOnlyForPersistedTransitions(t *testing.T) {
	db, sim, bus := signalServiceFixture(t)
	ctx := context.Background()
	d, err := db.Signal(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	d.Mode = model.SignalModeAutomatic
	if err := db.SetSignal(ctx, d); err != nil {
		t.Fatal(err)
	}
	s := NewSignalService(db, sim, bus, nil)
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ch, _, cancel := bus.SubscribeTypesWithOverflow(2, "signal.mode.changed")
	defer cancel()
	if err := s.SetMode(ctx, signalDispatcher, "s1", model.SignalModeManual); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-ch:
		p, ok := e.Payload.(model.SignalModeChanged)
		if !ok || p.SignalID != "s1" || p.Mode != model.SignalModeManual {
			t.Fatal(e)
		}
	default:
		t.Fatal("mode event missing")
	}
	if err := s.SetMode(ctx, signalDispatcher, "s1", model.SignalModeManual); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-ch:
		t.Fatal("idempotent mode change republished", e)
	default:
	}
}
