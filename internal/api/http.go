package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/store"
)

type contextKey string

const (
	userKey    contextKey = "user"
	sessionKey contextKey = "session"
)

type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
	Code   string `json:"code,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeProblem(w http.ResponseWriter, status int, code, detail string) {
	writeJSON(w, status, problem{Type: "about:blank", Title: http.StatusText(status), Status: status, Detail: detail, Code: code})
}
func writeOperationProblem(w http.ResponseWriter, err error, code string) {
	if errors.Is(err, station.ErrOffline) {
		code = "station_offline"
	}
	writeProblem(w, statusFor(err), code, err.Error())
}
func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_json", err.Error())
		return false
	}
	return true
}
func userFrom(r *http.Request) model.User       { return r.Context().Value(userKey).(model.User) }
func sessionFrom(r *http.Request) model.Session { return r.Context().Value(sessionKey).(model.Session) }
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			writeProblem(w, http.StatusUnauthorized, "missing_token", "Bearer token required")
			return
		}
		user, sess, err := s.auth.Authenticate(r.Context(), parts[1])
		if err != nil {
			writeProblem(w, http.StatusUnauthorized, "invalid_token", err.Error())
			return
		}
		ctx := context.WithValue(r.Context(), userKey, user)
		ctx = context.WithValue(ctx, sessionKey, sess)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
func statusFor(err error) int {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, store.ErrConflict):
		return http.StatusConflict
	case errors.Is(err, station.ErrOffline):
		return http.StatusServiceUnavailable
	case strings.Contains(err.Error(), "permission denied"):
		return http.StatusForbidden
	case strings.Contains(err.Error(), "required") ||
		strings.Contains(err.Error(), "must be") ||
		strings.Contains(err.Error(), "range") ||
		strings.Contains(err.Error(), "invalid") ||
		strings.Contains(err.Error(), "unsupported archive") ||
		strings.Contains(err.Error(), "duplicate") ||
		strings.Contains(err.Error(), "unknown block") ||
		strings.Contains(err.Error(), "unknown turnout") ||
		strings.Contains(err.Error(), "unknown conflict") ||
		strings.Contains(err.Error(), "unsafe archive") ||
		strings.Contains(err.Error(), "archive is missing"):
		return http.StatusBadRequest
	default:
		return http.StatusConflict
	}
}
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
