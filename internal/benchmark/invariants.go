package benchmark

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const (
	invariantExclusiveLease    = "exclusive_locomotive_lease"
	invariantLeaseRequired     = "valid_lease_required"
	invariantRouteConflict     = "incompatible_route_rejected"
	invariantWebSocketResync   = "websocket_resynchronization"
	invariantNoImplicitRestart = "no_implicit_restart"
	invariantActionState       = "action_state_consistency"
	invariantValidJSON         = "valid_json"
	invariantServerAvailable   = "server_availability"
)

var invariantNames = []string{
	invariantExclusiveLease,
	invariantLeaseRequired,
	invariantRouteConflict,
	invariantWebSocketResync,
	invariantNoImplicitRestart,
	invariantActionState,
	invariantValidJSON,
	invariantServerAvailable,
}

type invariantTracker struct {
	mu     sync.Mutex
	states map[string]*invariantState
}

type invariantState struct {
	observations int64
	violations   []string
}

func newInvariantTracker() *invariantTracker {
	tracker := &invariantTracker{states: make(map[string]*invariantState, len(invariantNames))}
	for _, name := range invariantNames {
		tracker.states[name] = &invariantState{}
	}
	return tracker
}

func (t *invariantTracker) Observe(name string) {
	t.mu.Lock()
	t.state(name).observations++
	t.mu.Unlock()
}

func (t *invariantTracker) Violate(name, detail string) {
	t.mu.Lock()
	state := t.state(name)
	state.observations++
	if len(state.violations) < 20 {
		state.violations = append(state.violations, detail)
	}
	t.mu.Unlock()
}

func (t *invariantTracker) state(name string) *invariantState {
	state := t.states[name]
	if state == nil {
		state = &invariantState{}
		t.states[name] = state
	}
	return state
}

func (t *invariantTracker) Results() ([]InvariantResult, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	names := make([]string, 0, len(t.states))
	for name := range t.states {
		names = append(names, name)
	}
	sort.Strings(names)
	results := make([]InvariantResult, 0, len(names))
	failed := false
	for _, name := range names {
		state := t.states[name]
		status := "PASS"
		if state.observations == 0 {
			status = "NOT_OBSERVED"
		}
		if len(state.violations) > 0 {
			status = "FAIL"
			failed = true
		}
		results = append(results, InvariantResult{
			Name: name, Status: status, Observations: state.observations,
			ViolationCount: int64(len(state.violations)), Violations: append([]string(nil), state.violations...),
		})
	}
	return results, failed
}

type pendingExpectation struct {
	id       uint64
	started  time.Time
	deadline time.Time
}

type expectationTracker struct {
	mu        sync.Mutex
	nextID    uint64
	pending   map[string][]pendingExpectation
	invariant *invariantTracker
}

func newExpectationTracker(invariants *invariantTracker) *expectationTracker {
	return &expectationTracker{pending: make(map[string][]pendingExpectation), invariant: invariants}
}

func (t *expectationTracker) Begin(key string, timeout time.Duration) uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.nextID++
	now := time.Now()
	t.pending[key] = append(t.pending[key], pendingExpectation{id: t.nextID, started: now, deadline: now.Add(timeout)})
	return t.nextID
}

func (t *expectationTracker) Cancel(key string, id uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	items := t.pending[key]
	for index, item := range items {
		if item.id == id {
			t.pending[key] = append(items[:index], items[index+1:]...)
			if len(t.pending[key]) == 0 {
				delete(t.pending, key)
			}
			return
		}
	}
}

func (t *expectationTracker) Fulfill(key string) (time.Duration, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	items := t.pending[key]
	if len(items) == 0 {
		return 0, false
	}
	item := items[0]
	if len(items) == 1 {
		delete(t.pending, key)
	} else {
		t.pending[key] = items[1:]
	}
	t.invariant.Observe(invariantActionState)
	return time.Since(item.started), true
}

