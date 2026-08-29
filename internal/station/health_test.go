package station

import (
	"testing"
	"time"
)

func TestHealthTransitions(t *testing.T) {
	offlineAfter := 3 * time.Second
	tracker := NewHealthTracker(offlineAfter)
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
	tracker.mu.RLock()
	firstFailure := tracker.failureSince
	tracker.mu.RUnlock()
	tracker.CommunicationError()
	tracker.mu.RLock()
	repeatedFailure := tracker.failureSince
	tracker.mu.RUnlock()
	if !repeatedFailure.Equal(firstFailure) {
		t.Fatalf("repeated error reset failureSince: first=%v repeated=%v", firstFailure, repeatedFailure)
	}

	margin := 100 * time.Millisecond
	tracker.mu.Lock()
	tracker.failureSince = time.Now().Add(-(offlineAfter - margin))
	tracker.mu.Unlock()
	if got := tracker.Health().Connectivity; got != Degraded {
		t.Fatalf("before timeout=%s want degraded", got)
	}
	tracker.mu.Lock()
	tracker.failureSince = time.Now().Add(-(offlineAfter + margin))
	tracker.mu.Unlock()
	if got := tracker.Health().Connectivity; got != Offline {
		t.Fatalf("after timeout=%s want offline", got)
	}
	tracker.ValidResponse()
	if got := tracker.Health().Connectivity; got != Online {
		t.Fatalf("after recovery=%s want online", got)
	}
	tracker.mu.RLock()
	clearedFailure := tracker.failureSince
	tracker.mu.RUnlock()
	if !clearedFailure.IsZero() {
		t.Fatalf("failureSince was not cleared after recovery: %v", clearedFailure)
	}
	tracker.CommunicationError()
	tracker.mu.RLock()
	newFailure := tracker.failureSince
	tracker.mu.RUnlock()
	if newFailure.IsZero() || !newFailure.After(firstFailure) {
		t.Fatalf("new failure did not start a new interval: first=%v new=%v", firstFailure, newFailure)
	}
}

func TestHealthTimeoutStartsAtCommunicationError(t *testing.T) {
	offlineAfter := 3 * time.Second
	tracker := NewHealthTracker(offlineAfter)
	now := time.Now()
	tracker.mu.Lock()
	tracker.startedAt = now.Add(-time.Minute)
	tracker.lastSeen = now.Add(-(offlineAfter + time.Second))
	tracker.failureSince = now.Add(-time.Second)
	tracker.mu.Unlock()

	if got := tracker.Health().Connectivity; got != Degraded {
		t.Fatalf("health=%s want degraded when lastSeen is old but failure is recent", got)
	}
}

func TestHealthWithoutInitialResponseUsesConfiguredOfflineAfter(t *testing.T) {
	offlineAfter := 30 * time.Second
	tracker := NewHealthTracker(offlineAfter)
	tracker.Connected()

	tracker.mu.Lock()
	tracker.failureSince = time.Now().Add(-(offlineAfter - 100*time.Millisecond))
	tracker.mu.Unlock()
	if got := tracker.Health().Connectivity; got != Degraded {
		t.Fatalf("health before initial-response timeout=%s want degraded", got)
	}

	tracker.mu.Lock()
	tracker.failureSince = time.Now().Add(-(offlineAfter + 100*time.Millisecond))
	tracker.mu.Unlock()
	if got := tracker.Health().Connectivity; got != Offline {
		t.Fatalf("health after initial-response timeout=%s want offline", got)
	}
}

func TestHealthTrackerUsesConfiguredOfflineAfter(t *testing.T) {
	for _, offlineAfter := range []time.Duration{3 * time.Second, 30 * time.Second} {
		t.Run(offlineAfter.String(), func(t *testing.T) {
			tracker := NewHealthTracker(offlineAfter)
			tracker.mu.Lock()
			tracker.startedAt = time.Now().Add(-time.Minute)
			tracker.failureSince = time.Now().Add(-(offlineAfter + 100*time.Millisecond))
			tracker.mu.Unlock()
			if got := tracker.Health().Connectivity; got != Offline {
				t.Fatalf("health=%s want offline after %v", got, offlineAfter)
			}
		})
	}
}

func TestNewHealthTrackerDefaultsNonPositiveThreshold(t *testing.T) {
	for _, offlineAfter := range []time.Duration{0, -time.Second} {
		tracker := NewHealthTracker(offlineAfter)
		if tracker.offlineAfter != DefaultOfflineAfter {
			t.Fatalf("offlineAfter=%v want %v", tracker.offlineAfter, DefaultOfflineAfter)
		}
	}
}
