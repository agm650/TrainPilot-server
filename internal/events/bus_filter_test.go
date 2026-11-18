package events

import "testing"

func TestFilteredSubscriberIgnoresUnrelatedTrafficAndReportsItsOwnOverflow(t *testing.T) {
	b := New()
	ch, overflow, cancel := b.SubscribeTypesWithOverflow(1, "layout.imported")
	defer cancel()
	for range 100 {
		b.Publish("signal.aspect.changed", nil)
	}
	select {
	case <-ch:
		t.Fatal("unrelated event delivered")
	default:
	}
	select {
	case <-overflow:
		t.Fatal("unrelated traffic overflowed filter")
	default:
	}
	b.Publish("layout.imported", nil)
	latest := b.Publish("layout.imported", nil)
	if got := <-ch; got.Sequence != latest.Sequence || got.Sequence != 102 {
		t.Fatal(got)
	}
	select {
	case <-overflow:
	default:
		t.Fatal("matching overflow not reported")
	}
}
