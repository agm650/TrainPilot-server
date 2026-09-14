package observability

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const otherLabel = "other"

// Metrics owns an isolated Prometheus registry. It is intentionally not
// registered with prometheus.DefaultRegisterer so tests and multiple server
// instances in one process cannot collide.
type Metrics struct {
	registry *prometheus.Registry

	httpRequests      *prometheus.CounterVec
	httpInFlight      *prometheus.GaugeVec
	httpDuration      *prometheus.HistogramVec
	httpRequestSize   *prometheus.HistogramVec
	httpResponseSize  *prometheus.HistogramVec
	wsConnections     prometheus.Gauge
	wsConnectionsSeen prometheus.Counter
	wsEvents          *prometheus.CounterVec
	wsEventsDropped   prometheus.Counter
	wsQueueOverflows  prometheus.Counter
	wsSnapshotRequest prometheus.Counter
	wsSnapshotTime    prometheus.Histogram
	wsSnapshotSize    prometheus.Histogram

	feedbackEvents        *prometheus.CounterVec
	feedbackDuration      *prometheus.HistogramVec
	feedbackMappingErrors *prometheus.CounterVec
	feedbackOccupancy     *prometheus.CounterVec

	activeLeases         prometheus.Gauge
	leaseOperations      *prometheus.CounterVec
	controlCommands      *prometheus.CounterVec
	leaseStopDuration    *prometheus.HistogramVec
	safetyStops          *prometheus.CounterVec
	routeOperations      *prometheus.CounterVec
	turnoutCommands      *prometheus.CounterVec
	turnoutConfirms      *prometheus.CounterVec
	turnoutDuration      *prometheus.HistogramVec
	turnoutPhaseTime     *prometheus.HistogramVec
	turnoutConfirmDetail *prometheus.HistogramVec

	stationState       *prometheus.GaugeVec
	stationTransitions *prometheus.CounterVec
	stationReconnects  prometheus.Counter
	stationCommands    *prometheus.CounterVec
	stationDuration    *prometheus.HistogramVec
	stationMu          sync.Mutex
	stationCurrent     string
	sqliteStatsOnce    sync.Once

	storeOperations   *prometheus.CounterVec
	storeDuration     *prometheus.HistogramVec
	storeTransactions *prometheus.CounterVec
}

