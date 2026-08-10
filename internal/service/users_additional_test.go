package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/auth"
	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/store"
)

func newUserServiceFixture(t *testing.T) (*UserService, *store.Store, *clock.Fake) {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	clk := clock.NewFake(time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC))
	params := auth.PasswordParams{Iterations: 100_000, SaltLength: 16, KeyLength: 32}
	return NewUserServiceWithPasswordParams(db, clk, params), db, clk
}

func TestUserCreateValidationAndBootstrap(t *testing.T) {
	ctx := context.Background()
	users, _, _ := newUserServiceFixture(t)

	for _, username := range []string{"", "ab", "bad name", "012345678901234567890123456789012"} {
		if _, err := users.Create(ctx, username, "", "correct-horse-1", model.RoleDriver, false, false); err == nil {
			t.Fatalf("username %q was accepted", username)
		}
	}
	if _, err := users.Create(ctx, "alice", "", "correct-horse-1", model.Role("unknown"), false, false); err == nil {
		t.Fatal("invalid role was accepted")
	}
	if _, err := users.Create(ctx, "alice", "", "short", model.RoleDriver, false, false); err == nil {
		t.Fatal("short password was accepted")
	}

	created, err := users.Create(ctx, " alice ", "Alice", "correct-horse-1", model.RoleAdministrator, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if created.Username != "alice" || !created.Enabled || !created.MustChangePassword {
		t.Fatalf("created user=%+v", created)
	}
	if _, err := users.Create(ctx, "bob", "Bob", "correct-horse-2", model.RoleDriver, false, true); err == nil {
		t.Fatal("second bootstrap succeeded")
	}
	if _, err := users.Create(ctx, "ALICE", "Duplicate", "correct-horse-3", model.RoleDriver, false, false); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate error=%v", err)
	}

	listed, err := users.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Username != "alice" {
		t.Fatalf("users=%+v", listed)
	}
}

func TestUserMutationsAndVerification(t *testing.T) {
	ctx := context.Background()
	users, db, clk := newUserServiceFixture(t)
	user, err := users.Create(ctx, "alice", "Alice", "correct-horse-1", model.RoleDriver, false, false)
	if err != nil {
		t.Fatal(err)
	}
	sess := model.Session{
		ID: "session-1", UserID: user.ID, ClientID: "client", AccessHash: "access", RefreshHash: "refresh",
		AccessExpiry: clk.Now().Add(time.Hour), RefreshExpiry: clk.Now().Add(time.Hour), CreatedAt: clk.Now(), LastSeenAt: clk.Now(),
	}
	if err := db.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}

	verified, err := users.Verify(ctx, "ALICE", "correct-horse-1")
	if err != nil || verified.ID != user.ID {
		t.Fatalf("verify user=%+v err=%v", verified, err)
	}
	if _, err := users.Verify(ctx, "alice", "wrong-password"); err == nil {
		t.Fatal("wrong password accepted")
	}
	if _, err := users.Verify(ctx, "missing", "correct-horse-1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing user error=%v", err)
	}

	if err := users.SetRole(ctx, "alice", model.Role("invalid")); err == nil {
		t.Fatal("invalid role accepted")
	}
	if err := users.SetRole(ctx, "missing", model.RoleViewer); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing role update error=%v", err)
	}
	if err := users.SetRole(ctx, "alice", model.RoleDispatcher); err != nil {
		t.Fatal(err)
	}
	found, err := db.GetUserByUsername(ctx, "alice")
	if err != nil || found.User.Role != model.RoleDispatcher {
		t.Fatalf("role=%s err=%v", found.User.Role, err)
	}

	if err := users.SetPassword(ctx, "alice", "short", false); err == nil {
		t.Fatal("short password accepted")
	}
	if err := users.SetPassword(ctx, "missing", "correct-horse-2", false); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing password update error=%v", err)
	}
	if err := users.SetPassword(ctx, "alice", "correct-horse-2", true); err != nil {
		t.Fatal(err)
	}
	if _, err := users.Verify(ctx, "alice", "correct-horse-1"); err == nil {
		t.Fatal("old password still accepted")
	}
	if _, err := users.Verify(ctx, "alice", "correct-horse-2"); err != nil {
		t.Fatalf("new password rejected: %v", err)
	}
	storedSession, err := db.SessionByAccessHash(ctx, "access")
	if err != nil || storedSession.RevokedAt == nil {
		t.Fatalf("session not revoked after password change: %+v err=%v", storedSession, err)
	}

	if err := users.SetEnabled(ctx, "missing", false); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing enable error=%v", err)
	}
	if err := users.SetEnabled(ctx, "alice", false); err != nil {
		t.Fatal(err)
	}
	if _, err := users.Verify(ctx, "alice", "correct-horse-2"); err == nil {
		t.Fatal("disabled user authenticated")
	}
	if err := users.SetEnabled(ctx, "alice", true); err != nil {
		t.Fatal(err)
	}
	if _, err := users.Verify(ctx, "alice", "correct-horse-2"); err != nil {
		t.Fatalf("re-enabled user rejected: %v", err)
	}
}
