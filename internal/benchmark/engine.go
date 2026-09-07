package benchmark

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/agm650/TrainPilot-server/internal/client"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/service"
	"github.com/agm650/TrainPilot-server/internal/station"
)

var (
	errSchedulerBackpressure = errors.New("benchmark scheduler queue is full")
	errNoOperationTarget     = errors.New("no operation target is currently available")
)

type RunOptions struct {
	Server              string
	Profile             Profile
	Fixture             Fixture
	Credentials         []Credential
	AllowActiveCommands bool
	AllowSimulatorAPI   bool
	AllowRealHardware   bool
	BenchmarkVersion    string
}

type benchSession struct {
	mu         sync.Mutex
	credential Credential
	client     *client.Client
	clientID   string
}

func (s *benchSession) withClient(ctx context.Context, operation func(*client.Client) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return operation(s.client)
}

func (s *benchSession) accessToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.client.AccessToken
}

type leaseBinding struct {
	lease        model.ControlLease
	sessionIndex int
}

type routeBinding struct {
	state        string
	sessionIndex int
}

type runEngine struct {
	options      RunOptions
	profile      Profile
	info         client.SystemInfo
	sessions     []*benchSession
	locomotives  []model.Locomotive
	blocks       []model.Block
	turnouts     []model.Turnout
	routes       []model.Route
	recorder     *operationRecorder
	invariants   *invariantTracker
	expectations *expectationTracker
	wsMetrics    *webSocketMetrics
	monitor      *eventMonitor
	stateMu      sync.Mutex
	leases       map[string]leaseBinding
	routeStates  map[string]routeBinding
	feedback     map[string]bool
	initialPower string
	jobDrops     atomic.Int64
}

