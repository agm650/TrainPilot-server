package benchmark

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/client"
)

func newFeedbackTestEngine(serverURL string, targets []FeedbackTarget) *runEngine {
	invariants := newInvariantTracker()
	return &runEngine{
		options:      RunOptions{Fixture: Fixture{FeedbackTargets: targets}},
		profile:      Profile{OperationTimeout: Duration{Duration: 5 * time.Second}},
		feedback:     make(map[string]bool),
		invariants:   invariants,
		expectations: newExpectationTracker(invariants),
		sessions: []*benchSession{
			{client: client.New(serverURL)},
			{client: client.New(serverURL)},
			{client: client.New(serverURL)},
		},
	}
}

func feedbackRandomFor(targetCount, targetIndex, sessionIndex int) *rand.Rand {
	for seed := int64(0); ; seed++ {
		random := rand.New(rand.NewSource(seed))
		if random.Intn(targetCount) == targetIndex && random.Intn(3) == sessionIndex {
			return rand.New(rand.NewSource(seed))
		}
	}
}

func TestConcurrentFeedbackJobsPreserveTransitions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var releaseOnce sync.Once
	finishFirst := func() { releaseOnce.Do(func() { close(releaseFirst) }) }
	var requests atomic.Int32
	var mu sync.Mutex
	var applied []bool
	var previous bool
	var engine *runEngine
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Active bool `json:"active"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if requests.Add(1) == 1 {
			close(firstStarted)
			select {
			case <-releaseFirst:
			case <-ctx.Done():
				return
			}
		}
		mu.Lock()
		applied = append(applied, request.Active)
		// Occupancy changes publish one event only when the state changes.
		if len(applied) == 1 || previous != request.Active {
			engine.expectations.Fulfill(fmt.Sprintf("feedback:block:%t", request.Active))
		}
		previous = request.Active
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	defer finishFirst()
	engine = newFeedbackTestEngine(server.URL, []FeedbackTarget{{Source: "simulator", Kind: "occupancy", Address: 1, BlockID: "block"}})
	done := make(chan error, 3)
	go func() { done <- engine.performFeedback(ctx, feedbackRandomFor(1, 0, 0)) }()
	select {
	case <-firstStarted:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for _, sessionIndex := range []int{1, 2} {
		go func() { done <- engine.performFeedback(ctx, feedbackRandomFor(1, 0, sessionIndex)) }()
	}
	select {
	case err := <-done:
		t.Fatalf("a feedback job finished before the first request was released: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	finishFirst()
	for range 3 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(applied, []bool{true, false, true}) {
		t.Fatalf("applied transitions=%v", applied)
	}
	if pending := engine.expectations.Pending(); pending != 0 {
		t.Fatalf("%d feedback events were not observed", pending)
	}
}

func TestFeedbackFailureDoesNotAdvanceState(t *testing.T) {
	for _, initial := range []bool{false, true} {
		t.Run(fmt.Sprintf("initial=%t", initial), func(t *testing.T) {
			var mu sync.Mutex
			var applied []bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Active bool `json:"active"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				mu.Lock()
				defer mu.Unlock()
				applied = append(applied, request.Active)
				if len(applied) == 1 {
					w.WriteHeader(http.StatusConflict)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			engine := newFeedbackTestEngine(server.URL, []FeedbackTarget{{Source: "simulator", Kind: "occupancy", Address: 1, BlockID: "block"}})
			engine.feedback["simulator:occupancy:1"] = initial
			if err := engine.performFeedback(context.Background(), feedbackRandomFor(1, 0, 0)); err == nil {
				t.Fatal("expected feedback rejection")
			}
			if pending := engine.expectations.Pending(); pending != 0 {
				t.Fatalf("failed feedback left %d pending expectations", pending)
			}
			if err := engine.performFeedback(context.Background(), feedbackRandomFor(1, 0, 1)); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if !reflect.DeepEqual(applied, []bool{!initial, !initial}) {
				t.Fatalf("rejected injection changed the next transition: %v", applied)
			}
		})
	}
}

func TestFeedbackTargetsRemainConcurrent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var releaseOnce sync.Once
	finishFirst := func() { releaseOnce.Do(func() { close(releaseFirst) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Address int `json:"address"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if request.Address == 1 {
			close(firstStarted)
			select {
			case <-releaseFirst:
			case <-ctx.Done():
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	defer finishFirst()
	engine := newFeedbackTestEngine(server.URL, []FeedbackTarget{
		{Source: "simulator", Kind: "occupancy", Address: 1, BlockID: "block-1"},
		{Source: "simulator", Kind: "occupancy", Address: 2, BlockID: "block-2"},
	})
	firstDone := make(chan error, 1)
	go func() { firstDone <- engine.performFeedback(ctx, feedbackRandomFor(2, 0, 0)) }()
	select {
	case <-firstStarted:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	otherDone := make(chan error, 1)
	go func() { otherDone <- engine.performFeedback(ctx, feedbackRandomFor(2, 1, 1)) }()
	select {
	case err := <-otherDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("feedback to another sensor was blocked")
	}
	finishFirst()
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
}

func TestFeedbackStartsFromExistingSimulatorState(t *testing.T) {
	var mu sync.Mutex
	states := map[int]bool{1: true, 2: false}
	var engine *runEngine
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodGet && r.URL.Path == "/test/v1/simulator/state" {
			_ = json.NewEncoder(w).Encode(map[string]any{"feedbackStates": []map[string]any{
				{"source": "simulator", "kind": "occupancy", "address": 1, "active": states[1]},
				{"source": "simulator", "kind": "occupancy", "address": 2, "active": states[2]},
			}})
			return
		}
		var request struct {
			Address int  `json:"address"`
			Active  bool `json:"active"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if states[request.Address] != request.Active {
			engine.expectations.Fulfill(fmt.Sprintf("feedback:block-%d:%t", request.Address, request.Active))
		}
		states[request.Address] = request.Active
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	targets := []FeedbackTarget{
		{Source: "simulator", Kind: "occupancy", Address: 1, BlockID: "block-1"},
		{Source: "simulator", Kind: "occupancy", Address: 2, BlockID: "block-2"},
	}
	// Each phase starts a new benchmark against the same simulator state.
	for range 2 {
		mu.Lock()
		engine = newFeedbackTestEngine(server.URL, targets)
		mu.Unlock()
		if err := engine.loadFeedbackState(context.Background()); err != nil {
			t.Fatal(err)
		}
		for targetIndex := range targets {
			if err := engine.performFeedback(context.Background(), feedbackRandomFor(len(targets), targetIndex, 0)); err != nil {
				t.Fatal(err)
			}
		}
		if pending := engine.expectations.Pending(); pending != 0 {
			t.Fatalf("new phase repeated existing sensor states: %d missing events", pending)
		}
	}
}
