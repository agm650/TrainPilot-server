package benchmark

import (
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	phaseNone        = "none"
	phaseSetup       = "setup"
	phaseWarmup      = "warmup"
	phaseMeasurement = "measurement"
	phaseStopping    = "stopping"
	phaseCleanup     = "cleanup"
	phaseFinished    = "finished"
	boundedOther     = "other"
)

var (
	benchmarkPhases = []string{
		phaseSetup,
		phaseWarmup,
		phaseMeasurement,
		phaseStopping,
		phaseCleanup,
		phaseFinished,
	}
	benchmarkOperations = map[string]struct{}{
		"login": {}, "refresh": {}, "lease_acquire": {}, "lease_heartbeat": {},
		"lease_release": {}, "throttle": {}, "function": {}, "feedback": {},
		"accessory": {}, "route": {}, "read": {}, "health": {},
		"lease_contention": {}, "route_contention": {}, "websocket_connect": {},
		"scheduler_drop": {},
	}
)

// LiveMetrics owns the optional, isolated Prometheus registry used by
// trainpilot-bench. It is never registered with the default registry.
type LiveMetrics struct {
	registry *prometheus.Registry

	phase                  *prometheus.GaugeVec
	phaseStarted           *prometheus.GaugeVec
	phaseTransitions       *prometheus.CounterVec
	requestedRate          *prometheus.GaugeVec
	requestedBurst         *prometheus.GaugeVec
	operations             *prometheus.CounterVec
	operationsSkipped      *prometheus.CounterVec
	operationDuration      *prometheus.HistogramVec
	webSocketCounters      map[string]*prometheus.CounterVec
	webSocketResync        *prometheus.CounterVec
	webSocketPendingResync prometheus.Gauge
	webSocketFeedback      *prometheus.HistogramVec

	phaseMu      sync.Mutex
	currentPhase atomic.Value
}

// NewLiveMetrics creates benchmark metrics in the setup phase.
func NewLiveMetrics() *LiveMetrics {
	registry := prometheus.NewRegistry()
	m := &LiveMetrics{
		registry: registry,
		phase: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "trainpilot_benchmark_phase",
			Help: "Current benchmark phase as a one-hot gauge.",
		}, []string{"phase"}),
		phaseStarted: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "trainpilot_benchmark_phase_started_timestamp_seconds",
			Help: "Unix timestamp when each benchmark phase last started.",
		}, []string{"phase"}),
		phaseTransitions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "trainpilot_benchmark_phase_transitions_total",
			Help: "Benchmark phase transitions.",
		}, []string{"from", "to"}),
		requestedRate: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "trainpilot_benchmark_requested_rate_per_second",
			Help: "Configured benchmark request rate by operation.",
		}, []string{"operation"}),
		requestedBurst: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "trainpilot_benchmark_requested_burst_operations",
			Help: "Configured burst operations by operation and phase.",
		}, []string{"operation", "phase"}),
		operations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "trainpilot_benchmark_operations_total",
			Help: "Executed benchmark operations by phase and result.",
		}, []string{"operation", "phase", "result"}),
		operationsSkipped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "trainpilot_benchmark_operations_skipped_total",
			Help: "Benchmark operations skipped because no target was available.",
		}, []string{"operation", "phase"}),
		operationDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "trainpilot_benchmark_operation_duration_seconds",
			Help:    "Benchmark operation duration by phase and result.",
			Buckets: prometheus.DefBuckets,
		}, []string{"operation", "phase", "result"}),
		webSocketCounters: map[string]*prometheus.CounterVec{
			"connections": prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "trainpilot_benchmark_websocket_connections_total",
				Help: "Successful benchmark WebSocket connections.",
			}, []string{"phase"}),
			"disconnections": prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "trainpilot_benchmark_websocket_disconnections_total",
				Help: "Benchmark WebSocket disconnections.",
			}, []string{"phase"}),
			"reconnects": prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "trainpilot_benchmark_websocket_reconnects_total",
				Help: "Successful benchmark WebSocket reconnections.",
			}, []string{"phase"}),
			"sequence_gaps": prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "trainpilot_benchmark_websocket_sequence_gaps_total",
				Help: "WebSocket sequence gaps observed by benchmark clients.",
			}, []string{"phase"}),
			"snapshots": prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "trainpilot_benchmark_websocket_snapshots_total",
				Help: "WebSocket snapshots observed by benchmark clients.",
			}, []string{"phase"}),
			"snapshot_requests": prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "trainpilot_benchmark_websocket_snapshot_requests_total",
				Help: "WebSocket snapshot requests sent by benchmark clients.",
			}, []string{"phase"}),
			"events_received": prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "trainpilot_benchmark_websocket_events_received_total",
				Help: "WebSocket events observed by benchmark clients.",
			}, []string{"phase"}),
			"invalid_messages": prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "trainpilot_benchmark_websocket_invalid_messages_total",
				Help: "Invalid WebSocket messages observed by benchmark clients.",
			}, []string{"phase"}),
		},
		webSocketResync: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "trainpilot_benchmark_websocket_resynchronizations_total",
			Help: "WebSocket resynchronizations by phase and state.",
		}, []string{"phase", "state"}),
		webSocketPendingResync: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "trainpilot_benchmark_websocket_resynchronizations_pending",
			Help: "WebSocket sequence gaps awaiting a replacement snapshot.",
		}),
		webSocketFeedback: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "trainpilot_benchmark_websocket_feedback_latency_seconds",
			Help:    "Feedback-to-WebSocket latency observed by benchmark clients.",
			Buckets: prometheus.DefBuckets,
		}, []string{"phase"}),
	}

	collectors := []prometheus.Collector{
		m.phase, m.phaseStarted, m.phaseTransitions, m.requestedRate, m.requestedBurst,
		m.operations, m.operationsSkipped, m.operationDuration,
		m.webSocketResync, m.webSocketPendingResync, m.webSocketFeedback,
	}
	for _, collector := range m.webSocketCounters {
		collectors = append(collectors, collector)
	}
	registry.MustRegister(collectors...)
	for _, phase := range benchmarkPhases {
		m.phase.WithLabelValues(phase).Set(0)
		m.phaseStarted.WithLabelValues(phase).Set(0)
	}
	for _, transition := range [][2]string{
		{phaseNone, phaseSetup},
		{phaseSetup, phaseWarmup},
		{phaseWarmup, phaseMeasurement},
		{phaseMeasurement, phaseStopping},
		{phaseStopping, phaseCleanup},
		{phaseCleanup, phaseFinished},
	} {
		m.phaseTransitions.WithLabelValues(transition[0], transition[1])
	}
	m.currentPhase.Store(phaseNone)
	m.setPhase(phaseSetup)
	return m
}

