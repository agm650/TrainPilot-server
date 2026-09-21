package benchmark

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/agm650/TrainPilot-server/internal/client"
)

const ReportSchemaVersion = 3

func newRunID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate run ID: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}

func validRunID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	compact := strings.ReplaceAll(value, "-", "")
	decoded, err := hex.DecodeString(compact)
	return err == nil && len(decoded) == 16
}

type Report struct {
	SchemaVersion        int                         `json:"schemaVersion"`
	RunID                string                      `json:"runId"`
	BenchmarkVersion     string                      `json:"benchmarkVersion"`
	StartedAt            time.Time                   `json:"startedAt"`
	MeasurementStartedAt time.Time                   `json:"measurementStartedAt"`
	EndedAt              time.Time                   `json:"endedAt"`
	Duration             string                      `json:"duration"`
	Warmup               string                      `json:"warmup"`
	Profile              Profile                     `json:"profile"`
	ProfileSHA256        string                      `json:"profileSha256"`
	FixtureSHA256        string                      `json:"fixtureSha256,omitempty"`
	Seed                 int64                       `json:"seed"`
	Server               ServerMetadata              `json:"server"`
	ClientHost           ClientHostMetadata          `json:"clientHost"`
	Hardware             *HardwareMetadata           `json:"hardware,omitempty"`
	SystemMetrics        *SystemMetricsSummary       `json:"systemMetrics,omitempty"`
	Operations           map[string]OperationSummary `json:"operations"`
	WebSocket            WebSocketSummary            `json:"webSocket"`
	Availability         AvailabilitySummary         `json:"availability"`
	Scenario             *ScenarioSummary            `json:"scenario,omitempty"`
	Invariants           []InvariantResult           `json:"invariants"`
	Warnings             []ThresholdResult           `json:"warnings,omitempty"`
	OverallResult        string                      `json:"overallResult"`
}

type ServerMetadata struct {
	URL             string            `json:"url"`
	ServerVersion   string            `json:"serverVersion,omitempty"`
	APIVersion      string            `json:"apiVersion,omitempty"`
	EventAPIVersion string            `json:"eventApiVersion,omitempty"`
	StationDriver   string            `json:"stationDriver,omitempty"`
	GitCommit       string            `json:"gitCommit,omitempty"`
	GoVersion       string            `json:"goVersion,omitempty"`
	OS              string            `json:"os,omitempty"`
	Arch            string            `json:"arch,omitempty"`
	Configuration   map[string]string `json:"configuration,omitempty"`
}

func serverMetadata(url string, info client.SystemInfo) ServerMetadata {
	return ServerMetadata{
		URL: sanitizedServerURL(url), ServerVersion: info.ServerVersion, APIVersion: info.APIVersion,
		EventAPIVersion: info.EventAPIVersion, StationDriver: info.Station.Driver,
	}
}

func sanitizedServerURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/")
}

