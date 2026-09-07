package benchmark

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const ComparisonSchemaVersion = 1

type Comparison struct {
	SchemaVersion int                            `json:"schemaVersion"`
	GeneratedAt   time.Time                      `json:"generatedAt"`
	Baseline      ComparisonGroup                `json:"baseline"`
	Candidate     ComparisonGroup                `json:"candidate"`
	Comparable    bool                           `json:"comparable"`
	Reason        string                         `json:"reason,omitempty"`
	Result        string                         `json:"result,omitempty"`
	Operations    map[string]OperationComparison `json:"operations"`
	WebSocket     map[string]MetricComparison    `json:"webSocket"`
	SystemMetrics map[string]MetricComparison    `json:"systemMetrics,omitempty"`
	Functional    FunctionalComparison           `json:"functional"`
}

type ComparisonGroup struct {
	Runs                int               `json:"runs"`
	ProfileName         string            `json:"profileName"`
	ProfileSHA256       string            `json:"profileSha256"`
	Hardware            *HardwareMetadata `json:"hardware,omitempty"`
	BenchmarkVersion    string            `json:"benchmarkVersion,omitempty"`
	ServerVersion       string            `json:"serverVersion,omitempty"`
	ServerGitCommit     string            `json:"serverGitCommit,omitempty"`
	ServerConfiguration map[string]string `json:"serverConfiguration,omitempty"`
}

type MetricComparison struct {
	BaselineMedian  float64  `json:"baselineMedian"`
	CandidateMedian float64  `json:"candidateMedian"`
	Delta           float64  `json:"delta"`
	PercentChange   *float64 `json:"percentChange,omitempty"`
}

type OperationComparison struct {
	P50              MetricComparison `json:"p50Milliseconds"`
	P95              MetricComparison `json:"p95Milliseconds"`
	P99              MetricComparison `json:"p99Milliseconds"`
	Throughput       MetricComparison `json:"achievedRatePerSecond"`
	ExpectedErrors   MetricComparison `json:"expectedErrors"`
	UnexpectedErrors MetricComparison `json:"unexpectedErrors"`
}

type FunctionalComparison struct {
	BaselineFailedRuns  int64 `json:"baselineFailedRuns"`
	CandidateFailedRuns int64 `json:"candidateFailedRuns"`
	BaselineViolations  int64 `json:"baselineInvariantViolations"`
	CandidateViolations int64 `json:"candidateInvariantViolations"`
	BaselineUnexpected  int64 `json:"baselineUnexpectedErrors"`
	CandidateUnexpected int64 `json:"candidateUnexpectedErrors"`
}

func CompareReports(baseline, candidate []Report) (Comparison, error) {
	if err := validateComparisonGroup("baseline", baseline); err != nil {
		return Comparison{}, err
	}
	if err := validateComparisonGroup("candidate", candidate); err != nil {
		return Comparison{}, err
	}
	baselineIDs := make(map[string]bool, len(baseline))
	for _, report := range baseline {
		baselineIDs[report.RunID] = true
	}
	for _, report := range candidate {
		if baselineIDs[report.RunID] {
			return Comparison{}, fmt.Errorf("runId %q is present in both groups", report.RunID)
		}
	}
	comparison := Comparison{
		SchemaVersion: ComparisonSchemaVersion,
		GeneratedAt:   time.Now().UTC(),
		Baseline:      comparisonGroup(baseline), Candidate: comparisonGroup(candidate),
		Operations:    compareOperations(baseline, candidate),
		WebSocket:     compareWebSocket(baseline, candidate),
		SystemMetrics: compareSystemMetrics(baseline, candidate),
		Functional:    compareFunctional(baseline, candidate),
	}
	comparison.Comparable, comparison.Reason = comparableGroups(comparison.Baseline, comparison.Candidate)
	if comparison.Comparable {
		comparison.Result = comparisonResult(comparison, candidate)
	}
	return comparison, nil
}

