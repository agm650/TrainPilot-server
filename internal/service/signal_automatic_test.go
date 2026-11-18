package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station/simulator"
	"github.com/agm650/TrainPilot-server/internal/store"
)

func automaticSignalFixture(t *testing.T, cycle bool) (*SignalService, *OccupancyService, *simulator.Simulator, *events.Bus) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	layout := model.LayoutDefinition{SignalProfiles: []model.SignalProfile{model.SNCFBAL3Profile()}}
	for i := 0; i < 4; i++ {
		kind := model.TopologyNodeJoint
		if !cycle && (i == 0 || i == 3) {
			kind = model.TopologyNodeBoundary
		}
		if cycle && i == 3 {
			break
		}
		layout.TopologyNodes = append(layout.TopologyNodes, model.TopologyNode{ID: fmt.Sprint("n", i), Kind: kind})
	}
	independent := false
	layout.OccupancyProviders = []model.OccupancyProvider{{ID: "external", Type: "vision", Required: true, TrackPowerDependent: &independent}}
	for i, id := range []string{"a", "b", "c"} {
		end := i + 1
		if cycle {
			end %= 3
		}
		layout.TrackSections = append(layout.TrackSections, model.TrackSection{ID: id, Name: id, NodeAID: fmt.Sprint("n", i), NodeBID: fmt.Sprint("n", end)})
		layout.Blocks = append(layout.Blocks, model.BlockDefinition{ID: id, Name: id, TrackSectionIDs: []string{id}})
		layout.OccupancySensorMappings = append(layout.OccupancySensorMappings, model.OccupancySensorMapping{ProviderID: "external", SensorID: id, BlockID: id})
		down := model.SignalDownstream{Kind: model.SignalDownstreamTerminal, Aspect: "S"}
		if i < 2 || cycle {
			down = model.SignalDownstream{Kind: model.SignalDownstreamSignal, SignalID: []string{"a", "b", "c"}[end]}
		}
		layout.Signals = append(layout.Signals, model.SignalDefinition{ID: id, Name: id, ProfileID: model.SignalProfileSNCFBAL3, Mode: model.SignalModeAutomatic, Placement: model.SignalPlacement{TrackSectionID: id, EntryNodeID: fmt.Sprint("n", i)}, ProtectedBlockID: id, Downstream: down, Endpoint: model.SignalEndpoint{Kind: model.SignalEndpointDCCExtended, LinearAddress: i + 1, AspectCodes: map[model.SignalAspectID]int{"S": 0, "A": 7, "VL": 16}}})
	}
	if err := db.ImportLayout(ctx, layout, true); err != nil {
		t.Fatal(err)
	}
	bus := events.New()
	occupancy := NewOccupancyService(db, bus, nil)
	sim := simulator.New()
	_ = sim.Connect(ctx)
	t.Cleanup(func() { _ = sim.Close() })
	_ = sim.SetTrackPower(ctx, true)
	s := NewSignalService(db, sim, bus, occupancy)
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	awaitAutomaticSignals(t, s, map[string]model.SignalAspectID{"a": "S", "b": "S", "c": "S"})
	return s, occupancy, sim, bus
}

func awaitAutomaticSignals(t *testing.T, s *SignalService, want map[string]model.SignalAspectID) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		matched := true
		for _, v := range s.Snapshot() {
			if v.Pending || v.CommandedAspect == nil || *v.CommandedAspect != want[v.ID] || v.DesiredAspect != want[v.ID] {
				matched = false
				break
			}
		}
		if matched {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("automatic signals did not converge: %+v", s.Snapshot())
}

func observeAutomaticBlocks(t *testing.T, o *OccupancyService, sequence uint64, states ...model.OccupancyState) {
	t.Helper()
	observations := make([]model.OccupancyObservation, 0, len(states))
	for i, state := range states {
		id := []string{"a", "b", "c"}[i]
		observations = append(observations, model.OccupancyObservation{ProviderID: "external", SensorID: id, State: state, Sequence: sequence, ObservedAt: time.Now()})
	}
	if err := o.ObserveBatch(context.Background(), observations); err != nil {
		t.Fatal(err)
	}
}

func TestAutomaticSignalPropagationAndAtomicTrainTransfer(t *testing.T) {
	s, o, sim, _ := automaticSignalFixture(t, false)
	observeAutomaticBlocks(t, o, 1, model.OccupancyOccupied, model.OccupancyFree, model.OccupancyFree)
	awaitAutomaticSignals(t, s, map[string]model.SignalAspectID{"a": "S", "b": "VL", "c": "A"})
	start := len(sim.ExtendedAccessoryCommands())
	observeAutomaticBlocks(t, o, 2, model.OccupancyFree, model.OccupancyOccupied, model.OccupancyFree)
	awaitAutomaticSignals(t, s, map[string]model.SignalAspectID{"a": "A", "b": "S", "c": "A"})
	for _, record := range sim.ExtendedAccessoryCommands()[start:] {
		if record.Command.Address == 1 && record.Command.Aspect == 16 {
			t.Fatal("transient opening during atomic transfer", record)
		}
	}
	states := s.Snapshot()
	if len(states) != 3 || states[0].CalculationRevision == 0 || states[0].OccupancyRevision == 0 {
		t.Fatal(states)
	}
}

func TestAutomaticSignalCycleConvergesWithoutRepeatedCommands(t *testing.T) {
	s, o, sim, bus := automaticSignalFixture(t, true)
	observeAutomaticBlocks(t, o, 1, model.OccupancyFree, model.OccupancyFree, model.OccupancyFree)
	want := map[string]model.SignalAspectID{"a": "VL", "b": "VL", "c": "VL"}
	awaitAutomaticSignals(t, s, want)
	// Wait for the calculation loop to consume the final transport observation.
	s.reconcile()
	before := len(sim.ExtendedAccessoryCommands())
	for range 20 {
		bus.Publish("block.occupancy.changed", model.BlockOccupancyChanged{BlockID: "a", State: model.OccupancyFree})
	}
	s.reconcile()
	if len(sim.ExtendedAccessoryCommands()) != before {
		t.Fatal("identical calculation emitted commands")
	}
	// Enough matching events to overflow the service's bounded subscription.
	for range 2000 {
		bus.Publish("block.occupancy.changed", nil)
	}
	deadline := time.Now().Add(4 * time.Second)
	for len(sim.ExtendedAccessoryCommands()) == before && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(sim.ExtendedAccessoryCommands()) == before {
		t.Fatal("overflow did not trigger resynchronization")
	}
	awaitAutomaticSignals(t, s, want)
	if snapshot := o.Snapshot(); snapshot.Blocks["a"].State != model.OccupancyFree || snapshot.Blocks["b"].State != model.OccupancyFree {
		t.Fatal(snapshot)
	}
}
