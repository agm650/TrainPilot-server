package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/service"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/station/simulator"
)

func TestSignalWebSocketChangesFailureAndReconnectSnapshot(t *testing.T) {
	f, runtime := newSignalHTTPFixture(t)
	ctx := context.Background()
	if err := f.occupancy.Observe(ctx, model.OccupancyObservation{ProviderID: "external", SensorID: "one", State: model.OccupancyFree, Sequence: 1, ObservedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	ws := dialTestWebSocket(t, f.server.URL, f.viewer.AccessToken)
	defer ws.close()
	snapshot := readTestSnapshot(t, ws)
	if len(snapshot.Payload.Signals) != 1 || len(snapshot.Payload.SignalProfiles) != 1 {
		t.Fatal(snapshot)
	}
	if err := f.dispatcher.SetSignalAspect(ctx, "s1", "VL"); err != nil {
		t.Fatal(err)
	}
	last := snapshot.Sequence
	commanded, changed := false, false
	for !(commanded && changed) {
		var e struct {
			Type     string          `json:"type"`
			Sequence uint64          `json:"sequence"`
			Payload  json.RawMessage `json:"payload"`
		}
		ws.readJSON(t, &e)
		if e.Sequence <= last {
			t.Fatal("out of order event", e)
		}
		last = e.Sequence
		if e.Type == "signal.commanded" {
			var p model.SignalCommanded
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				t.Fatal(err)
			}
			commanded = p.Aspect == "VL"
		}
		if e.Type == "signal.aspect.changed" {
			var p model.SignalState
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				t.Fatal(err)
			}
			changed = p.CommandedAspect != nil && *p.CommandedAspect == "VL" && !p.Pending
		}
	}
	if err := f.simulator.SetOperationFault(simulator.OpExtendedAccessory, simulator.OperationFault{Error: errors.New("secret transport details"), Remaining: 1}); err != nil {
		t.Fatal(err)
	}
	if err := f.dispatcher.SetSignalAspect(ctx, "s1", "S"); err == nil {
		t.Fatal("injected failure reported success")
	}
	failed, unknown := false, false
	for !(failed && unknown) {
		var e struct {
			Type     string          `json:"type"`
			Sequence uint64          `json:"sequence"`
			Payload  json.RawMessage `json:"payload"`
		}
		ws.readJSON(t, &e)
		if e.Sequence <= last || strings.Contains(string(e.Payload), "secret") {
			t.Fatal(e)
		}
		last = e.Sequence
		if e.Type == "signal.command.failed" {
			failed = true
		}
		if e.Type == "signal.aspect.changed" {
			var p model.SignalState
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				t.Fatal(err)
			}
			unknown = p.Error != nil && !p.Pending && p.CommandedAspect == nil && p.ReportedAspect == nil
		}
	}
	reconnected := dialTestWebSocket(t, f.server.URL, f.viewer.AccessToken)
	defer reconnected.close()
	fresh := readTestSnapshot(t, reconnected)
	if len(fresh.Payload.Signals) != 1 || fresh.Payload.Signals[0].State.Error == nil || fresh.Payload.Signals[0].State.ReportedAspect != nil || fresh.Sequence < last {
		t.Fatal(fresh)
	}
	state, _ := runtime.State("s1")
	if fresh.Payload.Signals[0].State.DesiredAspect != state.DesiredAspect {
		t.Fatal("snapshot differs from service")
	}
	// A legacy client ignores the added fields using ordinary JSON decoding.
	data, _ := json.Marshal(fresh)
	var legacy struct {
		Type    string `json:"type"`
		Payload struct {
			Blocks []model.Block `json:"blocks"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil || legacy.Type != "system.snapshot" || len(legacy.Payload.Blocks) != 1 {
		t.Fatal(legacy, err)
	}
}

func TestSignalWebSocketEmptyArrays(t *testing.T) {
	f := newWebsocketFixture(t)
	ws := dialTestWebSocket(t, f.server.URL, f.accessToken)
	defer ws.close()
	snapshot := readTestSnapshot(t, ws)
	if snapshot.Payload.Signals == nil || snapshot.Payload.SignalProfiles == nil || len(snapshot.Payload.Signals) != 0 {
		t.Fatal(snapshot)
	}
}

func TestSignalWebSocketOverflowRecoversCurrentState(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	f := newWebsocketFixtureWithStation(t, 15*time.Minute, func(sim *simulator.Simulator) station.CommandStation {
		return &snapshotBlockingStation{Simulator: sim, entered: entered, release: release}
	})
	f.api.eventBuffer = 1
	// This read-only registry needs no station probe or client to own its state.
	runtime := service.NewSignalService(f.api.store, f.api.station, f.bus, f.railway.OccupancyService())
	profile := model.SNCFBAL3Profile()
	if err := f.api.store.SetSignalProfile(context.Background(), profile); err != nil {
		t.Fatal(err)
	}
	// Reuse the already validated one-section layout fixture.
	layout := model.LayoutDefinition{TopologyNodes: []model.TopologyNode{{ID: "a", Kind: model.TopologyNodeBoundary}, {ID: "b", Kind: model.TopologyNodeBoundary}}, TrackSections: []model.TrackSection{{ID: "ab", Name: "AB", NodeAID: "a", NodeBID: "b"}}, Blocks: []model.BlockDefinition{{ID: "block-a", Name: "A", TrackSectionIDs: []string{"ab"}}}, SignalProfiles: []model.SignalProfile{profile}, Signals: []model.SignalDefinition{{ID: "s1", Name: "Signal", ProfileID: profile.ID, Placement: model.SignalPlacement{TrackSectionID: "ab", EntryNodeID: "a"}, ProtectedBlockID: "block-a", Downstream: model.SignalDownstream{Kind: model.SignalDownstreamTerminal, Aspect: "S"}, Endpoint: model.SignalEndpoint{Kind: model.SignalEndpointDCCExtended, LinearAddress: 1, AspectCodes: map[model.SignalAspectID]int{"S": 0, "A": 7, "VL": 16}}}}}
	if err := f.api.store.ImportLayout(context.Background(), layout, true); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.api.SetSignalService(runtime)
	ws := dialTestWebSocket(t, f.server.URL, f.accessToken)
	defer ws.close()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("snapshot did not block")
	}
	runtime.Invalidate("first")
	runtime.Invalidate("latest")
	close(release)
	initial := readTestSnapshot(t, ws)
	var gap events.Event
	ws.readJSON(t, &gap)
	if gap.Type != "signal.aspect.changed" || gap.Sequence <= initial.Sequence+1 {
		t.Fatal(initial.Sequence, gap)
	}
	ws.writeJSON(t, map[string]any{"type": "client.snapshot_request"})
	fresh := readTestSnapshot(t, ws)
	if len(fresh.Payload.Signals) != 1 || fresh.Payload.Signals[0].State.InhibitedReason != "latest" || fresh.Sequence < gap.Sequence {
		t.Fatal(fresh)
	}
}