func Run(ctx context.Context, options RunOptions) (Report, error) {
	profile := options.Profile
	profile.setDefaults()
	if err := profile.Validate(); err != nil {
		return Report{}, fmt.Errorf("profile: %w", err)
	}
	options.Profile = profile
	if err := ValidateFixtureForProfile(profile, options.Fixture); err != nil {
		return Report{}, fmt.Errorf("fixture: %w", err)
	}
	if len(options.Credentials) == 0 {
		return Report{}, errors.New("at least one credential is required")
	}
	serverURL, err := normalizedServerURL(options.Server)
	if err != nil {
		return Report{}, err
	}
	options.Server = serverURL
	if options.BenchmarkVersion == "" {
		options.BenchmarkVersion = executableVersion()
	}
	engine := &runEngine{
		options: options, profile: profile, recorder: newOperationRecorder(),
		invariants: newInvariantTracker(), wsMetrics: &webSocketMetrics{},
		leases: make(map[string]leaseBinding), routeStates: make(map[string]routeBinding),
		feedback: make(map[string]bool),
	}
	engine.expectations = newExpectationTracker(engine.invariants)
	engine.monitor = newEventMonitor(engine.invariants, engine.expectations, engine.wsMetrics, options.Fixture)
	if err := engine.preflight(ctx); err != nil {
		logoutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		engine.logoutSessions(logoutCtx)
		cancel()
		return Report{}, err
	}
	startedAt := time.Now().UTC()
	cleanupNeeded := true
	defer func() {
		if cleanupNeeded {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			engine.cleanup(cleanupCtx)
		}
	}()

	websocketCtx, stopWebSockets := context.WithCancel(ctx)
	defer stopWebSockets()
	schedulerCtx, stopSchedulers := context.WithCancel(ctx)
	defer stopSchedulers()
	ready := make(chan struct{}, engine.profile.Clients.WebSockets)
	var websocketWorkers sync.WaitGroup
	for index := 0; index < engine.profile.Clients.WebSockets; index++ {
		websocketWorkers.Add(1)
		go func(index int) {
			defer websocketWorkers.Done()
			engine.runWebSocket(websocketCtx, index, ready)
		}(index)
	}
	if err := waitForWebSockets(ctx, ready, engine.profile.Clients.WebSockets, engine.profile.OperationTimeout.Duration); err != nil {
		return Report{}, err
	}
	if err := engine.prepareActiveState(ctx); err != nil {
		return Report{}, err
	}

	jobs := make(chan scheduledJob, max(1024, engine.profile.Clients.Workers*16))
	var workerGroup sync.WaitGroup
	for index := 0; index < engine.profile.Clients.Workers; index++ {
		workerGroup.Add(1)
		go func() {
			defer workerGroup.Done()
			for job := range jobs {
				engine.executeJob(ctx, job)
			}
		}()
	}
	var schedulerGroup sync.WaitGroup
	for operation, rate := range engine.operationRates() {
		if rate <= 0 {
			continue
		}
		schedulerGroup.Add(1)
		go func(operation string, rate float64) {
			defer schedulerGroup.Done()
			runRateScheduler(schedulerCtx, operation, rate, engine.profile.Seed, jobs, func() {
				engine.jobDrops.Add(1)
				engine.recorder.Record("scheduler_drop", 0, errSchedulerBackpressure)
			})
		}(operation, rate)
	}

	runErr := waitContext(ctx, engine.profile.Warmup.Duration)
	var measurementStarted time.Time
	var measuredEnded time.Time
	if runErr == nil {
		measurementStarted = time.Now().UTC()
		engine.recorder.StartMeasurement(measurementStarted)
		runErr = waitContext(ctx, engine.profile.Duration.Duration)
		measuredEnded = time.Now().UTC()
	}
	stopSchedulers()
	schedulerGroup.Wait()
	close(jobs)
	workerGroup.Wait()
	if runErr == nil {
		waitForExpectations(ctx, engine.expectations, engine.profile.OperationTimeout.Duration)
		engine.expectations.Expire(time.Now())
	}
	stopWebSockets()
	websocketWorkers.Wait()
	if runErr != nil {
		return Report{}, runErr
	}
	cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 10*time.Second)
	engine.cleanup(cleanupCtx)
	cancelCleanup()
	cleanupNeeded = false

	results, invariantFailure := engine.invariants.Results()
	report := Report{
		SchemaVersion: ReportSchemaVersion, BenchmarkVersion: options.BenchmarkVersion,
		StartedAt: startedAt, EndedAt: measuredEnded, Duration: engine.profile.Duration.String(),
		Warmup: engine.profile.Warmup.String(), Profile: engine.profile,
		ProfileSHA256: profileHash(engine.profile), FixtureSHA256: fixtureHash(engine.options.Fixture), Seed: engine.profile.Seed,
		Server: serverMetadata(options.Server, engine.info), ClientHost: currentHostMetadata(),
		Operations: engine.recorder.Summaries(measuredEnded.Sub(measurementStarted)),
		WebSocket:  engine.wsMetrics.summary(), Invariants: results, OverallResult: "PASS",
	}
	if invariantFailure {
		report.OverallResult = "FAIL"
	}
	return report, nil
}

func (e *runEngine) preflight(ctx context.Context) error {
	probe := client.New(e.options.Server)
	probe.HTTP.Timeout = e.profile.OperationTimeout.Duration
	info, err := probe.SystemInfo(ctx)
	if err != nil {
		return fmt.Errorf("read server information: %w", err)
	}
	e.info = info
	if e.profile.HasActiveOperations() && !e.options.AllowActiveCommands {
		return errors.New("profile contains active commands; pass --allow-active-commands")
	}
	if e.profile.HasActiveOperations() && info.Station.Driver != "simulator" && !e.options.AllowRealHardware {
		return fmt.Errorf("active benchmark targets %s hardware; also pass --allow-real-hardware to confirm", info.Station.Driver)
	}
	if e.profile.HasSimulatorOperations() {
		if !e.options.AllowSimulatorAPI {
			return errors.New("profile injects simulator events; pass --allow-simulator-api")
		}
		if info.Station.Driver != "simulator" {
			return fmt.Errorf("simulator event injection requires the simulator driver, got %q", info.Station.Driver)
		}
		if e.profile.Clients.WebSockets == 0 {
			return errors.New("feedback injection requires at least one WebSocket for correlation")
		}
		if len(e.options.Fixture.FeedbackTargets) == 0 {
			return errors.New("feedback injection requires fixture.feedbackTargets")
		}
	}
	if err := e.loginSessions(ctx); err != nil {
		return err
	}
	if err := e.loadResources(ctx); err != nil {
		return err
	}
	if e.profile.HasSimulatorOperations() {
		var state json.RawMessage
		if err := e.sessions[0].withClient(ctx, func(c *client.Client) error {
			_, err := c.Do(ctx, http.MethodGet, "/test/v1/simulator/state", nil, &state)
			return err
		}); err != nil {
			return fmt.Errorf("simulator test API is unavailable: %w", err)
		}
	}
	status, err := e.stationStatus(ctx)
	if err != nil {
		return fmt.Errorf("read station status: %w", err)
	}
	e.initialPower = status.TrackPower
	e.invariants.Observe(invariantValidJSON)
	e.invariants.Observe(invariantServerAvailable)
	return nil
}

