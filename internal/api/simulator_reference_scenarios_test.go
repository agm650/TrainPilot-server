package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/client"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station"
	simscenario "github.com/agm650/TrainPilot-server/internal/station/simulator/scenario"
)

var referenceSimulatorScenarios = []string{
	"nominal-driving",
	"emergency-stop",
	"station-degraded-recovery",
	"station-offline-recovery",
	"electrical-short-circuit",
	"feedback-single-block",
	"feedback-multiple-blocks",
	"feedback-bounce",
	"feedback-event-loss",
	"accessory-confirmation-success",
	"accessory-confirmation-timeout-base",
	"accessory-wrong-confirmation",
}

func TestReferenceSimulatorScenarios(t *testing.T) {
	t.Run("all definitions are versioned and valid", func(t *testing.T) {
		for _, name := range referenceSimulatorScenarios {
			definition, err := simscenario.LoadFile(referenceSimulatorScenarioPath(name))
			if err != nil {
				t.Fatalf("load %s: %v", name, err)
			}
			if definition.Version != simscenario.CurrentVersion || definition.Name != name {
				t.Fatalf("scenario %s version=%d name=%q", name, definition.Version, definition.Name)
			}
		}
	})

	t.Run("nominal driving", testReferenceNominalDriving)
	t.Run("emergency stop", testReferenceEmergencyStop)
	t.Run("station degraded recovery", testReferenceStationDegradedRecovery)
	t.Run("station offline recovery without replay", testReferenceStationOfflineRecovery)
	t.Run("electrical short circuit", testReferenceElectricalShortCircuit)
	t.Run("feedback single block", testReferenceFeedbackSingleBlock)
	t.Run("feedback multiple blocks", testReferenceFeedbackMultipleBlocks)
	t.Run("feedback bounce", testReferenceFeedbackBounce)
	t.Run("feedback event loss", testReferenceFeedbackEventLoss)
	t.Run("accessory confirmation success", testReferenceAccessoryConfirmationSuccess)
	t.Run("accessory confirmation timeout base", testReferenceAccessoryConfirmationTimeout)
	t.Run("accessory wrong confirmation", testReferenceAccessoryWrongConfirmation)
}

func testReferenceNominalDriving(t *testing.T) {
	fixture := newSimulatorAPIFixture(t)
	startReferenceSimulatorScenario(t, fixture, "nominal-driving")

	locomotive, lease := acquireReferenceLocomotive(t, fixture)
	ctx := context.Background()
	if err := fixture.client.Throttle(ctx, locomotive.ID, lease.ID, 35, station.Forward); err != nil {
		t.Fatal(err)
	}
	if err := fixture.client.Throttle(ctx, locomotive.ID, lease.ID, 0, station.Forward); err != nil {
		t.Fatal(err)
	}
	if err := fixture.client.Throttle(ctx, locomotive.ID, lease.ID, 40, station.Reverse); err != nil {
		t.Fatal(err)
	}
	if err := fixture.client.Function(ctx, locomotive.ID, lease.ID, 0, true); err != nil {
		t.Fatal(err)
	}
	if err := fixture.client.Throttle(ctx, locomotive.ID, lease.ID, 0, station.Reverse); err != nil {
		t.Fatal(err)
	}
	if err := fixture.client.Release(ctx, lease.ID); err != nil {
		t.Fatal(err)
	}

	loco := fixture.sim.Loco(locomotive.DCCAddress)
	if loco.Speed != 0 || loco.Direction != station.Reverse || !loco.Functions[0] {
		t.Fatalf("final locomotive state=%+v", loco)
	}
}

