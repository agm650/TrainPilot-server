package api

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/client"
	"github.com/agm650/TrainPilot-server/internal/model"
)

func configureExternalOccupancy(t *testing.T, fixture detailedHTTPFixture, sensors ...string) {
	t.Helper()
	ctx := context.Background()
	simulatorProvider, err := fixture.db.OccupancyProvider(ctx, "simulator")
	if err != nil {
		t.Fatal(err)
	}
	simulatorProvider.Required = false
	if err := fixture.db.SetOccupancyProvider(ctx, simulatorProvider); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.SetOccupancyProvider(ctx, model.OccupancyProvider{
		ID: "camera-yard", Type: "vision", Priority: 90, Required: true,
		StaleAfter: 3 * time.Minute, FreshnessRequired: true,
	}); err != nil {
		t.Fatal(err)
	}
	for index, sensorID := range sensors {
		blockID := "block-a"
		if index%2 == 1 {
			blockID = "block-b"
		}
		if err := fixture.db.SetOccupancySensorMapping(ctx, model.OccupancySensorMapping{
			ProviderID: "camera-yard", SensorID: sensorID, BlockID: blockID,
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func externalObservation(sensorID string, state model.OccupancyState, sequence uint64) map[string]any {
	return map[string]any{
		"providerId": "camera-yard",
		"sensorId":   sensorID,
		"state":      state,
		"sequence":   sequence,
		"observedAt": time.Now().UTC(),
	}
}

func requireClientProblem(t *testing.T, err error, status int, code string) {
	t.Helper()
	httpError, ok := err.(*client.HTTPError)
	if !ok || httpError.StatusCode != status || httpError.Problem == nil || httpError.Problem.Code != code {
		t.Fatalf("error = %#v, want status=%d code=%q", err, status, code)
	}
}

func TestExternalOccupancyObservationAPI(t *testing.T) {
	fixture := newDetailedHTTPFixture(t)
	configureExternalOccupancy(t, fixture, "zone-1", "zone-2")
	ctx := context.Background()

	if status, err := fixture.sensor.Do(ctx, http.MethodPost, "/api/v1/occupancy/observations", externalObservation("zone-1", model.OccupancyFree, 1), nil); err != nil || status != http.StatusAccepted {
		t.Fatalf("free status=%d err=%v", status, err)
	}
	if got := fixture.occupancy.BlockState("block-a").State; got != model.OccupancyFree {
		t.Fatalf("free state = %q", got)
	}
	occupied := externalObservation("zone-1", model.OccupancyOccupied, 2)
	occupied["occupant"] = map[string]any{"type": "locomotive", "id": "BB72084"}
	if status, err := fixture.sensor.Do(ctx, http.MethodPost, "/api/v1/occupancy/observations", occupied, nil); err != nil || status != http.StatusAccepted {
		t.Fatalf("occupied status=%d err=%v", status, err)
	}
	state := fixture.occupancy.BlockState("block-a")
	if state.State != model.OccupancyOccupied || state.Occupant == nil || state.Occupant.ID != "BB72084" {
		t.Fatalf("occupied state = %+v", state)
	}

	invalid := externalObservation("zone-1", model.OccupancyState("clear"), 3)
	_, err := fixture.sensor.Do(ctx, http.MethodPost, "/api/v1/occupancy/observations", invalid, nil)
	requireClientProblem(t, err, http.StatusBadRequest, "invalid_occupancy_observation")
	invalid = externalObservation("zone-1", model.OccupancyFree, 3)
	invalid["occupant"] = map[string]any{"type": "locomotive", "id": "BB72084"}
	_, err = fixture.sensor.Do(ctx, http.MethodPost, "/api/v1/occupancy/observations", invalid, nil)
	requireClientProblem(t, err, http.StatusBadRequest, "invalid_occupancy_observation")
	unknownProvider := externalObservation("zone-1", model.OccupancyFree, 3)
	unknownProvider["providerId"] = "missing"
	_, err = fixture.sensor.Do(ctx, http.MethodPost, "/api/v1/occupancy/observations", unknownProvider, nil)
	requireClientProblem(t, err, http.StatusNotFound, "occupancy_sensor_not_found")
	_, err = fixture.sensor.Do(ctx, http.MethodPost, "/api/v1/occupancy/observations", externalObservation("missing", model.OccupancyFree, 3), nil)
	requireClientProblem(t, err, http.StatusNotFound, "occupancy_sensor_not_found")
}

func TestBlocksExposeAggregatedOccupancyAndSourceDiagnostics(t *testing.T) {
	fixture := newDetailedHTTPFixture(t)
	configureExternalOccupancy(t, fixture, "zone-1")
	ctx := context.Background()

	blocks, err := fixture.viewer.Blocks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var blockA model.Block
	for _, block := range blocks {
		if block.ID == "block-a" {
			blockA = block
		}
	}
	if blockA.Occupancy.State != model.OccupancyUnknown || blockA.Occupied {
		t.Fatalf("startup block=%+v", blockA)
	}

	observation := externalObservation("zone-1", model.OccupancyOccupied, 1)
	observation["occupant"] = map[string]any{"type": "locomotive", "id": "BB72084"}
	if _, err := fixture.sensor.Do(ctx, http.MethodPost, "/api/v1/occupancy/observations", observation, nil); err != nil {
		t.Fatal(err)
	}
	blocks, err = fixture.viewer.Blocks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, block := range blocks {
		if block.ID == "block-a" {
			blockA = block
		}
	}
	if blockA.Occupancy.State != model.OccupancyOccupied || !blockA.Occupied || blockA.Occupancy.Occupant == nil || blockA.Occupancy.Occupant.ID != "BB72084" {
		t.Fatalf("occupied block=%+v", blockA)
	}

	_, err = fixture.viewer.BlockOccupancySources(ctx, "block-a")
	requireClientProblem(t, err, http.StatusForbidden, "permission_denied")
	_, err = fixture.sensor.BlockOccupancySources(ctx, "block-a")
	requireClientProblem(t, err, http.StatusForbidden, "permission_denied")
	for name, apiClient := range map[string]*client.Client{"dispatcher": fixture.dispatcher, "administrator": fixture.administrator} {
		states, err := apiClient.BlockOccupancySources(ctx, "block-a")
		if err != nil {
			t.Fatalf("%s diagnostics: %v", name, err)
		}
		if len(states) != 2 {
			t.Fatalf("%s states=%+v", name, states)
		}
	}
	_, err = fixture.dispatcher.BlockOccupancySources(ctx, "missing")
	requireClientProblem(t, err, http.StatusNotFound, "block_not_found")
}

func TestExternalOccupancySequenceSemantics(t *testing.T) {
	fixture := newDetailedHTTPFixture(t)
	configureExternalOccupancy(t, fixture, "zone-1")
	ctx := context.Background()
	first := externalObservation("zone-1", model.OccupancyOccupied, 100)
	if _, err := fixture.sensor.Do(ctx, http.MethodPost, "/api/v1/occupancy/observations", first, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.sensor.Do(ctx, http.MethodPost, "/api/v1/occupancy/observations", first, nil); err != nil {
		t.Fatalf("identical duplicate: %v", err)
	}
	conflict := externalObservation("zone-1", model.OccupancyFree, 100)
	conflict["observedAt"] = first["observedAt"]
	_, err := fixture.sensor.Do(ctx, http.MethodPost, "/api/v1/occupancy/observations", conflict, nil)
	requireClientProblem(t, err, http.StatusConflict, "occupancy_sequence_conflict")
	stale := externalObservation("zone-1", model.OccupancyFree, 99)
	_, err = fixture.sensor.Do(ctx, http.MethodPost, "/api/v1/occupancy/observations", stale, nil)
	requireClientProblem(t, err, http.StatusConflict, "occupancy_sequence_stale")
	if got := fixture.occupancy.BlockState("block-a").State; got != model.OccupancyOccupied {
		t.Fatalf("state after out-of-order observation = %q", got)
	}
}

func TestExternalOccupancySnapshotIsAtomicAndAbsenceIsNotFree(t *testing.T) {
	fixture := newDetailedHTTPFixture(t)
	configureExternalOccupancy(t, fixture, "zone-1", "zone-2")
	ctx := context.Background()
	now := time.Now().UTC()
	snapshot := map[string]any{
		"providerId": "camera-yard", "sequence": 1, "observedAt": now,
		"observations": []map[string]any{
			{"sensorId": "zone-1", "state": "free"},
			{"sensorId": "zone-2", "state": "occupied"},
		},
	}
	if status, err := fixture.sensor.Do(ctx, http.MethodPost, "/api/v1/occupancy/snapshot", snapshot, nil); err != nil || status != http.StatusAccepted {
		t.Fatalf("snapshot status=%d err=%v", status, err)
	}
	if fixture.occupancy.BlockState("block-a").State != model.OccupancyFree || fixture.occupancy.BlockState("block-b").State != model.OccupancyOccupied {
		t.Fatalf("snapshot states: a=%+v b=%+v", fixture.occupancy.BlockState("block-a"), fixture.occupancy.BlockState("block-b"))
	}

	snapshot["sequence"] = 2
	snapshot["observations"] = []map[string]any{{"sensorId": "zone-1", "state": "free"}}
	if _, err := fixture.sensor.Do(ctx, http.MethodPost, "/api/v1/occupancy/snapshot", snapshot, nil); err != nil {
		t.Fatal(err)
	}
	if got := fixture.occupancy.BlockState("block-b").State; got != model.OccupancyOccupied {
		t.Fatalf("absent sensor changed block-b to %q", got)
	}

	snapshot["sequence"] = 3
	snapshot["observations"] = []map[string]any{
		{"sensorId": "zone-1", "state": "occupied"},
		{"sensorId": "zone-2", "state": "invalid"},
	}
	_, err := fixture.sensor.Do(ctx, http.MethodPost, "/api/v1/occupancy/snapshot", snapshot, nil)
	requireClientProblem(t, err, http.StatusBadRequest, "invalid_occupancy_observation")
	if got := fixture.occupancy.BlockState("block-a").State; got != model.OccupancyFree {
		t.Fatalf("invalid batch partially changed block-a to %q", got)
	}

	snapshot["observations"] = []map[string]any{
		{"sensorId": "zone-1", "state": "occupied"},
		{"sensorId": "missing", "state": "free"},
	}
	_, err = fixture.sensor.Do(ctx, http.MethodPost, "/api/v1/occupancy/snapshot", snapshot, nil)
	requireClientProblem(t, err, http.StatusNotFound, "occupancy_sensor_not_found")
	if got := fixture.occupancy.BlockState("block-a").State; got != model.OccupancyFree {
		t.Fatalf("partially mapped batch changed block-a to %q", got)
	}
}

func TestExternalOccupancyAuthenticationAndLimits(t *testing.T) {
	fixture := newDetailedHTTPFixture(t)
	configureExternalOccupancy(t, fixture, "zone-1")
	body := externalObservation("zone-1", model.OccupancyFree, 1)
	ctx := context.Background()
	unauthenticated := client.New(fixture.server.URL)
	_, err := unauthenticated.Do(ctx, http.MethodPost, "/api/v1/occupancy/observations", body, nil)
	requireClientProblem(t, err, http.StatusUnauthorized, "missing_token")
	_, err = fixture.viewer.Do(ctx, http.MethodPost, "/api/v1/occupancy/observations", body, nil)
	requireClientProblem(t, err, http.StatusForbidden, "permission_denied")

	future := externalObservation("zone-1", model.OccupancyFree, 2)
	future["observedAt"] = time.Now().UTC().Add(10 * time.Minute)
	_, err = fixture.sensor.Do(ctx, http.MethodPost, "/api/v1/occupancy/observations", future, nil)
	requireClientProblem(t, err, http.StatusBadRequest, "invalid_occupancy_observation")
	items := make([]map[string]any, maxOccupancySnapshotObservations+1)
	for index := range items {
		items[index] = map[string]any{"sensorId": "zone-1", "state": "free"}
	}
	_, err = fixture.sensor.Do(ctx, http.MethodPost, "/api/v1/occupancy/snapshot", map[string]any{
		"providerId": "camera-yard", "sequence": 2, "observedAt": time.Now().UTC(), "observations": items,
	}, nil)
	requireClientProblem(t, err, http.StatusBadRequest, "invalid_occupancy_observation")

	if _, err := fixture.sensor.Do(ctx, http.MethodPost, "/api/v1/auth/logout", nil, nil); err != nil {
		t.Fatal(err)
	}
	_, err = fixture.sensor.Do(ctx, http.MethodPost, "/api/v1/occupancy/observations", body, nil)
	requireClientProblem(t, err, http.StatusUnauthorized, "invalid_token")
}

func TestExternalOccupancyConcurrentSensors(t *testing.T) {
	const count = 100
	fixture := newDetailedHTTPFixture(t)
	sensors := make([]string, count)
	for index := range sensors {
		sensors[index] = fmt.Sprintf("zone-%03d", index)
	}
	configureExternalOccupancy(t, fixture, sensors...)
	var wait sync.WaitGroup
	errorsChannel := make(chan error, count)
	for index, sensorID := range sensors {
		index, sensorID := index, sensorID
		wait.Add(1)
		go func() {
			defer wait.Done()
			state := model.OccupancyFree
			if index == count-1 {
				state = model.OccupancyOccupied
			}
			status, err := fixture.sensor.Do(context.Background(), http.MethodPost, "/api/v1/occupancy/observations", externalObservation(sensorID, state, 1), nil)
			if err == nil && status != http.StatusAccepted {
				err = fmt.Errorf("status=%d", status)
			}
			errorsChannel <- err
		}()
	}
	wait.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatal(err)
		}
	}
	if fixture.occupancy.BlockState("block-b").State != model.OccupancyOccupied {
		t.Fatalf("block-b state = %+v", fixture.occupancy.BlockState("block-b"))
	}
}