func (e *runEngine) loginSessions(ctx context.Context) error {
	e.sessions = make([]*benchSession, 0, e.profile.Clients.Users)
	for index := 0; index < e.profile.Clients.Users; index++ {
		credential := e.options.Credentials[index%len(e.options.Credentials)]
		session := &benchSession{
			credential: credential, client: client.New(e.options.Server),
			clientID: fmt.Sprintf("trainpilot-bench-%d-%d", e.profile.Seed, index),
		}
		session.client.HTTP.Timeout = e.profile.OperationTimeout.Duration
		loginCtx, cancel := context.WithTimeout(ctx, e.profile.OperationTimeout.Duration)
		err := loginClient(loginCtx, session.client, credential, session.clientID)
		cancel()
		if err != nil {
			return fmt.Errorf("login virtual user %d (%s): %w", index, credential.Username, err)
		}
		e.sessions = append(e.sessions, session)
	}
	return nil
}

func loginClient(ctx context.Context, c *client.Client, credential Credential, clientID string) error {
	var pair service.TokenPair
	_, err := c.Do(ctx, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"username": credential.Username, "password": credential.Password,
		"clientId": clientID, "clientName": "trainpilot-bench", "platform": "cli",
	}, &pair)
	if err == nil {
		c.AccessToken = pair.AccessToken
		c.RefreshToken = pair.RefreshToken
	}
	return err
}

func (e *runEngine) loadResources(ctx context.Context) error {
	first := e.sessions[0]
	if err := first.withClient(ctx, func(c *client.Client) error {
		var err error
		e.locomotives, err = c.Locomotives(ctx)
		if err != nil {
			return err
		}
		e.blocks, err = c.Blocks(ctx)
		if err != nil {
			return err
		}
		e.turnouts, err = c.Turnouts(ctx)
		if err != nil {
			return err
		}
		e.routes, err = c.Routes(ctx)
		return err
	}); err != nil {
		return fmt.Errorf("load benchmark resources: %w", err)
	}
	if len(e.options.Fixture.LocomotiveIDs) > 0 {
		available := make(map[string]model.Locomotive, len(e.locomotives))
		for _, locomotive := range e.locomotives {
			available[locomotive.ID] = locomotive
		}
		selected := make([]model.Locomotive, 0, len(e.options.Fixture.LocomotiveIDs))
		for _, id := range e.options.Fixture.LocomotiveIDs {
			locomotive, exists := available[id]
			if !exists {
				return fmt.Errorf("fixture locomotive %q is not available", id)
			}
			selected = append(selected, locomotive)
		}
		e.locomotives = selected
	}
	if e.profile.Clients.ActiveLocomotives > len(e.locomotives) {
		return fmt.Errorf("profile requests %d active locomotives, but only %d are available", e.profile.Clients.ActiveLocomotives, len(e.locomotives))
	}
	if err := e.validateResourceRequirements(); err != nil {
		return err
	}
	return nil
}