func validateComparisonGroup(name string, reports []Report) error {
	if len(reports) < 3 {
		return fmt.Errorf("%s requires at least 3 reports", name)
	}
	profileHash := reports[0].ProfileSHA256
	hardwareKey := canonicalHardware(reports[0].Hardware)
	configurationKey := canonicalConfiguration(reports[0].Server.Configuration)
	seen := make(map[string]bool, len(reports))
	for index, report := range reports {
		if report.SchemaVersion != ReportSchemaVersion {
			return fmt.Errorf("%s report %d has unsupported schemaVersion %d", name, index+1, report.SchemaVersion)
		}
		if report.RunID == "" {
			return fmt.Errorf("%s report %d has no runId", name, index+1)
		}
		if !validRunID(report.RunID) {
			return fmt.Errorf("%s report %d has invalid runId %q", name, index+1, report.RunID)
		}
		if seen[report.RunID] {
			return fmt.Errorf("%s contains duplicate runId %q", name, report.RunID)
		}
		seen[report.RunID] = true
		if report.ProfileSHA256 != profileHash {
			return fmt.Errorf("%s reports do not share one profileSha256", name)
		}
		if canonicalHardware(report.Hardware) != hardwareKey {
			return fmt.Errorf("%s reports do not share one hardware description", name)
		}
		if report.BenchmarkVersion != reports[0].BenchmarkVersion {
			return fmt.Errorf("%s reports do not share one benchmarkVersion", name)
		}
		if report.Server.ServerVersion != reports[0].Server.ServerVersion || report.Server.GitCommit != reports[0].Server.GitCommit {
			return fmt.Errorf("%s reports do not share one server version and commit", name)
		}
		if canonicalConfiguration(report.Server.Configuration) != configurationKey {
			return fmt.Errorf("%s reports do not share one server configuration", name)
		}
	}
	return nil
}

func comparisonGroup(reports []Report) ComparisonGroup {
	return ComparisonGroup{
		Runs: len(reports), ProfileName: reports[0].Profile.Name,
		ProfileSHA256: reports[0].ProfileSHA256, Hardware: reports[0].Hardware,
		BenchmarkVersion: reports[0].BenchmarkVersion,
		ServerVersion:    reports[0].Server.ServerVersion, ServerGitCommit: reports[0].Server.GitCommit,
		ServerConfiguration: reports[0].Server.Configuration,
	}
}

func canonicalHardware(hardware *HardwareMetadata) string {
	if hardware == nil {
		return ""
	}
	data, _ := json.Marshal(hardware)
	return string(data)
}

func canonicalConfiguration(configuration map[string]string) string {
	if len(configuration) == 0 {
		return ""
	}
	data, _ := json.Marshal(configuration)
	return string(data)
}

func comparableGroups(baseline, candidate ComparisonGroup) (bool, string) {
	if baseline.ProfileSHA256 != candidate.ProfileSHA256 {
		return false, "profile hashes differ; comparison is informational"
	}
	if baseline.Hardware == nil || candidate.Hardware == nil {
		return false, "hardware metadata is missing; comparison is informational"
	}
	if canonicalHardware(baseline.Hardware) != canonicalHardware(candidate.Hardware) {
		return false, "hardware differs; comparison is informational"
	}
	if canonicalConfiguration(baseline.ServerConfiguration) != canonicalConfiguration(candidate.ServerConfiguration) {
		return false, "server configuration differs; comparison is informational"
	}
	return true, ""
}

