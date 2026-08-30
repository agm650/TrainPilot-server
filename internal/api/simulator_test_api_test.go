package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/auth"
	"github.com/agm650/TrainPilot-server/internal/client"
	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/service"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/station/simulator"
	simscenario "github.com/agm650/TrainPilot-server/internal/station/simulator/scenario"
	"github.com/agm650/TrainPilot-server/internal/store"
	"github.com/agm650/TrainPilot-server/internal/transfer"
)

type simulatorAPIFixture struct {
	server *httptest.Server
	api    *Server
	sim    *simulator.Simulator
	bus    *events.Bus
	client *client.Client
	token  string
}

func newSimulatorAPIFixture(t *testing.T) simulatorAPIFixture {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.SeedDemo(ctx); err != nil {
		t.Fatal(err)
	}

	realClock := clock.Real{}
	users := service.NewUserServiceWithPasswordParams(db, realClock, auth.PasswordParams{Iterations: 100_000, SaltLength: 16, KeyLength: 32})
	if _, err := users.Create(ctx, "dispatcher", "Dispatcher", "correct-horse-1", model.RoleDispatcher, false, false); err != nil {
		t.Fatal(err)
	}
	authService := service.NewAuthService(db, users, realClock, 15*time.Minute, time.Hour)
	sim := simulator.New()
	if err := sim.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	bus := events.New()
	railway := service.NewRailwayService(db, sim, bus)
	railway.StartFeedback(ctx)
	control := service.NewControlService(db, sim, bus, realClock, 15*time.Second, time.Second, time.Hour)
	routes := service.NewRouteService(db, railway, bus)
	apiServer := New(authService, control, railway, routes, transfer.New(db, bus, realClock), db, bus, sim, sim, true)
	httpServer := httptest.NewServer(apiServer.Handler())
	t.Cleanup(httpServer.Close)

	apiClient := client.New(httpServer.URL)
	if _, err := apiClient.Login(ctx, "dispatcher", "correct-horse-1", "simulator-test-api"); err != nil {
		t.Fatal(err)
	}
	return simulatorAPIFixture{server: httpServer, api: apiServer, sim: sim, bus: bus, client: apiClient, token: apiClient.AccessToken}
}

func TestSimulatorTestAPIRoutesAreAbsentUnlessExplicitlyEnabledForSimulator(t *testing.T) {
	routes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/test/v1/simulator/state"},
		{http.MethodPost, "/test/v1/simulator/reset"},
		{http.MethodPut, "/test/v1/simulator/connectivity"},
		{http.MethodPut, "/test/v1/simulator/electrical"},
		{http.MethodPut, "/test/v1/simulator/feedback"},
		{http.MethodPut, "/test/v1/simulator/accessories/1/reported-state"},
		{http.MethodPut, "/test/v1/simulator/accessories/1/behavior"},
		{http.MethodPut, "/test/v1/simulator/faults/status"},
		{http.MethodDelete, "/test/v1/simulator/faults"},
		{http.MethodPost, "/test/v1/simulator/scenarios"},
		{http.MethodPost, "/test/v1/simulator/scenarios/start"},
		{http.MethodPost, "/test/v1/simulator/scenarios/advance"},
		{http.MethodPost, "/test/v1/simulator/scenarios/stop"},
		{http.MethodPost, "/test/v1/simulator/blocks/block-a/occupancy"},
	}

	t.Run("test API disabled", func(t *testing.T) {
		sim := simulator.New()
		server := New(nil, nil, nil, nil, nil, nil, nil, sim, sim, false)
		assertSimulatorRoutesNotFound(t, server.Handler(), routes)
	})

	t.Run("non simulator driver", func(t *testing.T) {
		sim := simulator.New()
		nonSimulator := &testNonSimulatorStation{Simulator: sim}
		server := New(nil, nil, nil, nil, nil, nil, nil, nonSimulator, sim, true)
		assertSimulatorRoutesNotFound(t, server.Handler(), routes)
	})
}

