package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/client"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/service"
)

func newSignalHTTPFixture(t *testing.T) (detailedHTTPFixture, *service.SignalService) {
	t.Helper()
	f := newDetailedHTTPFixture(t)
	ctx := context.Background()
	p := model.SNCFBAL3Profile()
	l := model.LayoutDefinition{TopologyNodes: []model.TopologyNode{{ID: "a", Kind: model.TopologyNodeBoundary}, {ID: "b", Kind: model.TopologyNodeBoundary}}, TrackSections: []model.TrackSection{{ID: "ab", Name: "AB", NodeAID: "a", NodeBID: "b"}}, Blocks: []model.BlockDefinition{{ID: "block-a", Name: "A", TrackSectionIDs: []string{"ab"}}}, SignalProfiles: []model.SignalProfile{p}, Signals: []model.SignalDefinition{{ID: "s1", Name: "Signal", ProfileID: p.ID, Placement: model.SignalPlacement{TrackSectionID: "ab", EntryNodeID: "a"}, ProtectedBlockID: "block-a", Downstream: model.SignalDownstream{Kind: model.SignalDownstreamTerminal, Aspect: "S"}, Endpoint: model.SignalEndpoint{Kind: model.SignalEndpointDCCExtended, LinearAddress: 1, AspectCodes: map[model.SignalAspectID]int{"S": 0, "A": 7, "VL": 16}}}}}
	if err := f.db.ImportLayout(ctx, l, true); err != nil {
		t.Fatal(err)
	}
	independent := false
	if err := f.db.SetOccupancyProvider(ctx, model.OccupancyProvider{ID: "external", Type: "vision", Required: true, TrackPowerDependent: &independent}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.SetOccupancySensorMapping(ctx, model.OccupancySensorMapping{ProviderID: "external", SensorID: "one", BlockID: "block-a"}); err != nil {
		t.Fatal(err)
	}
	runtime := service.NewSignalService(f.db, f.simulator, f.bus, f.occupancy)
	f.control.SetSignalService(runtime)
	f.api.SetSignalService(runtime)
	f.api.transfer.SetSignalService(runtime)
	if err := runtime.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	if err := f.simulator.SetTrackPower(ctx, true); err != nil {
		t.Fatal(err)
	}
	status, err := f.simulator.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	runtime.ObserveStatus(status)
	waitForAPISignal(t, runtime, func(v model.SignalState) bool { return v.CommandedAspect != nil && !v.Pending })
	return f, runtime
}

func waitForAPISignal(t *testing.T, runtime *service.SignalService, condition func(model.SignalState) bool) model.SignalState {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		state, err := runtime.State("s1")
		if err == nil && condition(state) {
			return state
		}
		time.Sleep(time.Millisecond)
	}
	state, _ := runtime.State("s1")
	t.Fatalf("signal state not reached: %+v", state)
	return state
}

