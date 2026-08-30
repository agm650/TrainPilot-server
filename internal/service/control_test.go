package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/station/simulator"
	"github.com/agm650/TrainPilot-server/internal/store"
)

func newControlFixture(t *testing.T) (*ControlService, *store.Store, *simulator.Simulator, *clock.Fake, model.User, model.Session) {
	t.Helper()
	ctx := context.Background()
	sim := simulator.New()
	if err := sim.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	control, db, clk, user, sess := newControlFixtureWithStation(t, sim)
	return control, db, sim, clk, user, sess
}

func newControlFixtureWithStation(t *testing.T, commandStation station.CommandStation) (*ControlService, *store.Store, *clock.Fake, model.User, model.Session) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.SeedDemo(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 29, 20, 0, 0, 0, time.UTC)
	clk := clock.NewFake(now)
	user := model.User{ID: "user-1", Username: "alice", Role: model.RoleDriver, Enabled: true, CreatedAt: now, UpdatedAt: now}
	if err := db.CreateUser(ctx, user, "test-hash"); err != nil {
		t.Fatal(err)
	}
	sess := model.Session{ID: "session-1", UserID: user.ID, ClientID: "test", AccessHash: "a", RefreshHash: "r", AccessExpiry: now.Add(time.Hour), RefreshExpiry: now.Add(time.Hour), CreatedAt: now, LastSeenAt: now}
	if err := db.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	control := NewControlService(db, commandStation, events.New(), clk, 15*time.Second, 2*time.Second, time.Hour)
	return control, db, clk, user, sess
}