func TestSimulatorTestAPIRequiresAuthentication(t *testing.T) {
	fixture := newSimulatorAPIFixture(t)
	assertStatus(t, fixture.server.URL, http.MethodGet, "/test/v1/simulator/state", "", nil, http.StatusUnauthorized)
}

func TestSimulatorTestAPISnapshotReflectsInjectedStateAndReset(t *testing.T) {
	fixture := newSimulatorAPIFixture(t)
	authorization := "Bearer " + fixture.token
	assertStatus(t, fixture.server.URL, http.MethodPut, "/test/v1/simulator/connectivity", authorization, []byte(`{"connectivity":"degraded"}`), http.StatusNoContent)
	assertStatus(t, fixture.server.URL, http.MethodPut, "/test/v1/simulator/electrical", authorization, []byte(`{"mainCurrentMilliAmps":327,"filteredMainCurrentMilliAmps":300,"temperatureCelsius":42,"supplyVoltageMilliVolts":17950,"trackVoltageMilliVolts":17890,"highTemperature":true}`), http.StatusNoContent)
	assertStatus(t, fixture.server.URL, http.MethodPut, "/test/v1/simulator/feedback", authorization, []byte(`{"source":"simulator","kind":"occupancy","address":12,"active":true,"emit":false}`), http.StatusNoContent)
	assertStatus(t, fixture.server.URL, http.MethodPut, "/test/v1/simulator/accessories/12/behavior", authorization, []byte(`{"mode":"delayed","delay":"500ms"}`), http.StatusNoContent)
	assertStatus(t, fixture.server.URL, http.MethodPut, "/test/v1/simulator/accessories/12/reported-state", authorization, []byte(`{"state":"straight"}`), http.StatusNoContent)
	assertStatus(t, fixture.server.URL, http.MethodPut, "/test/v1/simulator/faults/throttle", authorization, []byte(`{"delay":"500ms","remaining":2,"error":"injected_failure"}`), http.StatusNoContent)

	var state simulatorStateResponse
	requestSimulatorAPI(t, fixture, http.MethodGet, "/test/v1/simulator/state", nil, http.StatusOK, &state)
	if !state.Connected || state.Connectivity != station.Degraded || state.TrackPower || state.EmergencyStop {
		t.Fatalf("control state=%+v", state)
	}
	if state.Electrical.MainCurrentMilliAmps != 327 || state.Electrical.TemperatureCelsius != 42 || !state.Electrical.HighTemperature {
		t.Fatalf("electrical=%+v", state.Electrical)
	}
	if accessory := state.Accessories[12]; accessory.Reported != "straight" {
		t.Fatalf("accessory=%+v", accessory)
	}
	if behavior := state.AccessoryBehaviors[12]; behavior.Mode != simulator.AccessoryBehaviorDelayed || behavior.Delay != "500ms" {
		t.Fatalf("behavior=%+v", behavior)
	}
	if fault := state.Faults["throttle"]; fault.Delay != "500ms" || fault.Remaining != 2 || fault.Error != "injected_failure" {
		t.Fatalf("fault=%+v", fault)
	}
	if len(state.FeedbackStates) != 1 || state.FeedbackStates[0].Address != 12 || !state.FeedbackStates[0].Active {
		t.Fatalf("feedback=%+v", state.FeedbackStates)
	}
	assertStatus(t, fixture.server.URL, http.MethodDelete, "/test/v1/simulator/faults", authorization, nil, http.StatusNoContent)
	var withoutFaults simulatorStateResponse
	requestSimulatorAPI(t, fixture, http.MethodGet, "/test/v1/simulator/state", nil, http.StatusOK, &withoutFaults)
	if len(withoutFaults.Faults) != 0 {
		t.Fatalf("faults after clear=%+v", withoutFaults.Faults)
	}

	assertStatus(t, fixture.server.URL, http.MethodPost, "/test/v1/simulator/reset", authorization, nil, http.StatusNoContent)
	state = simulatorStateResponse{}
	requestSimulatorAPI(t, fixture, http.MethodGet, "/test/v1/simulator/state", nil, http.StatusOK, &state)
	if state.Connectivity != station.Online || len(state.FeedbackStates) != 0 || len(state.Faults) != 0 || len(state.Accessories) != 0 || state.Scenario != nil {
		t.Fatalf("state after reset=%+v", state)
	}
}

