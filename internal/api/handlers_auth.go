package api

import (
	"errors"
	"net/http"

	"github.com/agm650/TrainPilot-server/internal/service"
)

const (
	serverVersion                = "0.2.0"
	apiVersion                   = "1.7.0"
	minimumClientAPIVersion      = "1.0.0"
	eventAPIVersion              = "1.9.0"
	minimumClientEventAPIVersion = "1.3.0"
)

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
func (s *Server) systemInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"serverVersion":                serverVersion,
		"apiVersion":                   apiVersion,
		"minimumClientApiVersion":      minimumClientAPIVersion,
		"eventApiVersion":              eventAPIVersion,
		"minimumClientEventApiVersion": minimumClientEventAPIVersion,
		"station":                      s.station.Capabilities(),
	})
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
		code := "invalid_refresh_token"
		detail := "refresh token is invalid"
		if errors.Is(err, service.ErrRefreshTokenExpired) {
			code = "expired_refresh_token"
			detail = "refresh token expired"
		}
		writeProblem(w, http.StatusUnauthorized, code, detail)
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
