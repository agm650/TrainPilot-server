package service

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station/simulator"
	"github.com/agm650/TrainPilot-server/internal/store"
)

func newRouteFixture(t *testing.T) (*RouteService, *RailwayService, *store.Store, *simulator.Simulator, *events.Bus) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
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
	railway.StartFeedback(ctx)
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

func TestRouteActivateRejectsLateOccupancy(t *testing.T) {
	ctx := context.Background()
	routes, _, db, sim, bus := newRouteFixture(t)
	setRouteTurnoutRequirement(t, db, "diverging")
	dispatcher := model.User{Role: model.RoleDispatcher}
	sess := model.Session{ID: "session-1"}
	if err := routes.Reserve(ctx, dispatcher, sess, "route-a-b"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetBlockOccupied(ctx, "block-a", true); err != nil {
		t.Fatal(err)
	}
	events, unsubscribe := bus.Subscribe(8)
	defer unsubscribe()
	beforeAccessories := sim.Snapshot().Accessories

	err := routes.Activate(ctx, dispatcher, sess, "route-a-b")
	if !errors.Is(err, ErrRouteOccupied) {
		t.Fatalf("activation error=%v", err)
	}
	assertRouteState(t, db, "reserved", sess.ID)
	assertNoAccessoryCommand(t, beforeAccessories, sim.Snapshot().Accessories)
	assertNoRouteActivated(t, events)
}

func TestRouteActivateRejectsLateConflict(t *testing.T) {
	for _, state := range []string{"reserved", "active"} {
		t.Run(state, func(t *testing.T) {
			ctx := context.Background()
			routes, _, db, sim, bus := newRouteFixture(t)
			setRouteTurnoutRequirement(t, db, "diverging")
			dispatcher := model.User{Role: model.RoleDispatcher}
			sess := model.Session{ID: "session-1"}
			if err := routes.Reserve(ctx, dispatcher, sess, "route-a-b"); err != nil {
				t.Fatal(err)
			}
			if err := addLateRouteConflict(ctx, db, state); err != nil {
				t.Fatal(err)
			}
			events, unsubscribe := bus.Subscribe(8)
			defer unsubscribe()
			beforeAccessories := sim.Snapshot().Accessories

			err := routes.Activate(ctx, dispatcher, sess, "route-a-b")
			if !errors.Is(err, ErrRouteConflict) {
				t.Fatalf("activation error=%v", err)
			}
			assertRouteState(t, db, "reserved", sess.ID)
			assertNoAccessoryCommand(t, beforeAccessories, sim.Snapshot().Accessories)
			assertNoRouteActivated(t, events)
		})
	}
}

func TestRouteActivateRejectsInvalidReservationBeforeTurnoutCommand(t *testing.T) {
	for _, test := range []struct {
		name      string
		prepare   func(context.Context, *RouteService, model.User, model.Session) error
		activate  model.Session
		wantState string
		wantOwner string
	}{
		{
			name: "another session",
			prepare: func(ctx context.Context, routes *RouteService, user model.User, owner model.Session) error {
				return routes.Reserve(ctx, user, owner, "route-a-b")
			},
			activate:  model.Session{ID: "session-2"},
			wantState: "reserved",
			wantOwner: "session-1",
		},
		{
			name: "released route",
			prepare: func(ctx context.Context, routes *RouteService, user model.User, owner model.Session) error {
				if err := routes.Reserve(ctx, user, owner, "route-a-b"); err != nil {
					return err
				}
				return routes.Release(ctx, owner, "route-a-b")
			},
			activate:  model.Session{ID: "session-1"},
			wantState: "idle",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			routes, _, db, sim, bus := newRouteFixture(t)
			setRouteTurnoutRequirement(t, db, "diverging")
			dispatcher := model.User{Role: model.RoleDispatcher}
			owner := model.Session{ID: "session-1"}
			if err := test.prepare(ctx, routes, dispatcher, owner); err != nil {
				t.Fatal(err)
			}
			events, unsubscribe := bus.Subscribe(8)
			defer unsubscribe()
			beforeAccessories := sim.Snapshot().Accessories

			err := routes.Activate(ctx, dispatcher, test.activate, "route-a-b")
			if !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("activation error=%v", err)
			}
			assertRouteState(t, db, test.wantState, test.wantOwner)
			assertNoAccessoryCommand(t, beforeAccessories, sim.Snapshot().Accessories)
			assertNoRouteActivated(t, events)
		})
	}
}

