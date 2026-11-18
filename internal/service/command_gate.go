package service

import (
	"context"
	"sync"
)

// priorityCommandGate serializes command-station writes. Once a safety
// command is waiting, later ordinary commands remain queued until every
// waiting safety command has run.
type priorityCommandGate struct {
	mu              sync.Mutex
	active          bool
	safetyWaiters   int
	ordinaryWaiters int
	changed         chan struct{}
	waiters         [3]int
	activePriority  int
	activeCancel    context.CancelFunc
}

const (
	commandOrdinary = iota
	commandSignalStop
	commandSafety
)

func newPriorityCommandGate() *priorityCommandGate {
	return &priorityCommandGate{changed: make(chan struct{})}
}

func (g *priorityCommandGate) acquire(ctx context.Context, safety bool) (func(), error) {
	priority := commandOrdinary
	if safety {
		priority = commandSafety
	}
	return g.acquirePriority(ctx, priority, nil)
}

// Only signal operations supply a cancellation hook. Safety commands preempt
// their transport I/O, but never run concurrent writes against the station.
func (g *priorityCommandGate) acquirePriority(ctx context.Context, priority int, cancel context.CancelFunc) (func(), error) {
	g.mu.Lock()
	safety := priority == commandSafety
	g.waiters[priority]++
	if safety {
		g.safetyWaiters++
	} else {
		g.ordinaryWaiters++
	}

	if g.active && priority > g.activePriority && g.activeCancel != nil {
		g.activeCancel()
	}
	higherWaiting := func() bool {
		for i := priority + 1; i < len(g.waiters); i++ {
			if g.waiters[i] > 0 {
				return true
			}
		}
		return false
	}
	for g.active || higherWaiting() {
		changed := g.changed
		g.mu.Unlock()

		select {
		case <-ctx.Done():
			g.mu.Lock()
			g.waiters[priority]--
			if safety {
				g.safetyWaiters--
			} else {
				g.ordinaryWaiters--
			}
			g.signalLocked()
			g.mu.Unlock()
			return nil, ctx.Err()
		case <-changed:
		}

		g.mu.Lock()
	}

	if err := ctx.Err(); err != nil {
		g.waiters[priority]--
		if safety {
			g.safetyWaiters--
		} else {
			g.ordinaryWaiters--
		}
		g.signalLocked()
		g.mu.Unlock()
		return nil, err
	}

	if safety {
		g.safetyWaiters--
	} else {
		g.ordinaryWaiters--
	}
	g.waiters[priority]--
	g.active = true
	g.activePriority = priority
	g.activeCancel = cancel
	g.mu.Unlock()

	return func() {
		g.mu.Lock()
		g.active = false
		g.activeCancel = nil
		g.signalLocked()
		g.mu.Unlock()
	}, nil
}

func (g *priorityCommandGate) signalLocked() {
	close(g.changed)
	g.changed = make(chan struct{})
}
