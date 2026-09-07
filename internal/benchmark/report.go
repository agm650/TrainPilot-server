package benchmark

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"github.com/agm650/TrainPilot-server/internal/client"
)

const ReportSchemaVersion = 1

type Report struct {
	SchemaVersion    int                         `json:"schemaVersion"`
	BenchmarkVersion string                      `json:"benchmarkVersion"`
	StartedAt        time.Time                   `json:"startedAt"`
	EndedAt          time.Time                   `json:"endedAt"`
	Duration         string                      `json:"duration"`
	Warmup           string                      `json:"warmup"`
	Profile          Profile                     `json:"profile"`
	ProfileSHA256    string                      `json:"profileSha256"`
	FixtureSHA256    string                      `json:"fixtureSha256,omitempty"`
	Seed             int64                       `json:"seed"`
	Server           ServerMetadata              `json:"server"`
	ClientHost       ClientHostMetadata          `json:"clientHost"`
	Operations       map[string]OperationSummary `json:"operations"`
	WebSocket        WebSocketSummary            `json:"webSocket"`
	Invariants       []InvariantResult           `json:"invariants"`
	OverallResult    string                      `json:"overallResult"`
}

type ServerMetadata struct {
	URL             string `json:"url"`
	ServerVersion   string `json:"serverVersion,omitempty"`
	APIVersion      string `json:"apiVersion,omitempty"`
	EventAPIVersion string `json:"eventApiVersion,omitempty"`
	StationDriver   string `json:"stationDriver,omitempty"`
}

func serverMetadata(url string, info client.SystemInfo) ServerMetadata {
	return ServerMetadata{
		URL: url, ServerVersion: info.ServerVersion, APIVersion: info.APIVersion,
		EventAPIVersion: info.EventAPIVersion, StationDriver: info.Station.Driver,
	}
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
	Total               int64          `json:"total"`
	Successes           int64          `json:"successes"`
	Failures            int64          `json:"failures"`
	Timeouts            int64          `json:"timeouts"`
	Skipped             int64          `json:"skipped"`
	ThroughputPerSecond float64        `json:"throughputPerSecond"`
	Latency             LatencySummary `json:"latency"`
}

type LatencySummary struct {
	P50Milliseconds float64 `json:"p50Milliseconds"`
	P90Milliseconds float64 `json:"p90Milliseconds"`
	P95Milliseconds float64 `json:"p95Milliseconds"`
	P99Milliseconds float64 `json:"p99Milliseconds"`
	MaxMilliseconds float64 `json:"maxMilliseconds"`
}

type WebSocketSummary struct {
	Connections      int64          `json:"connections"`
	Disconnections   int64          `json:"disconnections"`
	Reconnects       int64          `json:"reconnects"`
	SequenceGaps     int64          `json:"sequenceGaps"`
	Snapshots        int64          `json:"snapshots"`
	SnapshotRequests int64          `json:"snapshotRequests"`
	EventsReceived   int64          `json:"eventsReceived"`
	InvalidMessages  int64          `json:"invalidMessages"`
	FeedbackLatency  LatencySummary `json:"feedbackLatency"`
}

type InvariantResult struct {
	Name         string   `json:"name"`
	Status       string   `json:"status"`
	Observations int64    `json:"observations"`
	Violations   []string `json:"violations,omitempty"`
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
		fmt.Fprintf(w, "%-20s total=%d ok=%d failed=%d skipped=%d p95=%.3fms p99=%.3fms rate=%.2f/s\n",
			name, summary.Total, summary.Successes, summary.Failures, summary.Skipped,
			summary.Latency.P95Milliseconds, summary.Latency.P99Milliseconds, summary.ThroughputPerSecond)
	}
	for _, invariant := range report.Invariants {
		fmt.Fprintf(w, "invariant %-28s %s", invariant.Name, invariant.Status)
		if len(invariant.Violations) > 0 {
			fmt.Fprintf(w, " (%s)", invariant.Violations[0])
		}
		fmt.Fprintln(w)
	}
}
