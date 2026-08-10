package api

import "net/http"

func (s *Server) listBlocks(w http.ResponseWriter, r *http.Request) {
	items, err := s.railway.Blocks(r.Context())
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (s *Server) listTurnouts(w http.ResponseWriter, r *http.Request) {
	items, err := s.railway.Turnouts(r.Context())
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (s *Server) setTurnout(w http.ResponseWriter, r *http.Request) {
	var req struct {
		State string `json:"state"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.railway.SetTurnout(r.Context(), userFrom(r), r.PathValue("id"), req.State); err != nil {
		writeOperationProblem(w, err, "turnout_failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) listRoutes(w http.ResponseWriter, r *http.Request) {
	items, err := s.routes.List(r.Context())
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (s *Server) reserveRoute(w http.ResponseWriter, r *http.Request) {
	if err := s.routes.Reserve(r.Context(), userFrom(r), sessionFrom(r), r.PathValue("id")); err != nil {
		writeProblem(w, statusFor(err), "route_reservation_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) activateRoute(w http.ResponseWriter, r *http.Request) {
	if err := s.routes.Activate(r.Context(), userFrom(r), sessionFrom(r), r.PathValue("id")); err != nil {
		writeProblem(w, statusFor(err), "route_activation_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) releaseRoute(w http.ResponseWriter, r *http.Request) {
	if err := s.routes.Release(r.Context(), sessionFrom(r), r.PathValue("id")); err != nil {
		writeProblem(w, statusFor(err), "route_release_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) testBlockOccupancy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Occupied bool `json:"occupied"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.railway.SetBlockFeedback(r.Context(), r.PathValue("id"), req.Occupied); err != nil {
		writeProblem(w, statusFor(err), "feedback_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
