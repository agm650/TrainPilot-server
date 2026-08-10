package api

import (
	"net/http"
)

func (s *Server) setTrackPower(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Enabled == nil {
		writeProblem(w, http.StatusBadRequest, "enabled_required", "enabled is required")
		return
	}
	if err := s.control.SetTrackPower(r.Context(), userFrom(r), *req.Enabled); err != nil {
		writeOperationProblem(w, err, "track_power_failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) trackPowerStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.control.StationStatus(r.Context())
	if err != nil {
		writeProblem(w, statusFor(err), "station_status_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) emergencyStop(w http.ResponseWriter, r *http.Request) {
	if err := s.control.EmergencyStop(r.Context(), userFrom(r)); err != nil {
		writeOperationProblem(w, err, "emergency_stop_failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
