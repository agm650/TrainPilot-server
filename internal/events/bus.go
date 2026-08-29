package events

import (
	"sync"
	"sync/atomic"
	"time"
)

type Event struct {
	Type      string    `json:"type"`
	Sequence  uint64    `json:"sequence"`
	Timestamp time.Time `json:"timestamp"`
	Payload   any       `json:"payload"`
}

type Bus struct {
	seq  atomic.Uint64
	mu   sync.RWMutex
	subs map[*subscription]struct{}
}

type subscription struct {
	events   chan Event
	overflow chan struct{}
}

func New() *Bus { return &Bus{subs: make(map[*subscription]struct{})} }

func (b *Bus) CurrentSequence() uint64 {
	return b.seq.Load()
}

func (b *Bus) Publish(eventType string, payload any) Event {
	e := Event{Type: eventType, Sequence: b.seq.Add(1), Timestamp: time.Now().UTC(), Payload: payload}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for sub := range b.subs {
		select {
		case sub.events <- e:
		default:
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

// SubscribeWithOverflow reports when at least one event cannot be queued for
// the subscriber. The overflow signal is coalesced and never blocks Publish.
// Consumers that require a complete ordered stream must stop using the
// subscription after this signal and resynchronize or disconnect.
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
