package service

import (
	"context"
	"sync"

	"github.com/agm650/TrainPilot-server/internal/model"
)

const signalQueueCapacity = 64

type signalJob struct {
	id          string
	runtime     *signalRuntime
	generation  uint64
	target      model.SignalAspectID
	restrictive bool
	ctx         context.Context
	cancel      context.CancelFunc
	done        chan error
	sequence    uint64
}

type signalCommandQueue struct {
	mu       sync.Mutex
	pending  map[string]*signalJob
	active   *signalJob
	sequence uint64
	wake     chan struct{}
}

func newSignalCommandQueue() *signalCommandQueue {
	return &signalCommandQueue{pending: map[string]*signalJob{}, wake: make(chan struct{}, 1)}
}

func (q *signalCommandQueue) enqueue(job *signalJob) (*signalJob, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	var victim *signalJob
	if previous := q.pending[job.id]; previous != nil {
		victim = previous
		delete(q.pending, job.id)
		previous.cancel()
		previous.done <- ErrSafetyPreempted
	}
	if len(q.pending) >= signalQueueCapacity {
		if job.restrictive {
			for _, v := range q.pending {
				if !v.restrictive && (victim == nil || v.sequence < victim.sequence) {
					victim = v
				}
			}
		}
		if victim == nil {
			return nil, ErrSignalBusy
		}
		delete(q.pending, victim.id)
		victim.cancel()
		victim.done <- ErrSafetyPreempted
	}
	q.sequence++
	job.sequence = q.sequence
	q.pending[job.id] = job
	if job.restrictive && q.active != nil && !q.active.restrictive {
		q.active.cancel()
	}
	select {
	case q.wake <- struct{}{}:
	default:
	}
	return victim, nil
}

func (q *signalCommandQueue) take() *signalJob {
	q.mu.Lock()
	defer q.mu.Unlock()
	var next *signalJob
	for _, job := range q.pending {
		if next == nil || (job.restrictive && !next.restrictive) || (job.restrictive == next.restrictive && job.sequence < next.sequence) {
			next = job
		}
	}
	if next != nil {
		delete(q.pending, next.id)
	}
	q.active = next
	return next
}

func (s *SignalService) runCommands(ctx context.Context, q *signalCommandQueue) {
	defer s.wg.Done()
	defer func() {
		q.mu.Lock()
		defer q.mu.Unlock()
		q.active = nil
		for id, job := range q.pending {
			job.cancel()
			job.done <- ctx.Err()
			delete(q.pending, id)
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		job := q.take()
		if job == nil {
			select {
			case <-ctx.Done():
				return
			case <-q.wake:
			}
			continue
		}
		err := s.dispatch(job.ctx, job.id, job.runtime, job.generation, job.target, job.restrictive)
		job.cancel()
		job.done <- err
		s.notify()
	}
}

func (s *SignalService) enqueueSignalLocked(ctx context.Context, cancel context.CancelFunc, id string, r *signalRuntime, generation uint64, target model.SignalAspectID, restrictive bool) (*signalJob, error) {
	job := &signalJob{id: id, runtime: r, generation: generation, target: target, restrictive: restrictive, ctx: ctx, cancel: cancel, done: make(chan error, 1)}
	victim, err := s.queue.enqueue(job)
	if err != nil {
		return nil, err
	}
	if victim != nil && s.entries[victim.id] == victim.runtime && victim.runtime.generation == victim.generation {
		s.invalidateLocked(victim.runtime, "queue_preempted")
	}
	return job, nil
}
