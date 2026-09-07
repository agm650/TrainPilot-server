package benchmark

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

const (
	RunMetadataSchemaVersion   = 1
	SystemMetricsSchemaVersion = 1
)

type RunMetadata struct {
	SchemaVersion int                    `json:"schemaVersion"`
	Hardware      HardwareMetadata       `json:"hardware"`
	Server        ImportedServerMetadata `json:"server"`
}

type HardwareMetadata struct {
	Model    string `json:"model"`
	CPU      string `json:"cpu"`
	Cores    int    `json:"cores"`
	RAMBytes int64  `json:"ramBytes"`
	Storage  string `json:"storage"`
	OS       string `json:"os"`
	Kernel   string `json:"kernel"`
	Arch     string `json:"arch"`
	Network  string `json:"network"`
	Notes    string `json:"notes"`
}

type ImportedServerMetadata struct {
	GitCommit     string            `json:"gitCommit"`
	GoVersion     string            `json:"goVersion"`
	OS            string            `json:"os"`
	Arch          string            `json:"arch"`
	Configuration map[string]string `json:"configuration"`
}

type SystemMetricsSummary struct {
	SchemaVersion           int      `json:"schemaVersion"`
	Source                  string   `json:"source"`
	DatabaseInitialBytes    *int64   `json:"databaseInitialBytes,omitempty"`
	DatabaseFinalBytes      *int64   `json:"databaseFinalBytes,omitempty"`
	ProcessCPUMaxPercent    *float64 `json:"processCpuMaxPercent,omitempty"`
	ProcessRSSMaxBytes      *int64   `json:"processRssMaxBytes,omitempty"`
	HostCPUMaxPercent       *float64 `json:"hostCpuMaxPercent,omitempty"`
	HostRAMMaxBytes         *int64   `json:"hostRamMaxBytes,omitempty"`
	SwapMaxBytes            *int64   `json:"swapMaxBytes,omitempty"`
	SQLiteErrors            *int64   `json:"sqliteErrors,omitempty"`
	NetworkDrops            *int64   `json:"networkDrops,omitempty"`
	WebSocketQueueOverflows *int64   `json:"webSocketQueueOverflows,omitempty"`
	TemperatureMaxCelsius   *float64 `json:"temperatureMaxCelsius,omitempty"`
	ThermalThrottlingEvents *int64   `json:"thermalThrottlingEvents,omitempty"`
}

func LoadRunMetadata(path string) (RunMetadata, error) {
	var metadata RunMetadata
	if err := decodeStrictJSON(path, &metadata); err != nil {
		return RunMetadata{}, fmt.Errorf("load run metadata: %w", err)
	}
	if metadata.SchemaVersion != RunMetadataSchemaVersion {
		return RunMetadata{}, fmt.Errorf("unsupported run metadata schemaVersion %d (want %d)", metadata.SchemaVersion, RunMetadataSchemaVersion)
	}
	if metadata.Hardware.Cores < 0 || metadata.Hardware.RAMBytes < 0 {
		return RunMetadata{}, errors.New("hardware cores and ramBytes must not be negative")
	}
	for key := range metadata.Server.Configuration {
		if sensitiveMetadataKey(key) {
			return RunMetadata{}, fmt.Errorf("server.configuration key %q may contain a secret", key)
		}
	}
	return metadata, nil
}

func LoadSystemMetrics(path string) (SystemMetricsSummary, error) {
	var summary SystemMetricsSummary
	if err := decodeStrictJSON(path, &summary); err != nil {
		return SystemMetricsSummary{}, fmt.Errorf("load system metrics: %w", err)
	}
	if summary.SchemaVersion != SystemMetricsSchemaVersion {
		return SystemMetricsSummary{}, fmt.Errorf("unsupported system metrics schemaVersion %d (want %d)", summary.SchemaVersion, SystemMetricsSchemaVersion)
	}
	if strings.TrimSpace(summary.Source) == "" {
		return SystemMetricsSummary{}, errors.New("system metrics source is required")
	}
	if err := validateNonNegativeMetrics(summary); err != nil {
		return SystemMetricsSummary{}, err
	}
	return summary, nil
}

func decodeStrictJSON(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("file must contain one JSON document")
		}
		return err
	}
	return nil
}

func sensitiveMetadataKey(key string) bool {
	normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "", ".", "").Replace(key))
	for _, fragment := range []string{"password", "passwd", "secret", "token", "credential", "apikey", "authorization", "cookie"} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

func validateNonNegativeMetrics(summary SystemMetricsSummary) error {
	values := map[string]*int64{
		"databaseInitialBytes":    summary.DatabaseInitialBytes,
		"databaseFinalBytes":      summary.DatabaseFinalBytes,
		"processRssMaxBytes":      summary.ProcessRSSMaxBytes,
		"hostRamMaxBytes":         summary.HostRAMMaxBytes,
		"swapMaxBytes":            summary.SwapMaxBytes,
		"sqliteErrors":            summary.SQLiteErrors,
		"networkDrops":            summary.NetworkDrops,
		"webSocketQueueOverflows": summary.WebSocketQueueOverflows,
		"thermalThrottlingEvents": summary.ThermalThrottlingEvents,
	}
	for name, value := range values {
		if value != nil && *value < 0 {
			return fmt.Errorf("%s must not be negative", name)
		}
	}
	for name, value := range map[string]*float64{
		"processCpuMaxPercent":  summary.ProcessCPUMaxPercent,
		"hostCpuMaxPercent":     summary.HostCPUMaxPercent,
		"temperatureMaxCelsius": summary.TemperatureMaxCelsius,
	} {
		if value != nil && *value < 0 {
			return fmt.Errorf("%s must not be negative", name)
		}
	}
	return nil
}

