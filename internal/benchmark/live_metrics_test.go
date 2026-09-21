package benchmark

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLiveMetricsExposeAllBenchmarkPhasesAndResults(t *testing.T) {
	metrics := NewLiveMetrics()
	metrics.configure(
		map[string]float64{"read": 12, "health": 1},
		[]BurstProfile{
			{At: Duration{Duration: time.Second}, Operation: "read", Count: 3},
			{At: Duration{Duration: 11 * time.Second}, Operation: "route", Count: 2},
		},
		10*time.Second,
	)
	recorder := newOperationRecorder(metrics)
	metrics.setPhase(phaseWarmup)
	recorder.Record("read", 10*time.Millisecond, nil)
	recorder.Skip("route")
	metrics.setPhase(phaseMeasurement)
	recorder.Record("read", 20*time.Millisecond, expectedError(errors.New("expected")))
	recorder.Record("read", 30*time.Millisecond, errors.New("unexpected"))
	metrics.startWebSocketResync()
	metrics.completeWebSocketResync()
	metrics.observeWebSocket("events_received")
	metrics.observeWebSocketCount("action_expectations_superseded", 3)
	metrics.setPhase(phaseStopping)
	metrics.setPhase(phaseCleanup)
	metrics.setPhase(phaseFinished)

	response := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/metrics", nil)
	metrics.Handler().ServeHTTP(response, request)
	body := response.Body.String()
	for _, expected := range []string{
		`trainpilot_benchmark_phase{phase="finished"} 1`,
		`trainpilot_benchmark_phase_transitions_total{from="warmup",to="measurement"} 1`,
		`trainpilot_benchmark_requested_rate_per_second{operation="read"} 12`,
		`trainpilot_benchmark_requested_burst_operations{operation="read",phase="warmup"} 3`,
		`trainpilot_benchmark_requested_burst_operations{operation="route",phase="measurement"} 2`,
		`trainpilot_benchmark_operations_total{operation="read",phase="warmup",result="success"} 1`,
		`trainpilot_benchmark_operations_total{operation="read",phase="measurement",result="expected_error"} 1`,
		`trainpilot_benchmark_operations_total{operation="read",phase="measurement",result="unexpected_error"} 1`,
		`trainpilot_benchmark_operations_skipped_total{operation="route",phase="warmup"} 1`,
		`trainpilot_benchmark_websocket_resynchronizations_total{phase="measurement",state="completed"} 1`,
		`trainpilot_benchmark_websocket_events_received_total{phase="measurement"} 1`,
		`trainpilot_benchmark_websocket_action_expectations_superseded_total{phase="measurement"} 3`,
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("metrics do not contain %q", expected)
		}
	}
}

func TestLiveMetricsBoundUnknownLabels(t *testing.T) {
	metrics := NewLiveMetrics()
	metrics.configure(map[string]float64{"resource-123": 4}, nil, 0)
	metrics.setPhase("profile-user-456")
	metrics.observeOperation("locomotive-789", time.Millisecond, nil)

	families, err := metrics.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	foundRequestedOperation := false
	foundOperationPhase := false
	foundOperationName := false
	for _, family := range families {
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				value := label.GetValue()
				if strings.Contains(value, "123") || strings.Contains(value, "456") || strings.Contains(value, "789") {
					t.Fatalf("unbounded label %s=%q in %s", label.GetName(), value, family.GetName())
				}
				if family.GetName() == "trainpilot_benchmark_requested_rate_per_second" && label.GetName() == "operation" && value == boundedOther {
					foundRequestedOperation = true
				}
				if family.GetName() == "trainpilot_benchmark_operations_total" && label.GetName() == "phase" && value == boundedOther {
					foundOperationPhase = true
				}
				if family.GetName() == "trainpilot_benchmark_operations_total" && label.GetName() == "operation" && value == boundedOther {
					foundOperationName = true
				}
			}
		}
	}
	if !foundRequestedOperation || !foundOperationPhase || !foundOperationName {
		t.Fatalf("bounded labels missing: requested=%t phase=%t operation=%t", foundRequestedOperation, foundOperationPhase, foundOperationName)
	}
}