func TestSimulatorTestAPIFeedbackFlowsThroughRailwayAndWebSocket(t *testing.T) {
	fixture := newSimulatorAPIFixture(t)
	websocket := dialTestWebSocket(t, fixture.server.URL, fixture.token)
	defer websocket.close()
	readTestSnapshot(t, websocket)

	assertStatus(t, fixture.server.URL, http.MethodPut, "/test/v1/simulator/feedback", "Bearer "+fixture.token, []byte(`{"source":"simulator","kind":"occupancy","address":1,"active":true,"emit":true}`), http.StatusNoContent)
	var event struct {
		Type    string `json:"type"`
		Payload struct {
			BlockID  string `json:"blockId"`
			Occupied bool   `json:"occupied"`
		} `json:"payload"`
	}
	websocket.readJSON(t, &event)
	if event.Type != "block.occupancy.changed" || event.Payload.BlockID != "block-a" || !event.Payload.Occupied {
		t.Fatalf("event=%+v", event)
	}
	blocks, err := fixture.client.Blocks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, block := range blocks {
		if block.ID == "block-a" {
			found = block.Occupied
		}
	}
	if !found {
		t.Fatal("block-a was not occupied through normal feedback processing")
	}
}

func TestSimulatorTestAPIConnectivityUsesNormalSafetyPathWithoutReplay(t *testing.T) {
	fixture := newSimulatorAPIFixture(t)
	ctx := context.Background()
	locomotives, err := fixture.client.Locomotives(ctx)
	if err != nil || len(locomotives) == 0 {
		t.Fatalf("locomotives=%+v err=%v", locomotives, err)
	}
	locomotive := locomotives[0]
	lease, err := fixture.client.Acquire(ctx, locomotive.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.client.SetTrackPower(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := fixture.client.Throttle(ctx, locomotive.ID, lease.ID, 20, station.Forward); err != nil {
		t.Fatal(err)
	}

	assertStatus(t, fixture.server.URL, http.MethodPut, "/test/v1/simulator/connectivity", "Bearer "+fixture.token, []byte(`{"connectivity":"offline"}`), http.StatusNoContent)
	status, err := fixture.client.StationStatus(ctx)
	if err != nil || status.Connectivity != station.Offline {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	err = fixture.client.Throttle(ctx, locomotive.ID, lease.ID, 70, station.Reverse)
	var httpError *client.HTTPError
	if !errors.As(err, &httpError) || httpError.StatusCode != http.StatusServiceUnavailable || httpError.Problem == nil || httpError.Problem.Code != "station_offline" {
		t.Fatalf("offline throttle error=%v", err)
	}
	if loco := fixture.sim.Loco(locomotive.DCCAddress); loco.Speed != 0.2 || loco.Direction != station.Forward {
		t.Fatalf("failed command changed simulator state: %+v", loco)
	}

	assertStatus(t, fixture.server.URL, http.MethodPut, "/test/v1/simulator/connectivity", "Bearer "+fixture.token, []byte(`{"connectivity":"online"}`), http.StatusNoContent)
	if loco := fixture.sim.Loco(locomotive.DCCAddress); loco.Speed != 0.2 || loco.Direction != station.Forward {
		t.Fatalf("offline command was replayed after recovery: %+v", loco)
	}
	if err := fixture.client.Throttle(ctx, locomotive.ID, lease.ID, 40, station.Reverse); err != nil {
		t.Fatal(err)
	}
	if loco := fixture.sim.Loco(locomotive.DCCAddress); loco.Speed != 0.4 || loco.Direction != station.Reverse {
		t.Fatalf("new command after recovery=%+v", loco)
	}
}

func TestSimulatorTestAPIControlsManualScenario(t *testing.T) {
	fixture := newSimulatorAPIFixture(t)
	scenarioJSON := []byte(`{
  "version":1,
  "name":"api-connectivity",
  "initial":{"connectivity":"online"},
  "steps":[
    {"at":"5s","action":"station.connectivity","connectivity":"degraded"},
    {"at":"10s","action":"station.connectivity","connectivity":"offline"}
  ]
}`)
	var scenarioState simulatorScenarioState
	requestSimulatorAPI(t, fixture, http.MethodPost, "/test/v1/simulator/scenarios", scenarioJSON, http.StatusCreated, &scenarioState)
	if scenarioState.State != simscenario.StateLoaded || scenarioState.Elapsed != "0s" {
		t.Fatalf("loaded scenario=%+v", scenarioState)
	}
	requestSimulatorAPI(t, fixture, http.MethodPost, "/test/v1/simulator/scenarios/start", nil, http.StatusOK, &scenarioState)
	if scenarioState.State != simscenario.StateRunning {
		t.Fatalf("started scenario=%+v", scenarioState)
	}
	requestSimulatorAPI(t, fixture, http.MethodPost, "/test/v1/simulator/scenarios/advance", []byte(`{"duration":"4s"}`), http.StatusOK, &scenarioState)
	if status, err := fixture.client.StationStatus(context.Background()); err != nil || status.Connectivity != station.Online {
		t.Fatalf("before first step status=%+v err=%v", status, err)
	}
	requestSimulatorAPI(t, fixture, http.MethodPost, "/test/v1/simulator/scenarios/advance", []byte(`{"duration":"1s"}`), http.StatusOK, &scenarioState)
	if status, err := fixture.client.StationStatus(context.Background()); err != nil || status.Connectivity != station.Degraded {
		t.Fatalf("degraded status=%+v err=%v", status, err)
	}
	requestSimulatorAPI(t, fixture, http.MethodPost, "/test/v1/simulator/scenarios/advance", []byte(`{"duration":"5s"}`), http.StatusOK, &scenarioState)
	if scenarioState.State != simscenario.StateCompleted || scenarioState.Elapsed != "10s" || scenarioState.NextStep != 2 {
		t.Fatalf("completed scenario=%+v", scenarioState)
	}
	if status, err := fixture.client.StationStatus(context.Background()); err != nil || status.Connectivity != station.Offline {
		t.Fatalf("offline status=%+v err=%v", status, err)
	}

	var state simulatorStateResponse
	requestSimulatorAPI(t, fixture, http.MethodGet, "/test/v1/simulator/state", nil, http.StatusOK, &state)
	if state.Scenario == nil || state.Scenario.State != simscenario.StateCompleted {
		t.Fatalf("state scenario=%+v", state.Scenario)
	}

	stopScenario := []byte(`{"version":1,"name":"stop","initial":{},"steps":[{"at":"1h","action":"fault.clear"}]}`)
	requestSimulatorAPI(t, fixture, http.MethodPost, "/test/v1/simulator/scenarios", stopScenario, http.StatusCreated, &scenarioState)
	requestSimulatorAPI(t, fixture, http.MethodPost, "/test/v1/simulator/scenarios/start", nil, http.StatusOK, &scenarioState)
	requestSimulatorAPI(t, fixture, http.MethodPost, "/test/v1/simulator/scenarios/stop", nil, http.StatusOK, &scenarioState)
	if scenarioState.State != simscenario.StateStopped || scenarioState.NextStep != 0 {
		t.Fatalf("stopped scenario=%+v", scenarioState)
	}
}

func TestSimulatorTestAPIRejectsInvalidJSONAndValues(t *testing.T) {
	fixture := newSimulatorAPIFixture(t)
	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"unknown field", http.MethodPut, "/test/v1/simulator/connectivity", `{"connectivity":"online","extra":true}`},
		{"invalid connectivity", http.MethodPut, "/test/v1/simulator/connectivity", `{"connectivity":"lost"}`},
		{"negative feedback address", http.MethodPut, "/test/v1/simulator/feedback", `{"source":"simulator","kind":"occupancy","address":-1,"active":true,"emit":true}`},
		{"negative path address", http.MethodPut, "/test/v1/simulator/accessories/-1/reported-state", `{"state":"straight"}`},
		{"invalid behavior duration", http.MethodPut, "/test/v1/simulator/accessories/1/behavior", `{"mode":"delayed","delay":"later"}`},
		{"invalid fault duration", http.MethodPut, "/test/v1/simulator/faults/status", `{"delay":"later"}`},
		{"unknown operation", http.MethodPut, "/test/v1/simulator/faults/unknown", `{"error":"failure"}`},
		{"invalid scenario", http.MethodPost, "/test/v1/simulator/scenarios", `{"version":1,"name":"bad","initial":{},"steps":[{"at":"later","action":"fault.clear"}]}`},
		{"invalid advance duration", http.MethodPost, "/test/v1/simulator/scenarios/advance", `{"duration":"later"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req, err := http.NewRequest(test.method, fixture.server.URL+test.path, strings.NewReader(test.body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+fixture.token)
			req.Header.Set("Content-Type", "application/json")
			response, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusBadRequest || response.Header.Get("Content-Type") != "application/problem+json" {
				t.Fatalf("status=%d content-type=%q", response.StatusCode, response.Header.Get("Content-Type"))
			}
			var got problem
			if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
			if got.Category != "validation" || got.Code == "" {
				t.Fatalf("problem=%+v", got)
			}
		})
	}
}

func TestSimulatorTestAPIIsRaceSafeWithBusinessReads(t *testing.T) {
	fixture := newSimulatorAPIFixture(t)
	var wg sync.WaitGroup
	errorsFound := make(chan error, 3)
	wg.Add(3)
	go func() {
		defer wg.Done()
		for index := 0; index < 100; index++ {
			body := fmt.Sprintf(`{"temperatureCelsius":%d,"supplyVoltageMilliVolts":18000}`, 20+index%30)
			if status, err := rawSimulatorAPIRequest(fixture.server.URL, fixture.token, http.MethodPut, "/test/v1/simulator/electrical", []byte(body), nil); err != nil || status != http.StatusNoContent {
				errorsFound <- fmt.Errorf("electrical status=%d err=%v", status, err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for index := 0; index < 100; index++ {
			if _, err := fixture.client.StationStatus(context.Background()); err != nil {
				errorsFound <- fmt.Errorf("station status: %w", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for index := 0; index < 100; index++ {
			if status, err := rawSimulatorAPIRequest(fixture.server.URL, fixture.token, http.MethodGet, "/test/v1/simulator/state", nil, nil); err != nil || status != http.StatusOK {
				errorsFound <- fmt.Errorf("simulator state status=%d err=%v", status, err)
				return
			}
		}
	}()
	wg.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Error(err)
	}
}

type testNonSimulatorStation struct {
	*simulator.Simulator
}

func (s *testNonSimulatorStation) Capabilities() station.Capabilities {
	capabilities := s.Simulator.Capabilities()
	capabilities.Driver = "z21"
	return capabilities
}

func assertSimulatorRoutesNotFound(t *testing.T, handler http.Handler, routes []struct {
	method string
	path   string
}) {
	t.Helper()
	for _, route := range routes {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(route.method, route.path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Errorf("%s %s status=%d want 404", route.method, route.path, recorder.Code)
		}
	}
}

func requestSimulatorAPI(t *testing.T, fixture simulatorAPIFixture, method, path string, body []byte, wantStatus int, target any) {
	t.Helper()
	status, err := rawSimulatorAPIRequest(fixture.server.URL, fixture.token, method, path, body, target)
	if err != nil {
		t.Fatal(err)
	}
	if status != wantStatus {
		t.Fatalf("%s %s status=%d want=%d", method, path, status, wantStatus)
	}
}

func rawSimulatorAPIRequest(baseURL, token, method, path string, body []byte, target any) (int, error) {
	request, err := http.NewRequest(method, baseURL+path, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if target != nil {
		if err := json.NewDecoder(response.Body).Decode(target); err != nil {
			return response.StatusCode, err
		}
	}
	return response.StatusCode, nil
}
