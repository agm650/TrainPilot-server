package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDiagnosticHandlerFeatureGates(t *testing.T) {
	metrics := New(":memory:")
	tests := []struct {
		name           string
		metricsEnabled bool
		pprofEnabled   bool
		path           string
		wantStatus     int
	}{
		{"metrics enabled", true, false, "/metrics", http.StatusOK},
		{"metrics disabled", false, false, "/metrics", http.StatusNotFound},
		{"pprof disabled", true, false, "/debug/pprof/", http.StatusNotFound},
		{"pprof enabled", false, true, "/debug/pprof/", http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			DiagnosticHandler(metrics, test.metricsEnabled, test.pprofEnabled).ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status=%d want %d", recorder.Code, test.wantStatus)
			}
		})
	}
}

func TestMetricsIncludeRuntimeAndProcessCollectors(t *testing.T) {
	body := scrape(t, New(":memory:"))
	for _, metric := range []string{"go_goroutines", "go_memstats_heap_alloc_bytes", "process_cpu_seconds_total"} {
		if !strings.Contains(body, metric) {
			t.Fatalf("missing metric %q", metric)
		}
	}
}

func TestHTTPMetricsUseTemplatedRoutes(t *testing.T) {
	metrics := New(":memory:")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/locomotives/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler := metrics.HTTPMiddleware(mux)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/locomotives/secret-locomotive-id", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status=%d", recorder.Code)
	}

	body := scrape(t, metrics)
	if !strings.Contains(body, `route="/api/v1/locomotives/{id}"`) {
		t.Fatalf("templated route missing from metrics:\n%s", body)
	}
	if strings.Contains(body, "secret-locomotive-id") {
		t.Fatal("dynamic locomotive ID leaked into metrics")
	}
}

func TestStationReconnectMetric(t *testing.T) {
	metrics := New(":memory:")
	metrics.SetStationState("online")
	metrics.ObserveStationTransition("online", "degraded")
	metrics.ObserveStationTransition("degraded", "offline")
	metrics.ObserveStationTransition("offline", "online")
	body := scrape(t, metrics)
	if !strings.Contains(body, "trainpilot_station_reconnections_total 1") {
		t.Fatalf("reconnection metric missing:\n%s", body)
	}
}

func TestTurnoutDurationMetricsBoundLabels(t *testing.T) {
	metrics := New(":memory:")
	metrics.ObserveTurnoutCommandDuration("secret-result", time.Millisecond)
	metrics.ObserveTurnoutPhaseDuration("secret-turnout-id", time.Millisecond)
	metrics.ObserveTurnoutConfirmationDetail("secret-stage", time.Millisecond)
	metrics.ObserveStoreOperation("get_turnout_state", nil, time.Millisecond)
	body := scrape(t, metrics)
	for _, sample := range []string{
		`trainpilot_turnout_command_duration_seconds_count{result="other"} 1`,
		`trainpilot_turnout_command_phase_duration_seconds_count{phase="other"} 1`,
		`trainpilot_turnout_confirmation_detail_duration_seconds_count{stage="other"} 1`,
		`trainpilot_store_operation_duration_seconds_count{operation="get_turnout_state",result="success"} 1`,
	} {
		if !strings.Contains(body, sample) {
			t.Fatalf("missing metric sample %q", sample)
		}
	}
	if strings.Contains(body, "secret-") {
		t.Fatal("unbounded label leaked into turnout duration metrics")
	}
}

func scrape(t *testing.T, metrics *Metrics) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("metrics status=%d", recorder.Code)
	}
	return recorder.Body.String()
}
