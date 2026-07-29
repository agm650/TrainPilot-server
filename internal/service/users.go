package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/agm650/TrainPilot-server/internal/auth"
	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/store"
)

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{2,31}$`)

type UserService struct {
	store          *store.Store
	clock          clock.Clock
	passwordParams auth.PasswordParams
}

func NewUserService(s *store.Store, c clock.Clock) *UserService {
	return &UserService{store: s, clock: c, passwordParams: auth.DefaultPasswordParams()}
}
func NewUserServiceWithPasswordParams(s *store.Store, c clock.Clock, params auth.PasswordParams) *UserService {
	return &UserService{store: s, clock: c, passwordParams: params}
}
func (u *UserService) Create(ctx context.Context, username, display, password string, role model.Role, mustChange bool, bootstrap bool) (model.User, error) {
	username = strings.TrimSpace(username)
	if !usernamePattern.MatchString(username) {
		return model.User{}, errors.New("username must be 3-32 characters and use letters, digits, dot, underscore or dash")
	}
	if !role.Valid() {
		return model.User{}, errors.New("invalid role")
	}
	if bootstrap {
		count, err := u.store.UserCount(ctx)
		if err != nil {
			return model.User{}, err
		}
		if count != 0 {
			return model.User{}, errors.New("bootstrap is only allowed when no user exists")
		}
	}
	hash, err := auth.HashPassword(password, u.passwordParams)
	if err != nil {
		return model.User{}, err
	}
	now := u.clock.Now()
	user := model.User{ID: newID(), Username: username, DisplayName: display, Role: role, Enabled: true, MustChangePassword: mustChange, CreatedAt: now, UpdatedAt: now}
	if err := u.store.CreateUser(ctx, user, hash); err != nil {
		return model.User{}, err
	}
	return user, nil
}
func (u *UserService) List(ctx context.Context) ([]model.User, error) { return u.store.ListUsers(ctx) }
func (u *UserService) SetEnabled(ctx context.Context, username string, enabled bool) error {
	found, err := u.store.GetUserByUsername(ctx, username)
	if err != nil {
		return err
	}
	if err := u.store.SetUserEnabled(ctx, username, enabled, u.clock.Now()); err != nil {
		return err
	}
	if !enabled {
		return u.store.RevokeUserSessions(ctx, found.User.ID, u.clock.Now())
	}
	return nil
}
func (u *UserService) SetRole(ctx context.Context, username string, role model.Role) error {
	if !role.Valid() {
		return fmt.Errorf("invalid role")
	}
	return u.store.SetUserRole(ctx, username, role, u.clock.Now())
}
func (u *UserService) SetPassword(ctx context.Context, username, password string, mustChange bool) error {
	hash, err := auth.HashPassword(password, u.passwordParams)
	if err != nil {
		return err
	}
	found, err := u.store.GetUserByUsername(ctx, username)
	if err != nil {
		return err
	}
	if err := u.store.SetPassword(ctx, username, hash, mustChange, u.clock.Now()); err != nil {
		return err
	}
	return u.store.RevokeUserSessions(ctx, found.User.ID, u.clock.Now())
}
func (u *UserService) Verify(ctx context.Context, username, password string) (model.User, error) {
	found, err := u.store.GetUserByUsername(ctx, username)
	if err != nil {
		return model.User{}, err
	}
	if !found.User.Enabled {
		return model.User{}, errors.New("user disabled")
	}
	ok, err := auth.VerifyPassword(found.PasswordHash, password)
	if err != nil || !ok {
		return model.User{}, errors.New("invalid credentials")
	}
	return found.User, nil
}

var _ = time.Second