func New(databasePath string) *Metrics {
	registry := prometheus.NewRegistry()
	turnoutDurationBuckets := prometheus.ExponentialBuckets(0.0005, 2, 15)
	m := &Metrics{
		registry: registry,
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "trainpilot_http_requests_total",
			Help: "HTTP requests handled by the public API.",
		}, []string{"method", "route", "status_class"}),
		httpInFlight: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "trainpilot_http_requests_in_flight",
			Help: "HTTP requests currently handled by the public API.",
		}, []string{"method"}),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "trainpilot_http_request_duration_seconds",
			Help:    "Public API request duration in seconds.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "route", "status_class"}),
		httpRequestSize: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "trainpilot_http_request_size_bytes",
			Help:    "Public API request body size in bytes when known.",
			Buckets: prometheus.ExponentialBuckets(128, 4, 8),
		}, []string{"method", "route"}),
		httpResponseSize: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "trainpilot_http_response_size_bytes",
			Help:    "Public API response body size in bytes.",
			Buckets: prometheus.ExponentialBuckets(128, 4, 8),
		}, []string{"method", "route", "status_class"}),
		wsConnections: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "trainpilot_websocket_connections",
			Help: "Current public event WebSocket connections.",
		}),
		wsConnectionsSeen: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "trainpilot_websocket_connections_total",
			Help: "Accepted public event WebSocket connections.",
		}),
		wsEvents: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "trainpilot_websocket_events_total",
			Help: "Events written to public WebSocket connections.",
		}, []string{"event_type"}),
		wsEventsDropped: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "trainpilot_websocket_events_dropped_total",
			Help: "WebSocket events that could not be delivered.",
		}),
		wsQueueOverflows: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "trainpilot_websocket_queue_overflows_total",
			Help: "WebSocket subscriber queue overflows.",
		}),
		wsSnapshotRequest: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "trainpilot_websocket_snapshot_requests_total",
			Help: "Client snapshot requests received through WebSocket.",
		}),
		wsSnapshotTime: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "trainpilot_websocket_snapshot_generation_duration_seconds",
			Help:    "System snapshot generation duration in seconds.",
			Buckets: prometheus.DefBuckets,
		}),
		wsSnapshotSize: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "trainpilot_websocket_snapshot_size_bytes",
			Help:    "Serialized system snapshot size in bytes.",
			Buckets: prometheus.ExponentialBuckets(512, 4, 8),
		}),
		feedbackEvents: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "trainpilot_feedback_events_total",
			Help: "Feedback events grouped by bounded provider and result.",
		}, []string{"provider", "result"}),
		feedbackDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "trainpilot_feedback_processing_duration_seconds",
			Help:    "Feedback processing duration from receipt to result.",
			Buckets: prometheus.DefBuckets,
		}, []string{"provider", "result"}),
		feedbackMappingErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "trainpilot_feedback_mapping_errors_total",
			Help: "Feedback mapping storage errors.",
		}, []string{"provider"}),
		feedbackOccupancy: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "trainpilot_feedback_occupancy_updates_total",
			Help: "Mapped occupancy updates grouped by whether state changed.",
		}, []string{"provider", "result"}),
		activeLeases: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "trainpilot_control_leases_active",
			Help: "Live locomotive control leases.",
		}),
		leaseOperations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "trainpilot_control_lease_operations_total",
			Help: "Control lease operations grouped by result.",
		}, []string{"operation", "result"}),
		controlCommands: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "trainpilot_control_commands_total",
			Help: "Locomotive control commands grouped by result.",
		}, []string{"command", "result"}),
		leaseStopDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "trainpilot_control_lease_stop_duration_seconds",
			Help:    "Lease expiration or release processing duration.",
			Buckets: prometheus.DefBuckets,
		}, []string{"stage"}),
		safetyStops: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "trainpilot_control_safety_stops_total",
			Help: "Safety stop commands grouped by bounded reason.",
		}, []string{"reason"}),
		routeOperations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "trainpilot_route_operations_total",
			Help: "Route operations grouped by result.",
		}, []string{"operation", "result"}),
		turnoutCommands: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "trainpilot_turnout_commands_total",
			Help: "Logical turnout commands grouped by result.",
		}, []string{"result"}),
		turnoutConfirms: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "trainpilot_turnout_confirmations_total",
			Help: "Turnout confirmations grouped by result.",
		}, []string{"result"}),
		turnoutDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "trainpilot_turnout_command_duration_seconds",
			Help:    "End-to-end logical turnout command duration, including lock and confirmation waits.",
			Buckets: turnoutDurationBuckets,
		}, []string{"result"}),
		turnoutPhaseTime: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "trainpilot_turnout_command_phase_duration_seconds",
			Help:    "Duration of bounded logical turnout command phases.",
			Buckets: turnoutDurationBuckets,
		}, []string{"phase"}),
		turnoutConfirmDetail: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "trainpilot_turnout_confirmation_detail_duration_seconds",
			Help:    "Duration of bounded turnout confirmation wait and accessory event processing stages.",
			Buckets: turnoutDurationBuckets,
		}, []string{"stage"}),
		stationState: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "trainpilot_station_state",
			Help: "Current command-station connectivity state as a one-hot gauge.",
		}, []string{"state"}),
		stationTransitions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "trainpilot_station_state_changes_total",
			Help: "Command-station connectivity changes.",
		}, []string{"from", "to"}),
		stationReconnects: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "trainpilot_station_reconnections_total",
			Help: "Transitions from degraded or offline to online.",
		}),
		stationCommands: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "trainpilot_station_commands_total",
			Help: "Commands sent to the command station grouped by result.",
		}, []string{"operation", "result"}),
		stationDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "trainpilot_station_command_duration_seconds",
			Help:    "Observed command-station command duration.",
			Buckets: prometheus.DefBuckets,
		}, []string{"operation", "result"}),
		storeOperations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "trainpilot_store_operations_total",
			Help: "Logical store operations grouped by result.",
		}, []string{"operation", "result"}),
		storeDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "trainpilot_store_operation_duration_seconds",
			Help:    "Logical store operation duration in seconds.",
			Buckets: prometheus.DefBuckets,
		}, []string{"operation", "result"}),
		storeTransactions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "trainpilot_store_transactions_total",
			Help: "SQLite transactions grouped by terminal result.",
		}, []string{"result"}),
	}

	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.httpRequests, m.httpInFlight, m.httpDuration, m.httpRequestSize, m.httpResponseSize,
		m.wsConnections, m.wsConnectionsSeen, m.wsEvents, m.wsEventsDropped, m.wsQueueOverflows,
		m.wsSnapshotRequest, m.wsSnapshotTime, m.wsSnapshotSize,
		m.feedbackEvents, m.feedbackDuration, m.feedbackMappingErrors, m.feedbackOccupancy,
		m.activeLeases, m.leaseOperations, m.controlCommands, m.leaseStopDuration, m.safetyStops,
		m.routeOperations, m.turnoutCommands, m.turnoutConfirms, m.turnoutDuration, m.turnoutPhaseTime, m.turnoutConfirmDetail,
		m.stationState, m.stationTransitions, m.stationReconnects, m.stationCommands, m.stationDuration,
		m.storeOperations, m.storeDuration, m.storeTransactions,
	)
	m.registerDatabaseSize(databasePath)
	m.SetStationState("unknown")
	return m
}

