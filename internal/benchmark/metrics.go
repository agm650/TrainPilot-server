package benchmark

import (
	"context"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/agm650/TrainPilot-server/internal/client"
)

const maxUnexpectedErrorDetails = 8

type operationRecorder struct {
	measuring atomic.Bool
	mu        sync.Mutex
	started   time.Time
	stats     map[string]*operationStats
	live      *LiveMetrics
}

type operationStats struct {
	Count                    int64
	Successes                int64
	ExpectedErrors           int64
	UnexpectedErrors         int64
	Timeouts                 int64
	Skipped                  int64
	Latencies                []time.Duration
	UnexpectedErrorsByDetail map[operationErrorDetailKey]int64
}

type operationErrorDetailKey struct {
	Kind        string
	HTTPStatus  int
	ProblemCode string
}

type expectedOperationError struct {
	err error
}

func (e expectedOperationError) Error() string { return e.err.Error() }
func (e expectedOperationError) Unwrap() error { return e.err }

func expectedError(err error) error {
	if err == nil {
		return nil
	}
	return expectedOperationError{err: err}
}

func newOperationRecorder(live ...*LiveMetrics) *operationRecorder {
	var metrics *LiveMetrics
	if len(live) > 0 {
		metrics = live[0]
	}
	return &operationRecorder{stats: make(map[string]*operationStats), live: metrics}
}

func (r *operationRecorder) StartMeasurement(now time.Time) {
	r.mu.Lock()
	r.started = now
	r.mu.Unlock()
	r.measuring.Store(true)
}

func (r *operationRecorder) Record(name string, latency time.Duration, err error) {
	r.live.observeOperation(name, latency, err)
	if !r.measuring.Load() {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	stats := r.stats[name]
	if stats == nil {
		stats = &operationStats{}
		r.stats[name] = stats
	}
	stats.Count++
	if err == nil {
		stats.Successes++
	} else {
		var expected expectedOperationError
		if errors.As(err, &expected) {
			stats.ExpectedErrors++
		} else {
			stats.UnexpectedErrors++
			stats.recordUnexpectedError(err)
		}
		if contextCanceledOrDeadline(err) {
			stats.Timeouts++
		}
	}
	stats.Latencies = append(stats.Latencies, latency)
}

func (s *operationStats) recordUnexpectedError(err error) {
	if s.UnexpectedErrorsByDetail == nil {
		s.UnexpectedErrorsByDetail = make(map[operationErrorDetailKey]int64)
	}
	detail := classifyUnexpectedError(err)
	if _, exists := s.UnexpectedErrorsByDetail[detail]; !exists && len(s.UnexpectedErrorsByDetail) >= maxUnexpectedErrorDetails-1 {
		detail = operationErrorDetailKey{Kind: "other"}
	}
	s.UnexpectedErrorsByDetail[detail]++
}

func classifyUnexpectedError(err error) operationErrorDetailKey {
	var httpError *client.HTTPError
	if errors.As(err, &httpError) {
		detail := operationErrorDetailKey{Kind: "http", HTTPStatus: httpError.StatusCode}
		if httpError.Problem != nil {
			detail.ProblemCode = sanitizedProblemCode(httpError.Problem.Code)
		}
		return detail
	}
	if contextCanceledOrDeadline(err) {
		return operationErrorDetailKey{Kind: "timeout"}
	}
	if isNetworkError(err) {
		return operationErrorDetailKey{Kind: "network"}
	}
	if isJSONError(err) {
		return operationErrorDetailKey{Kind: "invalid_json"}
	}
	return operationErrorDetailKey{Kind: "other"}
}

func sanitizedProblemCode(value string) string {
	if value == "" || len(value) > 128 {
		return ""
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') &&
			character != '_' && character != '-' && character != '.' {
			return ""
		}
	}
	return value
}

func (r *operationRecorder) Skip(name string) {
	r.live.skipOperation(name)
	if !r.measuring.Load() {
		return
	}
	r.mu.Lock()
	stats := r.stats[name]
	if stats == nil {
		stats = &operationStats{}
		r.stats[name] = stats
	}
	stats.Skipped++
	r.mu.Unlock()
}

func contextCanceledOrDeadline(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

func (r *operationRecorder) Summaries(measured time.Duration, requestedRates map[string]float64, requestedBursts map[string]int64) map[string]OperationSummary {
	r.mu.Lock()
	defer r.mu.Unlock()
	names := make(map[string]struct{}, len(r.stats)+len(requestedRates)+len(requestedBursts))
	for name := range r.stats {
		names[name] = struct{}{}
	}
	for name, rate := range requestedRates {
		if rate > 0 {
			names[name] = struct{}{}
		}
	}
	for name, count := range requestedBursts {
		if count > 0 {
			names[name] = struct{}{}
		}
	}
	result := make(map[string]OperationSummary, len(names))
	for name := range names {
		stats := r.stats[name]
		if stats == nil {
			stats = &operationStats{}
		}
		latencies := append([]time.Duration(nil), stats.Latencies...)
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		summary := OperationSummary{
			RequestedBurstCount: requestedBursts[name], Count: stats.Count, Successes: stats.Successes,
			ExpectedErrors: stats.ExpectedErrors, UnexpectedErrors: stats.UnexpectedErrors,
			Timeouts: stats.Timeouts, Skipped: stats.Skipped, Latency: latencyPercentiles(latencies),
			UnexpectedErrorDetails: summarizeUnexpectedErrors(stats.UnexpectedErrorsByDetail),
		}
		if rate, ok := requestedRates[name]; ok && rate > 0 {
			rateCopy := rate
			summary.RequestedRatePerSecond = &rateCopy
		}
		if measured > 0 {
			summary.AchievedRatePerSecond = float64(stats.Count) / measured.Seconds()
		}
		result[name] = summary
	}
	return result
}

func summarizeUnexpectedErrors(values map[operationErrorDetailKey]int64) []OperationErrorDetail {
	result := make([]OperationErrorDetail, 0, len(values))
	for detail, count := range values {
		result = append(result, OperationErrorDetail{
			Kind: detail.Kind, HTTPStatus: detail.HTTPStatus, ProblemCode: detail.ProblemCode, Count: count,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Kind != result[j].Kind {
			return result[i].Kind < result[j].Kind
		}
		if result[i].HTTPStatus != result[j].HTTPStatus {
			return result[i].HTTPStatus < result[j].HTTPStatus
		}
		return result[i].ProblemCode < result[j].ProblemCode
	})
	return result
}

func latencyPercentiles(values []time.Duration) LatencySummary {
	if len(values) == 0 {
		return LatencySummary{}
	}
	return LatencySummary{
		P50Milliseconds: durationMillis(percentile(values, 50)),
		P90Milliseconds: durationMillis(percentile(values, 90)),
		P95Milliseconds: durationMillis(percentile(values, 95)),
		P99Milliseconds: durationMillis(percentile(values, 99)),
		MaxMilliseconds: durationMillis(values[len(values)-1]),
	}
}

func percentile(values []time.Duration, percent int) time.Duration {
	index := (len(values)*percent + 99) / 100
	if index < 1 {
		index = 1
	}
	if index > len(values) {
		index = len(values)
	}
	return values[index-1]
}

func durationMillis(value time.Duration) float64 {
	return float64(value) / float64(time.Millisecond)
}
