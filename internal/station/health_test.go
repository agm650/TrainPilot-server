package station

import (
	"testing"
	"time"
)

func TestHealthTransitions(t *testing.T) {
	var tracker HealthTracker
	tracker.Connected()
	if got := tracker.Health().Connectivity; got != Degraded {
		t.Fatalf("after connect=%s want degraded", got)
	}
	tracker.ValidResponse()
	health := tracker.Health()
	if health.Connectivity != Online || health.LastSeen == nil {
		t.Fatalf("after response=%+v", health)
	}
	tracker.CommunicationError()
	if got := tracker.Health().Connectivity; got != Degraded {
		t.Fatalf("after error=%s want degraded", got)
	}
	tracker.mu.Lock()
	tracker.failureSince = time.Now().Add(-OfflineAfter)
	tracker.lastSeen = time.Now().Add(-OfflineAfter)
	tracker.mu.Unlock()
	if got := tracker.Health().Connectivity; got != Offline {
		t.Fatalf("after timeout=%s want offline", got)
	}
	tracker.ValidResponse()
	if got := tracker.Health().Connectivity; got != Online {
		t.Fatalf("after recovery=%s want online", got)
	}
}