func (m *Metrics) registerDatabaseSize(path string) {
	if m == nil || path == "" || path == ":memory:" {
		return
	}
	for _, file := range []struct {
		label string
		path  string
	}{{"database", path}, {"wal", path + "-wal"}} {
		item := file
		m.registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name:        "trainpilot_sqlite_file_size_bytes",
			Help:        "SQLite database and WAL file size in bytes.",
			ConstLabels: prometheus.Labels{"file": item.label},
		}, func() float64 {
			info, err := os.Stat(item.path)
			if err != nil {
				return 0
			}
			return float64(info.Size())
		}))
	}
}

func (m *Metrics) Handler() http.Handler {
	if m == nil {
		return http.NotFoundHandler()
	}
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

func (m *Metrics) HTTPMiddleware(next http.Handler) http.Handler {
	if m == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := boundedMethod(r.Method)
		m.httpInFlight.WithLabelValues(method).Inc()
		started := time.Now()
		writer := &metricsResponseWriter{ResponseWriter: w, status: http.StatusOK}
		var finishOnce sync.Once
		finish := func() {
			finishOnce.Do(func() {
				m.httpInFlight.WithLabelValues(method).Dec()
				route := routeLabel(r.Pattern)
				class := statusClass(writer.status)
				m.httpRequests.WithLabelValues(method, route, class).Inc()
				m.httpDuration.WithLabelValues(method, route, class).Observe(time.Since(started).Seconds())
				if r.ContentLength >= 0 {
					m.httpRequestSize.WithLabelValues(method, route).Observe(float64(r.ContentLength))
				}
				m.httpResponseSize.WithLabelValues(method, route, class).Observe(float64(writer.bytes))
			})
		}
		writer.onHijack = finish
		defer finish()
		next.ServeHTTP(writer, r)
	})
}

func (m *Metrics) WebSocketConnected() func() {
	if m == nil {
		return func() {}
	}
	m.wsConnections.Inc()
	m.wsConnectionsSeen.Inc()
	return func() { m.wsConnections.Dec() }
}

