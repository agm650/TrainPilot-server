package simulator

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/station"
)

func TestFeedbackOccupationTransitionsUpdatePhysicalStateAndPreserveOrder(t *testing.T) {
	sim := New()
	event := station.FeedbackEvent{Source: "simulator", Kind: "occupancy", Address: 1}
	event.Active = true
	if err := sim.SetFeedback(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	event.Active = false
	if err := sim.SetFeedback(context.Background(), event); err != nil {
		t.Fatal(err)
	}

	first := receiveFeedback(t, sim.Feedback())
	second := receiveFeedback(t, sim.Feedback())
	if !first.Active || second.Active || first.Address != 1 || second.Address != 1 {
		t.Fatalf("feedback order=%+v, %+v", first, second)
	}
	key := FeedbackKey{Source: "simulator", Kind: "occupancy", Address: 1}
	if sim.Snapshot().FeedbackStates[key] {
		t.Fatal("final physical sensor state is active")
	}
}

func TestFeedbackSupportsSeveralSourcesKindsAndAddresses(t *testing.T) {
	sim := New()
	events := []station.FeedbackEvent{
		{Source: "simulator", Kind: "occupancy", Address: 1, Active: true},
		{Source: "simulator", Kind: "contact", Address: 2, Active: true},
		{Source: "external", Kind: "occupancy", Address: 1, Active: true},
	}
	for _, event := range events {
		if err := sim.SetFeedback(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := sim.Snapshot()
	if len(snapshot.FeedbackStates) != len(events) {
		t.Fatalf("feedback states=%+v", snapshot.FeedbackStates)
	}
	for _, event := range events {
		key := FeedbackKey{Source: event.Source, Kind: event.Kind, Address: event.Address}
		if !snapshot.FeedbackStates[key] {
			t.Fatalf("feedback key not active: %+v", key)
		}
	}
}

func TestFeedbackRepeatsUnchangedStateWhenRequestedAgain(t *testing.T) {
	sim := New()
	event := station.FeedbackEvent{Source: "simulator", Kind: "occupancy", Address: 1, Active: true}
	if err := sim.SetFeedback(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if err := sim.SetFeedback(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if first := receiveFeedback(t, sim.Feedback()); !first.Active {
		t.Fatalf("first feedback=%+v", first)
	}
	if second := receiveFeedback(t, sim.Feedback()); !second.Active {
		t.Fatalf("second feedback=%+v", second)
	}
}

func TestFeedbackBounceIsDeterministic(t *testing.T) {
	start := time.Date(2026, time.August, 30, 10, 0, 0, 0, time.UTC)
	clk := newFeedbackStepClock(start)
	sim := NewWithClock(clk)
	event := station.FeedbackEvent{Source: "simulator", Kind: "occupancy", Address: 1}
	done := make(chan error, 1)
	go func() {
		done <- sim.BounceFeedback(context.Background(), event, 20*time.Millisecond)
	}()

	if first := receiveFeedback(t, sim.Feedback()); !first.Active {
		t.Fatalf("first bounce feedback=%+v", first)
	}
	firstWait := receiveFeedbackWait(t, clk)
	if want := start.Add(20 * time.Millisecond); !firstWait.deadline.Equal(want) {
		t.Fatalf("first deadline=%v, want %v", firstWait.deadline, want)
	}
	clk.release(firstWait)
	if second := receiveFeedback(t, sim.Feedback()); second.Active {
		t.Fatalf("second bounce feedback=%+v", second)
	}
	secondWait := receiveFeedbackWait(t, clk)
	if want := start.Add(40 * time.Millisecond); !secondWait.deadline.Equal(want) {
		t.Fatalf("second deadline=%v, want %v", secondWait.deadline, want)
	}
	clk.release(secondWait)
	if third := receiveFeedback(t, sim.Feedback()); !third.Active {
		t.Fatalf("third bounce feedback=%+v", third)
	}
	if err := waitForOperation(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestFeedbackCanChangePhysicalStateWithoutEvent(t *testing.T) {
	sim := New()
	event := station.FeedbackEvent{Source: "simulator", Kind: "occupancy", Address: 1, Active: true}
	sim.SetFeedbackState(event)

	select {
	case received := <-sim.Feedback():
		t.Fatalf("unexpected feedback event: %+v", received)
	default:
	}
	key := FeedbackKey{Source: event.Source, Kind: event.Kind, Address: event.Address}
	snapshot := sim.Snapshot()
	if !snapshot.FeedbackStates[key] {
		t.Fatal("physical feedback state was not updated")
	}
	snapshot.FeedbackStates[key] = false
	if !sim.Snapshot().FeedbackStates[key] {
		t.Fatal("snapshot shared the simulator feedback map")
	}
}

func TestFeedbackTransitionFromAToBIsScriptable(t *testing.T) {
	sim := New()
	a := station.FeedbackEvent{Source: "simulator", Kind: "occupancy", Address: 1, Active: true}
	b := station.FeedbackEvent{Source: "simulator", Kind: "occupancy", Address: 2, Active: true}
	for _, event := range []station.FeedbackEvent{a, b, {Source: a.Source, Kind: a.Kind, Address: a.Address, Active: false}} {
		if err := sim.SetFeedback(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}

	sequence := []station.FeedbackEvent{
		receiveFeedback(t, sim.Feedback()),
		receiveFeedback(t, sim.Feedback()),
		receiveFeedback(t, sim.Feedback()),
	}
	if sequence[0].Address != 1 || !sequence[0].Active ||
		sequence[1].Address != 2 || !sequence[1].Active ||
		sequence[2].Address != 1 || sequence[2].Active {
		t.Fatalf("transition sequence=%+v", sequence)
	}
	snapshot := sim.Snapshot()
	if snapshot.FeedbackStates[FeedbackKey{Source: a.Source, Kind: a.Kind, Address: 1}] ||
		!snapshot.FeedbackStates[FeedbackKey{Source: b.Source, Kind: b.Kind, Address: 2}] {
		t.Fatalf("transition physical state=%+v", snapshot.FeedbackStates)
	}
}

func TestSetFeedbackReportsFullBufferWithoutDeadlock(t *testing.T) {
	sim := New()
	for address := 0; address < 64; address++ {
		if err := sim.SetFeedback(context.Background(), station.FeedbackEvent{Source: "simulator", Kind: "occupancy", Address: address, Active: true}); err != nil {
			t.Fatalf("address %d error=%v", address, err)
		}
	}
	overflow := station.FeedbackEvent{Source: "simulator", Kind: "occupancy", Address: 64, Active: true}
	if err := sim.SetFeedback(context.Background(), overflow); !errors.Is(err, ErrFeedbackBufferFull) {
		t.Fatalf("overflow error=%v", err)
	}
	key := FeedbackKey{Source: overflow.Source, Kind: overflow.Kind, Address: overflow.Address}
	if !sim.Snapshot().FeedbackStates[key] {
		t.Fatal("overflow did not update physical state")
	}
	for address := 0; address < 64; address++ {
		<-sim.Feedback()
	}
	select {
	case event := <-sim.Feedback():
		t.Fatalf("overflow event was unexpectedly delivered: %+v", event)
	default:
	}
}

func TestFeedbackInjectionRespectsCanceledContext(t *testing.T) {
	sim := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	event := station.FeedbackEvent{Source: "simulator", Kind: "occupancy", Address: 1, Active: true}
	if err := sim.SetFeedback(ctx, event); !errors.Is(err, context.Canceled) {
		t.Fatalf("SetFeedback error=%v", err)
	}
	key := FeedbackKey{Source: event.Source, Kind: event.Kind, Address: event.Address}
	if _, ok := sim.Snapshot().FeedbackStates[key]; ok {
		t.Fatal("canceled feedback changed physical state")
	}
}

func TestFeedbackStateIsConcurrentSafe(t *testing.T) {
	sim := New()
	const workers = 4
	const iterations = 250
	var wg sync.WaitGroup
	wg.Add(workers + 1)
	for worker := 0; worker < workers; worker++ {
		worker := worker
		go func() {
			defer wg.Done()
			for address := 0; address < iterations; address++ {
				sim.SetFeedbackState(station.FeedbackEvent{
					Source:  "source",
					Kind:    "occupancy",
					Address: worker*iterations + address,
					Active:  true,
				})
			}
		}()
	}
	go func() {
		defer wg.Done()
		for iteration := 0; iteration < iterations; iteration++ {
			_ = sim.Snapshot().FeedbackStates
		}
	}()
	wg.Wait()
	if got := len(sim.Snapshot().FeedbackStates); got != workers*iterations {
		t.Fatalf("feedback state count=%d", got)
	}
}

func TestFeedbackSequenceRejectsNegativeDelayBeforeEmitting(t *testing.T) {
	sim := New()
	err := sim.EmitFeedbackSequence(context.Background(), station.FeedbackEvent{Source: "simulator", Kind: "occupancy", Address: 1}, []FeedbackTransition{
		{Active: true},
		{Delay: -time.Millisecond, Active: false},
	})
	if err == nil {
		t.Fatal("negative feedback delay accepted")
	}
	if len(sim.Snapshot().FeedbackStates) != 0 {
		t.Fatal("invalid sequence partially changed physical state")
	}
}

func receiveFeedback(t *testing.T, feedback <-chan station.FeedbackEvent) station.FeedbackEvent {
	t.Helper()
	select {
	case event := <-feedback:
		return event
	case <-time.After(time.Second):
		t.Fatal("feedback event was not delivered")
		return station.FeedbackEvent{}
	}
}

type feedbackWait struct {
	deadline time.Time
	release  chan struct{}
}

type feedbackStepClock struct {
	mu    sync.RWMutex
	now   time.Time
	waits chan feedbackWait
}

func newFeedbackStepClock(start time.Time) *feedbackStepClock {
	return &feedbackStepClock{now: start, waits: make(chan feedbackWait)}
}

func (c *feedbackStepClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

func (c *feedbackStepClock) WaitUntil(ctx context.Context, deadline time.Time) error {
	wait := feedbackWait{deadline: deadline, release: make(chan struct{})}
	select {
	case c.waits <- wait:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-wait.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *feedbackStepClock) release(wait feedbackWait) {
	c.mu.Lock()
	c.now = wait.deadline
	c.mu.Unlock()
	close(wait.release)
}

func receiveFeedbackWait(t *testing.T, clk *feedbackStepClock) feedbackWait {
	t.Helper()
	select {
	case wait := <-clk.waits:
		return wait
	case <-time.After(time.Second):
		t.Fatal("feedback sequence did not wait for its next step")
		return feedbackWait{}
	}
}
