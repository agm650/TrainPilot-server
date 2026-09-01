package api

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/auth"
	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/service"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/station/simulator"
	"github.com/agm650/TrainPilot-server/internal/store"
	"github.com/agm650/TrainPilot-server/internal/transfer"
)

type websocketFixture struct {
	api         *Server
	server      *httptest.Server
	control     *service.ControlService
	railway     *service.RailwayService
	bus         *events.Bus
	user        model.User
	session     model.Session
	lease       model.ControlLease
	accessToken string
	locomotives []model.Locomotive
	clock       *clock.Fake
}

func newWebsocketFixture(t *testing.T) websocketFixture {
	return newWebsocketFixtureWithAccessTTL(t, 15*time.Minute)
}

func newWebsocketFixtureWithAccessTTL(t *testing.T, accessTTL time.Duration) websocketFixture {
	return newWebsocketFixtureWithStation(t, accessTTL, nil)
}

func newWebsocketFixtureWithStation(t *testing.T, accessTTL time.Duration, wrap func(*simulator.Simulator) station.CommandStation) websocketFixture {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = db.Close()
	})
	if err := db.SeedDemo(ctx); err != nil {
		t.Fatal(err)
	}

	authClock := clock.Real{}
	users := service.NewUserServiceWithPasswordParams(db, authClock, auth.PasswordParams{Iterations: 100_000, SaltLength: 16, KeyLength: 32})
	user, err := users.Create(ctx, "alice", "Alice", "correct-horse-1", model.RoleDriver, false, false)
	if err != nil {
		t.Fatal(err)
	}
	authSvc := service.NewAuthService(db, users, authClock, accessTTL, time.Hour)
	pair, err := authSvc.Login(ctx, "alice", "correct-horse-1", "snapshot-test", "Snapshot test", "test")
	if err != nil {
		t.Fatal(err)
	}
	_, session, err := authSvc.Authenticate(ctx, pair.AccessToken)
	if err != nil {
		t.Fatal(err)
	}

	sim := simulator.New()
	if err := sim.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	var commandStation station.CommandStation = sim
	if wrap != nil {
		commandStation = wrap(sim)
	}
	bus := events.New()
	railway := service.NewRailwayService(db, commandStation, bus)
	railway.StartFeedback(ctx)
	controlClock := clock.NewFake(time.Now().UTC())
	control := service.NewControlService(db, commandStation, bus, controlClock, 15*time.Second, time.Second, time.Hour)
	routes := service.NewRouteService(db, railway, bus)
	server := New(authSvc, control, railway, routes, transfer.New(db, bus, authClock), db, bus, commandStation, sim, true)

	locomotives, err := railway.Locomotives(ctx)
	if err != nil || len(locomotives) == 0 {
		t.Fatalf("locomotives=%d err=%v", len(locomotives), err)
	}
	lease, err := control.Acquire(ctx, user, session, locomotives[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)
	return websocketFixture{api: server, server: httpServer, control: control, railway: railway, bus: bus, user: user, session: session, lease: lease, accessToken: pair.AccessToken, locomotives: locomotives, clock: controlClock}
}