func (m *Metrics) WebSocketEvent(eventType string, delivered bool) {
	if m == nil {
		return
	}
	if delivered {
		m.wsEvents.WithLabelValues(boundedEventType(eventType)).Inc()
		return
	}
	m.wsEventsDropped.Inc()
}

func (m *Metrics) WebSocketQueueOverflow() {
	if m != nil {
		m.wsQueueOverflows.Inc()
		m.wsEventsDropped.Inc()
	}
}

func (m *Metrics) WebSocketQueueDrop(eventType string) {
	if m == nil {
		return
	}
	m.wsQueueOverflows.Inc()
	m.wsEventsDropped.Inc()
}

func (m *Metrics) WebSocketSnapshotRequest() {
	if m != nil {
		m.wsSnapshotRequest.Inc()
	}
}

func (m *Metrics) ObserveWebSocketSnapshot(duration time.Duration, size int) {
	if m != nil {
		m.wsSnapshotTime.Observe(duration.Seconds())
		m.wsSnapshotSize.Observe(float64(size))
	}
}

func (m *Metrics) ObserveFeedback(provider, result string, duration time.Duration) {
	if m == nil {
		return
	}
	provider = BoundedProvider(provider)
	result = bounded(result, "mapped", "unmapped", "mapping_error", "update_error")
	m.feedbackEvents.WithLabelValues(provider, result).Inc()
	m.feedbackDuration.WithLabelValues(provider, result).Observe(duration.Seconds())
	if result == "mapping_error" {
		m.feedbackMappingErrors.WithLabelValues(provider).Inc()
	}
}

func (m *Metrics) ObserveFeedbackOccupancy(provider string, changed bool) {
	if m == nil {
		return
	}
	result := "unchanged"
	if changed {
		result = "changed"
	}
	m.feedbackOccupancy.WithLabelValues(BoundedProvider(provider), result).Inc()
}

func (m *Metrics) SetActiveLeases(count int) {
	if m != nil {
		m.activeLeases.Set(float64(count))
	}
}

func (m *Metrics) AddActiveLeases(delta float64) {
	if m != nil {
		m.activeLeases.Add(delta)
	}
}

func (m *Metrics) ObserveLeaseOperation(operation, result string) {
	if m != nil {
		m.leaseOperations.WithLabelValues(bounded(operation, "acquire", "heartbeat", "takeover", "release", "expire"), boundedResult(result)).Inc()
	}
}

func (m *Metrics) ObserveControlCommand(command, result string) {
	if m != nil {
		m.controlCommands.WithLabelValues(bounded(command, "throttle", "function"), boundedResult(result)).Inc()
	}
}

func (m *Metrics) ObserveLeaseStop(stage string, duration time.Duration) {
	if m != nil {
		m.leaseStopDuration.WithLabelValues(bounded(stage, "stop", "release")).Observe(duration.Seconds())
	}
}

func (m *Metrics) SafetyStop(reason string) {
	if m != nil {
		m.safetyStops.WithLabelValues(bounded(reason, "heartbeat_timeout", "client_release", "takeover", "emergency_stop", "track_power_off")).Inc()
	}
}

func (m *Metrics) ObserveRoute(operation, result string) {
	if m != nil {
		m.routeOperations.WithLabelValues(bounded(operation, "reserve", "activate", "release"), boundedRouteResult(result)).Inc()
	}
}

func (m *Metrics) ObserveTurnoutCommand(result string) {
	if m != nil {
		m.turnoutCommands.WithLabelValues(boundedTurnoutResult(result)).Inc()
	}
}

func (m *Metrics) ObserveTurnoutConfirmation(result string) {
	if m != nil {
		m.turnoutConfirms.WithLabelValues(bounded(result, "confirmed", "timeout", "wrong_feedback", "interrupted")).Inc()
	}
}