// Handler returns the isolated Prometheus handler for the benchmark registry.
func (m *LiveMetrics) Handler() http.Handler {
	if m == nil {
		return http.NotFoundHandler()
	}
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

func (m *LiveMetrics) setPhase(next string) {
	if m == nil {
		return
	}
	next = boundedPhase(next)
	m.phaseMu.Lock()
	defer m.phaseMu.Unlock()
	previous := m.phaseName()
	if previous == next {
		return
	}
	if previous != phaseNone {
		m.phase.WithLabelValues(previous).Set(0)
	}
	m.phase.WithLabelValues(next).Set(1)
	m.phaseStarted.WithLabelValues(next).Set(float64(time.Now().UnixNano()) / float64(time.Second))
	m.phaseTransitions.WithLabelValues(previous, next).Inc()
	m.currentPhase.Store(next)
}

func (m *LiveMetrics) phaseName() string {
	if m == nil {
		return phaseSetup
	}
	value := m.currentPhase.Load()
	if value == nil {
		return phaseSetup
	}
	return boundedPhase(value.(string))
}

func (m *LiveMetrics) configure(requestedRates map[string]float64, bursts []BurstProfile, warmup time.Duration) {
	if m == nil {
		return
	}
	for operation, rate := range requestedRates {
		if rate > 0 {
			m.requestedRate.WithLabelValues(boundedOperation(operation)).Set(rate)
		}
	}
	for _, burst := range bursts {
		phase := phaseMeasurement
		if burst.At.Duration < warmup {
			phase = phaseWarmup
		}
		m.requestedBurst.WithLabelValues(boundedOperation(burst.Operation), phase).Add(float64(burst.Count))
	}
}

func (m *LiveMetrics) observeOperation(operation string, latency time.Duration, err error) {
	if m == nil {
		return
	}
	result := "success"
	if err != nil {
		var expected expectedOperationError
		if errors.As(err, &expected) {
			result = "expected_error"
		} else {
			result = "unexpected_error"
		}
	}
	operation = boundedOperation(operation)
	phase := m.phaseName()
	m.operations.WithLabelValues(operation, phase, result).Inc()
	m.operationDuration.WithLabelValues(operation, phase, result).Observe(latency.Seconds())
}

func (m *LiveMetrics) skipOperation(operation string) {
	if m == nil {
		return
	}
	m.operationsSkipped.WithLabelValues(boundedOperation(operation), m.phaseName()).Inc()
}

func (m *LiveMetrics) observeWebSocket(name string) {
	if m == nil {
		return
	}
	if counter := m.webSocketCounters[name]; counter != nil {
		counter.WithLabelValues(m.phaseName()).Inc()
	}
}

func (m *LiveMetrics) startWebSocketResync() {
	if m == nil {
		return
	}
	m.webSocketResync.WithLabelValues(m.phaseName(), "started").Inc()
	m.webSocketPendingResync.Inc()
}

func (m *LiveMetrics) completeWebSocketResync() {
	if m == nil {
		return
	}
	m.webSocketResync.WithLabelValues(m.phaseName(), "completed").Inc()
	m.webSocketPendingResync.Dec()
}

func (m *LiveMetrics) observeWebSocketFeedback(latency time.Duration) {
	if m == nil {
		return
	}
	m.webSocketFeedback.WithLabelValues(m.phaseName()).Observe(latency.Seconds())
}

func boundedOperation(operation string) string {
	if _, ok := benchmarkOperations[operation]; ok {
		return operation
	}
	return boundedOther
}

func boundedPhase(phase string) string {
	for _, allowed := range benchmarkPhases {
		if phase == allowed {
			return phase
		}
	}
	if phase == phaseNone {
		return phase
	}
	return boundedOther
}