func ApplyRunMetadata(report *Report, metadata RunMetadata) {
	report.Hardware = &metadata.Hardware
	report.Server.GitCommit = metadata.Server.GitCommit
	report.Server.GoVersion = metadata.Server.GoVersion
	report.Server.OS = metadata.Server.OS
	report.Server.Arch = metadata.Server.Arch
	report.Server.Configuration = metadata.Server.Configuration
}

func ApplySystemMetrics(report *Report, summary SystemMetricsSummary) {
	report.SystemMetrics = &summary
	report.WebSocket.QueueOverflows = summary.WebSocketQueueOverflows
}

func ValidateReportForPublication(report Report) error {
	if err := ValidateReport(report); err != nil {
		return fmt.Errorf("report is not valid: %w", err)
	}
	missing := make([]string, 0)
	requireText := func(name, value string) {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, name)
		}
	}
	if !validRunID(report.RunID) {
		missing = append(missing, "runId")
	}
	if report.StartedAt.IsZero() {
		missing = append(missing, "startedAt")
	}
	if report.EndedAt.IsZero() {
		missing = append(missing, "endedAt")
	}
	requireText("duration", report.Duration)
	requireText("warmup", report.Warmup)
	requireText("profile.name", report.Profile.Name)
	if report.Profile.SchemaVersion != ProfileSchemaVersion {
		missing = append(missing, "profile.schemaVersion")
	}
	requireText("benchmarkVersion", report.BenchmarkVersion)
	requireText("profileSha256", report.ProfileSHA256)
	requireText("server.url", report.Server.URL)
	requireText("server.serverVersion", report.Server.ServerVersion)
	requireText("server.gitCommit", report.Server.GitCommit)
	requireText("server.goVersion", report.Server.GoVersion)
	requireText("server.os", report.Server.OS)
	requireText("server.arch", report.Server.Arch)
	requireText("clientHost.hostname", report.ClientHost.Hostname)
	requireText("clientHost.os", report.ClientHost.OS)
	requireText("clientHost.arch", report.ClientHost.Arch)
	requireText("clientHost.goVersion", report.ClientHost.GoVersion)
	if report.ClientHost.CPUs <= 0 {
		missing = append(missing, "clientHost.cpus")
	}
	if len(report.Server.Configuration) == 0 {
		missing = append(missing, "server.configuration")
	}
	if report.Hardware == nil {
		missing = append(missing, "hardware")
	} else {
		requireText("hardware.model", report.Hardware.Model)
		requireText("hardware.cpu", report.Hardware.CPU)
		if report.Hardware.Cores <= 0 {
			missing = append(missing, "hardware.cores")
		}
		if report.Hardware.RAMBytes <= 0 {
			missing = append(missing, "hardware.ramBytes")
		}
		requireText("hardware.storage", report.Hardware.Storage)
		requireText("hardware.os", report.Hardware.OS)
		requireText("hardware.kernel", report.Hardware.Kernel)
		requireText("hardware.arch", report.Hardware.Arch)
		requireText("hardware.network", report.Hardware.Network)
		requireText("hardware.notes", report.Hardware.Notes)
	}
	if report.SystemMetrics == nil {
		missing = append(missing, "systemMetrics")
	} else {
		requireText("systemMetrics.source", report.SystemMetrics.Source)
		for name, present := range map[string]bool{
			"systemMetrics.databaseInitialBytes":    report.SystemMetrics.DatabaseInitialBytes != nil,
			"systemMetrics.databaseFinalBytes":      report.SystemMetrics.DatabaseFinalBytes != nil,
			"systemMetrics.processCpuMaxPercent":    report.SystemMetrics.ProcessCPUMaxPercent != nil,
			"systemMetrics.processRssMaxBytes":      report.SystemMetrics.ProcessRSSMaxBytes != nil,
			"systemMetrics.swapMaxBytes":            report.SystemMetrics.SwapMaxBytes != nil,
			"systemMetrics.sqliteErrors":            report.SystemMetrics.SQLiteErrors != nil,
			"systemMetrics.networkDrops":            report.SystemMetrics.NetworkDrops != nil,
			"systemMetrics.webSocketQueueOverflows": report.SystemMetrics.WebSocketQueueOverflows != nil,
			"systemMetrics.thermalThrottlingEvents": report.SystemMetrics.ThermalThrottlingEvents != nil,
		} {
			if !present {
				missing = append(missing, name)
			}
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("report is not publishable; missing: %s", strings.Join(missing, ", "))
	}
	return nil
}
