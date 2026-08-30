package clock

import (
	"context"
	"sync"
	"time"
)

type Clock interface {
	Now() time.Time
}

type Real struct{}

func (Real) Now() time.Time { return time.Now().UTC() }

func (Real) WaitUntil(ctx context.Context, deadline time.Time) error {
	delay := time.Until(deadline)
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type Fake struct {
	mu      sync.RWMutex
	now     time.Time
	changed chan struct{}
}

func NewFake(start time.Time) *Fake {
	return &Fake{now: start.UTC(), changed: make(chan struct{})}
}
func (f *Fake) Now() time.Time {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.now
}
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	f.notifyLocked()
	f.mu.Unlock()
}

// Set moves the fake clock to an explicit instant. It is useful when replaying
// the same deterministic scenario from the same logical origin.
func (f *Fake) Set(now time.Time) {
	f.mu.Lock()
	f.now = now.UTC()
	f.notifyLocked()
	f.mu.Unlock()
}

func (f *Fake) notifyLocked() {
	if f.changed != nil {
		close(f.changed)
	}
	f.changed = make(chan struct{})
}

func (f *Fake) WaitUntil(ctx context.Context, deadline time.Time) error {
	for {
		f.mu.Lock()
		if !f.now.Before(deadline) {
			f.mu.Unlock()
			return ctx.Err()
		}
		if f.changed == nil {
			f.changed = make(chan struct{})
		}
		changed := f.changed
		f.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}
