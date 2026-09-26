package benchmark

import (
	"context"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/client"
	"github.com/agm650/TrainPilot-server/internal/model"
)

func TestConcurrentRouteJobsAdvanceOneRouteInOrder(t *testing.T) {
	var state atomic.Int32 // idle, reserved, active
	firstReserveStarted := make(chan struct{})
	finishFirstReserve := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/routes/route-1/reserve":
			if !state.CompareAndSwap(0, 1) {
				w.WriteHeader(http.StatusConflict)
				return
			}
			close(firstReserveStarted)
			<-finishFirstReserve
			w.WriteHeader(http.StatusNoContent)
		case "/api/v1/routes/route-1/activate":
			if !state.CompareAndSwap(1, 2) {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	var finishOnce sync.Once
	finishFirst := func() { finishOnce.Do(func() { close(finishFirstReserve) }) }
	defer finishFirst()
	engine := &runEngine{
		routes:      []model.Route{{ID: "route-1"}},
		routeStates: make(map[string]routeBinding),
		sessions: []*benchSession{
			{client: client.New(server.URL)},
			{client: client.New(server.URL)},
		},
	}
	seedForSession := func(want int) int64 {
		for seed := int64(0); ; seed++ {
			random := rand.New(rand.NewSource(seed))
			random.Intn(1) // route selection
			if random.Intn(len(engine.sessions)) == want {
				return seed
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	firstDone := make(chan error, 1)
	go func() { firstDone <- engine.performRoute(ctx, rand.New(rand.NewSource(seedForSession(0)))) }()
	select {
	case <-firstReserveStarted:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	secondDone := make(chan error, 1)
	go func() { secondDone <- engine.performRoute(ctx, rand.New(rand.NewSource(seedForSession(1)))) }()
	select {
	case err := <-secondDone:
		finishFirst()
		t.Fatalf("second route job finished before first: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	finishFirst()
	if err := <-firstDone; err != nil {
		t.Fatalf("reserve route: %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("activate route: %v", err)
	}
	if state.Load() != 2 || engine.routeStates["route-1"].state != "active" {
		t.Fatalf("server state=%d, benchmark state=%+v", state.Load(), engine.routeStates["route-1"])
	}
}
