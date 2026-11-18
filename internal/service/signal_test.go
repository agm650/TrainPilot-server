package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/station/simulator"
	"github.com/agm650/TrainPilot-server/internal/store"
)

func signalServiceFixture(t *testing.T) (*store.Store, *simulator.Simulator, *events.Bus) {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	layout := model.LayoutDefinition{
		TopologyNodes:  []model.TopologyNode{{ID: "a", Kind: model.TopologyNodeBoundary}, {ID: "b", Kind: model.TopologyNodeBoundary}},
		TrackSections:  []model.TrackSection{{ID: "ab", Name: "AB", NodeAID: "a", NodeBID: "b"}},
		Blocks:         []model.BlockDefinition{{ID: "block", Name: "Block", TrackSectionIDs: []string{"ab"}}},
		SignalProfiles: []model.SignalProfile{model.SNCFBAL3Profile()},
		Signals:        []model.SignalDefinition{{ID: "s1", Name: "Signal", ProfileID: model.SignalProfileSNCFBAL3, Placement: model.SignalPlacement{TrackSectionID: "ab", EntryNodeID: "a"}, ProtectedBlockID: "block", Downstream: model.SignalDownstream{Kind: model.SignalDownstreamTerminal, Aspect: "S"}, Endpoint: model.SignalEndpoint{Kind: model.SignalEndpointDCCExtended, LinearAddress: 1, AspectCodes: map[model.SignalAspectID]int{"S": 0, "A": 7, "VL": 16}}}},
	}
	if err := db.ImportLayout(context.Background(), layout, true); err != nil {
		t.Fatal(err)
	}
	sim := simulator.New()
	if err := sim.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sim.Close() })
	return db, sim, events.New()
}

func awaitSignal(t *testing.T, s *SignalService, condition func(model.SignalState) bool) model.SignalState {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		state, err := s.State("s1")
		if err == nil && condition(state) {
			return state
		}
		time.Sleep(time.Millisecond)
	}
	state, _ := s.State("s1")
	t.Fatalf("signal condition not reached: %+v", state)
	return state
}

func TestSignalServiceStartupRecoveryImportAndRestart(t *testing.T) {
	db, sim, bus := signalServiceFixture(t)
	s := NewSignalService(db, sim, bus, nil)
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Power is initially off: no command, even without occupancy information.
	if state := awaitSignal(t, s, func(v model.SignalState) bool { return v.InhibitedReason != "" }); state.CommandedAspect != nil {
		t.Fatal(state)
	}
	_ = sim.SetTrackPower(context.Background(), true)
	s.ObserveStatus(station.Status{Connectivity: station.Online, TrackPower: "on"})
	state := awaitSignal(t, s, func(v model.SignalState) bool { return v.CommandedAspect != nil && v.ReportedAspect != nil })
	if *state.CommandedAspect != "S" || *state.ReportQuality != station.AccessoryReportAssumed || state.Synchronized {
		t.Fatal(state)
	}
	*state.CommandedAspect = "VL"
	*state.ReportQuality = station.AccessoryReportPhysical
	current, _ := s.State("s1")
	if *current.CommandedAspect != "S" || *current.ReportQuality != station.AccessoryReportAssumed {
		t.Fatal("snapshot aliases state")
	}
	s.ObserveStatus(station.Status{Connectivity: station.Degraded, TrackPower: "on"})
	current, _ = s.State("s1")
	if current.CommandedAspect != nil || current.ReportedAspect != nil {
		t.Fatal("degraded retained state", current)
	}
	s.ObserveStatus(station.Status{Connectivity: station.Online, TrackPower: "on"})
	awaitSignal(t, s, func(v model.SignalState) bool { return v.CommandedAspect != nil })
	if len(sim.ExtendedAccessoryCommands()) != 2 {
		t.Fatal(sim.ExtendedAccessoryCommands())
	}
	bus.Publish("layout.imported", nil)
	deadline := time.Now().Add(3 * time.Second)
	for len(sim.ExtendedAccessoryCommands()) < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(sim.ExtendedAccessoryCommands()) != 3 {
		t.Fatal("import did not resend fallback")
	}
	s.Close()
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, s, func(v model.SignalState) bool { return v.CommandedAspect != nil })
	if len(sim.ExtendedAccessoryCommands()) != 4 {
		t.Fatal("restart did not resend fallback")
	}
}

