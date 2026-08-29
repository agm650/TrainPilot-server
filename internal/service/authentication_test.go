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

func newAuthFixture(t *testing.T, accessTTL, refreshTTL time.Duration) (*AuthService, *UserService, *store.Store, *clock.Fake) {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	clk := clock.NewFake(time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC))
	users := NewUserServiceWithPasswordParams(db, clk, auth.PasswordParams{Iterations: 100_000, SaltLength: 16, KeyLength: 32})
	if _, err := users.Create(context.Background(), "alice", "Alice", "correct-horse-1", model.RoleDriver, false, false); err != nil {
		t.Fatal(err)
	}
	return NewAuthService(db, users, clk, accessTTL, refreshTTL), users, db, clk
}

func TestAuthenticationLifecycle(t *testing.T) {
	ctx := context.Background()
	authSvc, _, db, clk := newAuthFixture(t, 15*time.Minute, time.Hour)

	if _, err := authSvc.Login(ctx, "alice", "wrong-password", "client", "Client", "test"); err == nil {
		t.Fatal("login with wrong password succeeded")
	}
	pair, err := authSvc.Login(ctx, "alice", "correct-horse-1", "client", "Client", "test")
	if err != nil {
		t.Fatal(err)
	}
	if pair.AccessToken == "" || pair.RefreshToken == "" || pair.SessionID == "" || pair.User.Username != "alice" {
		t.Fatalf("token pair=%+v", pair)
	}
	if _, _, err := authSvc.Authenticate(ctx, ""); err == nil {
		t.Fatal("empty token accepted")
	}
	if _, _, err := authSvc.Authenticate(ctx, "invalid"); err == nil {
		t.Fatal("invalid token accepted")
	}
	user, sess, err := authSvc.Authenticate(ctx, pair.AccessToken)
	if err != nil || user.Username != "alice" || sess.ID != pair.SessionID {
		t.Fatalf("authenticate user=%+v session=%+v err=%v", user, sess, err)
	}
	stored, err := db.SessionByAccessHash(ctx, auth.HashToken(pair.AccessToken))
	if err != nil || !stored.LastSeenAt.Equal(clk.Now()) {
		t.Fatalf("stored session=%+v err=%v", stored, err)
	}

	refreshed, err := authSvc.Refresh(ctx, pair.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.AccessToken == pair.AccessToken || refreshed.RefreshToken == pair.RefreshToken || refreshed.SessionID != pair.SessionID {
		t.Fatalf("refresh did not rotate tokens: old=%+v new=%+v", pair, refreshed)
	}
	if _, err := authSvc.Refresh(ctx, pair.RefreshToken); !errors.Is(err, ErrInvalidRefreshToken) {
		t.Fatalf("old refresh token error=%v", err)
	}
	if _, _, err := authSvc.Authenticate(ctx, pair.AccessToken); !errors.Is(err, ErrInvalidAccessToken) {
		t.Fatalf("old access token error=%v", err)
	}
	if err := authSvc.Logout(ctx, refreshed.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := authSvc.Authenticate(ctx, refreshed.AccessToken); !errors.Is(err, ErrInvalidAccessToken) {
		t.Fatalf("revoked access token error=%v", err)
	}
	if _, err := authSvc.Refresh(ctx, refreshed.RefreshToken); !errors.Is(err, ErrInvalidRefreshToken) {
		t.Fatalf("revoked refresh token error=%v", err)
	}
}

func TestAuthenticationExpiryAndDisabledUser(t *testing.T) {
	ctx := context.Background()
	authSvc, users, _, clk := newAuthFixture(t, time.Second, 2*time.Second)
	pair, err := authSvc.Login(ctx, "alice", "correct-horse-1", "client", "Client", "test")
	if err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Second)
	if _, _, err := authSvc.Authenticate(ctx, pair.AccessToken); !errors.Is(err, ErrAccessTokenExpired) {
		t.Fatalf("expired access token error=%v", err)
	}
	clk.Advance(time.Second)
	if _, err := authSvc.Refresh(ctx, pair.RefreshToken); !errors.Is(err, ErrRefreshTokenExpired) {
		t.Fatalf("expired refresh token error=%v", err)
	}

	pair, err = authSvc.Login(ctx, "alice", "correct-horse-1", "client-2", "Client 2", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := users.SetEnabled(ctx, "alice", false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := authSvc.Authenticate(ctx, pair.AccessToken); err == nil {
		t.Fatal("disabled user's access token accepted")
	}
	if _, err := authSvc.Refresh(ctx, pair.RefreshToken); err == nil {
		t.Fatal("disabled user's refresh token accepted")
	}
}