func (e *runEngine) validateResourceRequirements() error {
	leaseRate := e.profile.Rates.LeaseAcquirePerSecond + e.profile.Rates.LeaseHeartbeatPerSecond + e.profile.Rates.LeaseReleasePerSecond + e.profile.Rates.ThrottlePerSecond + e.profile.Rates.FunctionsPerSecond
	if (e.profile.Clients.ActiveLocomotives > 0 || leaseRate > 0) && len(e.locomotives) == 0 {
		return errors.New("profile requires locomotives, but none are available")
	}
	if e.profile.Rates.ThrottlePerSecond > 0 && !e.info.Station.LocomotiveControl {
		return errors.New("profile requires throttle commands, but the station does not support locomotive control")
	}
	if e.profile.Rates.FunctionsPerSecond > 0 && e.info.Station.Functions == 0 {
		return errors.New("profile requires function commands, but the station does not support functions")
	}
	if e.profile.Rates.AccessoriesPerSecond > 0 && len(e.turnouts) == 0 {
		return errors.New("profile requires accessory commands, but no turnouts are available")
	}
	if e.profile.Rates.AccessoriesPerSecond > 0 && !e.info.Station.AccessoryControl {
		return errors.New("profile requires accessory commands, but the station does not support accessories")
	}
	if e.profile.Rates.RouteOperationsPerSecond > 0 && len(e.routes) == 0 {
		return errors.New("profile requires route operations, but no routes are available")
	}
	blocks := make(map[string]struct{}, len(e.blocks))
	for _, block := range e.blocks {
		blocks[block.ID] = struct{}{}
	}
	for _, target := range e.options.Fixture.FeedbackTargets {
		if _, exists := blocks[target.BlockID]; !exists {
			return fmt.Errorf("fixture feedback block %q is not available", target.BlockID)
		}
	}
	routes := make(map[string]struct{}, len(e.routes))
	for _, route := range e.routes {
		routes[route.ID] = struct{}{}
	}
	for _, pair := range e.options.Fixture.IncompatibleRoutePairs {
		if _, exists := routes[pair.First]; !exists {
			return fmt.Errorf("fixture route %q is not available", pair.First)
		}
		if _, exists := routes[pair.Second]; !exists {
			return fmt.Errorf("fixture route %q is not available", pair.Second)
		}
	}
	return nil
}

func (e *runEngine) prepareActiveState(ctx context.Context) error {
	if !e.profile.HasActiveOperations() {
		return nil
	}
	if err := e.sessions[0].withClient(ctx, func(c *client.Client) error { return c.SetTrackPower(ctx, true) }); err != nil {
		return fmt.Errorf("enable track power: %w", err)
	}
	for index := 0; index < e.profile.Clients.ActiveLocomotives; index++ {
		locomotive := e.locomotives[index]
		sessionIndex := index % len(e.sessions)
		var lease model.ControlLease
		if err := e.sessions[sessionIndex].withClient(ctx, func(c *client.Client) error {
			var err error
			lease, err = c.Acquire(ctx, locomotive.ID)
			return err
		}); err != nil {
			return fmt.Errorf("acquire initial lease for %s: %w", locomotive.ID, err)
		}
		e.leases[locomotive.ID] = leaseBinding{lease: lease, sessionIndex: sessionIndex}
	}
	if e.profile.Clients.ActiveLocomotives > 0 {
		locomotive := e.locomotives[0]
		owner := e.leases[locomotive.ID].sessionIndex
		other := (owner + 1) % len(e.sessions)
		var duplicate model.ControlLease
		err := e.sessions[other].withClient(ctx, func(c *client.Client) error {
			var err error
			duplicate, err = c.Acquire(ctx, locomotive.ID)
			return err
		})
		if err == nil {
			e.invariants.Violate(invariantExclusiveLease, fmt.Sprintf("locomotive %s accepted concurrent leases %s and %s", locomotive.ID, e.leases[locomotive.ID].lease.ID, duplicate.ID))
			_ = e.sessions[other].withClient(ctx, func(c *client.Client) error { return c.Release(ctx, duplicate.ID) })
		} else if hasHTTPStatus(err, http.StatusConflict) {
			e.invariants.Observe(invariantExclusiveLease)
		} else {
			return fmt.Errorf("exclusive lease probe: %w", err)
		}
	}
	if len(e.locomotives) > 0 {
		locomotive := e.locomotives[0]
		err := e.sessions[0].withClient(ctx, func(c *client.Client) error {
			return c.Throttle(ctx, locomotive.ID, "invalid-benchmark-lease", 0, station.Forward)
		})
		if err == nil {
			e.invariants.Violate(invariantLeaseRequired, "throttle with an invalid lease was accepted")
		} else if hasHTTPStatus(err, http.StatusConflict, http.StatusNotFound) {
			e.invariants.Observe(invariantLeaseRequired)
		} else {
			return fmt.Errorf("invalid lease probe: %w", err)
		}
	}
	return nil
}

