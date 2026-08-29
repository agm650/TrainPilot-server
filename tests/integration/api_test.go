package integration

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	httpapi "github.com/agm650/TrainPilot-server/internal/api"
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

func TestPublicAPIContract(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SeedDemo(ctx); err != nil {
		t.Fatal(err)
	}
	clk := clock.Real{}
	params := auth.PasswordParams{Iterations: 100_000, SaltLength: 16, KeyLength: 32}
	users := service.NewUserServiceWithPasswordParams(db, clk, params)
	for _, u := range []struct {
		name, password string
		role           model.Role
	}{{"alice", "correct-horse-1", model.RoleDriver}, {"bob", "correct-horse-2", model.RoleDriver}, {"viewer", "correct-horse-3", model.RoleViewer}, {"admin", "correct-horse-4", model.RoleAdministrator}} {
		if _, err := users.Create(ctx, u.name, u.name, u.password, u.role, false, false); err != nil {
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
	api := httpapi.New(authSvc, control, railway, routes, transferSvc, db, bus, sim, sim, true)
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	alice := client.New(server.URL)
	bob := client.New(server.URL)
	viewer := client.New(server.URL)
	administrator := client.New(server.URL)
	if _, err := alice.Login(ctx, "alice", "correct-horse-1", "alice-client"); err != nil {
		t.Fatal(err)
	}
	if _, err := bob.Login(ctx, "bob", "correct-horse-2", "bob-client"); err != nil {
		t.Fatal(err)
	}
	if _, err := viewer.Login(ctx, "viewer", "correct-horse-3", "viewer-client"); err != nil {
		t.Fatal(err)
	}
	if _, err := administrator.Login(ctx, "admin", "correct-horse-4", "admin-client"); err != nil {
		t.Fatal(err)
	}
	locos, err := alice.Locomotives(ctx)
	if err != nil || len(locos) == 0 {
		t.Fatalf("locomotives err=%v len=%d", err, len(locos))
	}
	lease, err := alice.Acquire(ctx, locos[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	var ignored any
	status, err := bob.Do(ctx, http.MethodPost, "/api/v1/locomotives/"+locos[0].ID+"/control-lease", nil, &ignored)
	if err == nil || status != http.StatusConflict {
		t.Fatalf("second lease status=%d err=%v", status, err)
	}
	if err := alice.SetTrackPower(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := alice.Throttle(ctx, locos[0].ID, lease.ID, 30, station.Forward); err != nil {
		t.Fatal(err)
	}
	if err := alice.Function(ctx, locos[0].ID, lease.ID, 0, true); err != nil {
		t.Fatal(err)
	}
	power, err := alice.StationStatus(ctx)
	if err != nil || power.TrackPower != "on" {
		t.Fatalf("track power=%+v err=%v", power, err)
	}
	if err := alice.EmergencyStop(ctx); err != nil {
		t.Fatal(err)
	}
	status, err = viewer.Do(ctx, http.MethodPost, "/api/v1/locomotives/"+locos[1].ID+"/control-lease", nil, &ignored)
	if err == nil || status != http.StatusForbidden {
		t.Fatalf("viewer lease status=%d err=%v", status, err)
	}
	status, err = alice.Do(ctx, http.MethodPost, "/api/v1/users", map[string]string{"username": "remote"}, nil)
	if err == nil || status != http.StatusNotFound {
		t.Fatalf("remote user route status=%d err=%v", status, err)
	}

	archive, contentType, status, err := rawRequest(ctx, alice, http.MethodGet, "/api/v1/exports/rolling-stock", nil)
	if err != nil || status != http.StatusOK || len(archive) == 0 || contentType != "application/vnd.dcc-control.package+zip" {
		t.Fatalf("rolling stock export status=%d type=%q len=%d err=%v", status, contentType, len(archive), err)
	}
	_, _, status, err = rawRequest(ctx, alice, http.MethodPost, "/api/v1/imports/rolling-stock?mode=merge", archive)
	if err != nil || status != http.StatusForbidden {
		t.Fatalf("driver import status=%d err=%v", status, err)
	}
	_, _, status, err = rawRequest(ctx, administrator, http.MethodPost, "/api/v1/imports/rolling-stock?mode=merge", archive)
	if err != nil || status != http.StatusNoContent {
		t.Fatalf("administrator import status=%d err=%v", status, err)
	}
}

func rawRequest(ctx context.Context, c *client.Client, method, path string, body []byte) ([]byte, string, int, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return nil, "", 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/vnd.dcc-control.package+zip")
	}
	req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, "", 0, err
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(resp.Body)
	return data, resp.Header.Get("Content-Type"), resp.StatusCode, readErr
}
