package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/service"
	"github.com/agm650/TrainPilot-server/internal/store"
)

const (
	maxOccupancySnapshotObservations = 256
	maxOccupancyFutureSkew           = 5 * time.Minute
)

type occupancyObservationRequest struct {
	ProviderID string               `json:"providerId"`
	SensorID   string               `json:"sensorId"`
	State      model.OccupancyState `json:"state"`
	Sequence   uint64               `json:"sequence"`
	ObservedAt time.Time            `json:"observedAt"`
	Occupant   *model.OccupantRef   `json:"occupant,omitempty"`
}

type occupancySnapshotItem struct {
	SensorID string               `json:"sensorId"`
	State    model.OccupancyState `json:"state"`
	Occupant *model.OccupantRef   `json:"occupant,omitempty"`
}

type occupancySnapshotRequest struct {
	ProviderID   string                  `json:"providerId"`
	Sequence     uint64                  `json:"sequence"`
	ObservedAt   time.Time               `json:"observedAt"`
	Observations []occupancySnapshotItem `json:"observations"`
}

func (s *Server) postOccupancyObservation(w http.ResponseWriter, r *http.Request) {
	if !service.Allowed(userFrom(r).Role, service.PermissionOccupancyWrite) {
		writeProblem(w, http.StatusForbidden, "permission_denied", "occupancy write permission required")
		return
	}
	var request occupancyObservationRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if !s.validateExternalOccupancyRequest(r.Context(), w, request.ProviderID, request.ObservedAt) {
		return
	}
	observation := model.OccupancyObservation{
		ProviderID: request.ProviderID,
		SensorID:   request.SensorID,
		State:      request.State,
		Sequence:   request.Sequence,
		ObservedAt: request.ObservedAt,
		Occupant:   request.Occupant,
	}
	err := s.railway.OccupancyService().Observe(r.Context(), observation)
	s.metrics.ObserveExternalOccupancyLatency(time.Since(request.ObservedAt), err == nil)
	if err != nil {
		writeOccupancyProblem(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) postOccupancySnapshot(w http.ResponseWriter, r *http.Request) {
	if !service.Allowed(userFrom(r).Role, service.PermissionOccupancyWrite) {
		writeProblem(w, http.StatusForbidden, "permission_denied", "occupancy write permission required")
		return
	}
	var request occupancySnapshotRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if len(request.Observations) == 0 || len(request.Observations) > maxOccupancySnapshotObservations {
		writeProblem(w, http.StatusBadRequest, "invalid_occupancy_observation", "observations must contain between 1 and 256 items")
		return
	}
	if !s.validateExternalOccupancyRequest(r.Context(), w, request.ProviderID, request.ObservedAt) {
		return
	}
	observations := make([]model.OccupancyObservation, 0, len(request.Observations))
	for _, item := range request.Observations {
		observations = append(observations, model.OccupancyObservation{
			ProviderID: request.ProviderID,
			SensorID:   item.SensorID,
			State:      item.State,
			Sequence:   request.Sequence,
			ObservedAt: request.ObservedAt,
			Occupant:   item.Occupant,
		})
	}
	err := s.railway.OccupancyService().ObserveBatch(r.Context(), observations)
	s.metrics.ObserveExternalOccupancyLatency(time.Since(request.ObservedAt), err == nil)
	if err != nil {
		writeOccupancyProblem(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) validateExternalOccupancyRequest(ctx context.Context, w http.ResponseWriter, providerID string, observedAt time.Time) bool {
	provider, err := s.store.OccupancyProvider(ctx, providerID)
	if err != nil || !provider.FreshnessRequired {
		writeProblem(w, http.StatusNotFound, "occupancy_sensor_not_found", "external occupancy provider or sensor not found")
		return false
	}
	if observedAt.IsZero() || observedAt.After(time.Now().UTC().Add(maxOccupancyFutureSkew)) {
		writeProblem(w, http.StatusBadRequest, "invalid_occupancy_observation", "observedAt is required and cannot be excessively in the future")
		return false
	}
	return true
}

func writeOccupancyProblem(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "occupancy_sensor_not_found", "external occupancy provider or sensor not found")
	case errors.Is(err, service.ErrOccupancySequenceStale):
		writeProblem(w, http.StatusConflict, "occupancy_sequence_stale", "occupancy sequence is older than the accepted sequence")
	case errors.Is(err, service.ErrOccupancySequenceConflict):
		writeProblem(w, http.StatusConflict, "occupancy_sequence_conflict", "occupancy sequence conflicts with the accepted observation")
	case errors.Is(err, model.ErrInvalidOccupancy):
		writeProblem(w, http.StatusBadRequest, "invalid_occupancy_observation", err.Error())
	default:
		writeProblem(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

func (s *Server) getBlockOccupancySources(w http.ResponseWriter, r *http.Request) {
	if !service.Allowed(userFrom(r).Role, service.PermissionDispatch) {
		writeProblem(w, http.StatusForbidden, "permission_denied", "dispatch permission required")
		return
	}
	blockID := r.PathValue("id")
	if _, err := s.store.ResourcesForBlock(r.Context(), blockID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeProblem(w, http.StatusNotFound, "block_not_found", "block not found")
			return
		}
		writeProblem(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	states, err := s.railway.OccupancyService().ProviderStates(r.Context(), blockID)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if states == nil {
		states = []model.SensorOccupancyState{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": states})
}
