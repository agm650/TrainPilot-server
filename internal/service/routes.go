package service

import (
	"context"
	"fmt"

	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/store"
)

type RouteService struct {
	store   *store.Store
	railway *RailwayService
	events  *events.Bus
}

func NewRouteService(s *store.Store, r *RailwayService, b *events.Bus) *RouteService {
	return &RouteService{store: s, railway: r, events: b}
}
func (r *RouteService) List(ctx context.Context) ([]model.Route, error) {
	return r.store.ListRoutes(ctx)
}
func (r *RouteService) Reserve(ctx context.Context, user model.User, sess model.Session, id string) error {
	if !Allowed(user.Role, PermissionDispatch) {
		return ErrPermissionDenied
	}
	occ, err := r.store.RouteBlocksOccupied(ctx, id)
	if err != nil {
		return err
	}
	if occ {
		return fmt.Errorf("route contains an occupied block: %w", store.ErrConflict)
	}
	conflict, err := r.store.RouteHasActiveConflict(ctx, id)
	if err != nil {
		return err
	}
	if conflict {
		return store.ErrConflict
	}
	if err := r.store.ReserveRoute(ctx, id, sess.ID); err != nil {
		return err
	}
	r.events.Publish("route.reserved", map[string]any{"routeId": id, "sessionId": sess.ID})
	return nil
}
func (r *RouteService) Activate(ctx context.Context, user model.User, sess model.Session, id string) error {
	if !Allowed(user.Role, PermissionDispatch) {
		return ErrPermissionDenied
	}
	requirements, err := r.store.RouteTurnoutRequirements(ctx, id)
	if err != nil {
		return err
	}
	for turnout, state := range requirements {
		if err := r.railway.SetTurnout(ctx, user, turnout, state); err != nil {
			return err
		}
	}
	if err := r.store.ActivateRoute(ctx, id, sess.ID); err != nil {
		return err
	}
	r.events.Publish("route.activated", map[string]any{"routeId": id})
	return nil
}
func (r *RouteService) Release(ctx context.Context, sess model.Session, id string) error {
	if err := r.store.ReleaseRoute(ctx, id, sess.ID); err != nil {
		return err
	}
	r.events.Publish("route.released", map[string]any{"routeId": id})
	return nil
}
