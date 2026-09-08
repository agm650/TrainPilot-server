package benchmark

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const SoakAnalysisSchemaVersion = 1

type SoakThresholds struct {
	RSSBytesPerHour      float64
	HeapBytesPerHour     float64
	HeapObjectsPerHour   float64
	GoroutinesPerHour    float64
	ThreadsPerHour       float64
	FileDescriptorsHour  float64
	SQLiteBytesPerHour   float64
	CPUIncreasePct       float64
	LatencyIncreasePct   float64
	MinimumCoverageRatio float64
}

func DefaultSoakThresholds() SoakThresholds {
	return SoakThresholds{
		RSSBytesPerHour: 5 * 1024 * 1024, HeapBytesPerHour: 5 * 1024 * 1024,
		HeapObjectsPerHour: 10_000, GoroutinesPerHour: 1, ThreadsPerHour: 1,
		FileDescriptorsHour: 1, SQLiteBytesPerHour: 1024 * 1024,
		CPUIncreasePct: 20, LatencyIncreasePct: 10, MinimumCoverageRatio: 0.99,
	}
}

type SoakAnalysis struct {
	SchemaVersion        int                  `json:"schemaVersion"`
	RunID                string               `json:"runId"`
	Source               string               `json:"source"`
	PrometheusURL        string               `json:"prometheusUrl"`
	Instance             string               `json:"instance"`
	MeasurementStartedAt time.Time            `json:"measurementStartedAt"`
	EndedAt              time.Time            `json:"endedAt"`
	Window               string               `json:"window"`
	ScrapeInterval       string               `json:"scrapeInterval"`
	CoverageRatio        float64              `json:"coverageRatio"`
	Metrics              []SoakMetricAnalysis `json:"metrics"`
	Warnings             []string             `json:"warnings,omitempty"`
	OverallResult        string               `json:"overallResult"`
}

type SoakMetricAnalysis struct {
	Name                  string   `json:"name"`
	Unit                  string   `json:"unit"`
	FirstAverage          float64  `json:"firstAverage"`
	LastAverage           float64  `json:"lastAverage"`
	AbsoluteChange        float64  `json:"absoluteChange"`
	RelativeChangePercent *float64 `json:"relativeChangePercent,omitempty"`
	SlopePerHour          *float64 `json:"slopePerHour,omitempty"`
	WarnLimit             float64  `json:"warnLimit"`
	Status                string   `json:"status"`
}

type SoakAnalysisOptions struct {
	PrometheusURL  string
	Instance       string
	Window         time.Duration
	ScrapeInterval time.Duration
	Thresholds     SoakThresholds
	HTTPClient     *http.Client
}

type soakMetricDefinition struct {
	name, unit, averageRecord, slopeRecord string
	limit                                  float64
	useRelativeLimit                       bool
}