func TestTurnoutCommandWebSocketFlowAndReconnectSnapshot(t *testing.T) {
	fixture := newWebsocketFixture(t)
	ctx := context.Background()
	turnout, err := fixture.api.store.GetTurnout(ctx, "turnout-1")
	if err != nil {
		t.Fatal(err)
	}
	client := dialTestWebSocket(t, fixture.server.URL, fixture.accessToken)
	defer client.close()
	readTestSnapshot(t, client)

	done := make(chan error, 1)
	go func() {
		done <- fixture.railway.SetTurnout(ctx, model.User{Role: model.RoleDispatcher}, turnout.ID, "diverging")
	}()
	var commanded struct {
		Type    string `json:"type"`
		Payload struct {
			TurnoutID      string `json:"turnoutId"`
			TargetPosition string `json:"targetPosition"`
		} `json:"payload"`
	}
	client.readJSON(t, &commanded)
	if commanded.Type != "turnout.commanded" || commanded.Payload.TurnoutID != turnout.ID || commanded.Payload.TargetPosition != "diverging" {
		t.Fatalf("commanded event=%+v", commanded)
	}
	var pending, completed struct {
		Type    string `json:"type"`
		Payload struct {
			TurnoutID        string                         `json:"turnoutId"`
			DesiredPosition  string                         `json:"desiredPosition"`
			ReportedPosition string                         `json:"reportedPosition"`
			Pending          bool                           `json:"pending"`
			ReportQuality    station.AccessoryReportQuality `json:"reportQuality"`
		} `json:"payload"`
	}
	client.readJSON(t, &pending)
	client.readJSON(t, &completed)
	if pending.Type != "turnout.state.changed" || !pending.Payload.Pending || pending.Payload.DesiredPosition != "diverging" || pending.Payload.ReportQuality == "" {
		t.Fatalf("pending event=%+v", pending)
	}
	if completed.Type != "turnout.state.changed" || completed.Payload.Pending || completed.Payload.ReportedPosition != "diverging" || completed.Payload.ReportQuality == "" {
		t.Fatalf("completed event=%+v", completed)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	sim := fixture.api.simulator
	if sim == nil {
		t.Fatal("fixture does not expose simulator")
	}
	if err := sim.SetAccessoryBehavior(turnout.Endpoints[0].LinearAddress, simulator.AccessoryBehavior{Mode: simulator.AccessoryBehaviorNoConfirmation}); err != nil {
		t.Fatal(err)
	}
	commandCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = fixture.railway.SetTurnout(commandCtx, model.User{Role: model.RoleDispatcher}, turnout.ID, "straight")
	}()
	deadline := time.Now().Add(time.Second)
	for {
		current, err := fixture.api.store.GetTurnout(ctx, turnout.ID)
		if err == nil && current.Pending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("turnout did not enter pending state")
		}
		time.Sleep(time.Millisecond)
	}
	reconnected := dialTestWebSocket(t, fixture.server.URL, fixture.accessToken)
	defer reconnected.close()
	snapshot := readTestSnapshot(t, reconnected)
	for _, current := range snapshot.Payload.Turnouts {
		if current.ID == turnout.ID {
			if !current.Pending || current.DesiredPosition != "straight" || len(current.Positions) != 2 || current.Quality == "" {
				t.Fatalf("pending reconnect snapshot turnout=%+v", current)
			}
			cancel()
			return
		}
	}
	t.Fatal("turnout missing from reconnect snapshot")
}