func compareOperations(baseline, candidate []Report) map[string]OperationComparison {
	names := make(map[string]bool)
	for _, report := range append(append([]Report(nil), baseline...), candidate...) {
		for name := range report.Operations {
			names[name] = true
		}
	}
	result := make(map[string]OperationComparison, len(names))
	for name := range names {
		result[name] = OperationComparison{
			P50:              compareMetric(operationValues(baseline, name, func(summary OperationSummary) float64 { return summary.Latency.P50Milliseconds }), operationValues(candidate, name, func(summary OperationSummary) float64 { return summary.Latency.P50Milliseconds })),
			P95:              compareMetric(operationValues(baseline, name, func(summary OperationSummary) float64 { return summary.Latency.P95Milliseconds }), operationValues(candidate, name, func(summary OperationSummary) float64 { return summary.Latency.P95Milliseconds })),
			P99:              compareMetric(operationValues(baseline, name, func(summary OperationSummary) float64 { return summary.Latency.P99Milliseconds }), operationValues(candidate, name, func(summary OperationSummary) float64 { return summary.Latency.P99Milliseconds })),
			Throughput:       compareMetric(operationValues(baseline, name, func(summary OperationSummary) float64 { return summary.AchievedRatePerSecond }), operationValues(candidate, name, func(summary OperationSummary) float64 { return summary.AchievedRatePerSecond })),
			ExpectedErrors:   compareMetric(operationValues(baseline, name, func(summary OperationSummary) float64 { return float64(summary.ExpectedErrors) }), operationValues(candidate, name, func(summary OperationSummary) float64 { return float64(summary.ExpectedErrors) })),
			UnexpectedErrors: compareMetric(operationValues(baseline, name, func(summary OperationSummary) float64 { return float64(summary.UnexpectedErrors) }), operationValues(candidate, name, func(summary OperationSummary) float64 { return float64(summary.UnexpectedErrors) })),
		}
	}
	return result
}

func operationValues(reports []Report, name string, selectValue func(OperationSummary) float64) []float64 {
	values := make([]float64, len(reports))
	for index, report := range reports {
		values[index] = selectValue(report.Operations[name])
	}
	return values
}

func compareWebSocket(baseline, candidate []Report) map[string]MetricComparison {
	result := map[string]MetricComparison{
		"sequenceGaps":            compareMetric(reportValues(baseline, func(report Report) float64 { return float64(report.WebSocket.SequenceGaps) }), reportValues(candidate, func(report Report) float64 { return float64(report.WebSocket.SequenceGaps) })),
		"unresolvedSequenceGaps":  compareMetric(reportValues(baseline, func(report Report) float64 { return float64(report.WebSocket.UnresolvedSequenceGaps) }), reportValues(candidate, func(report Report) float64 { return float64(report.WebSocket.UnresolvedSequenceGaps) })),
		"feedbackP95Milliseconds": compareMetric(reportValues(baseline, func(report Report) float64 { return report.WebSocket.FeedbackLatency.P95Milliseconds }), reportValues(candidate, func(report Report) float64 { return report.WebSocket.FeedbackLatency.P95Milliseconds })),
		"feedbackP99Milliseconds": compareMetric(reportValues(baseline, func(report Report) float64 { return report.WebSocket.FeedbackLatency.P99Milliseconds }), reportValues(candidate, func(report Report) float64 { return report.WebSocket.FeedbackLatency.P99Milliseconds })),
	}
	if values, ok := optionalReportValues(baseline, func(report Report) *int64 { return report.WebSocket.QueueOverflows }); ok {
		if candidateValues, candidateOK := optionalReportValues(candidate, func(report Report) *int64 { return report.WebSocket.QueueOverflows }); candidateOK {
			result["queueOverflows"] = compareMetric(values, candidateValues)
		}
	}
	return result
}

func compareSystemMetrics(baseline, candidate []Report) map[string]MetricComparison {
	type systemSelector func(SystemMetricsSummary) *float64
	selectors := map[string]systemSelector{
		"processCpuMaxPercent": func(summary SystemMetricsSummary) *float64 { return summary.ProcessCPUMaxPercent },
		"processRssMaxBytes":   func(summary SystemMetricsSummary) *float64 { return int64AsFloat(summary.ProcessRSSMaxBytes) },
		"hostCpuMaxPercent":    func(summary SystemMetricsSummary) *float64 { return summary.HostCPUMaxPercent },
		"hostRamMaxBytes":      func(summary SystemMetricsSummary) *float64 { return int64AsFloat(summary.HostRAMMaxBytes) },
	}
	result := make(map[string]MetricComparison)
	for name, selector := range selectors {
		baselineValues, baselineOK := systemMetricValues(baseline, selector)
		candidateValues, candidateOK := systemMetricValues(candidate, selector)
		if baselineOK && candidateOK {
			result[name] = compareMetric(baselineValues, candidateValues)
		}
	}
	return result
}