func AnalyzeSoak(ctx context.Context, report Report, options SoakAnalysisOptions) (SoakAnalysis, error) {
	if !validRunID(report.RunID) {
		return SoakAnalysis{}, fmt.Errorf("invalid report runId %q", report.RunID)
	}
	prometheusURL, err := normalizedPrometheusURL(options.PrometheusURL)
	if err != nil {
		return SoakAnalysis{}, err
	}
	if strings.TrimSpace(options.Instance) == "" {
		return SoakAnalysis{}, errors.New("Prometheus instance is required")
	}
	if options.Window <= 0 {
		options.Window = 30 * time.Minute
	}
	if options.Window != 30*time.Minute {
		return SoakAnalysis{}, errors.New("soak recording rules require a 30m analysis window")
	}
	if options.ScrapeInterval <= 0 {
		options.ScrapeInterval = 15 * time.Second
	}
	duration, err := time.ParseDuration(report.Duration)
	if err != nil || duration <= 0 {
		return SoakAnalysis{}, fmt.Errorf("invalid report duration %q", report.Duration)
	}
	measurementStarted := report.MeasurementStartedAt
	if measurementStarted.IsZero() {
		measurementStarted = report.EndedAt.Add(-duration)
	}
	if report.EndedAt.Sub(measurementStarted) < 2*options.Window {
		return SoakAnalysis{}, fmt.Errorf("measured duration must cover two %s analysis windows", options.Window)
	}
	thresholds := options.Thresholds
	if thresholds == (SoakThresholds{}) {
		thresholds = DefaultSoakThresholds()
	}
	if err := validateSoakThresholds(thresholds); err != nil {
		return SoakAnalysis{}, err
	}
	client := newPrometheusClient(prometheusURL, options.HTTPClient)
	selector := fmt.Sprintf("{instance=%s}", strconv.Quote(options.Instance))
	coverageQuery := fmt.Sprintf("sum_over_time(up{job=\"trainpilot\",instance=%s}[%s])", strconv.Quote(options.Instance), promDuration(duration))
	samples, err := client.queryScalar(ctx, coverageQuery, report.EndedAt)
	if err != nil {
		return SoakAnalysis{}, fmt.Errorf("query scrape coverage: %w", err)
	}
	expectedSamples := math.Floor(float64(duration)/float64(options.ScrapeInterval)) + 1
	coverage := math.Min(1, samples/expectedSamples)
	definitions := []soakMetricDefinition{
		{"rss", "bytes", "trainpilot:process_rss_bytes:avg_30m", "trainpilot:process_rss_bytes:slope_per_hour_30m", thresholds.RSSBytesPerHour, false},
		{"go_heap", "bytes", "trainpilot:go_heap_alloc_bytes:avg_30m", "trainpilot:go_heap_alloc_bytes:slope_per_hour_30m", thresholds.HeapBytesPerHour, false},
		{"go_heap_objects", "objects", "trainpilot:go_heap_objects:avg_30m", "trainpilot:go_heap_objects:slope_per_hour_30m", thresholds.HeapObjectsPerHour, false},
		{"goroutines", "goroutines", "trainpilot:go_goroutines:avg_30m", "trainpilot:go_goroutines:slope_per_hour_30m", thresholds.GoroutinesPerHour, false},
		{"threads", "threads", "trainpilot:go_threads:avg_30m", "trainpilot:go_threads:slope_per_hour_30m", thresholds.ThreadsPerHour, false},
		{"file_descriptors", "descriptors", "trainpilot:process_open_fds:avg_30m", "trainpilot:process_open_fds:slope_per_hour_30m", thresholds.FileDescriptorsHour, false},
		{"sqlite", "bytes", "trainpilot:sqlite_file_size_bytes:avg_30m", "trainpilot:sqlite_file_size_bytes:slope_per_hour_30m", thresholds.SQLiteBytesPerHour, false},
		{"cpu", "cores", "trainpilot:process_cpu_cores:avg_30m", "", thresholds.CPUIncreasePct, true},
		{"http_p95", "seconds", "trainpilot:http_request_duration_seconds:p95_avg_30m", "", thresholds.LatencyIncreasePct, true},
		{"http_p99", "seconds", "trainpilot:http_request_duration_seconds:p99_avg_30m", "", thresholds.LatencyIncreasePct, true},
	}
	analysis := SoakAnalysis{
		SchemaVersion: SoakAnalysisSchemaVersion, RunID: report.RunID,
		Source: "prometheus-recording-rules", PrometheusURL: prometheusURL,
		Instance: options.Instance, MeasurementStartedAt: measurementStarted,
		EndedAt: report.EndedAt, Window: options.Window.String(), ScrapeInterval: options.ScrapeInterval.String(), CoverageRatio: coverage,
		Metrics: make([]SoakMetricAnalysis, 0, len(definitions)), OverallResult: "PASS",
	}
	firstAt := measurementStarted.Add(options.Window)
	for _, definition := range definitions {
		first, err := client.queryScalar(ctx, definition.averageRecord+selector, firstAt)
		if err != nil {
			return SoakAnalysis{}, fmt.Errorf("query first %s: %w", definition.name, err)
		}
		last, err := client.queryScalar(ctx, definition.averageRecord+selector, report.EndedAt)
		if err != nil {
			return SoakAnalysis{}, fmt.Errorf("query last %s: %w", definition.name, err)
		}
		metric := SoakMetricAnalysis{
			Name: definition.name, Unit: definition.unit, FirstAverage: first,
			LastAverage: last, AbsoluteChange: last - first,
			WarnLimit: definition.limit, Status: "PASS",
		}
		if first != 0 {
			relative := (last - first) / math.Abs(first) * 100
			metric.RelativeChangePercent = &relative
		}
		actual := 0.0
		if definition.useRelativeLimit {
			if metric.RelativeChangePercent != nil {
				actual = *metric.RelativeChangePercent
			}
		} else {
			slope, err := client.queryScalar(ctx, definition.slopeRecord+selector, report.EndedAt)
			if err != nil {
				return SoakAnalysis{}, fmt.Errorf("query %s slope: %w", definition.name, err)
			}
			metric.SlopePerHour = &slope
			actual = slope
		}
		if actual > definition.limit {
			metric.Status = "WARN"
			analysis.Warnings = append(analysis.Warnings, definition.name)
			analysis.OverallResult = "WARN"
		}
		analysis.Metrics = append(analysis.Metrics, metric)
	}
	if coverage < thresholds.MinimumCoverageRatio {
		analysis.Warnings = append(analysis.Warnings, "prometheus_coverage")
		analysis.OverallResult = "WARN"
	}
	return analysis, nil
}

