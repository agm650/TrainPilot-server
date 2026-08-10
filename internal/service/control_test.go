package service

import (
	"context"
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
	sim := simulator.New()
	if err := sim.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	control := NewControlService(db, sim, events.New(), clk, 15*time.Second, 2*time.Second, time.Hour)
	return control, db, sim, clk, user, sess
}

func TestLeaseExpirationStopsBeforeRelease(t *testing.T) {
	ctx := context.Background()
	control, db, sim, clk, user, sess := newControlFixture(t)
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

func TestTrackPowerAndEmergencyStop(t *testing.T) {
	ctx := context.Background()
	control, _, sim, _, user, sess := newControlFixture(t)
	if got := control.TrackPowerStatus().State; got != "unknown" {
		t.Fatalf("initial power state=%q want unknown", got)
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
	if got := control.TrackPowerStatus().State; got != "on" {
		t.Fatalf("power state=%q want on", got)
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
	if got := control.TrackPowerStatus().State; got != "off" {
		t.Fatalf("power state=%q want off", got)
	}
}

func TestThrottleExtendsLeaseFromLastUse(t *testing.T) {
	ctx := context.Background()
	control, db, _, clk, user, sess := newControlFixture(t)
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