func int64AsFloat(value *int64) *float64 {
	if value == nil {
		return nil
	}
	converted := float64(*value)
	return &converted
}

func systemMetricValues(reports []Report, selector func(SystemMetricsSummary) *float64) ([]float64, bool) {
	values := make([]float64, len(reports))
	for index, report := range reports {
		if report.SystemMetrics == nil {
			return nil, false
		}
		value := selector(*report.SystemMetrics)
		if value == nil {
			return nil, false
		}
		values[index] = *value
	}
	return values, true
}

func reportValues(reports []Report, selector func(Report) float64) []float64 {
	values := make([]float64, len(reports))
	for index, report := range reports {
		values[index] = selector(report)
	}
	return values
}

func optionalReportValues(reports []Report, selector func(Report) *int64) ([]float64, bool) {
	values := make([]float64, len(reports))
	for index, report := range reports {
		value := selector(report)
		if value == nil {
			return nil, false
		}
		values[index] = float64(*value)
	}
	return values, true
}

func compareMetric(baseline, candidate []float64) MetricComparison {
	baselineMedian := median(baseline)
	candidateMedian := median(candidate)
	comparison := MetricComparison{BaselineMedian: baselineMedian, CandidateMedian: candidateMedian, Delta: candidateMedian - baselineMedian}
	if baselineMedian != 0 {
		change := comparison.Delta / baselineMedian * 100
		comparison.PercentChange = &change
	}
	return comparison
}

func median(values []float64) float64 {
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	middle := len(ordered) / 2
	if len(ordered)%2 == 1 {
		return ordered[middle]
	}
	return (ordered[middle-1] + ordered[middle]) / 2
}

func compareFunctional(baseline, candidate []Report) FunctionalComparison {
	failedRuns := func(reports []Report) (int64, int64, int64) {
		failed, violations, unexpected := int64(0), int64(0), int64(0)
		for _, report := range reports {
			if reportHasFunctionalFailure(report) {
				failed++
			}
			for _, invariant := range report.Invariants {
				violations += invariant.ViolationCount
			}
			for _, operation := range report.Operations {
				unexpected += operation.UnexpectedErrors
			}
		}
		return failed, violations, unexpected
	}
	baselineFailed, baselineViolations, baselineUnexpected := failedRuns(baseline)
	candidateFailed, candidateViolations, candidateUnexpected := failedRuns(candidate)
	return FunctionalComparison{
		BaselineFailedRuns: baselineFailed, CandidateFailedRuns: candidateFailed,
		BaselineViolations: baselineViolations, CandidateViolations: candidateViolations,
		BaselineUnexpected: baselineUnexpected, CandidateUnexpected: candidateUnexpected,
	}
}

func comparisonResult(comparison Comparison, candidate []Report) string {
	if comparison.Functional.CandidateFailedRuns > 0 {
		return "FAIL"
	}
	for _, operation := range comparison.Operations {
		if operation.P95.PercentChange != nil && *operation.P95.PercentChange >= 10 {
			return "WARN"
		}
	}
	if gaps := comparison.WebSocket["unresolvedSequenceGaps"]; gaps.CandidateMedian > 0 {
		return "WARN"
	}
	if comparison.Candidate.ProfileName == "medium" || comparison.Candidate.ProfileName == "large" {
		for name, operation := range comparison.Operations {
			if initialCommandOperation(name) && (operation.P95.CandidateMedian >= 50 || operation.P99.CandidateMedian >= 100) {
				return "WARN"
			}
		}
		if feedback := comparison.WebSocket["feedbackP95Milliseconds"]; feedback.CandidateMedian >= 100 {
			return "WARN"
		}
		for _, report := range candidate {
			for _, warning := range report.Warnings {
				if warning.Name == "websocket.queue_overflows" || warning.Name == "sqlite.errors" || warning.Name == "network.drops" || warning.Name == "swap.max_bytes" || warning.Name == "thermal.throttling_events" {
					return "WARN"
				}
			}
		}
	}
	return "PASS"
}

