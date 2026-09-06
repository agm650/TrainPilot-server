package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/observability"
)

func TestPublicAPIHasNoDiagnosticEndpoints(t *testing.T) {
	fixture := newDetailedHTTPFixture(t)
	for _, path := range []string{"/metrics", "/debug/pprof/", "/debug/pprof/heap"} {
		response, err := http.Get(fixture.server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("%s status=%d want 404", path, response.StatusCode)
		}
	}
}

func TestWebSocketConnectionGaugeAndSnapshotMetrics(t *testing.T) {
	metrics := observability.New(":memory:")
	fixture := newWebsocketFixtureWithStationAndMetrics(t, 15*time.Minute, nil, metrics)
	client := dialTestWebSocket(t, fixture.server.URL, fixture.accessToken)
	readTestSnapshot(t, client)
	client.writeJSON(t, map[string]any{"type": "client.snapshot_request"})
	readTestSnapshot(t, client)

	body := scrapeAPIMetrics(t, metrics)
	if !strings.Contains(body, "trainpilot_websocket_connections 1") {
		t.Fatalf("connected gauge missing:\n%s", body)
	}
	if !strings.Contains(body, "trainpilot_websocket_snapshot_generation_duration_seconds_count 2") {
		t.Fatalf("snapshot metric missing:\n%s", body)
	}
	if !strings.Contains(body, "trainpilot_websocket_snapshot_requests_total 1") {
		t.Fatalf("snapshot request metric missing:\n%s", body)
	}
	if !strings.Contains(body, `route="/api/v1/events"`) || !strings.Contains(body, `status_class="1xx"`) {
		t.Fatalf("WebSocket upgrade HTTP metric missing:\n%s", body)
	}
	client.close()

	deadline := time.Now().Add(time.Second)
	for {
		body = scrapeAPIMetrics(t, metrics)
		if strings.Contains(body, "trainpilot_websocket_connections 0") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("disconnected gauge not observed:\n%s", body)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func scrapeAPIMetrics(t *testing.T, metrics *observability.Metrics) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("metrics status=%d", recorder.Code)
	}
	return recorder.Body.String()
}