func (t *expectationTracker) Expire(now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for key, items := range t.pending {
		remaining := items[:0]
		for _, item := range items {
			if !item.deadline.After(now) {
				t.invariant.Violate(invariantActionState, fmt.Sprintf("no matching event for %s", key))
			} else {
				remaining = append(remaining, item)
			}
		}
		if len(remaining) == 0 {
			delete(t.pending, key)
		} else {
			t.pending[key] = remaining
		}
	}
}

func (t *expectationTracker) Pending() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	total := 0
	for _, items := range t.pending {
		total += len(items)
	}
	return total
}

type webSocketMetrics struct {
	connections      atomic.Int64
	disconnections   atomic.Int64
	reconnects       atomic.Int64
	sequenceGaps     atomic.Int64
	unresolvedGaps   atomic.Int64
	snapshots        atomic.Int64
	snapshotRequests atomic.Int64
	eventsReceived   atomic.Int64
	invalidMessages  atomic.Int64
	feedbackMu       sync.Mutex
	feedbackLatency  []time.Duration
}

func (m *webSocketMetrics) summary() WebSocketSummary {
	m.feedbackMu.Lock()
	latencies := append([]time.Duration(nil), m.feedbackLatency...)
	m.feedbackMu.Unlock()
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	return WebSocketSummary{
		Connections: m.connections.Load(), Disconnections: m.disconnections.Load(),
		Reconnects: m.reconnects.Load(), SequenceGaps: m.sequenceGaps.Load(),
		UnresolvedSequenceGaps: m.unresolvedGaps.Load(),
		Snapshots:              m.snapshots.Load(), SnapshotRequests: m.snapshotRequests.Load(),
		EventsReceived: m.eventsReceived.Load(), InvalidMessages: m.invalidMessages.Load(),
		FeedbackLatency: latencyPercentiles(latencies),
	}
}

type eventMonitor struct {
	mu                 sync.Mutex
	invariants         *invariantTracker
	expectations       *expectationTracker
	webSocket          *webSocketMetrics
	processedSequences map[uint64]struct{}
	activeLeases       map[string]string
	routeStates        map[string]string
	incompatible       map[string]map[string]struct{}
	recovered          bool
	explicitThrottle   map[string]int
}

func newEventMonitor(invariants *invariantTracker, expectations *expectationTracker, webSocket *webSocketMetrics, fixture Fixture) *eventMonitor {
	monitor := &eventMonitor{
		invariants: invariants, expectations: expectations, webSocket: webSocket,
		processedSequences: make(map[uint64]struct{}), activeLeases: make(map[string]string),
		routeStates: make(map[string]string), incompatible: make(map[string]map[string]struct{}),
		explicitThrottle: make(map[string]int),
	}
	for _, pair := range fixture.IncompatibleRoutePairs {
		if monitor.incompatible[pair.First] == nil {
			monitor.incompatible[pair.First] = make(map[string]struct{})
		}
		if monitor.incompatible[pair.Second] == nil {
			monitor.incompatible[pair.Second] = make(map[string]struct{})
		}
		monitor.incompatible[pair.First][pair.Second] = struct{}{}
		monitor.incompatible[pair.Second][pair.First] = struct{}{}
	}
	return monitor
}

func (m *eventMonitor) markExplicitThrottle(locomotiveID string) {
	m.mu.Lock()
	m.explicitThrottle[locomotiveID]++
	m.mu.Unlock()
}