func initialCommandOperation(name string) bool {
	switch name {
	case "lease_acquire", "lease_heartbeat", "lease_release", "throttle", "function", "accessory", "route":
		return true
	default:
		return false
	}
}

func WriteComparison(path string, comparison Comparison) error {
	if path == "" {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create comparison directory: %w", err)
	}
	temporary, err := os.CreateTemp(dir, ".trainpilot-bench-comparison-*.json")
	if err != nil {
		return fmt.Errorf("create comparison: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(comparison); err != nil {
		temporary.Close()
		return fmt.Errorf("encode comparison: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("publish comparison: %w", err)
	}
	return nil
}

func WriteComparisonSummary(w io.Writer, comparison Comparison) {
	if comparison.Comparable {
		fmt.Fprintf(w, "Comparison: %s (%d baseline, %d candidate runs)\n", comparison.Result, comparison.Baseline.Runs, comparison.Candidate.Runs)
	} else {
		fmt.Fprintf(w, "Comparison: INFORMATIONAL (%s)\n", comparison.Reason)
	}
	names := make([]string, 0, len(comparison.Operations))
	for name := range comparison.Operations {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		operation := comparison.Operations[name]
		fmt.Fprintf(w, "%-20s p50 %s | p95 %s | p99 %s | rate %s | expected %.1f -> %.1f | unexpected %.1f -> %.1f\n",
			name, formatMetric(operation.P50, "ms"), formatMetric(operation.P95, "ms"),
			formatMetric(operation.P99, "ms"), formatMetric(operation.Throughput, "/s"),
			operation.ExpectedErrors.BaselineMedian, operation.ExpectedErrors.CandidateMedian,
			operation.UnexpectedErrors.BaselineMedian, operation.UnexpectedErrors.CandidateMedian)
	}
	webSocketNames := make([]string, 0, len(comparison.WebSocket))
	for name := range comparison.WebSocket {
		webSocketNames = append(webSocketNames, name)
	}
	sort.Strings(webSocketNames)
	for _, name := range webSocketNames {
		fmt.Fprintf(w, "websocket.%-20s %s\n", name, formatMetric(comparison.WebSocket[name], ""))
	}
	systemNames := make([]string, 0, len(comparison.SystemMetrics))
	for name := range comparison.SystemMetrics {
		systemNames = append(systemNames, name)
	}
	sort.Strings(systemNames)
	for _, name := range systemNames {
		fmt.Fprintf(w, "system.%-23s %s\n", name, formatMetric(comparison.SystemMetrics[name], ""))
	}
	fmt.Fprintf(w, "functional failed_runs=%d -> %d invariant_violations=%d -> %d unexpected_errors=%d -> %d\n",
		comparison.Functional.BaselineFailedRuns, comparison.Functional.CandidateFailedRuns,
		comparison.Functional.BaselineViolations, comparison.Functional.CandidateViolations,
		comparison.Functional.BaselineUnexpected, comparison.Functional.CandidateUnexpected)
}

func formatMetric(metric MetricComparison, unit string) string {
	change := "n/a"
	if metric.PercentChange != nil {
		change = fmt.Sprintf("%+.1f%%", *metric.PercentChange)
	}
	return fmt.Sprintf("%.2f%s -> %.2f%s (%s)", metric.BaselineMedian, unit, metric.CandidateMedian, unit, change)
}

func LoadReports(paths []string) ([]Report, error) {
	if len(paths) == 0 {
		return nil, errors.New("no report paths provided")
	}
	reports := make([]Report, 0, len(paths))
	for _, path := range paths {
		report, err := LoadReport(strings.TrimSpace(path))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		reports = append(reports, report)
	}
	return reports, nil
}
