package benchmark

import (
	"errors"
	"testing"
	"time"
)

func TestOperationRecorderProducesExactPercentiles(t *testing.T) {
	recorder := newOperationRecorder()
	recorder.Record("read", time.Second, nil)
	recorder.StartMeasurement(time.Now())
	recorder.Skip("read")
	for value := 1; value <= 100; value++ {
		var err error
		if value == 100 {
			err = errors.New("failure")
		}
		recorder.Record("read", time.Duration(value)*time.Millisecond, err)
	}
	summary := recorder.Summaries(10 * time.Second)["read"]
	if summary.Total != 100 || summary.Successes != 99 || summary.Failures != 1 || summary.Skipped != 1 || summary.ThroughputPerSecond != 10 {
		t.Fatalf("summary=%+v", summary)
	}
	if summary.Latency.P50Milliseconds != 50 || summary.Latency.P90Milliseconds != 90 || summary.Latency.P95Milliseconds != 95 || summary.Latency.P99Milliseconds != 99 || summary.Latency.MaxMilliseconds != 100 {
		t.Fatalf("latency=%+v", summary.Latency)
	}
}