func testReferenceEmergencyStop(t *testing.T) {
	fixture := newSimulatorAPIFixture(t)
	startReferenceSimulatorScenario(t, fixture, "emergency-stop")
	locomotive, lease := acquireReferenceLocomotive(t, fixture)
	ctx := context.Background()
	if err := fixture.client.Throttle(ctx, locomotive.ID, lease.ID, 50, station.Forward); err != nil {
		t.Fatal(err)
	}

	advanceReferenceSimulatorScenario(t, fixture, "1s")
	if loco := fixture.sim.Loco(locomotive.DCCAddress); loco.Speed != 0 {
		t.Fatalf("emergency stop locomotive=%+v", loco)
	}
	status, err := fixture.client.StationStatus(ctx)
	if err != nil || !status.EmergencyStop {
		t.Fatalf("emergency status=%+v err=%v", status, err)
	}
	assertReferenceHTTPProblem(t, fixture.client.Throttle(ctx, locomotive.ID, lease.ID, 20, station.Forward), http.StatusConflict, "emergency_stop_active")

	if err := fixture.client.SetTrackPower(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := fixture.client.Throttle(ctx, locomotive.ID, lease.ID, 20, station.Forward); err != nil {
		t.Fatalf("explicit recovery throttle: %v", err)
	}
}

func testReferenceStationDegradedRecovery(t *testing.T) {
	fixture := newSimulatorAPIFixture(t)
	websocket := dialTestWebSocket(t, fixture.server.URL, fixture.token)
	defer websocket.close()
	readTestSnapshot(t, websocket)
	startReferenceSimulatorScenario(t, fixture, "station-degraded-recovery")
	locomotive, lease := acquireReferenceLocomotive(t, fixture)

	advanceReferenceSimulatorScenario(t, fixture, "1s")
	degradedSequence := readReferenceConnectivityEvent(t, websocket, station.Degraded)
	if err := fixture.client.Throttle(context.Background(), locomotive.ID, lease.ID, 25, station.Forward); err != nil {
		t.Fatalf("degraded throttle: %v", err)
	}

	advanceReferenceSimulatorScenario(t, fixture, "1s")
	onlineSequence := readReferenceConnectivityEvent(t, websocket, station.Online)
	if onlineSequence <= degradedSequence {
		t.Fatalf("connectivity event sequences degraded=%d online=%d", degradedSequence, onlineSequence)
	}
	status, err := fixture.client.StationStatus(context.Background())
	if err != nil || status.Connectivity != station.Online {
		t.Fatalf("recovered status=%+v err=%v", status, err)
	}
}

func testReferenceStationOfflineRecovery(t *testing.T) {
	fixture := newSimulatorAPIFixture(t)
	startReferenceSimulatorScenario(t, fixture, "station-offline-recovery")
	locomotive, lease := acquireReferenceLocomotive(t, fixture)
	ctx := context.Background()
	if err := fixture.client.Throttle(ctx, locomotive.ID, lease.ID, 20, station.Forward); err != nil {
		t.Fatal(err)
	}

	advanceReferenceSimulatorScenario(t, fixture, "1s")
	if err := fixture.client.Throttle(ctx, locomotive.ID, lease.ID, 30, station.Forward); err != nil {
		t.Fatalf("degraded throttle: %v", err)
	}
	advanceReferenceSimulatorScenario(t, fixture, "1s")
	assertReferenceHTTPProblem(t, fixture.client.Throttle(ctx, locomotive.ID, lease.ID, 70, station.Reverse), http.StatusServiceUnavailable, "station_offline")
	if loco := fixture.sim.Loco(locomotive.DCCAddress); loco.Speed != 0.3 || loco.Direction != station.Forward {
		t.Fatalf("offline command changed state=%+v", loco)
	}

	advanceReferenceSimulatorScenario(t, fixture, "1s")
	if loco := fixture.sim.Loco(locomotive.DCCAddress); loco.Speed != 0.3 || loco.Direction != station.Forward {
		t.Fatalf("offline command replayed after recovery=%+v", loco)
	}
	if err := fixture.client.Throttle(ctx, locomotive.ID, lease.ID, 40, station.Reverse); err != nil {
		t.Fatal(err)
	}
	if loco := fixture.sim.Loco(locomotive.DCCAddress); loco.Speed != 0.4 || loco.Direction != station.Reverse {
		t.Fatalf("new command after recovery=%+v", loco)
	}
}

func testReferenceElectricalShortCircuit(t *testing.T) {
	fixture := newSimulatorAPIFixture(t)
	startReferenceSimulatorScenario(t, fixture, "electrical-short-circuit")
	advanceReferenceSimulatorScenario(t, fixture, "1s")

	status, err := fixture.client.StationStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertReferenceElectricalStatus(t, status)

	websocket := dialTestWebSocket(t, fixture.server.URL, fixture.token)
	defer websocket.close()
	snapshot := readTestSnapshot(t, websocket)
	assertReferenceElectricalStatus(t, snapshot.Payload.StationStatus)
}

func testReferenceFeedbackSingleBlock(t *testing.T) {
	fixture := newSimulatorAPIFixture(t)
	websocket := dialTestWebSocket(t, fixture.server.URL, fixture.token)
	defer websocket.close()
	readTestSnapshot(t, websocket)
	startReferenceSimulatorScenario(t, fixture, "feedback-single-block")

	advanceReferenceSimulatorScenario(t, fixture, "1s")
	occupiedSequence := readReferenceBlockEvent(t, websocket, "block-a", true)
	advanceReferenceSimulatorScenario(t, fixture, "1s")
	freeSequence := readReferenceBlockEvent(t, websocket, "block-a", false)
	if freeSequence <= occupiedSequence {
		t.Fatalf("feedback event sequences occupied=%d free=%d", occupiedSequence, freeSequence)
	}
}

func testReferenceFeedbackMultipleBlocks(t *testing.T) {
	fixture := newSimulatorAPIFixture(t)
	websocket := dialTestWebSocket(t, fixture.server.URL, fixture.token)
	defer websocket.close()
	readTestSnapshot(t, websocket)
	startReferenceSimulatorScenario(t, fixture, "feedback-multiple-blocks")

	advanceReferenceSimulatorScenario(t, fixture, "1s")
	firstSequence := readReferenceBlockEvent(t, websocket, "block-a", true)
	secondSequence := readReferenceBlockEvent(t, websocket, "block-b", true)
	if secondSequence <= firstSequence {
		t.Fatalf("simultaneous feedback sequences first=%d second=%d", firstSequence, secondSequence)
	}
	blocks, err := fixture.client.Blocks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !referenceBlockOccupied(blocks, "block-a") || !referenceBlockOccupied(blocks, "block-b") {
		t.Fatalf("blocks after simultaneous feedback=%+v", blocks)
	}
}

func testReferenceFeedbackBounce(t *testing.T) {
	fixture := newSimulatorAPIFixture(t)
	websocket := dialTestWebSocket(t, fixture.server.URL, fixture.token)
	defer websocket.close()
	readTestSnapshot(t, websocket)
	startReferenceSimulatorScenario(t, fixture, "feedback-bounce")

	advanceReferenceSimulatorScenario(t, fixture, "1s")
	firstSequence := readReferenceBlockEvent(t, websocket, "block-a", true)
	advanceReferenceSimulatorScenario(t, fixture, "20ms")
	secondSequence := readReferenceBlockEvent(t, websocket, "block-a", false)
	advanceReferenceSimulatorScenario(t, fixture, "20ms")
	thirdSequence := readReferenceBlockEvent(t, websocket, "block-a", true)
	if !(firstSequence < secondSequence && secondSequence < thirdSequence) {
		t.Fatalf("bounce event sequences=%d,%d,%d", firstSequence, secondSequence, thirdSequence)
	}
}

func testReferenceFeedbackEventLoss(t *testing.T) {
	fixture := newSimulatorAPIFixture(t)
	startReferenceSimulatorScenario(t, fixture, "feedback-event-loss")
	sequence := fixture.bus.CurrentSequence()
	advanceReferenceSimulatorScenario(t, fixture, "1s")
	if got := fixture.bus.CurrentSequence(); got != sequence {
		t.Fatalf("lost feedback published an event: sequence %d -> %d", sequence, got)
	}

	var state simulatorStateResponse
	requestSimulatorAPI(t, fixture, http.MethodGet, "/test/v1/simulator/state", nil, http.StatusOK, &state)
	if len(state.FeedbackStates) != 1 || state.FeedbackStates[0].Address != 1 || !state.FeedbackStates[0].Active {
		t.Fatalf("physical feedback state=%+v", state.FeedbackStates)
	}
	blocks, err := fixture.client.Blocks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if referenceBlockOccupied(blocks, "block-a") {
		t.Fatalf("lost event unexpectedly changed service state=%+v", blocks)
	}
}

func testReferenceAccessoryConfirmationSuccess(t *testing.T) {
	fixture := newSimulatorAPIFixture(t)
	startReferenceSimulatorScenario(t, fixture, "accessory-confirmation-success")
	if err := fixture.client.SetTurnout(context.Background(), "turnout-1", "diverging"); err != nil {
		t.Fatal(err)
	}
	if accessory := fixture.sim.Accessory(1); accessory.Desired != "diverging" || accessory.Reported == "diverging" || !accessory.Pending {
		t.Fatalf("pending accessory=%+v", accessory)
	}
	advanceReferenceSimulatorScenario(t, fixture, "2s")
	if accessory := fixture.sim.Accessory(1); accessory.Desired != "diverging" || accessory.Reported != "diverging" || accessory.Pending {
		t.Fatalf("confirmed accessory=%+v", accessory)
	}
}

func testReferenceAccessoryConfirmationTimeout(t *testing.T) {
	fixture := newSimulatorAPIFixture(t)
	startReferenceSimulatorScenario(t, fixture, "accessory-confirmation-timeout-base")
	if err := fixture.client.SetTurnout(context.Background(), "turnout-1", "diverging"); err != nil {
		t.Fatal(err)
	}
	advanceReferenceSimulatorScenario(t, fixture, "30s")
	if accessory := fixture.sim.Accessory(1); accessory.Desired != "diverging" || accessory.Reported == "diverging" || !accessory.Pending {
		t.Fatalf("unconfirmed accessory=%+v", accessory)
	}
}

func testReferenceAccessoryWrongConfirmation(t *testing.T) {
	fixture := newSimulatorAPIFixture(t)
	startReferenceSimulatorScenario(t, fixture, "accessory-wrong-confirmation")
	if err := fixture.client.SetTurnout(context.Background(), "turnout-1", "diverging"); err != nil {
		t.Fatal(err)
	}
	advanceReferenceSimulatorScenario(t, fixture, "1s")
	if accessory := fixture.sim.Accessory(1); accessory.Desired != "diverging" || accessory.Reported != "straight" || !accessory.Pending {
		t.Fatalf("inconsistent accessory=%+v", accessory)
	}
}

func referenceSimulatorScenarioPath(name string) string {
	return filepath.Join("..", "..", "tests", "simulator", "scenarios", name+".json")
}

func startReferenceSimulatorScenario(t *testing.T, fixture simulatorAPIFixture, name string) simulatorScenarioState {
	t.Helper()
	body, err := os.ReadFile(referenceSimulatorScenarioPath(name))
	if err != nil {
		t.Fatal(err)
	}
	var state simulatorScenarioState
	requestSimulatorAPI(t, fixture, http.MethodPost, "/test/v1/simulator/scenarios", body, http.StatusCreated, &state)
	if state.Name != name || state.State != simscenario.StateLoaded {
		t.Fatalf("loaded scenario=%+v", state)
	}
	requestSimulatorAPI(t, fixture, http.MethodPost, "/test/v1/simulator/scenarios/start", nil, http.StatusOK, &state)
	if state.Name != name || (state.State != simscenario.StateRunning && state.State != simscenario.StateCompleted) {
		t.Fatalf("started scenario=%+v", state)
	}
	return state
}

func advanceReferenceSimulatorScenario(t *testing.T, fixture simulatorAPIFixture, duration string) simulatorScenarioState {
	t.Helper()
	body, err := json.Marshal(map[string]string{"duration": duration})
	if err != nil {
		t.Fatal(err)
	}
	var state simulatorScenarioState
	requestSimulatorAPI(t, fixture, http.MethodPost, "/test/v1/simulator/scenarios/advance", body, http.StatusOK, &state)
	return state
}

func acquireReferenceLocomotive(t *testing.T, fixture simulatorAPIFixture) (model.Locomotive, model.ControlLease) {
	t.Helper()
	locomotives, err := fixture.client.Locomotives(context.Background())
	if err != nil || len(locomotives) == 0 {
		t.Fatalf("locomotives=%+v err=%v", locomotives, err)
	}
	lease, err := fixture.client.Acquire(context.Background(), locomotives[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	return locomotives[0], lease
}

func assertReferenceHTTPProblem(t *testing.T, err error, status int, code string) {
	t.Helper()
	var httpError *client.HTTPError
	if !errors.As(err, &httpError) || httpError.StatusCode != status || httpError.Problem == nil || httpError.Problem.Code != code {
		t.Fatalf("error=%v want status=%d code=%s", err, status, code)
	}
}

func assertReferenceElectricalStatus(t *testing.T, status station.Status) {
	t.Helper()
	if status.TrackPower != "off" || !status.ShortCircuit || !status.ExternalShortCircuit || status.InternalShortCircuit || status.MainCurrentMilliAmps != 850 || status.FilteredMainCurrentMilliAmps != 810 || status.SupplyVoltageMilliVolts != 17950 || status.TrackVoltageMilliVolts != 0 {
		t.Fatalf("electrical status=%+v", status)
	}
}

type referenceWebSocketEvent struct {
	Type     string          `json:"type"`
	Sequence uint64          `json:"sequence"`
	Payload  json.RawMessage `json:"payload"`
}

func readReferenceConnectivityEvent(t *testing.T, websocket *testWebSocket, want station.Connectivity) uint64 {
	t.Helper()
	for attempt := 0; attempt < 12; attempt++ {
		var event referenceWebSocketEvent
		websocket.readJSON(t, &event)
		if event.Type != "station.status.changed" {
			continue
		}
		var payload struct {
			Connectivity station.Connectivity `json:"connectivity"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Connectivity == want {
			return event.Sequence
		}
	}
	t.Fatalf("station.status.changed connectivity=%s not received", want)
	return 0
}

func readReferenceBlockEvent(t *testing.T, websocket *testWebSocket, blockID string, occupied bool) uint64 {
	t.Helper()
	for attempt := 0; attempt < 12; attempt++ {
		var event referenceWebSocketEvent
		websocket.readJSON(t, &event)
		if event.Type != "block.occupancy.changed" {
			continue
		}
		var payload struct {
			BlockID  string `json:"blockId"`
			Occupied bool   `json:"occupied"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.BlockID == blockID && payload.Occupied == occupied {
			return event.Sequence
		}
		t.Fatalf("block event=%+v want block=%s occupied=%t", payload, blockID, occupied)
	}
	t.Fatalf("block.occupancy.changed block=%s occupied=%t not received", blockID, occupied)
	return 0
}

func referenceBlockOccupied(blocks []model.Block, id string) bool {
	for _, block := range blocks {
		if block.ID == id {
			return block.Occupied
		}
	}
	return false
}