func TestSignalRESTAndGoClient(t *testing.T) {
	f, runtime := newSignalHTTPFixture(t)
	ctx := context.Background()
	items, err := f.viewer.Signals(ctx)
	if err != nil || len(items) != 1 || items[0].Definition.ID != "s1" {
		t.Fatal(items, err)
	}
	item, err := f.viewer.Signal(ctx, "s1")
	if err != nil || item.State.DesiredAspect != "S" {
		t.Fatal(item, err)
	}
	profiles, err := f.dispatcher.SignalProfiles(ctx)
	if err != nil || len(profiles) != 1 || profiles[0].ID != model.SignalProfileSNCFBAL3 {
		t.Fatal(profiles, err)
	}
	info, err := f.viewer.SystemInfo(ctx)
	if err != nil || info.APIVersion != "1.19.0" || !info.Signalling.ManualControl || info.Signalling.AutomaticControl {
		t.Fatal(info, err)
	}
	_, err = f.viewer.Signal(ctx, "missing")
	requireClientProblem(t, err, 404, "signal_not_found")
	requireClientProblem(t, f.viewer.SetSignalAspect(ctx, "s1", "S"), 403, "permission_denied")
	requireClientProblem(t, f.sensor.SetSignalMode(ctx, "s1", model.SignalModeManual), 403, "permission_denied")
	_, err = f.sensor.Signals(ctx)
	requireClientProblem(t, err, 403, "permission_denied")
	requireClientProblem(t, f.dispatcher.SetSignalAspect(ctx, "s1", "invalid"), 400, "validation_failed")
	requireClientProblem(t, f.dispatcher.SetSignalAspect(ctx, "s1", "VL"), 409, "signal_unsafe")
	if err := f.occupancy.Observe(ctx, model.OccupancyObservation{ProviderID: "external", SensorID: "one", State: model.OccupancyFree, Sequence: 1, ObservedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := f.dispatcher.SetSignalAspect(ctx, "s1", "VL"); err != nil {
		t.Fatal(err)
	}
	item, err = f.viewer.Signal(ctx, "s1")
	if err != nil || item.State.CommandedAspect == nil || *item.State.CommandedAspect != "VL" {
		t.Fatal(item, err)
	}
	requireClientProblem(t, f.dispatcher.SetSignalMode(ctx, "s1", model.SignalModeAutomatic), 409, "signal_automatic_unavailable")
	if err := f.administrator.SetSignalAspect(ctx, "s1", "S"); err != nil {
		t.Fatal(err)
	}
	if err := f.administrator.SetSignalMode(ctx, "s1", model.SignalModeManual); err != nil {
		t.Fatal(err)
	}
	if err := f.administrator.SetTrackPower(ctx, false); err != nil {
		t.Fatal(err)
	}
	requireClientProblem(t, f.dispatcher.SetSignalAspect(ctx, "s1", "VL"), 503, "signal_unavailable")
	state, _ := runtime.State("s1")
	if state.ReportedAspect != nil {
		t.Fatal(state)
	}
	_, err = f.dispatcher.Do(ctx, http.MethodPut, "/api/v1/signals/s1/aspect", map[string]any{"aspect": "S", "unknown": true}, nil)
	requireClientProblem(t, err, 400, "invalid_json")
	_, err = client.New(f.server.URL).Signals(ctx)
	requireClientProblem(t, err, 401, "missing_token")
}

func TestSignalRESTEmptyListsAndReadWithoutController(t *testing.T) {
	f := newDetailedHTTPFixture(t)
	ctx := context.Background()
	items, err := f.viewer.Signals(ctx)
	if err != nil || items == nil || len(items) != 0 {
		t.Fatal(items, err)
	}
	profiles, err := f.viewer.SignalProfiles(ctx)
	if err != nil || profiles == nil || len(profiles) != 0 {
		t.Fatal(profiles, err)
	}
	requireClientProblem(t, f.dispatcher.SetSignalAspect(ctx, "missing", "S"), 503, "signal_unavailable")
}

func TestSignalRESTImportResetsRuntimeBeforeAcknowledgement(t *testing.T) {
	f, runtime := newSignalHTTPFixture(t)
	ctx := context.Background()
	archive, err := f.administrator.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.occupancy.Observe(ctx, model.OccupancyObservation{ProviderID: "external", SensorID: "one", State: model.OccupancyFree, Sequence: 1, ObservedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := f.dispatcher.SetSignalAspect(ctx, "s1", "VL"); err != nil {
		t.Fatal(err)
	}
	if err := f.administrator.ImportLayout(ctx, archive, true); err != nil {
		t.Fatal(err)
	}
	item, err := f.viewer.Signal(ctx, "s1")
	if err != nil || item.State.DesiredAspect != "S" {
		t.Fatal(item, err)
	}
	waitForAPISignal(t, runtime, func(v model.SignalState) bool { return v.CommandedAspect != nil && *v.CommandedAspect == "S" })
}