func (m *Metrics) ObserveTurnoutCommandDuration(result string, duration time.Duration) {
	if m != nil {
		m.turnoutDuration.WithLabelValues(boundedTurnoutResult(result)).Observe(duration.Seconds())
	}
}

func (m *Metrics) ObserveTurnoutPhaseDuration(phase string, duration time.Duration) {
	if m != nil {
		m.turnoutPhaseTime.WithLabelValues(bounded(phase, "lock_wait", "prepare", "station", "confirmation", "finalize")).Observe(duration.Seconds())
	}
}

func (m *Metrics) ObserveTurnoutConfirmationDetail(stage string, duration time.Duration) {
	if m != nil {
		m.turnoutConfirmDetail.WithLabelValues(bounded(stage, "event_delivery", "event_handler", "event_lookup", "event_persist", "event_publish", "wait_read", "wait_update")).Observe(duration.Seconds())
	}
}

func (m *Metrics) SetStationState(state string) {
	if m == nil {
		return
	}
	m.stationMu.Lock()
	defer m.stationMu.Unlock()
	state = bounded(state, "online", "degraded", "offline", "unknown")
	m.setStationStateLocked(state)
}

func (m *Metrics) setStationStateLocked(state string) {
	m.stationCurrent = state
	for _, candidate := range []string{"online", "degraded", "offline", "unknown"} {
		value := 0.0
		if candidate == state {
			value = 1
		}
		m.stationState.WithLabelValues(candidate).Set(value)
	}
}

func (m *Metrics) ObserveStationTransition(from, to string) {
	if m == nil {
		return
	}
	m.stationMu.Lock()
	defer m.stationMu.Unlock()
	from = bounded(from, "online", "degraded", "offline", "unknown")
	to = bounded(to, "online", "degraded", "offline", "unknown")
	if m.stationCurrent != "" && m.stationCurrent != "unknown" {
		from = m.stationCurrent
	}
	m.setStationStateLocked(to)
	if from != to {
		m.stationTransitions.WithLabelValues(from, to).Inc()
		if to == "online" && (from == "degraded" || from == "offline") {
			m.stationReconnects.Inc()
		}
	}
}

func (m *Metrics) ObserveStationCommand(operation string, err error, duration time.Duration) {
	if m == nil {
		return
	}
	operation = bounded(operation, "track_power", "emergency_stop", "throttle", "function", "accessory")
	result := Result(err)
	m.stationCommands.WithLabelValues(operation, result).Inc()
	m.stationDuration.WithLabelValues(operation, result).Observe(duration.Seconds())
}

func (m *Metrics) ObserveStoreOperation(operation string, err error, duration time.Duration) {
	if m == nil {
		return
	}
	operation = boundedStoreOperation(operation)
	result := Result(err)
	switch {
	case errors.Is(err, context.Canceled):
		result = "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		result = "timeout"
	}
	m.ObserveStoreOperationResult(operation, result, duration)
}

func (m *Metrics) ObserveStoreOperationResult(operation, result string, duration time.Duration) {
	if m == nil {
		return
	}
	operation = boundedStoreOperation(operation)
	result = bounded(result, "success", "not_found", "conflict", "canceled", "timeout", "error")
	m.storeOperations.WithLabelValues(operation, result).Inc()
	m.storeDuration.WithLabelValues(operation, result).Observe(duration.Seconds())
}

func (m *Metrics) RegisterSQLiteStats(stats func() sql.DBStats) {
	if m == nil || stats == nil {
		return
	}
	m.sqliteStatsOnce.Do(func() {
		m.registry.MustRegister(
			prometheus.NewGaugeFunc(prometheus.GaugeOpts{
				Name: "trainpilot_sqlite_connections_in_use",
				Help: "SQLite connections currently in use.",
			}, func() float64 { return float64(stats().InUse) }),
			prometheus.NewCounterFunc(prometheus.CounterOpts{
				Name: "trainpilot_sqlite_wait_count_total",
				Help: "Total waits for a SQLite connection.",
			}, func() float64 { return float64(stats().WaitCount) }),
			prometheus.NewCounterFunc(prometheus.CounterOpts{
				Name: "trainpilot_sqlite_wait_duration_seconds_total",
				Help: "Total time spent waiting for a SQLite connection.",
			}, func() float64 { return stats().WaitDuration.Seconds() }),
		)
	})
}