type snapshotBlockingStation struct {
	*simulator.Simulator
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (s *snapshotBlockingStation) Status(ctx context.Context) (station.Status, error) {
	blocked := false
	s.once.Do(func() {
		blocked = true
		close(s.entered)
	})
	if blocked {
		select {
		case <-s.release:
		case <-ctx.Done():
			return station.Status{}, ctx.Err()
		}
	}
	return s.Simulator.Status(ctx)
}

func TestSystemSnapshotContainsCompleteClientState(t *testing.T) {
	ctx := context.Background()
	fixture := newWebsocketFixture(t)
	published := fixture.bus.Publish("test.snapshot.barrier", map[string]any{"ready": true})

	snapshot, err := fixture.api.buildSystemSnapshot(ctx, fixture.session)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Type != "system.snapshot" || snapshot.Sequence != published.Sequence || snapshot.CapturedAt.IsZero() {
		t.Fatalf("snapshot metadata=%+v", snapshot)
	}
	if snapshot.Payload.Station.Driver != "simulator" || snapshot.Payload.StationStatus.TrackPower != "off" {
		t.Fatalf("station snapshot=%+v %+v", snapshot.Payload.Station, snapshot.Payload.StationStatus)
	}
	if len(snapshot.Payload.Locomotives) != len(fixture.locomotives) || len(snapshot.Payload.Blocks) == 0 || len(snapshot.Payload.Turnouts) == 0 || len(snapshot.Payload.Routes) == 0 {
		t.Fatalf("incomplete snapshot=%+v", snapshot.Payload)
	}
	turnout := snapshot.Payload.Turnouts[0]
	if turnout.Kind != model.TurnoutKindSimple || len(turnout.Endpoints) != 1 || len(turnout.Positions) != 2 || turnout.DesiredPosition != "straight" || turnout.ReportedPosition != "straight" {
		t.Fatalf("turnout model=%+v", turnout)
	}
	if len(snapshot.Payload.ControlLeases) != 1 || snapshot.Payload.ControlLeases[0].ID != fixture.lease.ID || snapshot.Payload.ControlLeases[0].HeartbeatMillis <= 0 {
		t.Fatalf("leases=%+v", snapshot.Payload.ControlLeases)
	}
	if len(snapshot.Payload.LocomotiveControlStates) != 1 || snapshot.Payload.LocomotiveControlStates[0].LocomotiveID != fixture.lease.LocomotiveID || snapshot.Payload.LocomotiveControlStates[0].Ownership != model.ControlOwnershipMine {
		t.Fatalf("locomotive control states=%+v", snapshot.Payload.LocomotiveControlStates)
	}
}

func createWebsocketTestSession(t *testing.T, db *store.Store, now time.Time, userID, sessionID string) model.Session {
	t.Helper()
	session := model.Session{
		ID:            sessionID,
		UserID:        userID,
		ClientID:      "client-" + sessionID,
		AccessHash:    "access-" + sessionID,
		RefreshHash:   "refresh-" + sessionID,
		AccessExpiry:  now.Add(time.Hour),
		RefreshExpiry: now.Add(2 * time.Hour),
		CreatedAt:     now,
		LastSeenAt:    now,
	}
	if err := db.CreateSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	return session
}

func TestSystemSnapshotExposesControlAvailabilityWithoutOtherLeaseDetails(t *testing.T) {
	ctx := context.Background()
	fixture := newWebsocketFixture(t)
	now := fixture.clock.Now()
	sessionB := createWebsocketTestSession(t, fixture.api.store, now, fixture.user.ID, "session-b")
	otherUser := model.User{ID: "user-b", Username: "bob", Role: model.RoleDriver, Enabled: true, CreatedAt: now, UpdatedAt: now}
	if err := fixture.api.store.CreateUser(ctx, otherUser, "test-hash"); err != nil {
		t.Fatal(err)
	}
	sessionC := createWebsocketTestSession(t, fixture.api.store, now, otherUser.ID, "session-c")
	for _, locomotive := range []model.Locomotive{
		{ID: "loco-c", Name: "Loco C", DCCAddress: 3, AddressKind: "short", SpeedSteps: 128},
		{ID: "loco-free", Name: "Loco libre", DCCAddress: 4, AddressKind: "short", SpeedSteps: 128},
	} {
		if err := fixture.api.store.CreateLocomotive(ctx, locomotive); err != nil {
			t.Fatal(err)
		}
	}
	leaseB, err := fixture.control.Acquire(ctx, fixture.user, sessionB, fixture.locomotives[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	leaseC, err := fixture.control.Acquire(ctx, otherUser, sessionC, "loco-c")
	if err != nil {
		t.Fatal(err)
	}

	snapshot, err := fixture.api.buildSystemSnapshot(ctx, fixture.session)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Payload.ControlLeases) != 1 || snapshot.Payload.ControlLeases[0].ID != fixture.lease.ID {
		t.Fatalf("session-private leases=%+v", snapshot.Payload.ControlLeases)
	}
	states := make(map[string]model.LocomotiveControlState, len(snapshot.Payload.LocomotiveControlStates))
	for _, state := range snapshot.Payload.LocomotiveControlStates {
		states[state.LocomotiveID] = state
	}
	wantOwnership := map[string]model.ControlOwnership{
		fixture.lease.LocomotiveID: model.ControlOwnershipMine,
		leaseB.LocomotiveID:        model.ControlOwnershipSameUserOtherSession,
		leaseC.LocomotiveID:        model.ControlOwnershipOther,
	}
	if len(states) != len(wantOwnership) {
		t.Fatalf("control states=%+v", snapshot.Payload.LocomotiveControlStates)
	}
	for locomotiveID, ownership := range wantOwnership {
		state, ok := states[locomotiveID]
		if !ok || state.State != model.LeaseActive || state.Ownership != ownership || state.ExpiresAt == nil {
			t.Fatalf("state for %s=%+v", locomotiveID, state)
		}
	}
	if _, ok := states["loco-free"]; ok {
		t.Fatal("free locomotive has a control-state entry")
	}
	encoded, err := json.Marshal(snapshot.Payload)
	if err != nil {
		t.Fatal(err)
	}
	for _, privateValue := range []string{leaseB.ID, leaseB.SessionID, leaseC.ID, leaseC.SessionID, leaseC.UserID} {
		if bytes.Contains(encoded, []byte(privateValue)) {
			t.Fatalf("snapshot exposes another lease detail %q: %s", privateValue, encoded)
		}
	}
}

func TestLeaseLifecycleEventsReachOtherWebSocketSessions(t *testing.T) {
	ctx := context.Background()
	fixture := newWebsocketFixture(t)
	pairB, err := fixture.api.auth.Login(ctx, "alice", "correct-horse-1", "events-b", "Events B", "test")
	if err != nil {
		t.Fatal(err)
	}

	clientA := dialTestWebSocket(t, fixture.server.URL, fixture.accessToken)
	defer clientA.close()
	readTestSnapshot(t, clientA)
	clientB := dialTestWebSocket(t, fixture.server.URL, pairB.AccessToken)
	defer clientB.close()
	readTestSnapshot(t, clientB)

	lease, err := fixture.control.Acquire(ctx, fixture.user, fixture.session, fixture.locomotives[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	var acquired struct {
		Type    string `json:"type"`
		Payload struct {
			LocomotiveID string `json:"locomotiveId"`
		} `json:"payload"`
	}
	clientB.readJSON(t, &acquired)
	if acquired.Type != "locomotive.control.acquired" || acquired.Payload.LocomotiveID != lease.LocomotiveID {
		t.Fatalf("acquired event=%+v", acquired)
	}

	if err := fixture.control.Release(ctx, lease.ID, fixture.session); err != nil {
		t.Fatal(err)
	}
	var stopping struct {
		Type    string `json:"type"`
		Payload struct {
			LocomotiveID string    `json:"locomotiveId"`
			ReleaseAfter time.Time `json:"releaseAfter"`
		} `json:"payload"`
	}
	clientB.readJSON(t, &stopping)
	if stopping.Type != "locomotive.control.expired" || stopping.Payload.LocomotiveID != lease.LocomotiveID || stopping.Payload.ReleaseAfter.IsZero() {
		t.Fatalf("stopping event=%+v", stopping)
	}

	fixture.clock.Advance(2 * time.Second)
	fixture.control.Sweep(ctx)
	var released struct {
		Type    string `json:"type"`
		Payload struct {
			LocomotiveID string `json:"locomotiveId"`
		} `json:"payload"`
	}
	clientB.readJSON(t, &released)
	if released.Type != "locomotive.control.released" || released.Payload.LocomotiveID != lease.LocomotiveID {
		t.Fatalf("released event=%+v", released)
	}
}

func TestTakeoverEventReachesAllSessionsAndSnapshotsReflectOwnership(t *testing.T) {
	ctx := context.Background()
	fixture := newWebsocketFixture(t)
	pair2, err := fixture.api.auth.Login(ctx, "alice", "correct-horse-1", "takeover-2", "Takeover 2", "test")
	if err != nil {
		t.Fatal(err)
	}
	user2, session2, err := fixture.api.auth.Authenticate(ctx, pair2.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	passwordHash, err := auth.HashPassword("correct-horse-2", auth.PasswordParams{Iterations: 100_000, SaltLength: 16, KeyLength: 32})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	otherUser := model.User{ID: "user-b", Username: "bob", Role: model.RoleDriver, Enabled: true, CreatedAt: now, UpdatedAt: now}
	if err := fixture.api.store.CreateUser(ctx, otherUser, passwordHash); err != nil {
		t.Fatal(err)
	}
	pair3, err := fixture.api.auth.Login(ctx, "bob", "correct-horse-2", "takeover-3", "Takeover 3", "test")
	if err != nil {
		t.Fatal(err)
	}
	_, session3, err := fixture.api.auth.Authenticate(ctx, pair3.AccessToken)
	if err != nil {
		t.Fatal(err)
	}

	clients := []*testWebSocket{
		dialTestWebSocket(t, fixture.server.URL, fixture.accessToken),
		dialTestWebSocket(t, fixture.server.URL, pair2.AccessToken),
		dialTestWebSocket(t, fixture.server.URL, pair3.AccessToken),
	}
	for _, websocketClient := range clients {
		defer websocketClient.close()
		readTestSnapshot(t, websocketClient)
	}

	transferred, err := fixture.control.TakeoverLease(ctx, user2, session2, fixture.lease.ID)
	if err != nil {
		t.Fatal(err)
	}
	for index, websocketClient := range clients {
		var event struct {
			Type    string                             `json:"type"`
			Payload model.LocomotiveControlTransferred `json:"payload"`
		}
		websocketClient.readJSON(t, &event)
		if event.Type != "locomotive.control.transferred" || event.Payload.LeaseID != fixture.lease.ID || event.Payload.LocomotiveID != fixture.lease.LocomotiveID || event.Payload.UserID != fixture.user.ID || event.Payload.FromSessionID != fixture.session.ID || event.Payload.ToSessionID != session2.ID || event.Payload.State != model.LeaseActive || event.Payload.RenewedAt.IsZero() || event.Payload.ExpiresAt.IsZero() {
			t.Fatalf("client %d takeover event=%+v", index+1, event)
		}
	}

	for _, tc := range []struct {
		name      string
		session   model.Session
		ownership model.ControlOwnership
		ownsLease bool
	}{
		{"old session", fixture.session, model.ControlOwnershipSameUserOtherSession, false},
		{"new session", session2, model.ControlOwnershipMine, true},
		{"other user", session3, model.ControlOwnershipOther, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot, err := fixture.api.buildSystemSnapshot(ctx, tc.session)
			if err != nil {
				t.Fatal(err)
			}
			if got := len(snapshot.Payload.ControlLeases); (got == 1) != tc.ownsLease {
				t.Fatalf("control leases=%+v", snapshot.Payload.ControlLeases)
			}
			if tc.ownsLease && snapshot.Payload.ControlLeases[0].ID != transferred.ID {
				t.Fatalf("control leases=%+v", snapshot.Payload.ControlLeases)
			}
			for _, state := range snapshot.Payload.LocomotiveControlStates {
				if state.LocomotiveID == transferred.LocomotiveID {
					if state.Ownership != tc.ownership || state.State != model.LeaseActive {
						t.Fatalf("control state=%+v", state)
					}
					return
				}
			}
			t.Fatal("transferred locomotive state is missing")
		})
	}
}

func TestEventSequenceFilterRejectsOldAndDuplicateEvents(t *testing.T) {
	for _, tc := range []struct {
		name     string
		event    uint64
		last     uint64
		expected bool
	}{
		{"old", 40, 42, false},
		{"duplicate", 42, 42, false},
		{"next", 43, 42, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := eventFollowsSequence(events.Event{Sequence: tc.event}, tc.last); got != tc.expected {
				t.Fatalf("event=%d last=%d got=%t want=%t", tc.event, tc.last, got, tc.expected)
			}
		})
	}
}

func TestEventPublishedDuringSnapshotIsDeliveredAfterSnapshot(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	fixture := newWebsocketFixtureWithStation(t, 15*time.Minute, func(sim *simulator.Simulator) station.CommandStation {
		return &snapshotBlockingStation{Simulator: sim, entered: entered, release: release}
	})
	client := dialTestWebSocket(t, fixture.server.URL, fixture.accessToken)
	defer client.close()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("snapshot did not reach the station-state read")
	}
	published := fixture.bus.Publish("block.occupancy.changed", map[string]any{"blockId": "block-a", "occupied": true})
	close(release)

	snapshot := readTestSnapshot(t, client)
	if published.Sequence <= snapshot.Sequence {
		t.Fatalf("concurrent event sequence=%d snapshot=%d", published.Sequence, snapshot.Sequence)
	}
	var event events.Event
	client.readJSON(t, &event)
	if event.Type != published.Type || event.Sequence != published.Sequence {
		t.Fatalf("event after snapshot=%+v want=%+v", event, published)
	}
}

func TestWebSocketSnapshotRequestAndReconnect(t *testing.T) {
	fixture := newWebsocketFixture(t)
	client := dialTestWebSocket(t, fixture.server.URL, fixture.accessToken)
	initial := readTestSnapshot(t, client)
	if len(initial.Payload.Locomotives) == 0 || len(initial.Payload.Blocks) == 0 {
		t.Fatalf("initial snapshot is incomplete: %+v", initial.Payload)
	}

	first := fixture.bus.Publish("block.occupancy.changed", map[string]any{"blockId": "block-a", "occupied": true})
	var event struct {
		Type     string `json:"type"`
		Sequence uint64 `json:"sequence"`
	}
	client.readJSON(t, &event)
	if event.Type != first.Type || event.Sequence != first.Sequence || event.Sequence <= initial.Sequence {
		t.Fatalf("event=%+v initial sequence=%d", event, initial.Sequence)
	}

	// A client that detects a gap asks for a new complete snapshot. lastSequence
	// is diagnostic; the server returns current authoritative state.
	fixture.bus.Publish("test.event.ignored-by-client", map[string]any{"ignored": true})
	client.readJSON(t, &event)
	client.writeJSON(t, map[string]any{"type": "client.snapshot_request", "lastSequence": first.Sequence})
	resynchronized := readTestSnapshot(t, client)
	if resynchronized.Sequence < event.Sequence || len(resynchronized.Payload.Routes) == 0 {
		t.Fatalf("resynchronized snapshot=%+v after event=%+v", resynchronized, event)
	}
	client.close()

	reconnected := dialTestWebSocket(t, fixture.server.URL, fixture.accessToken)
	defer reconnected.close()
	afterReconnect := readTestSnapshot(t, reconnected)
	if afterReconnect.Sequence < resynchronized.Sequence || len(afterReconnect.Payload.Turnouts) == 0 {
		t.Fatalf("reconnect snapshot=%+v", afterReconnect)
	}
}

func TestWebSocketDisconnectKeepsLeaseUntilExplicitReleaseOrTimeout(t *testing.T) {
	fixture := newWebsocketFixture(t)
	client := dialTestWebSocket(t, fixture.server.URL, fixture.accessToken)
	readTestSnapshot(t, client)
	client.close()

	leases, err := fixture.control.LeasesForSession(context.Background(), fixture.session)
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 1 || leases[0].ID != fixture.lease.ID || leases[0].State != model.LeaseActive {
		t.Fatalf("lease changed after websocket disconnect: %+v", leases)
	}
}

func TestWebSocketClosesAtAccessTokenExpiry(t *testing.T) {
	fixture := newWebsocketFixtureWithAccessTTL(t, 500*time.Millisecond)
	client := dialTestWebSocket(t, fixture.server.URL, fixture.accessToken)
	defer client.close()
	readTestSnapshot(t, client)
	_ = client.conn.SetReadDeadline(time.Now().Add(time.Second))
	first, err := client.reader.ReadByte()
	if err != nil {
		t.Fatalf("waiting for websocket close: %v", err)
	}
	if first&0x0f != 0x8 {
		t.Fatalf("websocket opcode=%d want close", first&0x0f)
	}
}

func TestWebSocketClosesAfterSessionRevocation(t *testing.T) {
	fixture := newWebsocketFixture(t)
	client := dialTestWebSocket(t, fixture.server.URL, fixture.accessToken)
	defer client.close()
	readTestSnapshot(t, client)
	if err := fixture.api.auth.Logout(context.Background(), fixture.session.ID); err != nil {
		t.Fatal(err)
	}
	_ = client.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	first, err := client.reader.ReadByte()
	if err != nil {
		t.Fatalf("waiting for websocket close after logout: %v", err)
	}
	if first&0x0f != 0x8 {
		t.Fatalf("websocket opcode=%d want close", first&0x0f)
	}
}

func TestSlowWebSocketClientIsDisconnectedOnOverflow(t *testing.T) {
	fixture := newWebsocketFixture(t)
	fixture.api.eventBuffer = 1
	fixture.api.eventWriteTimeout = 200 * time.Millisecond
	client := dialTestWebSocket(t, fixture.server.URL, fixture.accessToken)
	defer client.close()
	readTestSnapshot(t, client)

	payload := map[string]any{"data": strings.Repeat("x", 700<<10)}
	for i := 0; i < 100; i++ {
		fixture.bus.Publish("test.slow-client", payload)
	}
	if err := client.waitForClosure(3 * time.Second); err != nil {
		t.Fatal(err)
	}
}

type testWebSocket struct {
	conn   net.Conn
	reader *bufio.Reader
}

func dialTestWebSocket(t *testing.T, baseURL, accessToken string) *testWebSocket {
	t.Helper()
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", parsed.Host, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		t.Fatal(err)
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)
	request := fmt.Sprintf("GET /api/v1/events HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", parsed.Host, accessToken, key)
	if _, err := io.WriteString(conn, request); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		conn.Close()
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		conn.Close()
		t.Fatalf("websocket upgrade status=%d", response.StatusCode)
	}
	return &testWebSocket{conn: conn, reader: reader}
}

