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
	subs map[chan Event]struct{}
}

func New() *Bus { return &Bus{subs: make(map[chan Event]struct{})} }

func (b *Bus) Publish(eventType string, payload any) Event {
	e := Event{Type: eventType, Sequence: b.seq.Add(1), Timestamp: time.Now().UTC(), Payload: payload}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs {
		select {
		case ch <- e:
		default:
		}
	}
	return e
}

func (b *Bus) Subscribe(buffer int) (<-chan Event, func()) {
	ch := make(chan Event, buffer)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		if _, ok := b.subs[ch]; ok {
			delete(b.subs, ch)
			close(ch)
		}
		b.mu.Unlock()
	}
}
