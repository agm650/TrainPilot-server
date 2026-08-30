package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/model"
)

func TestLiveLeasesReturnsAllExclusiveStatesUntilSweepTransitionsThem(t *testing.T) {
	ctx := context.Background()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.SeedDemo(ctx); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 8, 30, 15, 0, 0, 0, time.UTC)
	users := []model.User{
		{ID: "user-a", Username: "alice", Role: model.RoleDriver, Enabled: true, CreatedAt: now, UpdatedAt: now},
		{ID: "user-b", Username: "bob", Role: model.RoleDriver, Enabled: true, CreatedAt: now, UpdatedAt: now},
	}
	for _, user := range users {
		if err := db.CreateUser(ctx, user, "test-hash"); err != nil {
			t.Fatal(err)
		}
	}
	sessions := []model.Session{
		{ID: "session-a", UserID: "user-a", ClientID: "client-a", AccessHash: "access-a", RefreshHash: "refresh-a", AccessExpiry: now.Add(time.Hour), RefreshExpiry: now.Add(2 * time.Hour), CreatedAt: now, LastSeenAt: now},
		{ID: "session-b", UserID: "user-a", ClientID: "client-b", AccessHash: "access-b", RefreshHash: "refresh-b", AccessExpiry: now.Add(time.Hour), RefreshExpiry: now.Add(2 * time.Hour), CreatedAt: now, LastSeenAt: now},
		{ID: "session-c", UserID: "user-b", ClientID: "client-c", AccessHash: "access-c", RefreshHash: "refresh-c", AccessExpiry: now.Add(time.Hour), RefreshExpiry: now.Add(2 * time.Hour), CreatedAt: now, LastSeenAt: now},
	}
	for _, session := range sessions {
		if err := db.CreateSession(ctx, session); err != nil {
			t.Fatal(err)
		}
	}
	for _, locomotive := range []model.Locomotive{
		{ID: "loco-c", Name: "Loco C", DCCAddress: 3, AddressKind: "short", SpeedSteps: 128},
		{ID: "loco-d", Name: "Loco D", DCCAddress: 4, AddressKind: "short", SpeedSteps: 128},
	} {
		if err := db.CreateLocomotive(ctx, locomotive); err != nil {
			t.Fatal(err)
		}
	}

	leases := []model.ControlLease{
		{ID: "lease-active-expired", LocomotiveID: "loco-bb26001", UserID: "user-a", SessionID: "session-a", State: model.LeaseActive, AcquiredAt: now.Add(-time.Hour), RenewedAt: now.Add(-time.Hour), ExpiresAt: now.Add(-time.Minute)},
		{ID: "lease-stopping", LocomotiveID: "loco-cc72030", UserID: "user-a", SessionID: "session-b", State: model.LeaseActive, AcquiredAt: now.Add(-30 * time.Minute), RenewedAt: now.Add(-30 * time.Minute), ExpiresAt: now.Add(time.Minute)},
		{ID: "lease-released", LocomotiveID: "loco-c", UserID: "user-b", SessionID: "session-c", State: model.LeaseActive, AcquiredAt: now.Add(-20 * time.Minute), RenewedAt: now.Add(-20 * time.Minute), ExpiresAt: now.Add(time.Minute)},
		{ID: "lease-active-other-user", LocomotiveID: "loco-d", UserID: "user-b", SessionID: "session-c", State: model.LeaseActive, AcquiredAt: now.Add(-10 * time.Minute), RenewedAt: now.Add(-10 * time.Minute), ExpiresAt: now.Add(time.Minute)},
	}
	for _, lease := range leases {
		if err := db.CreateLease(ctx, lease); err != nil {
			t.Fatal(err)
		}
	}
	releaseAfter := now.Add(5 * time.Second)
	if err := db.MarkLeaseStopping(ctx, "lease-stopping", "heartbeat_timeout", releaseAfter); err != nil {
		t.Fatal(err)
	}
	if err := db.ReleaseLease(ctx, "lease-released", "session-c", "test"); err != nil {
		t.Fatal(err)
	}

	live, err := db.LiveLeases(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 3 {
		t.Fatalf("live leases=%+v", live)
	}
	byID := make(map[string]model.ControlLease, len(live))
	for _, lease := range live {
		byID[lease.ID] = lease
	}
	if _, ok := byID["lease-active-expired"]; !ok {
		t.Fatal("expired active lease disappeared before Sweep")
	}
	stopping, ok := byID["lease-stopping"]
	if !ok || stopping.State != model.LeaseStopping || stopping.ReleaseAfter == nil || !stopping.ReleaseAfter.Equal(releaseAfter) {
		t.Fatalf("stopping lease=%+v", stopping)
	}
	if _, ok := byID["lease-active-other-user"]; !ok {
		t.Fatal("live lease from another user/session is missing")
	}
	if _, ok := byID["lease-released"]; ok {
		t.Fatal("released lease returned as live")
	}
}

