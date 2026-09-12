package benchmark

import (
	"context"
	"errors"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/client"
	"github.com/agm650/TrainPilot-server/internal/model"
)

func TestWithCurrentLeaseUsesCurrentBinding(t *testing.T) {
	binding := leaseBinding{lease: model.ControlLease{ID: "lease-current"}, sessionIndex: 0}
	engine := &runEngine{
		sessions: []*benchSession{{client: &client.Client{}}},
		leases:   map[string]leaseBinding{"loco-1": binding},
	}
	called := false
	err := engine.withCurrentLease(context.Background(), "loco-1", binding, func(*client.Client) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("current lease operation was not called")
	}
}

func TestWithCurrentLeaseRejectsBindingRemovedWhileWaitingForSession(t *testing.T) {
	binding := leaseBinding{lease: model.ControlLease{ID: "lease-released"}, sessionIndex: 0}
	session := &benchSession{client: &client.Client{}}
	engine := &runEngine{
		sessions: []*benchSession{session},
		leases:   map[string]leaseBinding{"loco-1": binding},
	}

	session.mu.Lock()
	started := make(chan struct{})
	done := make(chan error, 1)
	called := false
	go func() {
		close(started)
		done <- engine.withCurrentLease(context.Background(), "loco-1", binding, func(*client.Client) error {
			called = true
			return nil
		})
	}()
	<-started
	engine.stateMu.Lock()
	delete(engine.leases, "loco-1")
	engine.stateMu.Unlock()
	session.mu.Unlock()

	if err := <-done; !errors.Is(err, errNoOperationTarget) {
		t.Fatalf("error=%v want %v", err, errNoOperationTarget)
	}
	if called {
		t.Fatal("stale lease operation was called")
	}
}