type signalTestDriver struct {
	*simulator.Simulator
	unsupported bool
	fail        bool
	entered     chan struct{}
}

func (d *signalTestDriver) Capabilities() station.Capabilities {
	c := d.Simulator.Capabilities()
	c.ExtendedAccessoryControl = !d.unsupported
	return c
}
func (d *signalTestDriver) SetExtendedAccessory(ctx context.Context, c station.ExtendedAccessoryCommand) error {
	if d.entered != nil {
		select {
		case d.entered <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return ctx.Err()
	}
	if d.fail {
		return errors.New("transport failure")
	}
	return d.Simulator.SetExtendedAccessory(ctx, c)
}

func TestSignalServiceFailureUnsupportedCancellationAndConcurrency(t *testing.T) {
	for _, kind := range []string{"failure", "unsupported", "cancellation"} {
		t.Run(kind, func(t *testing.T) {
			db, sim, bus := signalServiceFixture(t)
			_ = sim.SetTrackPower(context.Background(), true)
			d := &signalTestDriver{Simulator: sim, unsupported: kind == "unsupported", fail: kind == "failure"}
			if kind == "cancellation" {
				d.entered = make(chan struct{}, 1)
			}
			s := NewSignalService(db, d, bus, nil)
			if err := s.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if kind == "cancellation" {
				select {
				case <-d.entered:
				case <-time.After(3 * time.Second):
					t.Fatal("command not started")
				}
				var wg sync.WaitGroup
				for range 8 {
					wg.Add(1)
					go func() {
						defer wg.Done()
						for range 50 {
							_ = s.Snapshot()
							_, _ = s.State("s1")
						}
					}()
				}
				s.ObserveStatus(station.Status{Connectivity: station.Offline, TrackPower: "unknown"})
				wg.Wait()
				s.Close()
			} else if kind == "failure" {
				awaitSignal(t, s, func(v model.SignalState) bool { return v.Error != nil })
				code := 16
				s.observeReport(station.ExtendedAccessoryStateEvent{Address: 1, Aspect: &code, State: station.AccessoryReportKnown, Quality: station.AccessoryReportPhysical, ObservedAt: time.Now()})
			} else {
				awaitSignal(t, s, func(v model.SignalState) bool { return v.InhibitedReason == "unsupported" })
			}
			state, _ := s.State("s1")
			if state.Pending || state.CommandedAspect != nil || state.ReportedAspect != nil || state.ReportQuality != nil {
				t.Fatal("false confirmation", state)
			}
		})
	}
}

func TestSignalServiceEmptyAndAutomaticDefinitions(t *testing.T) {
	db, sim, bus := signalServiceFixture(t)
	d, err := db.Signal(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	d.Mode = model.SignalModeAutomatic
	if err := db.SetSignal(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	_ = sim.SetTrackPower(context.Background(), true)
	s := NewSignalService(db, sim, bus, nil)
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	state := awaitSignal(t, s, func(v model.SignalState) bool { return v.CommandedAspect != nil })
	if state.InhibitedReason != "automatic_engine_unavailable" || *state.CommandedAspect != "S" {
		t.Fatal(state)
	}
	if err := db.ReplaceSignalDefinitions(context.Background(), nil, nil, true); err != nil {
		t.Fatal(err)
	}
	if err := s.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if result := s.Snapshot(); result == nil || len(result) != 0 {
		t.Fatal(result)
	}
	if _, err := s.State("missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
}
