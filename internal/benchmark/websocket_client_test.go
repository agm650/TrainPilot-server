package benchmark

import (
	"context"
	"errors"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	ws "github.com/agm650/TrainPilot-server/internal/websocket"
)

func TestWebSocketClientHandshakeReadAndWrite(t *testing.T) {
	received := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/events" || r.Header.Get("Authorization") != "Bearer token" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		connection, err := ws.Accept(w, r)
		if err != nil {
			return
		}
		defer connection.Close()
		_ = connection.WriteJSON(map[string]any{"type": "system.snapshot", "sequence": 4, "payload": map[string]any{}})
		var message struct {
			Type string `json:"type"`
		}
		if err := connection.ReadJSON(&message); err == nil {
			received <- message.Type
		}
	}))
	defer server.Close()

	client, err := dialWebSocket(context.Background(), server.URL, "token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var snapshot wireMessage
	if err := client.ReadJSON(&snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Type != "system.snapshot" || snapshot.Sequence != 4 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	if err := client.WriteJSON(map[string]string{"type": "client.snapshot_request"}, time.Second); err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-received:
		if message != "client.snapshot_request" {
			t.Fatalf("message=%q", message)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not receive client frame")
	}
}

func TestWebSocketClientRejectsHTTPResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	if _, err := dialWebSocket(context.Background(), server.URL, "token", time.Second); err == nil {
		t.Fatal("expected handshake error")
	}
}

func TestWebSocketSequenceGapRequestsAndAppliesSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := ws.Accept(w, r)
		if err != nil {
			return
		}
		defer connection.Close()
		_ = connection.WriteJSON(map[string]any{"type": "system.snapshot", "sequence": 1, "payload": map[string]any{"routes": []any{}}})
		_ = connection.WriteJSON(map[string]any{"type": "block.occupancy.changed", "sequence": 3, "payload": map[string]any{"blockId": "block-a", "occupied": true}})
		var request struct {
			Type string `json:"type"`
		}
		if err := connection.ReadJSON(&request); err != nil || request.Type != "client.snapshot_request" {
			return
		}
		_ = connection.WriteJSON(map[string]any{"type": "system.snapshot", "sequence": 3, "payload": map[string]any{"routes": []any{}}})
	}))
	defer server.Close()
	client, err := dialWebSocket(context.Background(), server.URL, "token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	invariants := newInvariantTracker()
	expectations := newExpectationTracker(invariants)
	metrics := &webSocketMetrics{}
	engine := &runEngine{
		profile:    Profile{OperationTimeout: Duration{Duration: time.Second}},
		invariants: invariants, expectations: expectations, wsMetrics: metrics,
	}
	engine.monitor = newEventMonitor(invariants, expectations, metrics, Fixture{})
	awaiting := false
	ready := false
	err = engine.consumeWebSocket(context.Background(), client, rand.New(rand.NewSource(650)), &awaiting, func() { ready = true })
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	if !ready || awaiting || metrics.sequenceGaps.Load() != 1 || metrics.snapshotRequests.Load() != 1 || metrics.snapshots.Load() != 2 {
		t.Fatalf("ready=%t awaiting=%t summary=%+v", ready, awaiting, metrics.summary())
	}
	results, failed := invariants.Results()
	if failed {
		t.Fatalf("invariants=%+v", results)
	}
	for _, result := range results {
		if result.Name == invariantWebSocketResync && result.Status != "PASS" {
			t.Fatalf("resynchronization=%+v", result)
		}
	}
}