type transferLeaseFixture struct {
	db       *Store
	now      time.Time
	lease    model.ControlLease
	session1 model.Session
	session2 model.Session
	session3 model.Session
}

func newTransferLeaseFixture(t *testing.T) transferLeaseFixture {
	t.Helper()
	ctx := context.Background()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.SeedDemo(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 30, 16, 0, 0, 0, time.UTC)
	for _, user := range []model.User{
		{ID: "user-a", Username: "alice", Role: model.RoleDriver, Enabled: true, CreatedAt: now, UpdatedAt: now},
		{ID: "user-b", Username: "bob", Role: model.RoleDriver, Enabled: true, CreatedAt: now, UpdatedAt: now},
	} {
		if err := db.CreateUser(ctx, user, "test-hash"); err != nil {
			t.Fatal(err)
		}
	}
	sessions := []model.Session{
		{ID: "session-1", UserID: "user-a", ClientID: "client-1", AccessHash: "access-1", RefreshHash: "refresh-1", AccessExpiry: now.Add(time.Hour), RefreshExpiry: now.Add(2 * time.Hour), CreatedAt: now, LastSeenAt: now},
		{ID: "session-2", UserID: "user-a", ClientID: "client-2", AccessHash: "access-2", RefreshHash: "refresh-2", AccessExpiry: now.Add(time.Hour), RefreshExpiry: now.Add(2 * time.Hour), CreatedAt: now, LastSeenAt: now},
		{ID: "session-3", UserID: "user-b", ClientID: "client-3", AccessHash: "access-3", RefreshHash: "refresh-3", AccessExpiry: now.Add(time.Hour), RefreshExpiry: now.Add(2 * time.Hour), CreatedAt: now, LastSeenAt: now},
	}
	for _, session := range sessions {
		if err := db.CreateSession(ctx, session); err != nil {
			t.Fatal(err)
		}
	}
	lease := model.ControlLease{
		ID:           "lease-1",
		LocomotiveID: "loco-bb26001",
		UserID:       "user-a",
		SessionID:    "session-1",
		State:        model.LeaseActive,
		AcquiredAt:   now.Add(-time.Minute),
		RenewedAt:    now.Add(-time.Minute),
		ExpiresAt:    now.Add(time.Minute),
	}
	if err := db.CreateLease(ctx, lease); err != nil {
		t.Fatal(err)
	}
	return transferLeaseFixture{db: db, now: now, lease: lease, session1: sessions[0], session2: sessions[1], session3: sessions[2]}
}

