package service

import (
	"context"
	"fmt"

	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/observability"
	"github.com/agm650/TrainPilot-server/internal/store"
)

type RouteService struct {
	store   *store.Store
	railway *RailwayService
	events  *events.Bus
	metrics *observability.Metrics
}

var ErrRouteOccupied = store.ErrRouteOccupied
var ErrRouteOccupancyUnknown = store.ErrRouteOccupancyUnknown
var ErrRouteConflict = store.ErrRouteConflict

func (r *RouteService) SetMetrics(metrics *observability.Metrics) { r.metrics = metrics }

func NewRouteService(s *store.Store, r *RailwayService, b *events.Bus) *RouteService {
	return &RouteService{store: s, railway: r, events: b}
}
func (r *RouteService) List(ctx context.Context) ([]model.Route, error) {
	return r.store.ListRoutes(ctx)
}
func (r *RouteService) Reserve(ctx context.Context, user model.User, sess model.Session, id string) (err error) {
	result := ""
	defer func() {
		if result == "" {
			result = routeMetricResult(err)
		}
		r.metrics.ObserveRoute("reserve", result)
	}()
	if !Allowed(user.Role, PermissionDispatch) {
		return ErrPermissionDenied
	}
	if err := r.validateRouteOccupancy(ctx, id); err != nil {
		result = routeMetricResult(err)
		return err
	}
	conflict, err := r.store.RouteHasActiveConflict(ctx, id)
	if err != nil {
		return err
	}
	if conflict {
		result = "conflict"
		return store.ErrConflict
	}
	if err := r.store.ReserveRoute(ctx, id, sess.ID); err != nil {
		return err
	}
	r.events.Publish("route.reserved", map[string]any{"routeId": id, "sessionId": sess.ID})
	return nil
}
func (r *RouteService) Activate(ctx context.Context, user model.User, sess model.Session, id string) (err error) {
	defer func() { r.metrics.ObserveRoute("activate", routeMetricResult(err)) }()
	if !Allowed(user.Role, PermissionDispatch) {
		return ErrPermissionDenied
	}
	if err := r.store.ValidateRouteActivation(ctx, id, sess.ID); err != nil {
		return err
	}
	if err := r.validateRouteOccupancy(ctx, id); err != nil {
		return err
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

func (r *RouteService) validateRouteOccupancy(ctx context.Context, routeID string) error {
	blockIDs, err := r.store.RouteBlockIDs(ctx, routeID)
	if err != nil {
		return err
	}
	unknown := false
	for _, blockID := range blockIDs {
		switch r.railway.OccupancyService().BlockState(blockID).State {
		case model.OccupancyOccupied:
			return fmt.Errorf("route block %q is occupied: %w", blockID, ErrRouteOccupied)
		case model.OccupancyUnknown:
			unknown = true
		}
	}
	if unknown {
		return ErrRouteOccupancyUnknown
	}
	return nil
}
func (r *RouteService) Release(ctx context.Context, sess model.Session, id string) (err error) {
	defer func() { r.metrics.ObserveRoute("release", routeMetricResult(err)) }()
	if err := r.store.ReleaseRoute(ctx, id, sess.ID); err != nil {
		return err
	}
	r.events.Publish("route.released", map[string]any{"routeId": id})
	return nil
}