func (e *runEngine) operationRates() map[string]float64 {
	return map[string]float64{
		"login":           e.profile.Rates.LoginPerSecond,
		"refresh":         e.profile.Rates.RefreshPerSecond,
		"lease_acquire":   e.profile.Rates.LeaseAcquirePerSecond,
		"lease_heartbeat": e.profile.Rates.LeaseHeartbeatPerSecond,
		"lease_release":   e.profile.Rates.LeaseReleasePerSecond,
		"throttle":        e.profile.Rates.ThrottlePerSecond,
		"function":        e.profile.Rates.FunctionsPerSecond,
		"feedback":        e.profile.Rates.FeedbackPerSecond,
		"accessory":       e.profile.Rates.AccessoriesPerSecond,
		"route":           e.profile.Rates.RouteOperationsPerSecond,
		"read":            e.profile.Rates.ReadsPerSecond,
		"health":          1,
	}
}

func (e *runEngine) executeJob(parent context.Context, job scheduledJob) {
	ctx, cancel := context.WithTimeout(parent, e.profile.OperationTimeout.Duration)
	defer cancel()
	started := time.Now()
	random := rand.New(rand.NewSource(job.seed))
	err := e.perform(ctx, job.operation, random)
	if errors.Is(err, errNoOperationTarget) {
		e.recorder.Skip(job.operation)
		return
	}
	e.recorder.Record(job.operation, time.Since(started), err)
	if isJSONError(err) {
		e.invariants.Violate(invariantValidJSON, fmt.Sprintf("invalid JSON during %s", job.operation))
	}
}

func (e *runEngine) perform(ctx context.Context, operation string, random *rand.Rand) error {
	switch operation {
	case "login":
		return e.performLogin(ctx, random)
	case "refresh":
		return e.performRefresh(ctx, random)
	case "lease_acquire":
		return e.performAcquire(ctx, random)
	case "lease_heartbeat":
		return e.performHeartbeat(ctx, random)
	case "lease_release":
		return e.performRelease(ctx, random)
	case "throttle":
		return e.performThrottle(ctx, random)
	case "function":
		return e.performFunction(ctx, random)
	case "feedback":
		return e.performFeedback(ctx, random)
	case "accessory":
		return e.performAccessory(ctx, random)
	case "route":
		return e.performRoute(ctx, random)
	case "read":
		return e.performRead(ctx, random)
	case "health":
		return e.performHealth(ctx)
	default:
		return fmt.Errorf("unknown benchmark operation %q", operation)
	}
}

func (e *runEngine) performLogin(ctx context.Context, random *rand.Rand) error {
	credential := e.options.Credentials[random.Intn(len(e.options.Credentials))]
	c := client.New(e.options.Server)
	c.HTTP.Timeout = e.profile.OperationTimeout.Duration
	if err := loginClient(ctx, c, credential, fmt.Sprintf("trainpilot-bench-transient-%d", random.Uint64())); err != nil {
		return err
	}
	return c.Logout(ctx)
}

func (e *runEngine) performRefresh(ctx context.Context, random *rand.Rand) error {
	session := e.sessions[random.Intn(len(e.sessions))]
	return session.withClient(ctx, func(c *client.Client) error {
		_, err := c.Refresh(ctx, c.RefreshToken)
		return err
	})
}

func (e *runEngine) performAcquire(ctx context.Context, random *rand.Rand) error {
	if len(e.locomotives) == 0 {
		return errNoOperationTarget
	}
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	free := make([]model.Locomotive, 0, len(e.locomotives))
	for _, locomotive := range e.locomotives {
		if _, exists := e.leases[locomotive.ID]; !exists {
			free = append(free, locomotive)
		}
	}
	if len(free) == 0 {
		return errNoOperationTarget
	}
	locomotive := free[random.Intn(len(free))]
	sessionIndex := random.Intn(len(e.sessions))
	var lease model.ControlLease
	err := e.sessions[sessionIndex].withClient(ctx, func(c *client.Client) error {
		var err error
		lease, err = c.Acquire(ctx, locomotive.ID)
		return err
	})
	if err != nil {
		return err
	}
	if previous, exists := e.leases[locomotive.ID]; exists && previous.lease.ID != lease.ID {
		e.invariants.Violate(invariantExclusiveLease, fmt.Sprintf("locomotive %s acquired twice", locomotive.ID))
	}
	e.leases[locomotive.ID] = leaseBinding{lease: lease, sessionIndex: sessionIndex}
	e.invariants.Observe(invariantExclusiveLease)
	return nil
}

