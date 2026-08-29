package station

import (
	"sync"
	"time"
)

const DefaultOfflineAfter = 10 * time.Second

type HealthTracker struct {
	mu           sync.RWMutex
	startedAt    time.Time
	lastSeen     time.Time
	failureSince time.Time
	offlineAfter time.Duration
}

func NewHealthTracker(offlineAfter time.Duration) HealthTracker {
	if offlineAfter <= 0 {
		offlineAfter = DefaultOfflineAfter
	}
	return HealthTracker{offlineAfter: offlineAfter}
}

func (h *HealthTracker) Connected() {
	h.mu.Lock()
	h.startedAt = time.Now()
	h.lastSeen = time.Time{}
	h.failureSince = h.startedAt
	h.mu.Unlock()
}

func (h *HealthTracker) ValidResponse() {
	h.mu.Lock()
	h.lastSeen = time.Now()
	h.failureSince = time.Time{}
	h.mu.Unlock()
}

func (h *HealthTracker) CommunicationError() {
	h.mu.Lock()
	if h.failureSince.IsZero() {
		h.failureSince = time.Now()
	}
	h.mu.Unlock()
}

func (h *HealthTracker) Health() Health {
	h.mu.RLock()
	started, seen, failed, offlineAfter := h.startedAt, h.lastSeen, h.failureSince, h.offlineAfter
	h.mu.RUnlock()
	if offlineAfter <= 0 {
		offlineAfter = DefaultOfflineAfter
	}
	now := time.Now()
	connectivity := Online
	if !failed.IsZero() {
		connectivity = Degraded
		if now.Sub(failed) >= offlineAfter {
			connectivity = Offline
		}
	}
	if started.IsZero() {
		connectivity = Offline
	}
	var lastSeen *time.Time
	if !seen.IsZero() {
		copy := seen
		lastSeen = &copy
	}
	return Health{Connectivity: connectivity, LastSeen: lastSeen}
}
