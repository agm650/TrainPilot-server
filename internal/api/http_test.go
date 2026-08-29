package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/auth"
	"github.com/agm650/TrainPilot-server/internal/client"
	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/service"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/station/simulator"
	"github.com/agm650/TrainPilot-server/internal/store"
	"github.com/agm650/TrainPilot-server/internal/transfer"
)

func TestDecodeJSONAndStatusMapping(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"value"}`))
		var target struct {
			Name string `json:"name"`
		}
		if !decodeJSON(recorder, req, &target) || target.Name != "value" {
			t.Fatalf("target=%+v response=%s", target, recorder.Body.String())
		}
	})
	for _, tc := range []struct {
		name string
		body string
	}{
		{"invalid", `{`},
		{"unknown field", `{"unknown":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			var target struct {
				Name string `json:"name"`
			}
			if decodeJSON(recorder, req, &target) {
				t.Fatal("invalid JSON accepted")
			}
			if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"code":"invalid_json"`) {
				t.Fatalf("response=%d %s", recorder.Code, recorder.Body.String())
			}
		})
	}

	cases := []struct {
		err  error
		want int
	}{
		{store.ErrNotFound, http.StatusNotFound},
		{store.ErrConflict, http.StatusConflict},
		{service.ErrPermissionDenied, http.StatusForbidden},
		{fmt.Errorf("%w: value is required", service.ErrValidation), http.StatusBadRequest},
		{station.ErrUnsupported, http.StatusConflict},
		{errors.New("unexpected database failure"), http.StatusInternalServerError},
		{station.ErrOffline, http.StatusServiceUnavailable},
		{service.ErrEmergencyStopActive, http.StatusConflict},
		{service.ErrTrackPowerOff, http.StatusConflict},
		{service.ErrTrackPowerUnknown, http.StatusConflict},
		{service.ErrSafetyPreempted, http.StatusConflict},
	}
	for _, tc := range cases {
		if got := statusFor(tc.err); got != tc.want {
			t.Errorf("statusFor(%q)=%d want %d", tc.err, got, tc.want)
		}
	}
}

func TestOperationProblemsUseStableCodes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		wantCat    string
	}{
		{"station offline", station.ErrOffline, http.StatusServiceUnavailable, "station_offline", "station_unavailable"},
		{"emergency stop", service.ErrEmergencyStopActive, http.StatusConflict, "emergency_stop_active", "safety"},
		{"track power off", service.ErrTrackPowerOff, http.StatusConflict, "track_power_off", "safety"},
		{"track power unknown", service.ErrTrackPowerUnknown, http.StatusConflict, "track_power_unknown", "safety"},
		{"safety preemption", service.ErrSafetyPreempted, http.StatusConflict, "safety_command_preempted", "safety"},
		{"permission", service.ErrPermissionDenied, http.StatusForbidden, "permission_denied", "authorization"},
		{"validation", service.ErrValidation, http.StatusBadRequest, "validation_failed", "validation"},
		{"internal", errors.New("database password secret"), http.StatusInternalServerError, "internal_error", "internal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writeOperationProblem(recorder, tc.err, "operation_failed")
			if got := recorder.Header().Get("Content-Type"); got != "application/problem+json" {
				t.Fatalf("content type=%q", got)
			}
			if recorder.Code != tc.wantStatus {
				t.Fatalf("status=%d want=%d", recorder.Code, tc.wantStatus)
			}
			var got problem
			if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Code != tc.wantCode || got.Category != tc.wantCat {
				t.Fatalf("problem=%+v", got)
			}
			if tc.wantStatus >= 500 && tc.wantStatus != http.StatusServiceUnavailable {
				if got.Detail != "internal server error" || strings.Contains(got.Detail, "secret") {
					t.Fatalf("internal detail leaked: %+v", got)
				}
			} else if got.Detail != tc.err.Error() {
				t.Fatalf("problem detail=%q want=%q", got.Detail, tc.err.Error())
			}
		})
	}
}

func TestSecurityHeaders(t *testing.T) {
	handler := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Header().Get("X-Content-Type-Options") != "nosniff" || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("headers=%v", recorder.Header())
	}
}

func TestHTTPHandlersCoverSuccessAndErrorPaths(t *testing.T) {
	server, dispatcher, viewer := newHTTPFixture(t)
	ctx := context.Background()

	assertStatus(t, server.URL, http.MethodGet, "/healthz", "", nil, http.StatusOK)
	assertStatus(t, server.URL, http.MethodGet, "/api/v1/system/info", "", nil, http.StatusOK)
	assertStatus(t, server.URL, http.MethodGet, "/api/v1/blocks", "", nil, http.StatusUnauthorized)
	assertStatus(t, server.URL, http.MethodGet, "/api/v1/blocks", "NotBearer token", nil, http.StatusUnauthorized)
	assertStatus(t, server.URL, http.MethodPost, "/api/v1/auth/login", "", []byte(`{`), http.StatusBadRequest)
	assertStatus(t, server.URL, http.MethodPost, "/api/v1/auth/login", "", []byte(`{"username":"dispatcher","password":"correct-horse-1"}`), http.StatusBadRequest)
	assertStatus(t, server.URL, http.MethodPost, "/api/v1/auth/login", "", []byte(`{"username":"dispatcher","password":"wrong-password","clientId":"bad"}`), http.StatusUnauthorized)

	for _, path := range []string{"/api/v1/me", "/api/v1/locomotives", "/api/v1/blocks", "/api/v1/turnouts", "/api/v1/routes"} {
		assertStatus(t, server.URL, http.MethodGet, path, "Bearer "+dispatcher.AccessToken, nil, http.StatusOK)
	}
	assertStatus(t, server.URL, http.MethodPut, "/api/v1/turnouts/turnout-1", "Bearer "+viewer.AccessToken, []byte(`{"state":"straight"}`), http.StatusForbidden)
	assertStatus(t, server.URL, http.MethodPut, "/api/v1/turnouts/turnout-1", "Bearer "+dispatcher.AccessToken, []byte(`{"state":"invalid"}`), http.StatusBadRequest)
	assertStatus(t, server.URL, http.MethodPut, "/api/v1/turnouts/missing", "Bearer "+dispatcher.AccessToken, []byte(`{"state":"straight"}`), http.StatusNotFound)
	assertStatus(t, server.URL, http.MethodPut, "/api/v1/turnouts/turnout-1", "Bearer "+dispatcher.AccessToken, []byte(`{"state":"diverging"}`), http.StatusNoContent)

	assertStatus(t, server.URL, http.MethodPost, "/api/v1/routes/route-a-b/reserve", "Bearer "+dispatcher.AccessToken, nil, http.StatusNoContent)
	assertStatus(t, server.URL, http.MethodPost, "/api/v1/routes/route-a-b/activate", "Bearer "+dispatcher.AccessToken, nil, http.StatusNoContent)
	assertStatus(t, server.URL, http.MethodPost, "/api/v1/routes/route-a-b/release", "Bearer "+viewer.AccessToken, nil, http.StatusNotFound)
	assertStatus(t, server.URL, http.MethodPost, "/api/v1/routes/route-a-b/release", "Bearer "+dispatcher.AccessToken, nil, http.StatusNoContent)

	assertStatus(t, server.URL, http.MethodPost, "/test/v1/simulator/blocks/missing/occupancy", "Bearer "+dispatcher.AccessToken, []byte(`{"occupied":true}`), http.StatusNotFound)
	assertStatus(t, server.URL, http.MethodPost, "/test/v1/simulator/blocks/block-a/occupancy", "Bearer "+dispatcher.AccessToken, []byte(`{"occupied":true}`), http.StatusNoContent)
	assertStatus(t, server.URL, http.MethodPost, "/api/v1/routes/route-a-b/reserve", "Bearer "+dispatcher.AccessToken, nil, http.StatusConflict)
	assertStatus(t, server.URL, http.MethodPut, "/api/v1/track-power", "Bearer "+dispatcher.AccessToken, []byte(`{}`), http.StatusBadRequest)
	assertStatus(t, server.URL, http.MethodPut, "/api/v1/track-power", "Bearer "+viewer.AccessToken, []byte(`{"enabled":true}`), http.StatusForbidden)
	assertStatus(t, server.URL, http.MethodPut, "/api/v1/track-power", "Bearer "+dispatcher.AccessToken, []byte(`{"enabled":true}`), http.StatusNoContent)
	assertStatus(t, server.URL, http.MethodGet, "/api/v1/track-power", "Bearer "+viewer.AccessToken, nil, http.StatusOK)
	assertStatus(t, server.URL, http.MethodGet, "/api/v1/station/status", "Bearer "+viewer.AccessToken, nil, http.StatusOK)
	assertStatus(t, server.URL, http.MethodPost, "/api/v1/emergency-stop", "Bearer "+viewer.AccessToken, nil, http.StatusForbidden)
	assertStatus(t, server.URL, http.MethodPost, "/api/v1/emergency-stop", "Bearer "+dispatcher.AccessToken, nil, http.StatusNoContent)

	locos, err := dispatcher.Locomotives(ctx)
	if err != nil || len(locos) == 0 {
		t.Fatalf("locomotives len=%d err=%v", len(locos), err)
	}
	lease, err := dispatcher.Acquire(ctx, locos[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	assertProblemCode(t, server.URL, http.MethodPut, "/api/v1/locomotives/"+locos[0].ID+"/throttle", "Bearer "+dispatcher.AccessToken, []byte(`{"leaseId":"`+lease.ID+`","speed":30,"direction":"forward"}`), http.StatusConflict, "emergency_stop_active")
	assertStatus(t, server.URL, http.MethodPut, "/api/v1/track-power", "Bearer "+dispatcher.AccessToken, []byte(`{"enabled":true}`), http.StatusNoContent)
	assertStatus(t, server.URL, http.MethodPut, "/api/v1/locomotives/"+locos[0].ID+"/throttle", "Bearer "+dispatcher.AccessToken, []byte(`{"leaseId":"`+lease.ID+`","speed":30,"direction":"forward"}`), http.StatusNoContent)
	assertStatus(t, server.URL, http.MethodPut, "/api/v1/track-power", "Bearer "+dispatcher.AccessToken, []byte(`{"enabled":false}`), http.StatusNoContent)
	assertProblemCode(t, server.URL, http.MethodPut, "/api/v1/locomotives/"+locos[0].ID+"/functions/1", "Bearer "+dispatcher.AccessToken, []byte(`{"leaseId":"`+lease.ID+`","enabled":true}`), http.StatusConflict, "track_power_off")
	assertStatus(t, server.URL, http.MethodPut, "/api/v1/locomotives/"+locos[0].ID+"/throttle", "Bearer "+dispatcher.AccessToken, []byte(`{"leaseId":"`+lease.ID+`","speed":0.5,"direction":"forward"}`), http.StatusBadRequest)
	assertStatus(t, server.URL, http.MethodPut, "/api/v1/locomotives/"+locos[0].ID+"/throttle", "Bearer "+dispatcher.AccessToken, []byte(`{"leaseId":"`+lease.ID+`","speed":101,"direction":"forward"}`), http.StatusBadRequest)
	assertStatus(t, server.URL, http.MethodPut, "/api/v1/locomotives/"+locos[0].ID+"/throttle", "Bearer "+dispatcher.AccessToken, []byte(`{"leaseId":"`+lease.ID+`","speed":30,"direction":"sideways"}`), http.StatusBadRequest)
	assertStatus(t, server.URL, http.MethodPut, "/api/v1/locomotives/"+locos[0].ID+"/functions/not-a-number", "Bearer "+dispatcher.AccessToken, []byte(`{"leaseId":"`+lease.ID+`","enabled":true}`), http.StatusBadRequest)
	assertStatus(t, server.URL, http.MethodPut, "/api/v1/control-leases/missing/heartbeat", "Bearer "+dispatcher.AccessToken, nil, http.StatusNotFound)
	assertStatus(t, server.URL, http.MethodDelete, "/api/v1/control-leases/"+lease.ID, "Bearer "+viewer.AccessToken, nil, http.StatusConflict)

	assertStatus(t, server.URL, http.MethodPost, "/api/v1/auth/refresh", "", []byte(`{"refreshToken":"invalid"}`), http.StatusUnauthorized)
	assertStatus(t, server.URL, http.MethodPost, "/api/v1/auth/logout", "Bearer "+dispatcher.AccessToken, nil, http.StatusNoContent)
	assertStatus(t, server.URL, http.MethodGet, "/api/v1/me", "Bearer "+dispatcher.AccessToken, nil, http.StatusUnauthorized)
}

func assertProblemCode(t *testing.T, baseURL, method, path, authorization string, body []byte, wantStatus int, wantCode string) {
	t.Helper()
	req, err := http.NewRequest(method, baseURL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", authorization)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got problem
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != wantStatus || got.Code != wantCode {
		t.Fatalf("%s %s status=%d code=%q want status=%d code=%q", method, path, resp.StatusCode, got.Code, wantStatus, wantCode)
	}
}

func newHTTPFixture(t *testing.T) (*httptest.Server, *client.Client, *client.Client) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.SeedDemo(ctx); err != nil {
		t.Fatal(err)
	}
	clk := clock.Real{}
	users := service.NewUserServiceWithPasswordParams(db, clk, auth.PasswordParams{Iterations: 100_000, SaltLength: 16, KeyLength: 32})
	for _, item := range []struct {
		name string
		role model.Role
	}{{"dispatcher", model.RoleDispatcher}, {"viewer", model.RoleViewer}} {
		if _, err := users.Create(ctx, item.name, item.name, "correct-horse-1", item.role, false, false); err != nil {
			t.Fatal(err)
		}
	}
	authSvc := service.NewAuthService(db, users, clk, 15*time.Minute, time.Hour)
	sim := simulator.New()
	if err := sim.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	bus := events.New()
	railway := service.NewRailwayService(db, sim, bus)
	control := service.NewControlService(db, sim, bus, clk, 15*time.Second, time.Second, time.Hour)
	routes := service.NewRouteService(db, railway, bus)
	transferSvc := transfer.New(db, bus, clk)
	apiServer := New(authSvc, control, railway, routes, transferSvc, db, bus, sim, sim, true)
	server := httptest.NewServer(apiServer.Handler())
	t.Cleanup(server.Close)

	dispatcher := client.New(server.URL)
	viewer := client.New(server.URL)
	if _, err := dispatcher.Login(ctx, "dispatcher", "correct-horse-1", "dispatcher-client"); err != nil {
		t.Fatal(err)
	}
	if _, err := viewer.Login(ctx, "viewer", "correct-horse-1", "viewer-client"); err != nil {
		t.Fatal(err)
	}
	return server, dispatcher, viewer
}

func assertStatus(t *testing.T, baseURL, method, path, authorization string, body []byte, want int) {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, baseURL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		var problemBody any
		_ = json.NewDecoder(resp.Body).Decode(&problemBody)
		t.Fatalf("%s %s status=%d want=%d body=%v", method, path, resp.StatusCode, want, problemBody)
	}
}
