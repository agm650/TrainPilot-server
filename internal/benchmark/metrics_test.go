package benchmark

import (
	"errors"
	"testing"
	"time"
)

func TestOperationRecorderProducesExactPercentiles(t *testing.T) {
	recorder := newOperationRecorder()
	recorder.Record("read", time.Second, nil)
	recorder.StartMeasurement(time.Now())
	recorder.Skip("read")
	for value := 1; value <= 100; value++ {
		var err error
		if value == 100 {
			err = errors.New("failure")
		} else if value == 99 {
			err = expectedError(errors.New("expected conflict"))
		}
		recorder.Record("read", time.Duration(value)*time.Millisecond, err)
	}
	rate := 12.0
	summary := recorder.Summaries(10*time.Second, map[string]float64{"read": rate}, nil)["read"]
	if summary.Count != 100 || summary.Successes != 98 || summary.ExpectedErrors != 1 || summary.UnexpectedErrors != 1 || summary.Skipped != 1 || summary.AchievedRatePerSecond != 10 {
		t.Fatalf("summary=%+v", summary)
	}
	if summary.RequestedRatePerSecond == nil || *summary.RequestedRatePerSecond != rate {
		t.Fatalf("requested rate=%v", summary.RequestedRatePerSecond)
	}
	if summary.Latency.P50Milliseconds != 50 || summary.Latency.P90Milliseconds != 90 || summary.Latency.P95Milliseconds != 95 || summary.Latency.P99Milliseconds != 99 || summary.Latency.MaxMilliseconds != 100 {
		t.Fatalf("latency=%+v", summary.Latency)
	}
}

func TestReportPolicySeparatesExpectedAndUnexpectedErrors(t *testing.T) {
	report := Report{Profile: Profile{Name: "smoke"}, Operations: map[string]OperationSummary{"contention": {Count: 1, ExpectedErrors: 1}}}
	ApplyReportPolicy(&report)
	if report.OverallResult != "PASS" {
		t.Fatalf("expected error result=%s", report.OverallResult)
	}
	summary := report.Operations["contention"]
	summary.ExpectedErrors = 0
	summary.UnexpectedErrors = 1
	report.Operations["contention"] = summary
	ApplyReportPolicy(&report)
	if report.OverallResult != "FAIL" {
		t.Fatalf("unexpected error result=%s", report.OverallResult)
	}
}