func TestRouteActivateRevalidationRace(t *testing.T) {
	for _, test := range []struct {
		name    string
		mutate  func(context.Context, *store.Store) error
		wantErr error
	}{
		{
			name: "late occupancy",
			mutate: func(ctx context.Context, db *store.Store) error {
				return db.SetBlockOccupied(ctx, "block-a", true)
			},
			wantErr: ErrRouteOccupied,
		},
		{
			name: "late conflict",
			mutate: func(ctx context.Context, db *store.Store) error {
				return addLateRouteConflict(ctx, db, "reserved")
			},
			wantErr: ErrRouteConflict,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			routes, _, db, sim, bus := newRouteFixture(t)
			setRouteTurnoutRequirement(t, db, "diverging")
			dispatcher := model.User{Role: model.RoleDispatcher}
			sess := model.Session{ID: "session-1"}
			reserved := make(chan struct{})
			reserveErr := make(chan error, 1)
			invalidated := make(chan error, 1)
			activationErr := make(chan error, 1)
			events, unsubscribe := bus.Subscribe(8)
			defer unsubscribe()
			beforeAccessories := sim.Snapshot().Accessories

			go func() {
				err := routes.Reserve(ctx, dispatcher, sess, "route-a-b")
				reserveErr <- err
				close(reserved)
				if mutationErr := <-invalidated; err == nil && mutationErr != nil {
					err = mutationErr
				}
				if err == nil {
					err = routes.Activate(ctx, dispatcher, sess, "route-a-b")
				}
				activationErr <- err
			}()
			go func() {
				<-reserved
				invalidated <- test.mutate(ctx, db)
			}()

			if err := <-reserveErr; err != nil {
				t.Fatalf("reserve error=%v", err)
			}
			if err := <-activationErr; !errors.Is(err, test.wantErr) {
				t.Fatalf("activation error=%v want %v", err, test.wantErr)
			}
			assertRouteState(t, db, "reserved", sess.ID)
			assertNoAccessoryCommand(t, beforeAccessories, sim.Snapshot().Accessories)
			assertNoRouteActivated(t, events)
		})
	}
}

func TestRouteActivationAndRelease(t *testing.T) {
	ctx := context.Background()
	routes, _, db, sim, bus := newRouteFixture(t)
	setRouteTurnoutRequirement(t, db, "diverging")
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
	activationDeadline := time.NewTimer(time.Second)
	defer activationDeadline.Stop()
	for {
		select {
		case event := <-ch:
			if event.Type == "route.activated" {
				goto activated
			}
		case <-activationDeadline.C:
			t.Fatal("route.activated event not published")
		}
	}

activated:
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

func addLateRouteConflict(ctx context.Context, db *store.Store, state string) error {
	if _, err := db.DB.ExecContext(ctx, `INSERT INTO routes(id,name,state,reserved_by_session) VALUES('route-conflict','Conflict',?,'other')`, state); err != nil {
		return err
	}
	_, err := db.DB.ExecContext(ctx, `INSERT INTO route_conflicts(route_id,conflict_route_id) VALUES('route-a-b','route-conflict')`)
	return err
}

func setRouteTurnoutRequirement(t *testing.T, db *store.Store, state string) {
	t.Helper()
	result, err := db.DB.ExecContext(context.Background(), `UPDATE route_turnouts SET required_state=? WHERE route_id='route-a-b'`, state)
	if err != nil {
		t.Fatal(err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		t.Fatalf("route turnout requirement rows=%d err=%v", affected, err)
	}
}

func assertRouteState(t *testing.T, db *store.Store, state, owner string) {
	t.Helper()
	route, err := db.GetRoute(context.Background(), "route-a-b")
	if err != nil || route.State != state || route.ReservedBySession != owner {
		t.Fatalf("route=%+v err=%v want state=%q owner=%q", route, err, state, owner)
	}
}

func assertNoAccessoryCommand(t *testing.T, before, after map[int]simulator.AccessoryState) {
	t.Helper()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("accessories changed: before=%+v after=%+v", before, after)
	}
}

func assertNoRouteActivated(t *testing.T, ch <-chan events.Event) {
	t.Helper()
	for {
		select {
		case event := <-ch:
			if event.Type == "route.activated" {
				t.Fatalf("unexpected route.activated event: %+v", event)
			}
		default:
			return
		}
	}
}
