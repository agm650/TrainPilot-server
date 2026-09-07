package benchmark

import (
	"testing"
	"time"
)

func TestInvariantViolationForcesFailure(t *testing.T) {
	tracker := newInvariantTracker()
	tracker.Observe(invariantValidJSON)
	tracker.Violate(invariantExclusiveLease, "duplicate lease")
	results, failed := tracker.Results()
	if !failed {
		t.Fatal("expected invariant failure")
	}
	statuses := make(map[string]string)
	for _, result := range results {
		statuses[result.Name] = result.Status
	}
	if statuses[invariantExclusiveLease] != "FAIL" || statuses[invariantValidJSON] != "PASS" || statuses[invariantRouteConflict] != "NOT_OBSERVED" {
		t.Fatalf("statuses=%v", statuses)
	}
}

func TestExpectationExpiryIsAnInvariantViolation(t *testing.T) {
	invariants := newInvariantTracker()
	expectations := newExpectationTracker(invariants)
	expectations.Begin("throttle:loco:50", time.Millisecond)
	expectations.Expire(time.Now().Add(time.Second))
	_, failed := invariants.Results()
	if !failed {
		t.Fatal("expected missing event to fail")
	}
}

func TestImplicitRestartRequiresExplicitThrottle(t *testing.T) {
	invariants := newInvariantTracker()
	expectations := newExpectationTracker(invariants)
	monitor := newEventMonitor(invariants, expectations, &webSocketMetrics{}, Fixture{})
	monitor.process(1, "station.status.changed", map[string]any{"connectivity": "offline"})
	monitor.process(2, "station.status.changed", map[string]any{"connectivity": "online"})
	monitor.process(3, "locomotive.speed.changed", map[string]any{"locomotiveId": "loco-1", "speed": float64(30)})
	_, failed := invariants.Results()
	if !failed {
		t.Fatal("expected implicit restart violation")
	}

	invariants = newInvariantTracker()
	expectations = newExpectationTracker(invariants)
	monitor = newEventMonitor(invariants, expectations, &webSocketMetrics{}, Fixture{})
	monitor.process(1, "station.status.changed", map[string]any{"connectivity": "offline"})
	monitor.process(2, "station.status.changed", map[string]any{"connectivity": "online"})
	monitor.markExplicitThrottle("loco-1")
	monitor.process(3, "locomotive.speed.changed", map[string]any{"locomotiveId": "loco-1", "speed": float64(30)})
	_, failed = invariants.Results()
	if failed {
		t.Fatal("explicit throttle was reported as an implicit restart")
	}
}

func TestEventMonitorDetectsExclusiveLeaseAndRouteConflict(t *testing.T) {
	invariants := newInvariantTracker()
	expectations := newExpectationTracker(invariants)
	fixture := Fixture{IncompatibleRoutePairs: []IncompatibleRoutePair{{First: "route-a", Second: "route-b"}}}
	monitor := newEventMonitor(invariants, expectations, &webSocketMetrics{}, fixture)
	monitor.process(1, "locomotive.control.acquired", map[string]any{"locomotiveId": "loco-1", "leaseId": "lease-a"})
	monitor.process(2, "locomotive.control.acquired", map[string]any{"locomotiveId": "loco-1", "leaseId": "lease-b"})
	monitor.process(3, "route.reserved", map[string]any{"routeId": "route-a"})
	monitor.process(4, "route.activated", map[string]any{"routeId": "route-b"})
	results, failed := invariants.Results()
	if !failed {
		t.Fatal("expected invariant failures")
	}
	statuses := make(map[string]string)
	for _, result := range results {
		statuses[result.Name] = result.Status
	}
	if statuses[invariantExclusiveLease] != "FAIL" || statuses[invariantRouteConflict] != "FAIL" {
		t.Fatalf("statuses=%v", statuses)
	}
}
