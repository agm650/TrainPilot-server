package benchmark

import (
	"sync"
	"time"
)

type availabilityTracker struct {
	mu sync.Mutex

	downSince          time.Time
	downExpected       bool
	expectedOutages    int64
	unexpectedOutages  int64
	recoveries         int64
	totalUnavailable   time.Duration
	maximumUnavailable time.Duration
}

func (t *availabilityTracker) Record(at time.Time, available, expected bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if available {
		t.closeOutage(at, true)
		return
	}
	if t.downSince.IsZero() {
		t.downSince = at
		t.downExpected = expected
		if expected {
			t.expectedOutages++
		} else {
			t.unexpectedOutages++
		}
		return
	}
	if !expected && t.downExpected {
		t.downExpected = false
		t.expectedOutages--
		t.unexpectedOutages++
	}
}

func (t *availabilityTracker) Summary(endedAt time.Time) AvailabilitySummary {
	t.mu.Lock()
	defer t.mu.Unlock()
	unrecovered := int64(0)
	if !t.downSince.IsZero() {
		unrecovered = 1
		t.closeOutage(endedAt, false)
	}
	return AvailabilitySummary{
		ExpectedOutages:              t.expectedOutages,
		UnexpectedOutages:            t.unexpectedOutages,
		Recoveries:                   t.recoveries,
		TotalUnavailableMilliseconds: durationMilliseconds(t.totalUnavailable),
		MaxUnavailableMilliseconds:   durationMilliseconds(t.maximumUnavailable),
		UnrecoveredOutages:           unrecovered,
	}
}

func (t *availabilityTracker) closeOutage(at time.Time, recovered bool) {
	if t.downSince.IsZero() {
		return
	}
	duration := at.Sub(t.downSince)
	if duration < 0 {
		duration = 0
	}
	t.totalUnavailable += duration
	if duration > t.maximumUnavailable {
		t.maximumUnavailable = duration
	}
	if recovered {
		t.recoveries++
	}
	t.downSince = time.Time{}
	t.downExpected = false
}

func durationMilliseconds(duration time.Duration) float64 {
	return float64(duration) / float64(time.Millisecond)
}
