package api

import (
	"context"
	"net/http"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/service"
	"github.com/agm650/TrainPilot-server/internal/store"
)

func (s *Server) readSignals(ctx context.Context) ([]model.Signal, error) {
	if runtime := s.signals.Load(); runtime != nil {
		return runtime.Signals(), nil
	}
	// Reads in embedded servers never create a controller or publish events.
	profiles, definitions, err := s.store.SignalConfiguration(ctx)
	if err != nil {
		return nil, err
	}
	byProfile := map[string]model.SignalProfile{}
	for _, p := range profiles {
		byProfile[p.ID] = p
	}
	result := make([]model.Signal, 0, len(definitions))
	for _, d := range definitions {
		state, err := model.NewSignalState(d, byProfile[d.ProfileID])
		if err != nil {
			return nil, err
		}
		state.InhibitedReason = "service_unavailable"
		result = append(result, model.Signal{Definition: d, State: state})
	}
	return result, nil
}

func (s *Server) signalReadAllowed(w http.ResponseWriter, r *http.Request) bool {
	if !service.Allowed(userFrom(r).Role, service.PermissionView) {
		writeOperationProblem(w, service.ErrPermissionDenied, "permission_denied")
		return false
	}
	return true
}
func (s *Server) listSignals(w http.ResponseWriter, r *http.Request) {
	if !s.signalReadAllowed(w, r) {
		return
	}
	items, err := s.readSignals(r.Context())
	if err != nil {
		writeOperationProblem(w, err, "signal_list_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (s *Server) getSignal(w http.ResponseWriter, r *http.Request) {
	if !s.signalReadAllowed(w, r) {
		return
	}
	items, err := s.readSignals(r.Context())
	if err != nil {
		writeOperationProblem(w, err, "signal_lookup_failed")
		return
	}
	for _, item := range items {
		if item.Definition.ID == r.PathValue("id") {
			writeJSON(w, http.StatusOK, item)
			return
		}
	}
	writeOperationProblem(w, store.ErrNotFound, "signal_not_found")
}
func (s *Server) listSignalProfiles(w http.ResponseWriter, r *http.Request) {
	if !s.signalReadAllowed(w, r) {
		return
	}
	items, err := s.store.ListSignalProfiles(r.Context())
	if err != nil {
		writeOperationProblem(w, err, "signal_profile_list_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (s *Server) setSignalAspect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Aspect model.SignalAspectID `json:"aspect"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	runtime := s.signals.Load()
	if runtime == nil {
		if !service.Allowed(userFrom(r).Role, service.PermissionDispatch) {
			writeOperationProblem(w, service.ErrPermissionDenied, "permission_denied")
			return
		}
		writeOperationProblem(w, service.ErrSignalUnavailable, "signal_unavailable")
		return
	}
	if err := runtime.CommandAspect(r.Context(), userFrom(r), r.PathValue("id"), req.Aspect); err != nil {
		writeOperationProblem(w, err, "signal_command_failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) setSignalMode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode model.SignalMode `json:"mode"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	runtime := s.signals.Load()
	if runtime == nil {
		if !service.Allowed(userFrom(r).Role, service.PermissionDispatch) {
			writeOperationProblem(w, service.ErrPermissionDenied, "permission_denied")
			return
		}
		writeOperationProblem(w, service.ErrSignalUnavailable, "signal_unavailable")
		return
	}
	if err := runtime.SetMode(r.Context(), userFrom(r), r.PathValue("id"), req.Mode); err != nil {
		writeOperationProblem(w, err, "signal_mode_failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
