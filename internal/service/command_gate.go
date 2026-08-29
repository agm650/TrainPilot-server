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
}

func newPriorityCommandGate() *priorityCommandGate {
	return &priorityCommandGate{changed: make(chan struct{})}
}

func (g *priorityCommandGate) acquire(ctx context.Context, safety bool) (func(), error) {
	g.mu.Lock()
	if safety {
		g.safetyWaiters++
	} else {
		g.ordinaryWaiters++
	}

	for g.active || (!safety && g.safetyWaiters > 0) {
		changed := g.changed
		g.mu.Unlock()

		select {
		case <-ctx.Done():
			g.mu.Lock()
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
	g.active = true
	g.mu.Unlock()

	return func() {
		g.mu.Lock()
		g.active = false
		g.signalLocked()
		g.mu.Unlock()
	}, nil
}

func (g *priorityCommandGate) signalLocked() {
	close(g.changed)
	g.changed = make(chan struct{})
}