func TestLeaseExpirationStopsBeforeRelease(t *testing.T) {
	ctx := context.Background()
	control, db, sim, clk, user, sess := newControlFixture(t)
	if err := control.SetTrackPower(ctx, user, true); err != nil {
		t.Fatal(err)
	}
	lease, err := control.Acquire(ctx, user, sess, "loco-bb26001")
	if err != nil {
		t.Fatal(err)
	}
	if err := control.Throttle(ctx, user, sess, "loco-bb26001", lease.ID, 40, station.Forward); err != nil {
		t.Fatal(err)
	}
	if got := sim.Loco(2601).Speed; got != 0.4 {
		t.Fatalf("speed=%v want 0.4", got)
	}
	clk.Advance(16 * time.Second)
	control.Sweep(ctx)
	stored, err := db.GetLease(ctx, lease.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != model.LeaseStopping {
		t.Fatalf("state=%s want stopping", stored.State)
	}
	if got := sim.Loco(2601).Speed; got != 0 {
		t.Fatalf("speed=%v want 0", got)
	}
	if _, err := control.Acquire(ctx, user, sess, "loco-bb26001"); err == nil {
		t.Fatal("acquisition succeeded while stopping")
	}
	clk.Advance(3 * time.Second)
	control.Sweep(ctx)
	stored, err = db.GetLease(ctx, lease.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != model.LeaseReleased {
		t.Fatalf("state=%s want released", stored.State)
	}
	if _, err := control.Acquire(ctx, user, sess, "loco-bb26001"); err != nil {
		t.Fatalf("acquisition after release: %v", err)
	}
}

func TestConcurrentLeaseAcquisitionAllowsOneWinner(t *testing.T) {
	ctx := context.Background()
	control, _, _, _, user, sess := newControlFixture(t)
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := control.Acquire(ctx, user, sess, "loco-bb26001")
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	success := 0
	failure := 0
	for err := range results {
		if err == nil {
			success++
		} else {
			failure++
		}
	}
	if success != 1 || failure != 1 {
		t.Fatalf("success=%d failure=%d", success, failure)
	}
}

func createAdditionalControlSession(t *testing.T, db *store.Store, now time.Time, user model.User, sessionID string) model.Session {
	t.Helper()
	session := model.Session{
		ID:            sessionID,
		UserID:        user.ID,
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

func TestLocomotiveControlStatesClassifiesOwnershipAndStopping(t *testing.T) {
	ctx := context.Background()
	control, db, _, clk, user, sessionA := newControlFixture(t)
	sessionB := createAdditionalControlSession(t, db, clk.Now(), user, "session-2")
	otherUser := model.User{ID: "user-2", Username: "bob", Role: model.RoleDriver, Enabled: true, CreatedAt: clk.Now(), UpdatedAt: clk.Now()}
	if err := db.CreateUser(ctx, otherUser, "test-hash"); err != nil {
		t.Fatal(err)
	}
	sessionC := createAdditionalControlSession(t, db, clk.Now(), otherUser, "session-3")
	for _, locomotive := range []model.Locomotive{
		{ID: "loco-c", Name: "Loco C", DCCAddress: 3, AddressKind: "short", SpeedSteps: 128},
		{ID: "loco-free", Name: "Loco libre", DCCAddress: 4, AddressKind: "short", SpeedSteps: 128},
	} {
		if err := db.CreateLocomotive(ctx, locomotive); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := control.Acquire(ctx, user, sessionA, "loco-bb26001"); err != nil {
		t.Fatal(err)
	}
	leaseB, err := control.Acquire(ctx, user, sessionB, "loco-cc72030")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Acquire(ctx, otherUser, sessionC, "loco-c"); err != nil {
		t.Fatal(err)
	}

	states, err := control.LocomotiveControlStates(ctx, sessionA)
	if err != nil {
		t.Fatal(err)
	}
	byLocomotive := make(map[string]model.LocomotiveControlState, len(states))
	for _, state := range states {
		byLocomotive[state.LocomotiveID] = state
	}
	if got := byLocomotive["loco-bb26001"]; got.Ownership != model.ControlOwnershipMine || got.State != model.LeaseActive || got.ExpiresAt == nil {
		t.Fatalf("mine state=%+v", got)
	}
	if got := byLocomotive["loco-cc72030"]; got.Ownership != model.ControlOwnershipSameUserOtherSession || got.State != model.LeaseActive {
		t.Fatalf("same-user state=%+v", got)
	}
	if got := byLocomotive["loco-c"]; got.Ownership != model.ControlOwnershipOther || got.State != model.LeaseActive {
		t.Fatalf("other-user state=%+v", got)
	}
	if _, ok := byLocomotive["loco-free"]; ok {
		t.Fatal("free locomotive has a control-state entry")
	}

	if err := control.Release(ctx, leaseB.ID, sessionB); err != nil {
		t.Fatal(err)
	}
	states, err = control.LocomotiveControlStates(ctx, sessionA)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range states {
		if state.LocomotiveID == "loco-cc72030" {
			if state.State != model.LeaseStopping || state.Ownership != model.ControlOwnershipSameUserOtherSession || state.ReleaseAfter == nil {
				t.Fatalf("stopping state=%+v", state)
			}
			return
		}
	}
	t.Fatal("stopping locomotive state is missing")
}

func TestLeaseAcquisitionRemainsExclusiveAcrossSessionsAndUsers(t *testing.T) {
	ctx := context.Background()
	control, db, _, clk, user, sessionA := newControlFixture(t)
	sessionB := createAdditionalControlSession(t, db, clk.Now(), user, "session-2")
	otherUser := model.User{ID: "user-2", Username: "bob", Role: model.RoleDriver, Enabled: true, CreatedAt: clk.Now(), UpdatedAt: clk.Now()}
	if err := db.CreateUser(ctx, otherUser, "test-hash"); err != nil {
		t.Fatal(err)
	}
	sessionC := createAdditionalControlSession(t, db, clk.Now(), otherUser, "session-3")

	lease, err := control.Acquire(ctx, user, sessionA, "loco-bb26001")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Acquire(ctx, user, sessionB, "loco-bb26001"); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("same-user other-session acquisition error=%v", err)
	}
	if _, err := control.Acquire(ctx, otherUser, sessionC, "loco-bb26001"); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("other-user acquisition error=%v", err)
	}
	if err := control.Release(ctx, lease.ID, sessionA); err != nil {
		t.Fatal(err)
	}
	clk.Advance(3 * time.Second)
	control.Sweep(ctx)
	if _, err := control.Acquire(ctx, user, sessionB, "loco-bb26001"); err != nil {
		t.Fatalf("acquisition after release: %v", err)
	}
}

func TestTrackPowerAndEmergencyStop(t *testing.T) {
	ctx := context.Background()
	control, _, sim, _, user, sess := newControlFixture(t)
	status, err := control.StationStatus(ctx)
	if err != nil || status.TrackPower != "off" {
		t.Fatalf("initial station status=%+v err=%v", status, err)
	}
	viewer := user
	viewer.Role = model.RoleViewer
	if err := control.SetTrackPower(ctx, viewer, true); err == nil {
		t.Fatal("viewer changed track power")
	}
	if err := control.EmergencyStop(ctx, viewer); err == nil {
		t.Fatal("viewer triggered emergency stop")
	}
	if err := control.SetTrackPower(ctx, user, true); err != nil {
		t.Fatal(err)
	}
	status, err = control.StationStatus(ctx)
	if err != nil || status.TrackPower != "on" {
		t.Fatalf("station status=%+v err=%v", status, err)
	}
	lease, err := control.Acquire(ctx, user, sess, "loco-bb26001")
	if err != nil {
		t.Fatal(err)
	}
	if err := control.Throttle(ctx, user, sess, "loco-bb26001", lease.ID, 50, station.Forward); err != nil {
		t.Fatal(err)
	}
	if err := control.EmergencyStop(ctx, user); err != nil {
		t.Fatal(err)
	}
	if got := sim.Loco(2601).Speed; got != 0 {
		t.Fatalf("speed after emergency stop=%v want 0", got)
	}
	if err := control.SetTrackPower(ctx, user, false); err != nil {
		t.Fatal(err)
	}
	status, err = control.StationStatus(ctx)
	if err != nil || status.TrackPower != "off" {
		t.Fatalf("station status=%+v err=%v", status, err)
	}
}

type healthOnlyStation struct {
	station.CommandStation
	health station.Health
}

func (s *healthOnlyStation) Health() station.Health { return s.health }

func TestStationStatusUsesGenericHealthProvider(t *testing.T) {
	sim := simulator.New()
	lastSeen := time.Now().UTC()
	commandStation := &healthOnlyStation{
		CommandStation: sim,
		health:         station.Health{Connectivity: station.Offline, LastSeen: &lastSeen},
	}
	control, _, _, _, _ := newControlFixtureWithStation(t, commandStation)
	status, err := control.StationStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Connectivity != station.Offline || status.LastSeen == nil || !status.LastSeen.Equal(lastSeen) {
		t.Fatalf("station status=%+v", status)
	}
}

func TestThrottleExtendsLeaseFromLastUse(t *testing.T) {
	ctx := context.Background()
	control, db, _, clk, user, sess := newControlFixture(t)
	if err := control.SetTrackPower(ctx, user, true); err != nil {
		t.Fatal(err)
	}
	lease, err := control.Acquire(ctx, user, sess, "loco-bb26001")
	if err != nil {
		t.Fatal(err)
	}
	clk.Advance(10 * time.Second)
	if err := control.Throttle(ctx, user, sess, "loco-bb26001", lease.ID, 40, station.Forward); err != nil {
		t.Fatal(err)
	}
	stored, err := db.GetLease(ctx, lease.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantExpiry := clk.Now().Add(15 * time.Second)
	if !stored.RenewedAt.Equal(clk.Now()) || !stored.ExpiresAt.Equal(wantExpiry) {
		t.Fatalf("renewed=%v expires=%v want renewed=%v expires=%v", stored.RenewedAt, stored.ExpiresAt, clk.Now(), wantExpiry)
	}
	clk.Advance(10 * time.Second)
	control.Sweep(ctx)
	stored, err = db.GetLease(ctx, lease.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != model.LeaseActive {
		t.Fatalf("state=%s want active", stored.State)
	}
}

func TestThrottleCannotReviveExpiredUnsweptLease(t *testing.T) {
	ctx := context.Background()
	control, db, sim, clk, user, sess := newControlFixture(t)
	if err := control.SetTrackPower(ctx, user, true); err != nil {
		t.Fatal(err)
	}
	lease, err := control.Acquire(ctx, user, sess, "loco-bb26001")
	if err != nil {
		t.Fatal(err)
	}
	clk.Advance(15 * time.Second)
	if err := control.Throttle(ctx, user, sess, "loco-bb26001", lease.ID, 40, station.Forward); err == nil {
		t.Fatal("throttle revived an expired lease")
	}
	if got := sim.Loco(2601).Speed; got != 0 {
		t.Fatalf("speed=%v want 0", got)
	}
	stored, err := db.GetLease(ctx, lease.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.ExpiresAt.Equal(lease.ExpiresAt) {
		t.Fatalf("expires=%v want %v", stored.ExpiresAt, lease.ExpiresAt)
	}
}

func TestHeartbeatCannotReviveExpiredUnsweptLease(t *testing.T) {
	ctx := context.Background()
	control, db, _, clk, user, sess := newControlFixture(t)
	lease, err := control.Acquire(ctx, user, sess, "loco-bb26001")
	if err != nil {
		t.Fatal(err)
	}
	clk.Advance(14 * time.Second)
	renewed, err := control.Heartbeat(ctx, lease.ID, sess)
	if err != nil {
		t.Fatalf("heartbeat before expiry: %v", err)
	}
	if !renewed.ExpiresAt.Equal(clk.Now().Add(15 * time.Second)) {
		t.Fatalf("renewed expiry=%v", renewed.ExpiresAt)
	}
	clk.Advance(15 * time.Second)
	if _, err := control.Heartbeat(ctx, lease.ID, sess); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("heartbeat after expiry error=%v", err)
	}
	stored, err := db.GetLease(ctx, lease.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.ExpiresAt.Equal(renewed.ExpiresAt) {
		t.Fatalf("expired lease was modified: expires=%v want=%v", stored.ExpiresAt, renewed.ExpiresAt)
	}
}
