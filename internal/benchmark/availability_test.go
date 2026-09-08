package benchmark

import (
	"errors"
	"net"
	"testing"
	"time"
)

func TestAvailabilityTrackerSeparatesExpectedAndUnexpectedOutages(t *testing.T) {
	start := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	var tracker availabilityTracker
	tracker.Record(start, false, true)
	tracker.Record(start.Add(time.Second), false, false)
	tracker.Record(start.Add(3*time.Second), true, false)
	summary := tracker.Summary(start.Add(4 * time.Second))
	if summary.ExpectedOutages != 0 || summary.UnexpectedOutages != 1 || summary.Recoveries != 1 {
		t.Fatalf("summary=%+v", summary)
	}
	if summary.TotalUnavailableMilliseconds != 3000 || summary.MaxUnavailableMilliseconds != 3000 {
		t.Fatalf("duration summary=%+v", summary)
	}
}

func TestAvailabilityTrackerReportsUnrecoveredOutage(t *testing.T) {
	start := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	var tracker availabilityTracker
	tracker.Record(start, false, true)
	summary := tracker.Summary(start.Add(2 * time.Second))
	if summary.ExpectedOutages != 1 || summary.Recoveries != 0 || summary.UnrecoveredOutages != 1 {
		t.Fatalf("summary=%+v", summary)
	}
}

func TestExpectedErrorsAreBoundedToMeasuredWindow(t *testing.T) {
	from := Duration{Duration: time.Second}
	to := Duration{Duration: 2 * time.Second}
	engine := runEngine{profile: Profile{ExpectedErrors: []ExpectedErrorRule{{Operation: "health", Kinds: []string{"network"}, From: &from, To: &to}}}}
	started := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	engine.measurementStarted.Store(started.UnixNano())
	err := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("offline")}
	if engine.isExpectedErrorAt("health", err, started.Add(500*time.Millisecond)) {
		t.Fatal("error before window was expected")
	}
	if !engine.isExpectedErrorAt("health", err, started.Add(1500*time.Millisecond)) {
		t.Fatal("error inside window was unexpected")
	}
	if engine.isExpectedErrorAt("health", err, started.Add(2*time.Second)) {
		t.Fatal("window end must be exclusive")
	}
}
