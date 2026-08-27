package events

import (
	"testing"
	"time"
)

func TestPublishDeliversOrderedEvents(t *testing.T) {
	bus := New()
	ch, unsubscribe := bus.Subscribe(2)
	defer unsubscribe()

	first := bus.Publish("first", map[string]int{"value": 1})
	second := bus.Publish("second", "payload")
	if first.Sequence != 1 || second.Sequence != 2 {
		t.Fatalf("sequences=%d,%d", first.Sequence, second.Sequence)
	}
	if first.Timestamp.Location() != time.UTC {
		t.Fatalf("timestamp location=%v", first.Timestamp.Location())
	}

	if got := <-ch; got.Type != "first" || got.Sequence != 1 {
		t.Fatalf("first event=%+v", got)
	}
	if got := <-ch; got.Type != "second" || got.Payload != "payload" {
		t.Fatalf("second event=%+v", got)
	}
}

func TestCurrentSequenceTracksPublishedEvents(t *testing.T) {
	bus := New()
	if got := bus.CurrentSequence(); got != 0 {
		t.Fatalf("initial sequence=%d want 0", got)
	}

	bus.Publish("first", nil)
	bus.Publish("second", nil)

	if got := bus.CurrentSequence(); got != 2 {
		t.Fatalf("current sequence=%d want 2", got)
	}
}

func TestSlowSubscriberDoesNotBlockPublisher(t *testing.T) {
	bus := New()
	_, unsubscribe := bus.Subscribe(0)
	defer unsubscribe()

	done := make(chan struct{})
	go func() {
		bus.Publish("dropped", nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish blocked on an unbuffered subscriber")
	}
	if got := bus.Publish("next", nil).Sequence; got != 2 {
		t.Fatalf("sequence=%d want 2", got)
	}
}

func TestUnsubscribeIsIdempotentAndClosesChannel(t *testing.T) {
	bus := New()
	ch, unsubscribe := bus.Subscribe(1)
	unsubscribe()
	unsubscribe()
	if _, ok := <-ch; ok {
		t.Fatal("subscription channel is still open")
	}
	bus.Publish("after-unsubscribe", nil)
}
