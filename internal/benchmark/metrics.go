package benchmark

import (
	"context"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type operationRecorder struct {
	measuring atomic.Bool
	mu        sync.Mutex
	started   time.Time
	stats     map[string]*operationStats
}

type operationStats struct {
	Count            int64
	Successes        int64
	ExpectedErrors   int64
	UnexpectedErrors int64
	Timeouts         int64
	Skipped          int64
	Latencies        []time.Duration
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

func newOperationRecorder() *operationRecorder {
	return &operationRecorder{stats: make(map[string]*operationStats)}
}

func (r *operationRecorder) StartMeasurement(now time.Time) {
	r.mu.Lock()
	r.started = now
	r.mu.Unlock()
	r.measuring.Store(true)
}

func (r *operationRecorder) Record(name string, latency time.Duration, err error) {
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
		}
		if contextCanceledOrDeadline(err) {
			stats.Timeouts++
		}
	}
	stats.Latencies = append(stats.Latencies, latency)
}

func (r *operationRecorder) Skip(name string) {
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