func readTestSnapshot(t *testing.T, client *testWebSocket) systemSnapshot {
	t.Helper()
	var snapshot systemSnapshot
	client.readJSON(t, &snapshot)
	if snapshot.Type != "system.snapshot" {
		t.Fatalf("message type=%q want system.snapshot", snapshot.Type)
	}
	return snapshot
}

func (c *testWebSocket) readJSON(t *testing.T, target any) {
	t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	first, err := c.reader.ReadByte()
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.reader.ReadByte()
	if err != nil {
		t.Fatal(err)
	}
	if first&0x0f != 0x1 || second&0x80 != 0 {
		t.Fatalf("unexpected websocket frame header %x %x", first, second)
	}
	length := uint64(second & 0x7f)
	if length == 126 {
		var size [2]byte
		if _, err := io.ReadFull(c.reader, size[:]); err != nil {
			t.Fatal(err)
		}
		length = uint64(binary.BigEndian.Uint16(size[:]))
	} else if length == 127 {
		var size [8]byte
		if _, err := io.ReadFull(c.reader, size[:]); err != nil {
			t.Fatal(err)
		}
		length = binary.BigEndian.Uint64(size[:])
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(c.reader, payload); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(payload, target); err != nil {
		t.Fatalf("decode websocket payload %s: %v", payload, err)
	}
}

func (c *testWebSocket) writeJSON(t *testing.T, value any) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var frame bytes.Buffer
	frame.WriteByte(0x81)
	length := len(payload)
	switch {
	case length < 126:
		frame.WriteByte(0x80 | byte(length))
	case length <= 65535:
		frame.WriteByte(0x80 | 126)
		var size [2]byte
		binary.BigEndian.PutUint16(size[:], uint16(length))
		frame.Write(size[:])
	default:
		t.Fatalf("test websocket payload too large: %d", length)
	}
	mask := [4]byte{0x12, 0x34, 0x56, 0x78}
	frame.Write(mask[:])
	for i, b := range payload {
		frame.WriteByte(b ^ mask[i%len(mask)])
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.conn.Write(frame.Bytes()); err != nil {
		t.Fatal(err)
	}
}

func (c *testWebSocket) waitForClosure(timeout time.Duration) error {
	_ = c.conn.SetReadDeadline(time.Now().Add(timeout))
	for {
		first, err := c.reader.ReadByte()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("waiting for websocket close: %w", err)
		}
		second, err := c.reader.ReadByte()
		if err != nil {
			return fmt.Errorf("reading websocket close header: %w", err)
		}
		length := uint64(second & 0x7f)
		switch length {
		case 126:
			var size [2]byte
			if _, err := io.ReadFull(c.reader, size[:]); err != nil {
				return err
			}
			length = uint64(binary.BigEndian.Uint16(size[:]))
		case 127:
			var size [8]byte
			if _, err := io.ReadFull(c.reader, size[:]); err != nil {
				return err
			}
			length = binary.BigEndian.Uint64(size[:])
		}
		if _, err := io.CopyN(io.Discard, c.reader, int64(length)); err != nil {
			return fmt.Errorf("discarding websocket frame: %w", err)
		}
		if first&0x0f == 0x8 {
			return nil
		}
	}
}

func (c *testWebSocket) close() {
	_ = c.conn.Close()
}