func (e *runEngine) randomLease(random *rand.Rand) (string, leaseBinding, bool) {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	if len(e.leases) == 0 {
		return "", leaseBinding{}, false
	}
	ids := make([]string, 0, len(e.leases))
	for id := range e.leases {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	id := ids[random.Intn(len(ids))]
	return id, e.leases[id], true
}

func (e *runEngine) performHeartbeat(ctx context.Context, random *rand.Rand) error {
	locomotiveID, binding, ok := e.randomLease(random)
	if !ok {
		return errNoOperationTarget
	}
	var renewed model.ControlLease
	err := e.sessions[binding.sessionIndex].withClient(ctx, func(c *client.Client) error {
		var err error
		renewed, err = c.Heartbeat(ctx, binding.lease.ID)
		return err
	})
	if err == nil {
		e.stateMu.Lock()
		if current, exists := e.leases[locomotiveID]; exists && current.lease.ID == binding.lease.ID {
			current.lease = renewed
			e.leases[locomotiveID] = current
		}
		e.stateMu.Unlock()
	}
	return err
}

func (e *runEngine) performRelease(ctx context.Context, random *rand.Rand) error {
	locomotiveID, binding, ok := e.randomLease(random)
	if !ok {
		return errNoOperationTarget
	}
	err := e.sessions[binding.sessionIndex].withClient(ctx, func(c *client.Client) error {
		return c.Release(ctx, binding.lease.ID)
	})
	if err == nil {
		e.stateMu.Lock()
		if current, exists := e.leases[locomotiveID]; exists && current.lease.ID == binding.lease.ID {
			delete(e.leases, locomotiveID)
		}
		e.stateMu.Unlock()
	}
	return err
}

func (e *runEngine) performThrottle(ctx context.Context, random *rand.Rand) error {
	locomotiveID, binding, ok := e.randomLease(random)
	if !ok {
		return errNoOperationTarget
	}
	speed := random.Intn(101)
	direction := station.Forward
	if random.Intn(2) == 1 {
		direction = station.Reverse
	}
	e.monitor.markExplicitThrottle(locomotiveID)
	key := fmt.Sprintf("throttle:%s:%d", locomotiveID, speed)
	expectation := uint64(0)
	if e.profile.Clients.WebSockets > 0 {
		expectation = e.expectations.Begin(key, e.profile.OperationTimeout.Duration)
	}
	err := e.sessions[binding.sessionIndex].withClient(ctx, func(c *client.Client) error {
		return c.Throttle(ctx, locomotiveID, binding.lease.ID, speed, direction)
	})
	if err != nil {
		e.monitor.unmarkExplicitThrottle(locomotiveID)
		if expectation != 0 {
			e.expectations.Cancel(key, expectation)
		}
	}
	return err
}

func (e *runEngine) performFunction(ctx context.Context, random *rand.Rand) error {
	locomotiveID, binding, ok := e.randomLease(random)
	if !ok {
		return errNoOperationTarget
	}
	maxFunction := e.info.Station.MaxFunctionNumber
	if maxFunction < 0 || e.info.Station.Functions == 0 {
		return errNoOperationTarget
	}
	function := random.Intn(maxFunction + 1)
	enabled := random.Intn(2) == 1
	key := fmt.Sprintf("function:%s:%d:%t", locomotiveID, function, enabled)
	expectation := uint64(0)
	if e.profile.Clients.WebSockets > 0 {
		expectation = e.expectations.Begin(key, e.profile.OperationTimeout.Duration)
	}
	err := e.sessions[binding.sessionIndex].withClient(ctx, func(c *client.Client) error {
		return c.Function(ctx, locomotiveID, binding.lease.ID, function, enabled)
	})
	if err != nil && expectation != 0 {
		e.expectations.Cancel(key, expectation)
	}
	return err
}

func (e *runEngine) performFeedback(ctx context.Context, random *rand.Rand) error {
	target := e.options.Fixture.FeedbackTargets[random.Intn(len(e.options.Fixture.FeedbackTargets))]
	e.stateMu.Lock()
	key := fmt.Sprintf("%s:%d", target.Source, target.Address)
	active := !e.feedback[key]
	e.feedback[key] = active
	e.stateMu.Unlock()
	expectationKey := fmt.Sprintf("feedback:%s:%t", target.BlockID, active)
	expectation := e.expectations.Begin(expectationKey, e.profile.OperationTimeout.Duration)
	session := e.sessions[random.Intn(len(e.sessions))]
	err := session.withClient(ctx, func(c *client.Client) error {
		_, err := c.Do(ctx, http.MethodPut, "/test/v1/simulator/feedback", map[string]any{
			"source": target.Source, "kind": target.Kind, "address": target.Address,
			"active": active, "emit": true,
		}, nil)
		return err
	})
	if err != nil {
		e.expectations.Cancel(expectationKey, expectation)
	}
	return err
}

func (e *runEngine) performAccessory(ctx context.Context, random *rand.Rand) error {
	if len(e.turnouts) == 0 {
		return errNoOperationTarget
	}
	turnout := e.turnouts[random.Intn(len(e.turnouts))]
	if len(turnout.Positions) == 0 {
		return errNoOperationTarget
	}
	position := turnout.Positions[random.Intn(len(turnout.Positions))].ID
	key := fmt.Sprintf("turnout:%s:%s", turnout.ID, position)
	expectation := uint64(0)
	if e.profile.Clients.WebSockets > 0 {
		expectation = e.expectations.Begin(key, e.profile.OperationTimeout.Duration)
	}
	session := e.sessions[random.Intn(len(e.sessions))]
	err := session.withClient(ctx, func(c *client.Client) error { return c.SetTurnout(ctx, turnout.ID, position) })
	if err != nil && expectation != 0 {
		e.expectations.Cancel(key, expectation)
	}
	return err
}

func (e *runEngine) performRoute(ctx context.Context, random *rand.Rand) error {
	if len(e.routes) == 0 {
		return errNoOperationTarget
	}
	route := e.routes[random.Intn(len(e.routes))]
	e.stateMu.Lock()
	binding, exists := e.routeStates[route.ID]
	if !exists {
		binding = routeBinding{state: "idle", sessionIndex: random.Intn(len(e.sessions))}
	}
	e.stateMu.Unlock()
	session := e.sessions[binding.sessionIndex]
	var next string
	var err error
	switch binding.state {
	case "idle":
		next = "reserved"
		err = session.withClient(ctx, func(c *client.Client) error { return c.ReserveRoute(ctx, route.ID) })
	case "reserved":
		next = "active"
		err = session.withClient(ctx, func(c *client.Client) error { return c.ActivateRoute(ctx, route.ID) })
	default:
		next = "idle"
		err = session.withClient(ctx, func(c *client.Client) error { return c.ReleaseRoute(ctx, route.ID) })
	}
	if err == nil {
		e.stateMu.Lock()
		e.routeStates[route.ID] = routeBinding{state: next, sessionIndex: binding.sessionIndex}
		e.stateMu.Unlock()
	}
	return err
}

func (e *runEngine) performRead(ctx context.Context, random *rand.Rand) error {
	session := e.sessions[random.Intn(len(e.sessions))]
	return session.withClient(ctx, func(c *client.Client) error {
		switch random.Intn(6) {
		case 0:
			_, err := c.Locomotives(ctx)
			return err
		case 1:
			_, err := c.Blocks(ctx)
			return err
		case 2:
			_, err := c.Turnouts(ctx)
			return err
		case 3:
			_, err := c.Routes(ctx)
			return err
		case 4:
			_, err := c.StationStatus(ctx)
			return err
		default:
			_, err := c.Me(ctx)
			return err
		}
	})
}

func (e *runEngine) performHealth(ctx context.Context) error {
	c := client.New(e.options.Server)
	c.HTTP.Timeout = e.profile.OperationTimeout.Duration
	var response struct {
		Status string `json:"status"`
	}
	_, err := c.Do(ctx, http.MethodGet, "/healthz", nil, &response)
	if err != nil || response.Status != "ok" {
		detail := "health endpoint failed"
		if err == nil {
			detail = fmt.Sprintf("health endpoint returned status %q", response.Status)
			err = errors.New(detail)
		}
		e.invariants.Violate(invariantServerAvailable, detail)
		return err
	}
	e.invariants.Observe(invariantServerAvailable)
	return nil
}

func (e *runEngine) stationStatus(ctx context.Context) (station.Status, error) {
	var status station.Status
	err := e.sessions[0].withClient(ctx, func(c *client.Client) error {
		var err error
		status, err = c.StationStatus(ctx)
		return err
	})
	return status, err
}

func (e *runEngine) cleanup(ctx context.Context) {
	e.stateMu.Lock()
	leases := make([]leaseBinding, 0, len(e.leases))
	for _, binding := range e.leases {
		leases = append(leases, binding)
	}
	routes := make(map[string]routeBinding, len(e.routeStates))
	for id, binding := range e.routeStates {
		routes[id] = binding
	}
	e.stateMu.Unlock()
	for _, binding := range leases {
		locomotiveID := binding.lease.LocomotiveID
		_ = e.sessions[binding.sessionIndex].withClient(ctx, func(c *client.Client) error {
			_ = c.Throttle(ctx, locomotiveID, binding.lease.ID, 0, station.Forward)
			return c.Release(ctx, binding.lease.ID)
		})
	}
	for routeID, binding := range routes {
		if binding.state != "idle" {
			_ = e.sessions[binding.sessionIndex].withClient(ctx, func(c *client.Client) error { return c.ReleaseRoute(ctx, routeID) })
		}
	}
	if e.profile.HasActiveOperations() && e.initialPower != "on" && len(e.sessions) > 0 {
		_ = e.sessions[0].withClient(ctx, func(c *client.Client) error { return c.SetTrackPower(ctx, false) })
	}
	e.logoutSessions(ctx)
}

func (e *runEngine) logoutSessions(ctx context.Context) {
	for _, session := range e.sessions {
		_ = session.withClient(ctx, func(c *client.Client) error { return c.Logout(ctx) })
	}
}

func normalizedServerURL(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(value, "/"))
	if err != nil {
		return "", fmt.Errorf("parse server URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("server URL must use http or https")
	}
	if parsed.Host == "" {
		return "", errors.New("server URL must include a host")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("server URL must not contain credentials, a query, or a fragment")
	}
	return parsed.String(), nil
}

func profileHash(profile Profile) string {
	data, _ := json.Marshal(profile)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func fixtureHash(fixture Fixture) string {
	if fixture.SchemaVersion == 0 {
		return ""
	}
	data, _ := json.Marshal(fixture)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func executableVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return "dev"
	}
	return info.Main.Version
}

func waitContext(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func waitForWebSockets(ctx context.Context, ready <-chan struct{}, count int, timeout time.Duration) error {
	if count == 0 {
		return nil
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for index := 0; index < count; index++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return fmt.Errorf("only %d of %d WebSocket clients became ready", index, count)
		case <-ready:
		}
	}
	return nil
}

func waitForExpectations(ctx context.Context, expectations *expectationTracker, timeout time.Duration) {
	if expectations.Pending() == 0 {
		return
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			return
		case <-ticker.C:
			if expectations.Pending() == 0 {
				return
			}
		}
	}
}

func isJSONError(err error) bool {
	if err == nil {
		return false
	}
	var syntax *json.SyntaxError
	var typeError *json.UnmarshalTypeError
	return errors.As(err, &syntax) || errors.As(err, &typeError)
}

func hasHTTPStatus(err error, statuses ...int) bool {
	var httpError *client.HTTPError
	if !errors.As(err, &httpError) {
		return false
	}
	for _, status := range statuses {
		if httpError.StatusCode == status {
			return true
		}
	}
	return false
}
