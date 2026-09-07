package benchmark_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	httpapi "github.com/agm650/TrainPilot-server/internal/api"
	"github.com/agm650/TrainPilot-server/internal/auth"
	bench "github.com/agm650/TrainPilot-server/internal/benchmark"
	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/service"
	"github.com/agm650/TrainPilot-server/internal/station/simulator"
	"github.com/agm650/TrainPilot-server/internal/store"
	"github.com/agm650/TrainPilot-server/internal/transfer"
)

func TestRunAgainstSimulatorWithFiftyWebSockets(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	database, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.SeedDemo(ctx); err != nil {
		t.Fatal(err)
	}
	realClock := clock.Real{}
	users := service.NewUserServiceWithPasswordParams(database, realClock, auth.PasswordParams{Iterations: 100_000, SaltLength: 16, KeyLength: 32})
	const password = "benchmark-integration-secret"
	if _, err := users.Create(ctx, "benchmark", "Benchmark", password, model.RoleDispatcher, false, false); err != nil {
		t.Fatal(err)
	}
	authService := service.NewAuthService(database, users, realClock, 15*time.Minute, time.Hour)
	sim := simulator.New()
	if err := sim.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	bus := events.New()
	railway := service.NewRailwayService(database, sim, bus, 500*time.Millisecond)
	railway.StartFeedback(ctx)
	control := service.NewControlService(database, sim, bus, realClock, 15*time.Second, 20*time.Millisecond, 10*time.Millisecond)
	control.Start()
	defer control.Close()
	routes := service.NewRouteService(database, railway, bus)
	serverAPI := httpapi.New(authService, control, railway, routes, transfer.New(database, bus, realClock), database, bus, sim, sim, true)
	server := httptest.NewServer(serverAPI.Handler())
	defer server.Close()

	profile := bench.Profile{
		SchemaVersion:    bench.ProfileSchemaVersion,
		Name:             "integration",
		Warmup:           bench.Duration{Duration: 50 * time.Millisecond},
		Duration:         bench.Duration{Duration: 500 * time.Millisecond},
		Seed:             650,
		OperationTimeout: bench.Duration{Duration: 3 * time.Second},
		Clients:          bench.ClientProfile{Users: 2, WebSockets: 50, ActiveLocomotives: 1, Workers: 8},
		Rates: bench.RateProfile{
			LoginPerSecond: 2, RefreshPerSecond: 2, LeaseHeartbeatPerSecond: 5,
			ThrottlePerSecond: 10, FunctionsPerSecond: 5, FeedbackPerSecond: 10,
			AccessoriesPerSecond: 2, ReadsPerSecond: 10,
		},
	}
	fixture := bench.Fixture{
		SchemaVersion:   bench.FixtureSchemaVersion,
		LocomotiveIDs:   []string{"loco-bb26001", "loco-cc72030"},
		FeedbackTargets: []bench.FeedbackTarget{{Source: "simulator", Kind: "occupancy", Address: 1, BlockID: "block-a"}},
	}
	report, err := bench.Run(ctx, bench.RunOptions{
		Server: server.URL, Profile: profile, Fixture: fixture,
		Credentials:         []bench.Credential{{Username: "benchmark", Password: password}},
		AllowActiveCommands: true, AllowSimulatorAPI: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.OverallResult != "PASS" {
		t.Fatalf("result=%s invariants=%+v", report.OverallResult, report.Invariants)
	}
	if report.WebSocket.Connections < 50 || report.WebSocket.Snapshots < 50 {
		t.Fatalf("WebSocket summary=%+v", report.WebSocket)
	}
	if len(report.Operations) == 0 {
		t.Fatal("no operation summaries")
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), password) {
		t.Fatal("credential leaked into report")
	}
}
