package benchmark

import (
	"context"
	"encoding/binary"
	"hash/fnv"
	"math/rand"
	"sort"
	"time"
)

type scheduledJob struct {
	operation string
	seed      int64
}

func runBurstScheduler(ctx context.Context, bursts []BurstProfile, seed int64, jobs chan<- scheduledJob, dropped func()) {
	ordered := append([]BurstProfile(nil), bursts...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].At.Duration < ordered[j].At.Duration })
	started := time.Now()
	var sequence uint64
	for _, burst := range ordered {
		delay := burst.At.Duration - time.Since(started)
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		for index := 0; index < burst.Count; index++ {
			sequence++
			job := scheduledJob{operation: burst.Operation, seed: operationSeed(seed, "burst:"+burst.Operation, sequence)}
			select {
			case jobs <- job:
			default:
				dropped()
			}
		}
	}
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
