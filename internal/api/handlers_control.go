package api

import (
	"net/http"
	"strconv"

	"github.com/agm650/TrainPilot-server/internal/station"
)

func (s *Server) listLocomotives(w http.ResponseWriter, r *http.Request) {
	items, err := s.railway.Locomotives(r.Context())
	if err != nil {
		writeOperationProblem(w, err, "locomotive_list_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (s *Server) acquireLease(w http.ResponseWriter, r *http.Request) {
	lease, err := s.control.Acquire(r.Context(), userFrom(r), sessionFrom(r), r.PathValue("id"))
	if err != nil {
		writeOperationProblem(w, err, "lease_acquisition_failed")
		return
	}
	writeJSON(w, http.StatusCreated, lease)
}
func (s *Server) heartbeatLease(w http.ResponseWriter, r *http.Request) {
	lease, err := s.control.Heartbeat(r.Context(), r.PathValue("id"), sessionFrom(r))
	if err != nil {
		writeOperationProblem(w, err, "lease_heartbeat_failed")
		return
	}
	writeJSON(w, http.StatusOK, lease)
}
func (s *Server) takeoverLease(w http.ResponseWriter, r *http.Request) {
	lease, err := s.control.TakeoverLease(r.Context(), userFrom(r), sessionFrom(r), r.PathValue("id"))
	if err != nil {
		writeOperationProblem(w, err, "lease_not_found")
		return
	}
	writeJSON(w, http.StatusOK, lease)
}
func (s *Server) releaseLease(w http.ResponseWriter, r *http.Request) {
	if err := s.control.Release(r.Context(), r.PathValue("id"), sessionFrom(r)); err != nil {
		writeOperationProblem(w, err, "lease_release_failed")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
func (s *Server) throttle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LeaseID   string            `json:"leaseId"`
		Speed     int               `json:"speed"`
		Direction station.Direction `json:"direction"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Direction != station.Forward && req.Direction != station.Reverse {
		writeProblem(w, http.StatusBadRequest, "invalid_direction", "direction must be forward or reverse")
		return
	}
	if err := s.control.Throttle(r.Context(), userFrom(r), sessionFrom(r), r.PathValue("id"), req.LeaseID, req.Speed, req.Direction); err != nil {
		writeOperationProblem(w, err, "throttle_failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) setFunction(w http.ResponseWriter, r *http.Request) {
	fn, err := strconv.Atoi(r.PathValue("function"))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_function", "function must be an integer")
		return
	}
	var req struct {
		LeaseID string `json:"leaseId"`
		Enabled bool   `json:"enabled"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.control.Function(r.Context(), sessionFrom(r), r.PathValue("id"), req.LeaseID, fn, req.Enabled); err != nil {
		writeOperationProblem(w, err, "function_failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
