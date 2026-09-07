package benchmark

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestCompareReportsUsesAnyGroupSizeAboveMinimumAndMedians(t *testing.T) {
	hardware := &HardwareMetadata{Model: "test-host", CPU: "test-cpu", Cores: 4, RAMBytes: 8 << 30}
	baseline := comparisonReports(4, "baseline", hardware, []float64{10, 12, 14, 100})
	candidate := comparisonReports(5, "candidate", hardware, []float64{11, 12, 14, 17, 100})
	comparison, err := CompareReports(baseline, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if !comparison.Comparable || comparison.Result != "PASS" || comparison.Baseline.Runs != 4 || comparison.Candidate.Runs != 5 {
		t.Fatalf("comparison=%+v", comparison)
	}
	p95 := comparison.Operations["throttle"].P95
	if p95.BaselineMedian != 13 || p95.CandidateMedian != 14 || p95.PercentChange == nil {
		t.Fatalf("p95=%+v", p95)
	}
}

func TestCompareReportsWarnsAtTenPercentAndNeverFailsForLatency(t *testing.T) {
	hardware := &HardwareMetadata{Model: "test-host"}
	baseline := comparisonReports(3, "baseline", hardware, []float64{100, 100, 100})
	candidate := comparisonReports(3, "candidate", hardware, []float64{120, 120, 120})
	comparison, err := CompareReports(baseline, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if comparison.Result != "WARN" {
		t.Fatalf("result=%s", comparison.Result)
	}
}

func TestCompareReportsDoesNotWarnFromOneLatencyOutlier(t *testing.T) {
	hardware := &HardwareMetadata{Model: "test-host"}
	baseline := comparisonReports(3, "baseline", hardware, []float64{40, 40, 40})
	candidate := comparisonReports(3, "candidate", hardware, []float64{40, 40, 60})
	candidate[2].OverallResult = "WARN"
	comparison, err := CompareReports(baseline, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if comparison.Result != "PASS" {
		t.Fatalf("result=%s", comparison.Result)
	}
}

func TestCompareReportsIsInformationalAcrossHardware(t *testing.T) {
	baseline := comparisonReports(3, "baseline", &HardwareMetadata{Model: "one"}, []float64{10, 10, 10})
	candidate := comparisonReports(3, "candidate", &HardwareMetadata{Model: "two"}, []float64{20, 20, 20})
	comparison, err := CompareReports(baseline, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if comparison.Comparable || comparison.Result != "" {
		t.Fatalf("comparison=%+v", comparison)
	}
}

func TestCompareReportsFailsForCandidateFunctionalError(t *testing.T) {
	hardware := &HardwareMetadata{Model: "test-host"}
	baseline := comparisonReports(3, "baseline", hardware, []float64{10, 10, 10})
	candidate := comparisonReports(3, "candidate", hardware, []float64{10, 10, 10})
	summary := candidate[1].Operations["throttle"]
	summary.UnexpectedErrors = 1
	candidate[1].Operations["throttle"] = summary
	comparison, err := CompareReports(baseline, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if comparison.Result != "FAIL" || comparison.Functional.CandidateFailedRuns != 1 {
		t.Fatalf("comparison=%+v", comparison)
	}
}

func TestCompareReportsRequiresAtLeastThreeRuns(t *testing.T) {
	hardware := &HardwareMetadata{Model: "test-host"}
	_, err := CompareReports(comparisonReports(2, "baseline", hardware, []float64{1, 1}), comparisonReports(3, "candidate", hardware, []float64{1, 1, 1}))
	if err == nil {
		t.Fatal("expected minimum run count error")
	}
}

func TestWriteComparisonUsesPrivatePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "comparison.json")
	if err := WriteComparison(path, Comparison{SchemaVersion: ComparisonSchemaVersion}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions=%04o", info.Mode().Perm())
	}
}

func comparisonReports(count int, prefix string, hardware *HardwareMetadata, p95 []float64) []Report {
	reports := make([]Report, count)
	for index := range reports {
		reports[index] = Report{
			SchemaVersion: ReportSchemaVersion,
			RunID:         fmt.Sprintf("00000000-0000-4000-8000-%012d", index+1+len(prefix)*100),
			Profile:       Profile{Name: "medium"}, ProfileSHA256: "same-profile",
			Hardware: hardware, OverallResult: "PASS",
			Operations: map[string]OperationSummary{
				"throttle": {AchievedRatePerSecond: 15, Latency: LatencySummary{P50Milliseconds: p95[index] / 2, P95Milliseconds: p95[index], P99Milliseconds: p95[index] * 1.2}},
			},
		}
	}
	return reports
}