func TestTransferActiveLease(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		fixture := newTransferLeaseFixture(t)
		renewedAt := fixture.now.Add(time.Second)
		expiresAt := renewedAt.Add(10 * time.Minute)
		transferred, err := fixture.db.TransferActiveLease(context.Background(), fixture.lease.ID, fixture.lease.UserID, fixture.session1.ID, fixture.session2.ID, renewedAt, expiresAt)
		if err != nil {
			t.Fatal(err)
		}
		if transferred.ID != fixture.lease.ID || transferred.SessionID != fixture.session2.ID || transferred.State != model.LeaseActive || !transferred.RenewedAt.Equal(renewedAt) || !transferred.ExpiresAt.Equal(expiresAt) {
			t.Fatalf("transferred lease=%+v", transferred)
		}
		if err := fixture.db.MarkLeaseStoppingForSession(context.Background(), fixture.lease.ID, fixture.session1.ID, "stale_release", renewedAt.Add(time.Second)); !errors.Is(err, ErrNotFound) {
			t.Fatalf("old session changed transferred lease: %v", err)
		}
		stored, err := fixture.db.GetLease(context.Background(), fixture.lease.ID)
		if err != nil || stored.State != model.LeaseActive || stored.SessionID != fixture.session2.ID {
			t.Fatalf("stored lease after stale release=%+v err=%v", stored, err)
		}
	})

	t.Run("other user", func(t *testing.T) {
		fixture := newTransferLeaseFixture(t)
		_, err := fixture.db.TransferActiveLease(context.Background(), fixture.lease.ID, fixture.session3.UserID, fixture.session1.ID, fixture.session3.ID, fixture.now, fixture.now.Add(time.Minute))
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("transfer error=%v", err)
		}
		stored, getErr := fixture.db.GetLease(context.Background(), fixture.lease.ID)
		if getErr != nil || stored.SessionID != fixture.session1.ID {
			t.Fatalf("stored lease=%+v err=%v", stored, getErr)
		}
	})

	for _, state := range []model.LeaseState{model.LeaseStopping, model.LeaseReleased} {
		t.Run(string(state), func(t *testing.T) {
			fixture := newTransferLeaseFixture(t)
			var err error
			if state == model.LeaseStopping {
				err = fixture.db.MarkLeaseStopping(context.Background(), fixture.lease.ID, "test", fixture.now.Add(time.Second))
			} else {
				err = fixture.db.ReleaseLease(context.Background(), fixture.lease.ID, fixture.session1.ID, "test")
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = fixture.db.TransferActiveLease(context.Background(), fixture.lease.ID, fixture.lease.UserID, fixture.session1.ID, fixture.session2.ID, fixture.now, fixture.now.Add(time.Minute))
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("transfer error=%v", err)
			}
		})
	}

	t.Run("stale source session", func(t *testing.T) {
		fixture := newTransferLeaseFixture(t)
		if _, err := fixture.db.TransferActiveLease(context.Background(), fixture.lease.ID, fixture.lease.UserID, fixture.session1.ID, fixture.session2.ID, fixture.now, fixture.now.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.db.TransferActiveLease(context.Background(), fixture.lease.ID, fixture.lease.UserID, fixture.session1.ID, fixture.session3.ID, fixture.now, fixture.now.Add(time.Minute)); !errors.Is(err, ErrNotFound) {
			t.Fatalf("second transfer error=%v", err)
		}
		stored, err := fixture.db.GetLease(context.Background(), fixture.lease.ID)
		if err != nil || stored.SessionID != fixture.session2.ID {
			t.Fatalf("stored lease=%+v err=%v", stored, err)
		}
	})

	t.Run("expired active lease", func(t *testing.T) {
		fixture := newTransferLeaseFixture(t)
		if _, err := fixture.db.DB.ExecContext(context.Background(), `UPDATE control_leases SET expires_at=? WHERE id=?`, timeText(fixture.now), fixture.lease.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.db.TransferActiveLease(context.Background(), fixture.lease.ID, fixture.lease.UserID, fixture.session1.ID, fixture.session2.ID, fixture.now, fixture.now.Add(time.Minute)); !errors.Is(err, ErrNotFound) {
			t.Fatalf("expired transfer error=%v", err)
		}
	})
}