func (m *eventMonitor) processSnapshot(payload map[string]any) error {
	var snapshot struct {
		Routes []struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"routes"`
	}
	if err := decodePayload(payload, &snapshot); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, route := range snapshot.Routes {
		m.routeStates[route.ID] = route.State
	}
	for routeID, conflicts := range m.incompatible {
		if m.routeStates[routeID] != "active" {
			continue
		}
		for conflictID := range conflicts {
			if m.routeStates[conflictID] == "active" {
				m.invariants.Violate(invariantRouteConflict, fmt.Sprintf("snapshot contains active incompatible routes %s and %s", routeID, conflictID))
			}
		}
	}
	return nil
}

func (m *eventMonitor) unmarkExplicitThrottle(locomotiveID string) {
	m.mu.Lock()
	if m.explicitThrottle[locomotiveID] <= 1 {
		delete(m.explicitThrottle, locomotiveID)
	} else {
		m.explicitThrottle[locomotiveID]--
	}
	m.mu.Unlock()
}

func (m *eventMonitor) process(sequence uint64, eventType string, payload map[string]any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sequence > 0 {
		if _, exists := m.processedSequences[sequence]; exists {
			return
		}
		m.processedSequences[sequence] = struct{}{}
	}
	switch eventType {
	case "locomotive.control.acquired", "locomotive.control.transferred":
		locomotiveID, _ := payload["locomotiveId"].(string)
		leaseID, _ := payload["leaseId"].(string)
		if previous := m.activeLeases[locomotiveID]; previous != "" && previous != leaseID {
			m.invariants.Violate(invariantExclusiveLease, fmt.Sprintf("locomotive %s has leases %s and %s", locomotiveID, previous, leaseID))
		} else if locomotiveID != "" && leaseID != "" {
			m.activeLeases[locomotiveID] = leaseID
			m.invariants.Observe(invariantExclusiveLease)
		}
	case "locomotive.control.released", "locomotive.control.expired":
		locomotiveID, _ := payload["locomotiveId"].(string)
		delete(m.activeLeases, locomotiveID)
	case "station.status.changed":
		connectivity, _ := payload["connectivity"].(string)
		if connectivity == "offline" {
			m.recovered = false
			m.explicitThrottle = make(map[string]int)
		}
		if connectivity == "online" {
			m.recovered = true
			m.invariants.Observe(invariantNoImplicitRestart)
		}
	case "locomotive.speed.changed":
		locomotiveID, _ := payload["locomotiveId"].(string)
		speed, _ := numericInt(payload["speed"])
		if m.recovered && speed > 0 && m.explicitThrottle[locomotiveID] == 0 {
			m.invariants.Violate(invariantNoImplicitRestart, fmt.Sprintf("locomotive %s restarted without an explicit throttle command", locomotiveID))
		}
		m.expectations.Fulfill(fmt.Sprintf("throttle:%s:%d", locomotiveID, speed))
	case "locomotive.function.changed":
		locomotiveID, _ := payload["locomotiveId"].(string)
		function, _ := numericInt(payload["function"])
		enabled, _ := payload["enabled"].(bool)
		m.expectations.Fulfill(fmt.Sprintf("function:%s:%d:%t", locomotiveID, function, enabled))
	case "turnout.state.changed":
		turnoutID, _ := payload["turnoutId"].(string)
		position, _ := payload["reportedPosition"].(string)
		if position == "" {
			position, _ = payload["position"].(string)
		}
		m.expectations.Fulfill(fmt.Sprintf("turnout:%s:%s", turnoutID, position))
	case "block.occupancy.changed":
		blockID, _ := payload["blockId"].(string)
		occupied, _ := payload["occupied"].(bool)
		if latency, ok := m.expectations.Fulfill(fmt.Sprintf("feedback:%s:%t", blockID, occupied)); ok {
			m.webSocket.feedbackMu.Lock()
			m.webSocket.feedbackLatency = append(m.webSocket.feedbackLatency, latency)
			m.webSocket.feedbackMu.Unlock()
		}
	case "route.reserved":
		routeID, _ := payload["routeId"].(string)
		m.routeStates[routeID] = "reserved"
	case "route.activated":
		routeID, _ := payload["routeId"].(string)
		for conflictID := range m.incompatible[routeID] {
			if state := m.routeStates[conflictID]; state == "reserved" || state == "active" {
				m.invariants.Violate(invariantRouteConflict, fmt.Sprintf("route %s activated while %s is %s", routeID, conflictID, state))
			}
		}
		m.routeStates[routeID] = "active"
		m.invariants.Observe(invariantRouteConflict)
	case "route.released":
		routeID, _ := payload["routeId"].(string)
		m.routeStates[routeID] = "idle"
	}
}

func numericInt(value any) (int, bool) {
	switch number := value.(type) {
	case float64:
		return int(number), number == float64(int(number))
	case int:
		return number, true
	default:
		return 0, false
	}
}
