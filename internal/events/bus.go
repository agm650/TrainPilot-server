package events

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/agm650/TrainPilot-server/internal/observability"
)

type Event struct {
	Type      string    `json:"type"`
	Sequence  uint64    `json:"sequence"`
	Timestamp time.Time `json:"timestamp"`
	Payload   any       `json:"payload"`
}

type Bus struct {
	seq     atomic.Uint64
	mu      sync.RWMutex
	subs    map[*subscription]struct{}
	metrics *observability.Metrics
}

type subscription struct {
	mu       sync.Mutex
	events   chan Event
	overflow chan struct{}
}

func New() *Bus { return &Bus{subs: make(map[*subscription]struct{})} }

func (b *Bus) SetMetrics(metrics *observability.Metrics) { b.metrics = metrics }

func (b *Bus) CurrentSequence() uint64 {
	return b.seq.Load()
}

func (b *Bus) Publish(eventType string, payload any) Event {
	e := Event{Type: eventType, Sequence: b.seq.Add(1), Timestamp: time.Now().UTC(), Payload: payload}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for sub := range b.subs {
		sub.mu.Lock()
		dropped := false
		select {
		case sub.events <- e:
		default:
			// Keep the newest state change available to the subscriber. This
			// makes the lost sequence observable as soon as it catches up.
			select {
			case <-sub.events:
				dropped = true
			default:
			}
			select {
			case sub.events <- e:
			default:
				dropped = true
			}
		}
		sub.mu.Unlock()
		if dropped {
			b.metrics.WebSocketQueueDrop(e.Type)
			select {
			case sub.overflow <- struct{}{}:
			default:
			}
		}
	}
	return e
}

func (b *Bus) Subscribe(buffer int) (<-chan Event, func()) {
	events, _, unsubscribe := b.SubscribeWithOverflow(buffer)
	return events, unsubscribe
}

// SubscribeWithOverflow reports when at least one event is dropped for the
// subscriber. A full buffered subscription evicts its oldest event and keeps
// the newest one. The overflow signal is coalesced and never blocks Publish.
// Consumers that require a complete ordered stream must resynchronize or
// disconnect after this signal.
func (b *Bus) SubscribeWithOverflow(buffer int) (<-chan Event, <-chan struct{}, func()) {
	sub := &subscription{events: make(chan Event, buffer), overflow: make(chan struct{}, 1)}
	b.mu.Lock()
	b.subs[sub] = struct{}{}
	b.mu.Unlock()
	return sub.events, sub.overflow, func() {
		b.mu.Lock()
		if _, ok := b.subs[sub]; ok {
			delete(b.subs, sub)
			close(sub.events)
		}
		b.mu.Unlock()
	}
}
