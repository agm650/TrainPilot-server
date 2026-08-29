package main

import (
	"bytes"
	"context"
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
		})
	}
}

func newConformanceServer(t *testing.T) *httptest.Server {
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
	authSvc := service.NewAuthService(db, users, clk, 15*time.Minute, time.Hour)
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
