package store

import (
	"context"
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
