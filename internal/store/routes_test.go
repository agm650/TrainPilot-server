package store

import (
	"context"
	"errors"
	"testing"
)

func TestValidateRouteActivation(t *testing.T) {
	tests := []struct {
		name      string
		routeID   string
		sessionID string
		setup     func(*testing.T, *Store)
		wantErr   error
	}{
		{name: "missing route", routeID: "missing", sessionID: "owner", wantErr: ErrNotFound},
		{name: "idle route", routeID: "route-a-b", sessionID: "owner", wantErr: ErrNotFound},
		{
			name:      "reserved by another session",
			routeID:   "route-a-b",
			sessionID: "other",
			setup: func(t *testing.T, store *Store) {
				if err := store.ReserveRoute(context.Background(), "route-a-b", "owner"); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: ErrNotFound,
		},
		{
			name:      "reserved by owner",
			routeID:   "route-owner-only",
			sessionID: "owner",
			setup: func(t *testing.T, store *Store) {
				if _, err := store.DB.ExecContext(context.Background(), `INSERT INTO routes(id,name,state,reserved_by_session) VALUES('route-owner-only','Owner only','reserved','owner')`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:      "occupied block",
			routeID:   "route-a-b",
			sessionID: "owner",
			setup: func(t *testing.T, store *Store) {
				reserveRouteForValidation(t, store)
				if err := store.SetBlockOccupied(context.Background(), "block-a", true); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: ErrRouteOccupied,
		},
		{
			name:      "reserved conflict",
			routeID:   "route-a-b",
			sessionID: "owner",
			setup: func(t *testing.T, store *Store) {
				reserveRouteForValidation(t, store)
				addRouteConflictForValidation(t, store, "reserved")
			},
			wantErr: ErrRouteConflict,
		},
		{
			name:      "active conflict",
			routeID:   "route-a-b",
			sessionID: "owner",
			setup: func(t *testing.T, store *Store) {
				reserveRouteForValidation(t, store)
				addRouteConflictForValidation(t, store, "active")
			},
			wantErr: ErrRouteConflict,
		},
		{
			name:      "nominal route",
			routeID:   "route-a-b",
			sessionID: "owner",
			setup:     reserveRouteForValidation,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			store, err := Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			if err := store.SeedDemo(ctx); err != nil {
				t.Fatal(err)
			}
			if test.setup != nil {
				test.setup(t, store)
			}

			before, beforeErr := store.GetRoute(ctx, test.routeID)
			beforeOccupied, occupiedErr := store.RouteBlocksOccupied(ctx, test.routeID)
			if occupiedErr != nil {
				t.Fatal(occupiedErr)
			}
			beforeConflict, conflictErr := store.RouteHasActiveConflict(ctx, test.routeID)
			if conflictErr != nil {
				t.Fatal(conflictErr)
			}

			err = store.ValidateRouteActivation(ctx, test.routeID, test.sessionID)
			if test.wantErr == nil && err != nil {
				t.Fatalf("ValidateRouteActivation() error=%v", err)
			}
			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Fatalf("ValidateRouteActivation() error=%v want %v", err, test.wantErr)
			}

			after, afterErr := store.GetRoute(ctx, test.routeID)
			if !errors.Is(beforeErr, afterErr) || before != after {
				t.Fatalf("route mutated: before=%+v err=%v after=%+v err=%v", before, beforeErr, after, afterErr)
			}
			afterOccupied, err := store.RouteBlocksOccupied(ctx, test.routeID)
			if err != nil || afterOccupied != beforeOccupied {
				t.Fatalf("occupancy mutated: before=%v after=%v err=%v", beforeOccupied, afterOccupied, err)
			}
			afterConflict, err := store.RouteHasActiveConflict(ctx, test.routeID)
			if err != nil || afterConflict != beforeConflict {
				t.Fatalf("conflict state mutated: before=%v after=%v err=%v", beforeConflict, afterConflict, err)
			}
		})
	}
}

func reserveRouteForValidation(t *testing.T, store *Store) {
	t.Helper()
	if err := store.ReserveRoute(context.Background(), "route-a-b", "owner"); err != nil {
		t.Fatal(err)
	}
}

func addRouteConflictForValidation(t *testing.T, store *Store, state string) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO routes(id,name,state,reserved_by_session) VALUES('route-conflict','Conflict',?,'other')`, state); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO route_conflicts(route_id,conflict_route_id) VALUES('route-a-b','route-conflict')`); err != nil {
		t.Fatal(err)
	}
}
