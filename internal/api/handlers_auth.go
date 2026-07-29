package api

import (
	"net/http"
)

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
func (s *Server) systemInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"serverVersion": "0.2.0", "apiVersion": "1.1.0", "minimumClientApiVersion": "1.0.0", "station": s.station.Capabilities()})
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username   string `json:"username"`
		Password   string `json:"password"`
		ClientID   string `json:"clientId"`
		ClientName string `json:"clientName"`
		Platform   string `json:"platform"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ClientID == "" {
		writeProblem(w, http.StatusBadRequest, "client_id_required", "clientId is required")
		return
	}
	pair, err := s.auth.Login(r.Context(), req.Username, req.Password, req.ClientID, req.ClientName, req.Platform)
	if err != nil {
		writeProblem(w, http.StatusUnauthorized, "invalid_credentials", "authentication failed")
		return
	}
	writeJSON(w, http.StatusOK, pair)
}
func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refreshToken"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	pair, err := s.auth.Refresh(r.Context(), req.RefreshToken)
	if err != nil {
		writeProblem(w, http.StatusUnauthorized, "invalid_refresh_token", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, pair)
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.auth.Logout(r.Context(), sessionFrom(r).ID); err != nil {
		writeProblem(w, statusFor(err), "logout_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) me(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, userFrom(r)) }
