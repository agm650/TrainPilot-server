package main

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	httpapi "github.com/agm650/TrainPilot-server/internal/api"
	"github.com/agm650/TrainPilot-server/internal/auth"
	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/service"
	"github.com/agm650/TrainPilot-server/internal/station/simulator"
	"github.com/agm650/TrainPilot-server/internal/store"
	"github.com/agm650/TrainPilot-server/internal/transfer"
)

func TestPublicEndpointInventoryMatchesServerAndOpenAPI(t *testing.T) {
	serverSource, err := os.ReadFile("../../internal/api/server.go")
	if err != nil {
		t.Fatal(err)
	}
	openAPI, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	serverPattern := regexp.MustCompile(`s\.mux\.Handle(?:Func)?\("([A-Z]+) ([^"?]+)`)
	placeholder := regexp.MustCompile(`\{[^}]+\}`)
	var serverEndpoints []string
	for _, match := range serverPattern.FindAllStringSubmatch(string(serverSource), -1) {
		if strings.HasPrefix(match[2], "/test/") {
			continue
		}
		serverEndpoints = append(serverEndpoints, match[1]+" "+placeholder.ReplaceAllString(match[2], "{}"))
	}
	var inventory []string
	for _, endpoint := range publicEndpointInventory {
		inventory = append(inventory, endpoint.Method+" "+endpoint.Path)
	}

	var documented []string
	currentPath := ""
	for _, line := range strings.Split(string(openAPI), "\n") {
		if strings.HasPrefix(line, "  /") && strings.HasSuffix(line, ":") {
			currentPath = strings.TrimSuffix(strings.TrimSpace(line), ":")
			continue
		}
		if currentPath == "" || !strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "      ") {
			continue
		}
		method := strings.TrimSuffix(strings.TrimSpace(line), ":")
		switch method {
		case "get", "post", "put", "delete":
			documented = append(documented, strings.ToUpper(method)+" "+placeholder.ReplaceAllString(currentPath, "{}"))
		}
	}
	sort.Strings(serverEndpoints)
	sort.Strings(inventory)
	sort.Strings(documented)
	if strings.Join(serverEndpoints, "\n") != strings.Join(inventory, "\n") {
		t.Fatalf("server routes and conformance inventory differ\nserver:\n%s\n\ninventory:\n%s", strings.Join(serverEndpoints, "\n"), strings.Join(inventory, "\n"))
	}
	if strings.Join(documented, "\n") != strings.Join(inventory, "\n") {
		t.Fatalf("OpenAPI routes and conformance inventory differ\nOpenAPI:\n%s\n\ninventory:\n%s", strings.Join(documented, "\n"), strings.Join(inventory, "\n"))
	}
}

func TestCompoundTurnoutContractsAreDocumented(t *testing.T) {
	openAPI, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	asyncAPI, err := os.ReadFile("../../api/asyncapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"version: 1.7.0",
		"position:",
		"deprecated: true",
		"reportQuality:",
		"turnout_confirmation_timeout",
		"station_unsupported",
	} {
		if !bytes.Contains(openAPI, []byte(fragment)) {
			t.Errorf("OpenAPI is missing %q", fragment)
		}
	}
	for _, fragment := range []string{
		"version: 1.9.0",
		"name: turnout.commanded",
		"name: turnout.state.changed",
		"name: turnout.command.failed",
		"reportQuality:",
	} {
		if !bytes.Contains(asyncAPI, []byte(fragment)) {
			t.Errorf("AsyncAPI is missing %q", fragment)
		}
	}
}

func TestConformanceRunnerAgainstSimulator(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(map[bool]string{false: "passive", true: "active"}[active], func(t *testing.T) {
			server := newConformanceServer(t)
			var output bytes.Buffer
			failed := run(context.Background(), configuration{
				server:              server.URL,
				user1:               "alice",
				pass1:               "correct-horse-1",
				user2:               "bob",
				pass2:               "correct-horse-2",
				admin:               "admin",
				adminPass:           "correct-horse-admin",
				allowActiveCommands: active,
				allowConfigChanges:  active,
			}, &output)
			if failed != 0 {
				t.Fatalf("conformance failures=%d\n%s", failed, output.String())
			}
			if !strings.Contains(output.String(), "SKIP session expiration checks") {
				t.Fatalf("standard run did not report the expiration skip\n%s", output.String())
			}
		})
	}
}

