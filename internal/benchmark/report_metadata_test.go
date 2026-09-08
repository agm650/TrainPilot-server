package benchmark

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMetadataLoadersAreStrictAndRejectSecretKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata.json")
	data := `{"schemaVersion":1,"hardware":{"model":"host","cpu":"cpu","cores":4,"ramBytes":1024,"storage":"ssd","os":"linux","kernel":"6","arch":"arm64","network":"ethernet","notes":"cooled"},"server":{"gitCommit":"abc","goVersion":"go1.26","os":"linux","arch":"arm64","configuration":{"apiToken":"secret"}}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRunMetadata(path); err == nil || !strings.Contains(err.Error(), "may contain a secret") {
		t.Fatalf("error=%v", err)
	}
}

func TestDocumentedMetadataExamplesLoad(t *testing.T) {
	root := filepath.Join("..", "..", "benchmarks", "examples")
	if _, err := LoadRunMetadata(filepath.Join(root, "run-metadata.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSystemMetrics(filepath.Join(root, "system-metrics.json")); err != nil {
		t.Fatal(err)
	}
}

func TestPublishedReportRequiresCompleteExternalMetadata(t *testing.T) {
	report := Report{
		SchemaVersion: ReportSchemaVersion, RunID: "00000000-0000-4000-8000-000000000001",
		BenchmarkVersion: "v1", StartedAt: time.Now().UTC(), MeasurementStartedAt: time.Now().UTC(), EndedAt: time.Now().UTC(), Duration: "1m", Warmup: "1s",
		Profile: Profile{SchemaVersion: ProfileSchemaVersion, Name: "medium"}, ProfileSHA256: strings.Repeat("a", 64),
		Server: ServerMetadata{URL: "http://example.test", ServerVersion: "v1"}, Operations: map[string]OperationSummary{}, OverallResult: "PASS",
		ClientHost: ClientHostMetadata{Hostname: "generator", OS: "linux", Arch: "amd64", CPUs: 4, GoVersion: "go1.26"},
	}
	if err := ValidateReportForPublication(report); err == nil {
		t.Fatal("expected incomplete report rejection")
	}
	zero := int64(0)
	cpu := 1.0
	report.Server.GitCommit = "abc"
	report.Server.GoVersion = "go1.26"
	report.Server.OS = "linux"
	report.Server.Arch = "arm64"
	report.Server.Configuration = map[string]string{"diagnosticsEnabled": "true"}
	report.Hardware = &HardwareMetadata{Model: "host", CPU: "cpu", Cores: 4, RAMBytes: 1024, Storage: "ssd", OS: "linux", Kernel: "6", Arch: "arm64", Network: "ethernet", Notes: "cooled"}
	metrics := SystemMetricsSummary{
		SchemaVersion: SystemMetricsSchemaVersion, Source: "external-script",
		DatabaseInitialBytes: &zero, DatabaseFinalBytes: &zero,
		ProcessCPUMaxPercent: &cpu, ProcessRSSMaxBytes: &zero,
		SwapMaxBytes: &zero, SQLiteErrors: &zero, NetworkDrops: &zero,
		WebSocketQueueOverflows: &zero, ThermalThrottlingEvents: &zero,
	}
	ApplySystemMetrics(&report, metrics)
	if err := ValidateReportForPublication(report); err != nil {
		t.Fatal(err)
	}
}

func TestApplySystemMetricsUpdatesWarningsAndWebSocketOverflow(t *testing.T) {
	one := int64(1)
	report := Report{Profile: Profile{Name: "medium"}, Operations: map[string]OperationSummary{}, OverallResult: "PASS"}
	ApplySystemMetrics(&report, SystemMetricsSummary{SchemaVersion: 1, Source: "script", WebSocketQueueOverflows: &one, SQLiteErrors: &one})
	ApplyReportPolicy(&report)
	if report.OverallResult != "WARN" || report.WebSocket.QueueOverflows == nil || len(report.Warnings) != 2 {
		t.Fatalf("report=%+v", report)
	}
}
