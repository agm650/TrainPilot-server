package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/client"
	"github.com/agm650/TrainPilot-server/internal/model"
)

func TestLocomotiveFunctionsAPICompatibility(t *testing.T) {
	ctx := context.Background()
	fixture := newDetailedHTTPFixture(t)
	body := map[string]any{"name": "Horn", "dccAddress": 3, "addressKind": "short", "speedSteps": 128}
	var loco model.Locomotive
	if _, err := fixture.administrator.Do(ctx, http.MethodPost, "/api/v1/locomotives", body, &loco); err != nil {
		t.Fatal(err)
	}
	if loco.Functions == nil || loco.IsMomentary(2) {
		t.Fatalf("legacy creation=%+v", loco)
	}
	path := "/api/v1/locomotives/" + loco.ID
	body["functions"] = []map[string]any{{"number": 0}, {"number": 2, "momentary": true}}
	if _, err := fixture.administrator.Do(ctx, http.MethodPut, path, body, &loco); err != nil {
		t.Fatal(err)
	}
	if !loco.IsMomentary(2) || loco.IsMomentary(0) {
		t.Fatalf("function defaults=%+v", loco)
	}
	for _, null := range []bool{false, true} {
		delete(body, "functions")
		if null {
			body["functions"] = nil
		}
		if _, err := fixture.administrator.Do(ctx, http.MethodPut, path, body, &loco); err != nil {
			t.Fatal(err)
		}
		if !loco.IsMomentary(2) {
			t.Fatalf("omitted/null definitions erased settings: %+v", loco)
		}
	}
	got, err := fixture.viewer.Locomotive(ctx, loco.ID)
	if err != nil || !got.IsMomentary(2) {
		t.Fatalf("GET definitions=%+v err=%v", got, err)
	}
	for _, invalid := range []any{[]map[string]any{{"number": -1}}, []map[string]any{{"number": 69}}, []map[string]any{{"number": 2}, {"number": 2}}, []map[string]any{{"number": 2, "momentary": "yes"}}, []map[string]any{{"momentary": true}}, []map[string]any{{"number": 2, "unknown": true}}} {
		body["functions"] = invalid
		status, err := fixture.administrator.Do(ctx, http.MethodPut, path, body, nil)
		if status != http.StatusBadRequest || err == nil {
			t.Fatalf("invalid definitions status=%d err=%v", status, err)
		}
	}
	// The typed Go client must be able to explicitly clear an empty list.
	cleared, err := fixture.administrator.UpdateLocomotive(ctx, loco.ID, model.LocomotiveInput{Name: loco.Name, DCCAddress: loco.DCCAddress, AddressKind: loco.AddressKind, SpeedSteps: loco.SpeedSteps, Functions: []model.LocomotiveFunction{}})
	if err != nil || len(cleared.Functions) != 0 {
		t.Fatalf("explicit clear=%+v err=%v", cleared, err)
	}
}

func TestMomentaryWebSocketDisconnectStopsFunctionAndNotifiesOtherClients(t *testing.T) {
	ctx := context.Background()
	fixture := newWebsocketFixture(t)
	loco := fixture.locomotives[0]
	loco.Functions = []model.LocomotiveFunction{{Number: 2, Momentary: true}}
	if err := fixture.api.store.UpdateLocomotive(ctx, loco); err != nil {
		t.Fatal(err)
	}
	owner := dialTestWebSocket(t, fixture.server.URL, fixture.accessToken)
	defer owner.close()
	snapshot := readTestSnapshot(t, owner)
	found := false
	for _, item := range snapshot.Payload.Locomotives {
		if item.ID == loco.ID {
			found = item.IsMomentary(2)
		}
	}
	if !found {
		t.Fatal("initial snapshot omitted momentary configuration")
	}
	observerPair, err := fixture.api.auth.Login(ctx, "alice", "correct-horse-1", "observer", "Observer", "test")
	if err != nil {
		t.Fatal(err)
	}
	observer := dialTestWebSocket(t, fixture.server.URL, observerPair.AccessToken)
	defer observer.close()
	readTestSnapshot(t, observer)
	if err := fixture.control.SetTrackPower(ctx, fixture.user, true); err != nil {
		t.Fatal(err)
	}
	apiClient := client.New(fixture.server.URL)
	apiClient.AccessToken = fixture.accessToken
	for _, fn := range []int{2, 3} {
		if err := apiClient.Function(ctx, loco.ID, fixture.lease.ID, fn, true); err != nil {
			t.Fatal(err)
		}
	}
	owner.close() // Abrupt TCP close; no OFF command from the client.
	for i := 0; i < 16; i++ {
		var event struct {
			Type    string `json:"type"`
			Payload struct {
				LocomotiveID string `json:"locomotiveId"`
				Function     int    `json:"function"`
				Enabled      bool   `json:"enabled"`
			} `json:"payload"`
		}
		observer.readJSON(t, &event)
		if event.Type == "locomotive.function.changed" && event.Payload.LocomotiveID == loco.ID && event.Payload.Function == 2 && !event.Payload.Enabled {
			state := fixture.api.simulator.Loco(loco.DCCAddress)
			if state.Functions[2] || !state.Functions[3] {
				t.Fatalf("disconnect changed wrong functions: %+v", state.Functions)
			}
			return
		}
	}
	t.Fatal("observer did not receive automatic momentary OFF")
}
