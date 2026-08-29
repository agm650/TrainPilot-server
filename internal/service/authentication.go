package service

import (
	"context"
	"time"

	"github.com/agm650/TrainPilot-server/internal/auth"
	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/store"
)

type TokenPair struct {
	AccessToken      string     `json:"accessToken"`
	RefreshToken     string     `json:"refreshToken"`
	AccessExpiresAt  time.Time  `json:"accessExpiresAt"`
	RefreshExpiresAt time.Time  `json:"refreshExpiresAt"`
	SessionID        string     `json:"sessionId"`
	User             model.User `json:"user"`
}
type AuthService struct {
	store                 *store.Store
	users                 *UserService
	clock                 clock.Clock
	accessTTL, refreshTTL time.Duration
}

func NewAuthService(s *store.Store, u *UserService, c clock.Clock, access, refresh time.Duration) *AuthService {
	return &AuthService{store: s, users: u, clock: c, accessTTL: access, refreshTTL: refresh}
}
func (a *AuthService) Login(ctx context.Context, username, password, clientID, clientName, platform string) (TokenPair, error) {
	user, err := a.users.Verify(ctx, username, password)
	if err != nil {
		return TokenPair{}, err
	}
	return a.newSession(ctx, user, clientID, clientName, platform)
}
func (a *AuthService) newSession(ctx context.Context, user model.User, clientID, clientName, platform string) (TokenPair, error) {
	at, ah, err := auth.NewToken()
	if err != nil {
		return TokenPair{}, err
	}
	rt, rh, err := auth.NewToken()
	if err != nil {
		return TokenPair{}, err
	}
	now := a.clock.Now()
	sess := model.Session{ID: newID(), UserID: user.ID, ClientID: clientID, ClientName: clientName, Platform: platform, AccessHash: ah, RefreshHash: rh, AccessExpiry: now.Add(a.accessTTL), RefreshExpiry: now.Add(a.refreshTTL), CreatedAt: now, LastSeenAt: now}
	if err := a.store.CreateSession(ctx, sess); err != nil {
		return TokenPair{}, err
	}
	_ = a.store.UpdateLastLogin(ctx, user.ID, now)
	return TokenPair{AccessToken: at, RefreshToken: rt, AccessExpiresAt: sess.AccessExpiry, RefreshExpiresAt: sess.RefreshExpiry, SessionID: sess.ID, User: user}, nil
}
func (a *AuthService) Authenticate(ctx context.Context, token string) (model.User, model.Session, error) {
	if token == "" {
		return model.User{}, model.Session{}, ErrInvalidAccessToken
	}
	sess, err := a.store.SessionByAccessHash(ctx, auth.HashToken(token))
	if err != nil {
		return model.User{}, model.Session{}, ErrInvalidAccessToken
	}
	now := a.clock.Now()
	if sess.RevokedAt != nil {
		return model.User{}, model.Session{}, ErrInvalidAccessToken
	}
	if !now.Before(sess.AccessExpiry) {
		return model.User{}, model.Session{}, ErrAccessTokenExpired
	}
	found, err := a.store.GetUserByID(ctx, sess.UserID)
	if err != nil || !found.User.Enabled {
		return model.User{}, model.Session{}, ErrInvalidAccessToken
	}
	_ = a.store.TouchSession(ctx, sess.ID, now)
	return found.User, sess, nil
}
func (a *AuthService) Refresh(ctx context.Context, token string) (TokenPair, error) {
	sess, err := a.store.SessionByRefreshHash(ctx, auth.HashToken(token))
	if err != nil {
		return TokenPair{}, ErrInvalidRefreshToken
	}
	now := a.clock.Now()
	if sess.RevokedAt != nil {
		return TokenPair{}, ErrInvalidRefreshToken
	}
	if !now.Before(sess.RefreshExpiry) {
		return TokenPair{}, ErrRefreshTokenExpired
	}
	found, err := a.store.GetUserByID(ctx, sess.UserID)
	if err != nil || !found.User.Enabled {
		return TokenPair{}, ErrInvalidRefreshToken
	}
	at, ah, err := auth.NewToken()
	if err != nil {
		return TokenPair{}, err
	}
	rt, rh, err := auth.NewToken()
	if err != nil {
		return TokenPair{}, err
	}
	accessExp := now.Add(a.accessTTL)
	refreshExp := now.Add(a.refreshTTL)
	if err := a.store.RotateSessionTokens(ctx, sess.ID, ah, rh, accessExp, refreshExp, now); err != nil {
		return TokenPair{}, err
	}
	return TokenPair{AccessToken: at, RefreshToken: rt, AccessExpiresAt: accessExp, RefreshExpiresAt: refreshExp, SessionID: sess.ID, User: found.User}, nil
}
func (a *AuthService) Logout(ctx context.Context, sessionID string) error {
	return a.store.RevokeSession(ctx, sessionID, a.clock.Now())
}
