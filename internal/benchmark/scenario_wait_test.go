package benchmark

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestWaitForScenarioRejectsIncompleteScenarioAtDeadline(t *testing.T) {
	done := make(chan scenarioRunResult)
	result, err := waitForScenario(context.Background(), time.Millisecond, done)
	if err == nil || !strings.Contains(err.Error(), "simulator scenario did not complete") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestWaitForScenarioAcceptsResultReadyAtDeadline(t *testing.T) {
	done := make(chan scenarioRunResult, 1)
	want := scenarioRunResult{summary: ScenarioSummary{Name: "ready", Status: "completed"}}
	done <- want

	result, err := waitForScenario(context.Background(), time.Millisecond, done)
	if err != nil {
		t.Fatal(err)
	}
	if result.summary.Name != want.summary.Name || result.summary.Status != want.summary.Status {
		t.Fatalf("result=%+v want=%+v", result, want)
	}
}
