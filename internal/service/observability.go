package service

import (
	"context"
	"errors"

	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/store"
)

func metricResult(err error) string {
	switch {
	case err == nil:
		return "success"
	case errors.Is(err, ErrPermissionDenied):
		return "denied"
	case errors.Is(err, store.ErrConflict):
		return "conflict"
	case errors.Is(err, store.ErrNotFound):
		return "not_found"
	case errors.Is(err, station.ErrOffline):
		return "offline"
	default:
		return "error"
	}
}

func turnoutMetricResult(err error) string {
	switch {
	case err == nil:
		return "success"
	case errors.Is(err, ErrPermissionDenied):
		return "denied"
	case errors.Is(err, ErrInvalidTurnoutPosition), errors.Is(err, ErrUnsafeTurnoutTransition):
		return "invalid"
	case errors.Is(err, station.ErrOffline):
		return "offline"
	case errors.Is(err, ErrTurnoutConfirmationTimeout):
		return "timeout"
	case errors.Is(err, ErrTurnoutTransitionFailed):
		return "driver_error"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "interrupted"
	default:
		return "error"
	}
}

func routeMetricResult(err error) string {
	switch {
	case err == nil:
		return "success"
	case errors.Is(err, ErrPermissionDenied):
		return "denied"
	case errors.Is(err, ErrRouteOccupied):
		return "occupied"
	case errors.Is(err, ErrRouteConflict):
		return "conflict"
	case errors.Is(err, store.ErrConflict):
		return "conflict"
	default:
		return "error"
	}
}
