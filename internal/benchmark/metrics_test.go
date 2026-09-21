package benchmark

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/client"
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

func TestOperationRecorderSummarizesUnexpectedErrorsWithoutMessages(t *testing.T) {
	recorder := newOperationRecorder()
	recorder.StartMeasurement(time.Now())
	recorder.Record("throttle", time.Millisecond, &client.HTTPError{
		StatusCode: 409,
		Status:     "409 Conflict",
		Body:       `{"refreshToken":"secret"}`,
		Problem:    &client.Problem{Code: "lease_conflict"},
	})
	recorder.Record("throttle", time.Millisecond, context.DeadlineExceeded)

	summary := recorder.Summaries(time.Second, nil, nil)["throttle"]
	want := []OperationErrorDetail{
		{Kind: "http", HTTPStatus: 409, ProblemCode: "lease_conflict", Count: 1},
		{Kind: "timeout", Count: 1},
	}
	if !reflect.DeepEqual(summary.UnexpectedErrorDetails, want) {
		t.Fatalf("details=%+v, want %+v", summary.UnexpectedErrorDetails, want)
	}
	data, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret") || strings.Contains(string(data), "refreshToken") {
		t.Fatalf("unexpected error details leaked the raw error: %s", data)
	}
}

func TestOperationRecorderBoundsUnexpectedErrorDetails(t *testing.T) {
	recorder := newOperationRecorder()
	recorder.StartMeasurement(time.Now())
	for index := 0; index < 20; index++ {
		recorder.Record("read", time.Millisecond, &client.HTTPError{
			StatusCode: 400 + index,
			Problem:    &client.Problem{Code: fmt.Sprintf("error_%d", index)},
		})
	}
	details := recorder.Summaries(time.Second, nil, nil)["read"].UnexpectedErrorDetails
	if len(details) != maxUnexpectedErrorDetails {
		t.Fatalf("details=%d, want %d", len(details), maxUnexpectedErrorDetails)
	}
	var count int64
	for _, detail := range details {
		count += detail.Count
	}
	if count != 20 {
		t.Fatalf("detail count=%d, want 20", count)
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
