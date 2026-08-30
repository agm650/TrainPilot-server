package clock

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFakeWaitUntilAdvancesDeterministically(t *testing.T) {
	start := time.Date(2026, time.August, 30, 10, 0, 0, 0, time.UTC)
	clk := NewFake(start)
	done := make(chan error, 1)
	go func() {
		done <- clk.WaitUntil(context.Background(), start.Add(5*time.Second))
	}()

	clk.Advance(5 * time.Second)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("fake clock wait did not complete after advance")
	}
}

func TestFakeWaitUntilRespectsContext(t *testing.T) {
	start := time.Date(2026, time.August, 30, 10, 0, 0, 0, time.UTC)
	clk := NewFake(start)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := clk.WaitUntil(ctx, start.Add(time.Hour)); !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitUntil error=%v", err)
	}
}
