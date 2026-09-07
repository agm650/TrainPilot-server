package benchmark

import (
	"context"
	"encoding/binary"
	"hash/fnv"
	"math/rand"
	"time"
)

type scheduledJob struct {
	operation string
	seed      int64
}

func scheduleOffsets(rate float64, duration time.Duration, seed int64) []time.Duration {
	if rate <= 0 || duration <= 0 {
		return nil
	}
	interval := time.Duration(float64(time.Second) / rate)
	if interval <= 0 {
		interval = time.Nanosecond
	}
	random := rand.New(rand.NewSource(seed))
	first := time.Duration(0)
	if interval > 1 {
		first = time.Duration(random.Int63n(int64(interval)))
	}
	var result []time.Duration
	for offset := first; offset < duration; offset += interval {
		result = append(result, offset)
	}
	return result
}

func operationSeed(base int64, operation string, sequence uint64) int64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(operation))
	var data [16]byte
	binary.LittleEndian.PutUint64(data[:8], uint64(base))
	binary.LittleEndian.PutUint64(data[8:], sequence)
	_, _ = hash.Write(data[:])
	return int64(hash.Sum64())
}

func runRateScheduler(ctx context.Context, operation string, rate float64, seed int64, jobs chan<- scheduledJob, dropped func()) {
	if rate <= 0 {
		return
	}
	interval := time.Duration(float64(time.Second) / rate)
	if interval <= 0 {
		interval = time.Nanosecond
	}
	random := rand.New(rand.NewSource(operationSeed(seed, operation, 0)))
	delay := time.Duration(0)
	if interval > 1 {
		delay = time.Duration(random.Int63n(int64(interval)))
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	var sequence uint64
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			sequence++
			job := scheduledJob{operation: operation, seed: operationSeed(seed, operation, sequence)}
			select {
			case jobs <- job:
			default:
				dropped()
			}
			timer.Reset(interval)
		}
	}
}