type ClientHostMetadata struct {
	Hostname  string `json:"hostname"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	CPUs      int    `json:"cpus"`
	GoVersion string `json:"goVersion"`
}

func currentHostMetadata() ClientHostMetadata {
	hostname, _ := os.Hostname()
	return ClientHostMetadata{Hostname: hostname, OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU(), GoVersion: runtime.Version()}
}

type OperationSummary struct {
	RequestedRatePerSecond *float64               `json:"requestedRatePerSecond,omitempty"`
	RequestedBurstCount    int64                  `json:"requestedBurstCount,omitempty"`
	Count                  int64                  `json:"count"`
	Successes              int64                  `json:"successes"`
	ExpectedErrors         int64                  `json:"expectedErrors"`
	UnexpectedErrors       int64                  `json:"unexpectedErrors"`
	Timeouts               int64                  `json:"timeouts"`
	Skipped                int64                  `json:"skipped"`
	AchievedRatePerSecond  float64                `json:"achievedRatePerSecond"`
	Latency                LatencySummary         `json:"latency"`
	UnexpectedErrorDetails []OperationErrorDetail `json:"unexpectedErrorDetails,omitempty"`
}

type OperationErrorDetail struct {
	Kind        string `json:"kind"`
	HTTPStatus  int    `json:"httpStatus,omitempty"`
	ProblemCode string `json:"problemCode,omitempty"`
	Count       int64  `json:"count"`
}

type LatencySummary struct {
	P50Milliseconds float64 `json:"p50Milliseconds"`
	P90Milliseconds float64 `json:"p90Milliseconds"`
	P95Milliseconds float64 `json:"p95Milliseconds"`
	P99Milliseconds float64 `json:"p99Milliseconds"`
	MaxMilliseconds float64 `json:"maxMilliseconds"`
}

type WebSocketSummary struct {
	Connections                  int64          `json:"connections"`
	Disconnections               int64          `json:"disconnections"`
	Reconnects                   int64          `json:"reconnects"`
	SequenceGaps                 int64          `json:"sequenceGaps"`
	Snapshots                    int64          `json:"snapshots"`
	SnapshotRequests             int64          `json:"snapshotRequests"`
	EventsReceived               int64          `json:"eventsReceived"`
	InvalidMessages              int64          `json:"invalidMessages"`
	UnresolvedSequenceGaps       int64          `json:"unresolvedSequenceGaps"`
	ActionExpectationsSuperseded int64          `json:"actionExpectationsSuperseded,omitempty"`
	QueueOverflows               *int64         `json:"queueOverflows,omitempty"`
	FeedbackLatency              LatencySummary `json:"feedbackLatency"`
}

type AvailabilitySummary struct {
	ExpectedOutages              int64   `json:"expectedOutages"`
	UnexpectedOutages            int64   `json:"unexpectedOutages"`
	Recoveries                   int64   `json:"recoveries"`
	TotalUnavailableMilliseconds float64 `json:"totalUnavailableMilliseconds"`
	MaxUnavailableMilliseconds   float64 `json:"maxUnavailableMilliseconds"`
	UnrecoveredOutages           int64   `json:"unrecoveredOutages"`
}

type ThresholdResult struct {
	Name   string  `json:"name"`
	Actual float64 `json:"actual"`
	Limit  float64 `json:"limit"`
	Unit   string  `json:"unit"`
	Status string  `json:"status"`
}

type InvariantResult struct {
	Name           string   `json:"name"`
	Status         string   `json:"status"`
	Observations   int64    `json:"observations"`
	ViolationCount int64    `json:"violationCount"`
	Violations     []string `json:"violations,omitempty"`
}

func WriteReport(path string, report Report) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create report directory: %w", err)
	}
	temporary, err := os.CreateTemp(dir, ".trainpilot-bench-*.json")
	if err != nil {
		return fmt.Errorf("create report: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		temporary.Close()
		return fmt.Errorf("encode report: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("publish report: %w", err)
	}
	return nil
}

func LoadReport(path string) (Report, error) {
	file, err := os.Open(path)
	if err != nil {
		return Report{}, fmt.Errorf("open report: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var report Report
	if err := decoder.Decode(&report); err != nil {
		return Report{}, fmt.Errorf("decode report: %w", err)
	}
	if report.SchemaVersion != 2 && report.SchemaVersion != ReportSchemaVersion {
		return Report{}, fmt.Errorf("unsupported report schemaVersion %d (want 2 or %d)", report.SchemaVersion, ReportSchemaVersion)
	}
	if !validRunID(report.RunID) {
		return Report{}, fmt.Errorf("invalid report runId %q", report.RunID)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return Report{}, errors.New("report must contain one JSON document")
		}
		return Report{}, fmt.Errorf("decode report: %w", err)
	}
	if report.SchemaVersion == 2 {
		duration, parseErr := time.ParseDuration(report.Duration)
		if parseErr != nil {
			return Report{}, fmt.Errorf("invalid legacy report duration %q", report.Duration)
		}
		report.SchemaVersion = ReportSchemaVersion
		report.MeasurementStartedAt = report.EndedAt.Add(-duration)
	}
	if err := ValidateReport(report); err != nil {
		return Report{}, err
	}
	return report, nil
}

func ValidateReport(report Report) error {
	if report.SchemaVersion != ReportSchemaVersion {
		return fmt.Errorf("unsupported report schemaVersion %d", report.SchemaVersion)
	}
	if report.MeasurementStartedAt.IsZero() || report.EndedAt.Before(report.MeasurementStartedAt) {
		return errors.New("measurement timestamps are invalid")
	}
	if report.OverallResult != "PASS" && report.OverallResult != "WARN" && report.OverallResult != "FAIL" {
		return fmt.Errorf("invalid overallResult %q", report.OverallResult)
	}
	if len(report.ProfileSHA256) != 64 {
		return errors.New("profileSha256 must contain 64 hexadecimal characters")
	}
	if _, err := hex.DecodeString(report.ProfileSHA256); err != nil {
		return errors.New("profileSha256 must contain 64 hexadecimal characters")
	}
	for name, summary := range report.Operations {
		if summary.RequestedBurstCount < 0 || summary.Count < 0 || summary.Successes < 0 || summary.ExpectedErrors < 0 || summary.UnexpectedErrors < 0 || summary.Timeouts < 0 || summary.Skipped < 0 {
			return fmt.Errorf("operation %q contains a negative counter", name)
		}
		if summary.Count != summary.Successes+summary.ExpectedErrors+summary.UnexpectedErrors {
			return fmt.Errorf("operation %q count does not match its outcomes", name)
		}
		if summary.Timeouts > summary.ExpectedErrors+summary.UnexpectedErrors {
			return fmt.Errorf("operation %q has more timeouts than errors", name)
		}
		var detailedUnexpectedErrors int64
		for _, detail := range summary.UnexpectedErrorDetails {
			if !validOperationErrorDetail(detail) {
				return fmt.Errorf("operation %q contains an invalid unexpected error detail", name)
			}
			detailedUnexpectedErrors += detail.Count
		}
		if len(summary.UnexpectedErrorDetails) > maxUnexpectedErrorDetails ||
			(len(summary.UnexpectedErrorDetails) > 0 && detailedUnexpectedErrors != summary.UnexpectedErrors) {
			return fmt.Errorf("operation %q unexpected error details do not match its counter", name)
		}
		if (summary.RequestedRatePerSecond != nil && *summary.RequestedRatePerSecond < 0) || summary.AchievedRatePerSecond < 0 {
			return fmt.Errorf("operation %q contains a negative rate", name)
		}
		if !validLatency(summary.Latency) {
			return fmt.Errorf("operation %q contains invalid latency percentiles", name)
		}
	}
	if !validLatency(report.WebSocket.FeedbackLatency) {
		return errors.New("WebSocket feedback latency percentiles are invalid")
	}
	for _, value := range []int64{
		report.WebSocket.Connections, report.WebSocket.Disconnections, report.WebSocket.Reconnects,
		report.WebSocket.SequenceGaps, report.WebSocket.Snapshots, report.WebSocket.SnapshotRequests,
		report.WebSocket.EventsReceived, report.WebSocket.InvalidMessages, report.WebSocket.UnresolvedSequenceGaps,
		report.WebSocket.ActionExpectationsSuperseded,
	} {
		if value < 0 {
			return errors.New("WebSocket summary contains a negative counter")
		}
	}
	if report.WebSocket.QueueOverflows != nil && *report.WebSocket.QueueOverflows < 0 {
		return errors.New("WebSocket queueOverflows must not be negative")
	}
	if report.Availability.ExpectedOutages < 0 || report.Availability.UnexpectedOutages < 0 ||
		report.Availability.Recoveries < 0 || report.Availability.TotalUnavailableMilliseconds < 0 ||
		report.Availability.MaxUnavailableMilliseconds < 0 || report.Availability.UnrecoveredOutages < 0 {
		return errors.New("availability summary contains a negative value")
	}
	outages := report.Availability.ExpectedOutages + report.Availability.UnexpectedOutages
	if report.Availability.UnrecoveredOutages > 1 || report.Availability.Recoveries+report.Availability.UnrecoveredOutages != outages {
		return errors.New("availability outage counters are inconsistent")
	}
	if report.Hardware != nil && (report.Hardware.Cores < 0 || report.Hardware.RAMBytes < 0) {
		return errors.New("hardware cores and ramBytes must not be negative")
	}
	for key := range report.Server.Configuration {
		if sensitiveMetadataKey(key) {
			return fmt.Errorf("server.configuration key %q may contain a secret", key)
		}
	}
	if report.SystemMetrics != nil {
		if report.SystemMetrics.SchemaVersion != SystemMetricsSchemaVersion {
			return fmt.Errorf("unsupported system metrics schemaVersion %d", report.SystemMetrics.SchemaVersion)
		}
		if err := validateNonNegativeMetrics(*report.SystemMetrics); err != nil {
			return err
		}
		if strings.TrimSpace(report.SystemMetrics.Source) == "" {
			return errors.New("system metrics source is required")
		}
		if report.SystemMetrics.WebSocketQueueOverflows != nil && report.WebSocket.QueueOverflows == nil {
			return errors.New("WebSocket queue overflow summary is missing")
		}
		if report.WebSocket.QueueOverflows != nil && report.SystemMetrics.WebSocketQueueOverflows != nil && *report.WebSocket.QueueOverflows != *report.SystemMetrics.WebSocketQueueOverflows {
			return errors.New("WebSocket queue overflow values disagree")
		}
	}
	for _, invariant := range report.Invariants {
		if invariant.Status != "PASS" && invariant.Status != "FAIL" && invariant.Status != "NOT_OBSERVED" {
			return fmt.Errorf("invariant %q has invalid status %q", invariant.Name, invariant.Status)
		}
		if invariant.ViolationCount != int64(len(invariant.Violations)) {
			return fmt.Errorf("invariant %q violationCount does not match details", invariant.Name)
		}
		if invariant.Observations < 0 || invariant.ViolationCount < 0 {
			return fmt.Errorf("invariant %q contains a negative counter", invariant.Name)
		}
	}
	for _, warning := range report.Warnings {
		if warning.Status != "WARN" {
			return fmt.Errorf("threshold %q has invalid status %q", warning.Name, warning.Status)
		}
	}
	if report.Scenario != nil {
		if report.Scenario.Name == "" || len(report.Scenario.SHA256) != 64 || report.Scenario.StartedAt.IsZero() || report.Scenario.EndedAt.Before(report.Scenario.StartedAt) {
			return errors.New("scenario summary is invalid")
		}
		if _, err := hex.DecodeString(report.Scenario.SHA256); err != nil {
			return errors.New("scenario sha256 must contain 64 hexadecimal characters")
		}
		if report.Scenario.StartedAt.Before(report.MeasurementStartedAt) || report.Scenario.EndedAt.After(report.EndedAt) {
			return errors.New("scenario summary is outside the measured interval")
		}
		if report.Scenario.Status != "completed" && report.Scenario.Status != "failed" && report.Scenario.Status != "stopped" {
			return fmt.Errorf("scenario summary has invalid status %q", report.Scenario.Status)
		}
		var previous time.Duration
		for index, step := range report.Scenario.Steps {
			offset, err := time.ParseDuration(step.At)
			if err != nil || offset < 0 || (index > 0 && offset < previous) || step.Action == "" || step.AppliedAt.IsZero() {
				return fmt.Errorf("scenario step %d is invalid", index)
			}
			if step.AppliedAt.Before(report.Scenario.StartedAt) || step.AppliedAt.After(report.Scenario.EndedAt) {
				return fmt.Errorf("scenario step %d timestamp is outside the scenario interval", index)
			}
			previous = offset
		}
	}
	return nil
}

func validOperationErrorDetail(detail OperationErrorDetail) bool {
	if detail.Count <= 0 || len(detail.ProblemCode) > 128 || sanitizedProblemCode(detail.ProblemCode) != detail.ProblemCode {
		return false
	}
	switch detail.Kind {
	case "http":
		return detail.HTTPStatus >= 100 && detail.HTTPStatus <= 599
	case "timeout", "network", "invalid_json", "other":
		return detail.HTTPStatus == 0 && detail.ProblemCode == ""
	default:
		return false
	}
}

func validLatency(latency LatencySummary) bool {
	return latency.P50Milliseconds >= 0 &&
		latency.P50Milliseconds <= latency.P90Milliseconds &&
		latency.P90Milliseconds <= latency.P95Milliseconds &&
		latency.P95Milliseconds <= latency.P99Milliseconds &&
		latency.P99Milliseconds <= latency.MaxMilliseconds
}

func WriteConsoleSummary(w io.Writer, report Report) {
	fmt.Fprintf(w, "TrainPilot benchmark: %s\n", report.OverallResult)
	fmt.Fprintf(w, "Profile: %s | seed: %d | measured: %s\n", report.Profile.Name, report.Seed, report.Duration)
	names := make([]string, 0, len(report.Operations))
	for name := range report.Operations {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		summary := report.Operations[name]
		requested := "n/a"
		if summary.RequestedRatePerSecond != nil {
			requested = fmt.Sprintf("%.2f/s", *summary.RequestedRatePerSecond)
		}
		if summary.RequestedBurstCount > 0 {
			if summary.RequestedRatePerSecond == nil {
				requested = fmt.Sprintf("burst:%d", summary.RequestedBurstCount)
			} else {
				requested += fmt.Sprintf("+burst:%d", summary.RequestedBurstCount)
			}
		}
		fmt.Fprintf(w, "%-20s requested=%-8s achieved=%6.2f/s count=%d ok=%d expected=%d unexpected=%d p50=%.3fms p95=%.3fms p99=%.3fms\n",
			name, requested, summary.AchievedRatePerSecond, summary.Count, summary.Successes,
			summary.ExpectedErrors, summary.UnexpectedErrors, summary.Latency.P50Milliseconds,
			summary.Latency.P95Milliseconds, summary.Latency.P99Milliseconds)
		for _, detail := range summary.UnexpectedErrorDetails {
			fmt.Fprintf(w, "  unexpected_error kind=%s status=%d code=%s count=%d\n",
				detail.Kind, detail.HTTPStatus, detail.ProblemCode, detail.Count)
		}
	}
	var violationCount int64
	for _, invariant := range report.Invariants {
		violationCount += invariant.ViolationCount
		fmt.Fprintf(w, "invariant %-28s %s", invariant.Name, invariant.Status)
		if len(invariant.Violations) > 0 {
			fmt.Fprintf(w, " (%s)", invariant.Violations[0])
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "Invariants: %d violations\n", violationCount)
	overflows := "n/a"
	if report.WebSocket.QueueOverflows != nil {
		overflows = fmt.Sprintf("%d", *report.WebSocket.QueueOverflows)
	}
	fmt.Fprintf(w, "WebSocket: overflows=%s sequence_gaps=%d unresolved=%d action_expectations_superseded=%d\n",
		overflows, report.WebSocket.SequenceGaps, report.WebSocket.UnresolvedSequenceGaps, report.WebSocket.ActionExpectationsSuperseded)
	fmt.Fprintf(w, "Availability: expected_outages=%d unexpected_outages=%d recoveries=%d unavailable=%.0fms max=%.0fms unrecovered=%d\n",
		report.Availability.ExpectedOutages, report.Availability.UnexpectedOutages, report.Availability.Recoveries,
		report.Availability.TotalUnavailableMilliseconds, report.Availability.MaxUnavailableMilliseconds, report.Availability.UnrecoveredOutages)
	if report.Scenario != nil {
		fmt.Fprintf(w, "Scenario: %s status=%s steps=%d\n", report.Scenario.Name, report.Scenario.Status, len(report.Scenario.Steps))
	}
	for _, warning := range report.Warnings {
		fmt.Fprintf(w, "warning %-30s %.3f%s (target < %.3f%s)\n", warning.Name, warning.Actual, warning.Unit, warning.Limit, warning.Unit)
	}
}