func validateSoakThresholds(thresholds SoakThresholds) error {
	values := []float64{
		thresholds.RSSBytesPerHour, thresholds.HeapBytesPerHour, thresholds.HeapObjectsPerHour,
		thresholds.GoroutinesPerHour, thresholds.ThreadsPerHour, thresholds.FileDescriptorsHour,
		thresholds.SQLiteBytesPerHour, thresholds.CPUIncreasePct, thresholds.LatencyIncreasePct,
	}
	for _, value := range values {
		if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return errors.New("soak warning thresholds must be finite and non-negative")
		}
	}
	if thresholds.MinimumCoverageRatio < 0 || thresholds.MinimumCoverageRatio > 1 || math.IsNaN(thresholds.MinimumCoverageRatio) {
		return errors.New("minimum coverage ratio must be between zero and one")
	}
	return nil
}

func WriteSoakAnalysis(path string, analysis SoakAnalysis) error {
	if err := ValidateSoakAnalysis(analysis); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create analysis directory: %w", err)
	}
	temporary, err := os.CreateTemp(dir, ".trainpilot-soak-*.json")
	if err != nil {
		return fmt.Errorf("create soak analysis: %w", err)
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(analysis); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func ValidateSoakAnalysis(analysis SoakAnalysis) error {
	if analysis.SchemaVersion != SoakAnalysisSchemaVersion {
		return fmt.Errorf("unsupported soak analysis schemaVersion %d", analysis.SchemaVersion)
	}
	if !validRunID(analysis.RunID) || analysis.Source == "" || analysis.PrometheusURL == "" || analysis.Instance == "" {
		return errors.New("soak analysis identity is incomplete")
	}
	if analysis.OverallResult != "PASS" && analysis.OverallResult != "WARN" {
		return fmt.Errorf("invalid soak analysis result %q", analysis.OverallResult)
	}
	if analysis.CoverageRatio < 0 || analysis.CoverageRatio > 1 {
		return errors.New("soak analysis coverage ratio must be between zero and one")
	}
	if analysis.MeasurementStartedAt.IsZero() || !analysis.EndedAt.After(analysis.MeasurementStartedAt) {
		return errors.New("soak analysis measurement interval is invalid")
	}
	if analysis.Window == "" || analysis.ScrapeInterval == "" {
		return errors.New("soak analysis timing metadata is incomplete")
	}
	for _, metric := range analysis.Metrics {
		if metric.Name == "" || (metric.Status != "PASS" && metric.Status != "WARN") {
			return errors.New("soak metric is invalid")
		}
	}
	return nil
}

type prometheusClient struct {
	baseURL string
	http    *http.Client
}

func newPrometheusClient(baseURL string, httpClient *http.Client) prometheusClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return prometheusClient{baseURL: baseURL, http: httpClient}
}

func (c prometheusClient) queryScalar(ctx context.Context, query string, at time.Time) (float64, error) {
	values := url.Values{"query": {query}, "time": {strconv.FormatInt(at.Unix(), 10)}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1/query?"+values.Encode(), nil)
	if err != nil {
		return 0, err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return 0, fmt.Errorf("Prometheus returned %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	var envelope struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Value []json.RawMessage `json:"value"`
			} `json:"result"`
		} `json:"data"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		return 0, err
	}
	if envelope.Status != "success" {
		return 0, fmt.Errorf("Prometheus query failed: %s", envelope.Error)
	}
	if envelope.Data.ResultType != "vector" || len(envelope.Data.Result) != 1 || len(envelope.Data.Result[0].Value) != 2 {
		return 0, fmt.Errorf("Prometheus query returned %d series, want exactly one", len(envelope.Data.Result))
	}
	var text string
	if err := json.Unmarshal(envelope.Data.Result[0].Value[1], &text); err != nil {
		return 0, err
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("invalid Prometheus sample %q", text)
	}
	return value, nil
}

func normalizedPrometheusURL(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(value, "/"))
	if err != nil {
		return "", fmt.Errorf("parse Prometheus URL: %w", err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", errors.New("Prometheus URL must use http or https and include a host")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("Prometheus URL must not contain credentials, a query, or a fragment")
	}
	return parsed.String(), nil
}

func promDuration(duration time.Duration) string {
	return strconv.FormatInt(int64(duration/time.Second), 10) + "s"
}
