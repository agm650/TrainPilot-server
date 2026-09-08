package benchmark

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestAnalyzeSoakUsesFirstAndLastRecordingRuleWindows(t *testing.T) {
	started := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	ended := started.Add(2 * time.Hour)
	firstAt := started.Add(30 * time.Minute).Unix()
	requests := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		query := request.URL.Query().Get("query")
		value := 100.0
		switch {
		case strings.Contains(query, "sum_over_time(up"):
			value = 481
		case strings.Contains(query, "slope_per_hour"):
			value = 2
		case request.URL.Query().Get("time") != strconv.FormatInt(firstAt, 10):
			value = 120
		}
		body := fmt.Sprintf(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[0,%q]}]}}`, strconv.FormatFloat(value, 'f', -1, 64))
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	report := Report{
		SchemaVersion: ReportSchemaVersion, RunID: "00000000-0000-4000-8000-000000000001",
		MeasurementStartedAt: started, EndedAt: ended, Duration: "2h",
	}
	thresholds := DefaultSoakThresholds()
	thresholds.RSSBytesPerHour = 10
	thresholds.HeapBytesPerHour = 10
	thresholds.HeapObjectsPerHour = 10
	thresholds.GoroutinesPerHour = 10
	thresholds.ThreadsPerHour = 10
	thresholds.FileDescriptorsHour = 10
	thresholds.SQLiteBytesPerHour = 10
	thresholds.CPUIncreasePct = 20
	analysis, err := AnalyzeSoak(context.Background(), report, SoakAnalysisOptions{
		PrometheusURL: "http://prometheus.example", Instance: "dut:6060",
		Window: 30 * time.Minute, ScrapeInterval: 15 * time.Second, Thresholds: thresholds, HTTPClient: httpClient,
	})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 28 {
		t.Fatalf("requests=%d", requests)
	}
	if analysis.OverallResult != "WARN" || len(analysis.Warnings) != 2 {
		t.Fatalf("analysis=%+v", analysis)
	}
	if analysis.Metrics[0].FirstAverage != 100 || analysis.Metrics[0].LastAverage != 120 || *analysis.Metrics[0].SlopePerHour != 2 {
		t.Fatalf("first metric=%+v", analysis.Metrics[0])
	}
}

func TestPrometheusURLRejectsCredentialsAndQuery(t *testing.T) {
	for _, value := range []string{"http://user:password@example.test", "http://example.test?token=secret"} {
		if _, err := normalizedPrometheusURL(value); err == nil {
			t.Fatalf("URL %q was accepted", value)
		}
	}
	if got, err := normalizedPrometheusURL("https://example.test/prometheus/"); err != nil || got != "https://example.test/prometheus" {
		t.Fatalf("URL=%q error=%v", got, err)
	}
}

func TestPrometheusQueryEscapesParameters(t *testing.T) {
	values := url.Values{"query": {`up{instance="dut:6060"}`}}
	if !strings.Contains(values.Encode(), "%7B") {
		t.Fatalf("encoded query=%q", values.Encode())
	}
}