func (m *Metrics) ObserveStoreTransaction(result string) {
	if m != nil {
		m.storeTransactions.WithLabelValues(bounded(result, "commit", "rollback", "begin_error", "commit_error", "panic")).Inc()
	}
}

func Result(err error) string {
	if err == nil {
		return "success"
	}
	return "error"
}

func BoundedProvider(provider string) string {
	provider = strings.ToLower(provider)
	switch {
	case provider == "simulator":
		return "simulator"
	case strings.HasPrefix(provider, "z21"):
		return "z21-rbus"
	case strings.HasPrefix(provider, "dccex") || strings.HasPrefix(provider, "dcc-ex"):
		return "dccex"
	default:
		return otherLabel
	}
}

func boundedMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions:
		return method
	default:
		return otherLabel
	}
}

func routeLabel(pattern string) string {
	if _, route, ok := strings.Cut(pattern, " "); ok {
		pattern = route
	}
	if pattern == "" {
		return "unmatched"
	}
	return pattern
}

func statusClass(status int) string {
	if status < 100 || status > 599 {
		return otherLabel
	}
	return fmt.Sprintf("%dxx", status/100)
}

func boundedEventType(eventType string) string {
	if eventType == "" || len(eventType) > 96 {
		return otherLabel
	}
	for _, r := range eventType {
		if (r < 'a' || r > 'z') && r != '.' && r != '_' {
			return otherLabel
		}
	}
	return eventType
}

func bounded(value string, allowed ...string) string {
	for _, candidate := range allowed {
		if value == candidate {
			return value
		}
	}
	return otherLabel
}

func boundedResult(result string) string {
	return bounded(result, "success", "denied", "conflict", "not_found", "offline", "error")
}

func boundedRouteResult(result string) string {
	return bounded(result, "success", "denied", "conflict", "occupied", "error")
}

func boundedTurnoutResult(result string) string {
	return bounded(result, "success", "denied", "invalid", "offline", "driver_error", "timeout", "interrupted", "error")
}

func boundedStoreOperation(operation string) string {
	return bounded(operation,
		"get_locomotive", "list_locomotives", "get_lease", "create_lease", "heartbeat_lease",
		"renew_lease", "list_live_leases", "release_lease", "map_feedback", "update_block",
		"get_turnout", "get_turnout_state", "list_turnouts", "update_turnout", "list_routes", "reserve_route",
		"activate_route", "release_route", "get_session", "touch_session")
}

type metricsResponseWriter struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
	onHijack    func()
}

func (w *metricsResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *metricsResponseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	return n, err
}

func (w *metricsResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *metricsResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *metricsResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("HTTP server does not support hijacking")
	}
	conn, rw, err := hijacker.Hijack()
	if err == nil {
		w.status = http.StatusSwitchingProtocols
		w.wroteHeader = true
		if w.onHijack != nil {
			w.onHijack()
		}
	}
	return conn, rw, err
}

func (w *metricsResponseWriter) Push(target string, options *http.PushOptions) error {
	if pusher, ok := w.ResponseWriter.(http.Pusher); ok {
		return pusher.Push(target, options)
	}
	return http.ErrNotSupported
}

func (w *metricsResponseWriter) ReadFrom(r io.Reader) (int64, error) {
	reader, ok := w.ResponseWriter.(io.ReaderFrom)
	if !ok {
		return io.Copy(struct{ io.Writer }{w}, r)
	}
	n, err := reader.ReadFrom(r)
	w.bytes += n
	return n, err
}
