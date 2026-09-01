package service

import (
	"context"
	"errors"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station/simulator"
	"github.com/agm650/TrainPilot-server/internal/store"
)

func newRouteFixture(t *testing.T) (*RouteService, *RailwayService, *store.Store, *simulator.Simulator, *events.Bus) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.SeedDemo(ctx); err != nil {
		t.Fatal(err)
	}
	sim := simulator.New()
	if err := sim.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	bus := events.New()
	railway := NewRailwayService(db, sim, bus)
	return NewRouteService(db, railway, bus), railway, db, sim, bus
}

func TestRouteReserveValidationAndConflict(t *testing.T) {
	ctx := context.Background()
	routes, _, db, _, bus := newRouteFixture(t)
	viewer := model.User{Role: model.RoleViewer}
	dispatcher := model.User{Role: model.RoleDispatcher}
	sess := model.Session{ID: "session-1"}

	if list, err := routes.List(ctx); err != nil || len(list) != 1 {
		t.Fatalf("routes len=%d err=%v", len(list), err)
	}
	if err := routes.Reserve(ctx, viewer, sess, "route-a-b"); err == nil {
		t.Fatal("viewer reserved a route")
	}
	if err := db.SetBlockOccupied(ctx, "block-a", true); err != nil {
		t.Fatal(err)
	}
	if err := routes.Reserve(ctx, dispatcher, sess, "route-a-b"); err == nil {
		t.Fatal("route containing an occupied block was reserved")
	}
	if err := db.SetBlockOccupied(ctx, "block-a", false); err != nil {
		t.Fatal(err)
	}

	ch, unsubscribe := bus.Subscribe(2)
	defer unsubscribe()
	if err := routes.Reserve(ctx, dispatcher, sess, "route-a-b"); err != nil {
		t.Fatal(err)
	}
	if event := <-ch; event.Type != "route.reserved" {
		t.Fatalf("event=%+v", event)
	}
	if err := routes.Reserve(ctx, dispatcher, model.Session{ID: "session-2"}, "route-a-b"); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second reservation error=%v", err)
	}
	if err := routes.Reserve(ctx, dispatcher, sess, "missing"); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("missing route error=%v", err)
	}
}

func TestRouteActiveConflict(t *testing.T) {
	ctx := context.Background()
	routes, _, db, _, _ := newRouteFixture(t)
	dispatcher := model.User{Role: model.RoleDispatcher}
	if _, err := db.DB.ExecContext(ctx, `INSERT INTO routes(id,name,state,reserved_by_session) VALUES('route-conflict','Conflict','reserved','other')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.ExecContext(ctx, `INSERT INTO route_conflicts(route_id,conflict_route_id) VALUES('route-a-b','route-conflict')`); err != nil {
		t.Fatal(err)
	}
	if err := routes.Reserve(ctx, dispatcher, model.Session{ID: "session-1"}, "route-a-b"); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("conflict error=%v", err)
	}
}

func TestRouteActivationAndRelease(t *testing.T) {
	ctx := context.Background()
	routes, _, db, sim, bus := newRouteFixture(t)
	viewer := model.User{Role: model.RoleViewer}
	dispatcher := model.User{Role: model.RoleDispatcher}
	sess := model.Session{ID: "session-1"}
	other := model.Session{ID: "session-2"}

	if err := routes.Activate(ctx, viewer, sess, "route-a-b"); err == nil {
		t.Fatal("viewer activated route")
	}
	if err := routes.Activate(ctx, dispatcher, sess, "route-a-b"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unreserved route error=%v", err)
	}
	if err := routes.Reserve(ctx, dispatcher, sess, "route-a-b"); err != nil {
		t.Fatal(err)
	}
	if err := sim.Close(); err != nil {
		t.Fatal(err)
	}
	if err := routes.Activate(ctx, dispatcher, sess, "route-a-b"); err == nil {
		t.Fatal("activation succeeded while station disconnected")
	}
	stored, err := db.GetRoute(ctx, "route-a-b")
	if err != nil || stored.State != "reserved" {
		t.Fatalf("route after failed activation=%+v err=%v", stored, err)
	}
	if err := sim.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	ch, unsubscribe := bus.Subscribe(4)
	defer unsubscribe()
	if err := routes.Activate(ctx, dispatcher, sess, "route-a-b"); err != nil {
		t.Fatal(err)
	}
	seenActivated := false
	for i := 0; i < 3; i++ {
		if event := <-ch; event.Type == "route.activated" {
			seenActivated = true
		}
	}
	if !seenActivated {
		t.Fatal("route.activated event not published")
	}
	stored, err = db.GetRoute(ctx, "route-a-b")
	if err != nil || stored.State != "active" {
		t.Fatalf("active route=%+v err=%v", stored, err)
	}
	if err := routes.Release(ctx, other, "route-a-b"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("other session release error=%v", err)
	}
	if err := routes.Release(ctx, sess, "route-a-b"); err != nil {
		t.Fatal(err)
	}
	if event := <-ch; event.Type != "route.released" {
		t.Fatalf("event=%+v", event)
	}
	stored, err = db.GetRoute(ctx, "route-a-b")
	if err != nil || stored.State != "idle" || stored.ReservedBySession != "" {
		t.Fatalf("released route=%+v err=%v", stored, err)
	}
}
