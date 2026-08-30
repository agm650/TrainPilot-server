package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/service"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/store"
	"github.com/agm650/TrainPilot-server/internal/transfer"
)

type contextKey string

const (
	userKey    contextKey = "user"
	sessionKey contextKey = "session"
)

type problem struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Code     string `json:"code"`
	Category string `json:"category"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeProblem(w http.ResponseWriter, status int, code, detail string) {
	category := categoryForStatus(status)
	if status >= 500 {
		code = "internal_error"
		detail = "internal server error"
		category = "internal"
	}
	writeProblemJSON(w, status, problem{Type: "about:blank", Title: http.StatusText(status), Status: status, Detail: detail, Code: code, Category: category})
}
func writeOperationProblem(w http.ResponseWriter, err error, code string) {
	status := statusFor(err)
	category := categoryForStatus(status)
	switch {
	case errors.Is(err, station.ErrOffline):
		code = "station_offline"
		category = "station_unavailable"
	case errors.Is(err, service.ErrEmergencyStopActive):
		code = "emergency_stop_active"
		category = "safety"
	case errors.Is(err, service.ErrTrackPowerOff):
		code = "track_power_off"
		category = "safety"
	case errors.Is(err, service.ErrTrackPowerUnknown):
		code = "track_power_unknown"
		category = "safety"
	case errors.Is(err, service.ErrSafetyPreempted):
		code = "safety_command_preempted"
		category = "safety"
	case errors.Is(err, service.ErrLeaseNotOwned):
		code = "lease_not_owned"
	case errors.Is(err, service.ErrLeaseNotActive):
		code = "lease_not_active"
	case errors.Is(err, service.ErrLeaseOwnedByOtherUser):
		code = "lease_owned_by_other_user"
	case errors.Is(err, service.ErrLeaseTakeoverConflict):
		code = "lease_takeover_conflict"
	case errors.Is(err, service.ErrPermissionDenied):
		code = "permission_denied"
	case errors.Is(err, service.ErrValidation):
		code = "validation_failed"
	case errors.Is(err, transfer.ErrInvalidArchive):
		code = "invalid_archive"
	}
	detail := err.Error()
	if status >= 500 && status != http.StatusServiceUnavailable {
		code = "internal_error"
		category = "internal"
		detail = "internal server error"
	}
	writeProblemJSON(w, status, problem{Type: "about:blank", Title: http.StatusText(status), Status: status, Detail: detail, Code: code, Category: category})
}

func writeProblemJSON(w http.ResponseWriter, status int, value problem) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func categoryForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "validation"
	case http.StatusUnauthorized:
		return "authentication"
	case http.StatusForbidden:
		return "authorization"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict:
		return "conflict"
	case http.StatusServiceUnavailable:
		return "station_unavailable"
	default:
		return "internal"
	}
}
func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_json", err.Error())
		return false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			err = errors.New("request body must contain exactly one JSON value")
		}
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
			code := "invalid_token"
			detail := "authentication failed"
			if errors.Is(err, service.ErrAccessTokenExpired) {
				code = "expired_token"
				detail = "access token expired"
			}
			writeProblem(w, http.StatusUnauthorized, code, detail)
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
	case errors.Is(err, service.ErrPermissionDenied):
		return http.StatusForbidden
	case errors.Is(err, service.ErrValidation), errors.Is(err, transfer.ErrInvalidArchive):
		return http.StatusBadRequest
	case errors.Is(err, station.ErrOffline):
		return http.StatusServiceUnavailable
	case errors.Is(err, station.ErrUnsupported), errors.Is(err, service.ErrLeaseNotOwned):
		return http.StatusConflict
	case errors.Is(err, service.ErrLeaseNotActive),
		errors.Is(err, service.ErrLeaseOwnedByOtherUser),
		errors.Is(err, service.ErrLeaseTakeoverConflict):
		return http.StatusConflict
	case errors.Is(err, service.ErrEmergencyStopActive),
		errors.Is(err, service.ErrTrackPowerOff),
		errors.Is(err, service.ErrTrackPowerUnknown),
		errors.Is(err, service.ErrSafetyPreempted):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