func TestSessionExpirationWait(t *testing.T) {
	now := time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)

	t.Run("near expiry", func(t *testing.T) {
		wait, err := sessionExpirationWait(now, now.Add(500*time.Millisecond), 150*time.Millisecond, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if wait != 650*time.Millisecond {
			t.Fatalf("wait=%v want 650ms", wait)
		}
	})

	t.Run("expiry exceeds maximum", func(t *testing.T) {
		_, err := sessionExpirationWait(now, now.Add(time.Minute), 150*time.Millisecond, 15*time.Second)
		var tooLong *expirationWaitTooLongError
		if !errors.As(err, &tooLong) {
			t.Fatalf("error=%v want expirationWaitTooLongError", err)
		}
	})

	t.Run("already expired", func(t *testing.T) {
		wait, err := sessionExpirationWait(now, now.Add(-time.Second), 150*time.Millisecond, 15*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if wait != 0 {
			t.Fatalf("wait=%v want zero", wait)
		}
	})

	t.Run("invalid maximum", func(t *testing.T) {
		if _, err := sessionExpirationWait(now, now.Add(time.Second), 150*time.Millisecond, 0); err == nil {
			t.Fatal("expected invalid maximum error")
		}
	})
}

func TestWaitUntilHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := waitUntil(ctx, time.Now().Add(time.Second), sessionExpirationMargin, 2*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v want context.Canceled", err)
	}
}

func TestWaitForSessionExpirationReportsConfiguration(t *testing.T) {
	err := waitForSessionExpiration(context.Background(), "access token", "security.accessTokenTTL", time.Now().Add(time.Minute), 15*time.Second)
	if err == nil {
		t.Fatal("expected maximum-wait error")
	}
	for _, text := range []string{"access token", "security.accessTokenTTL", "--session-expiration-max-wait"} {
		if !strings.Contains(err.Error(), text) {
			t.Fatalf("error=%q does not contain %q", err, text)
		}
	}
}

func TestSessionExpirationChecksAgainstSimulator(t *testing.T) {
	server := newConformanceServerWithTokenTTLs(t, 250*time.Millisecond, time.Second)
	results := make([]result, 0, 5)
	runSessionExpirationChecks(context.Background(), configuration{
		server:                   server.URL,
		user1:                    "alice",
		pass1:                    "correct-horse-1",
		sessionExpirationMaxWait: 2 * time.Second,
	}, func(name string, err error) {
		results = append(results, result{name: name, err: err})
	})

	wantNames := []string{
		"access token is accepted before expiration",
		"expired access token is rejected",
		"refresh token remains valid after access-token expiration",
		"refreshed access token is accepted",
		"expired refresh token is rejected",
	}
	if len(results) != len(wantNames) {
		t.Fatalf("results=%d want %d: %+v", len(results), len(wantNames), results)
	}
	for i, item := range results {
		if item.name != wantNames[i] {
			t.Fatalf("result[%d].name=%q want %q", i, item.name, wantNames[i])
		}
		if item.err != nil {
			t.Fatalf("%s: %v", item.name, item.err)
		}
	}
}

func newConformanceServer(t *testing.T) *httptest.Server {
	return newConformanceServerWithTokenTTLs(t, 15*time.Minute, time.Hour)
}

func newConformanceServerWithTokenTTLs(t *testing.T, accessTTL, refreshTTL time.Duration) *httptest.Server {
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
		username string
		password string
		role     model.Role
	}{{"alice", "correct-horse-1", model.RoleDriver}, {"bob", "correct-horse-2", model.RoleDriver}, {"admin", "correct-horse-admin", model.RoleAdministrator}} {
		if _, err := users.Create(ctx, item.username, item.username, item.password, item.role, false, false); err != nil {
			t.Fatal(err)
		}
	}
	authSvc := service.NewAuthService(db, users, clk, accessTTL, refreshTTL)
	sim := simulator.New()
	if err := sim.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	bus := events.New()
	railway := service.NewRailwayService(db, sim, bus)
	control := service.NewControlService(db, sim, bus, clk, 15*time.Second, time.Second, time.Hour)
	routes := service.NewRouteService(db, railway, bus)
	api := httpapi.New(authSvc, control, railway, routes, transfer.New(db, bus, clk), db, bus, sim, sim, true)
	server := httptest.NewServer(api.Handler())
	t.Cleanup(server.Close)
	return server
}
