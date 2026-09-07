package benchmark

import "sort"

func applyReportPolicy(report *Report, invariantFailure bool) {
	report.Warnings = reportWarnings(*report)
	report.OverallResult = "PASS"
	if invariantFailure || reportHasFunctionalFailure(*report) {
		report.OverallResult = "FAIL"
		return
	}
	if len(report.Warnings) > 0 {
		report.OverallResult = "WARN"
	}
}

func ApplyReportPolicy(report *Report) {
	applyReportPolicy(report, false)
}

func reportHasFunctionalFailure(report Report) bool {
	for _, invariant := range report.Invariants {
		if invariant.Status == "FAIL" {
			return true
		}
	}
	for _, operation := range report.Operations {
		if operation.UnexpectedErrors > 0 {
			return true
		}
	}
	return false
}

func reportWarnings(report Report) []ThresholdResult {
	if report.Profile.Name != "medium" && report.Profile.Name != "large" {
		return nil
	}
	warnings := make([]ThresholdResult, 0)
	for name, operation := range report.Operations {
		if !initialCommandOperation(name) {
			continue
		}
		warnings = appendThresholdWarning(warnings, name+".p95", operation.Latency.P95Milliseconds, 50, "ms")
		warnings = appendThresholdWarning(warnings, name+".p99", operation.Latency.P99Milliseconds, 100, "ms")
	}
	warnings = appendThresholdWarning(warnings, "feedback_to_websocket.p95", report.WebSocket.FeedbackLatency.P95Milliseconds, 100, "ms")
	if report.WebSocket.QueueOverflows != nil {
		warnings = appendThresholdWarning(warnings, "websocket.queue_overflows", float64(*report.WebSocket.QueueOverflows), 1, "count")
	}
	if report.SystemMetrics != nil {
		warnings = appendIntZeroWarning(warnings, "sqlite.errors", report.SystemMetrics.SQLiteErrors)
		warnings = appendIntZeroWarning(warnings, "network.drops", report.SystemMetrics.NetworkDrops)
		warnings = appendIntZeroWarning(warnings, "swap.max_bytes", report.SystemMetrics.SwapMaxBytes)
		warnings = appendIntZeroWarning(warnings, "thermal.throttling_events", report.SystemMetrics.ThermalThrottlingEvents)
	}
	sort.Slice(warnings, func(i, j int) bool { return warnings[i].Name < warnings[j].Name })
	return warnings
}

func appendThresholdWarning(warnings []ThresholdResult, name string, actual, limit float64, unit string) []ThresholdResult {
	if actual < limit {
		return warnings
	}
	return append(warnings, ThresholdResult{Name: name, Actual: actual, Limit: limit, Unit: unit, Status: "WARN"})
}

func appendIntZeroWarning(warnings []ThresholdResult, name string, actual *int64) []ThresholdResult {
	if actual == nil || *actual == 0 {
		return warnings
	}
	return append(warnings, ThresholdResult{Name: name, Actual: float64(*actual), Limit: 1, Unit: "count", Status: "WARN"})
}
