package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/sqlite"
)

type UserAuth struct {
	User         model.User
	PasswordHash string
}

func scanUser(scanner interface{ Scan(...any) error }) (UserAuth, error) {
	var result UserAuth
	var enabled, mustChange int
	var created, updated string
	var last sql.NullString
	err := scanner.Scan(&result.User.ID, &result.User.Username, &result.User.DisplayName, &result.PasswordHash,
		&result.User.Role, &enabled, &mustChange, &created, &updated, &last)
	if err != nil {
		return result, err
	}
	result.User.Enabled = enabled != 0
	result.User.MustChangePassword = mustChange != 0
	result.User.CreatedAt, err = parseTime(created)
	if err != nil {
		return result, err
	}
	result.User.UpdatedAt, err = parseTime(updated)
	if err != nil {
		return result, err
	}
	result.User.LastLoginAt, err = nullableTime(last)
	return result, err
}

func (s *Store) UserCount(ctx context.Context) (int, error) {
	var count int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count)
	return count, err
}

func (s *Store) CreateUser(ctx context.Context, user model.User, passwordHash string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO users(id,username,display_name,password_hash,role,enabled,must_change_password,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?)`, user.ID, strings.TrimSpace(user.Username), user.DisplayName, passwordHash, user.Role,
		boolInt(user.Enabled), boolInt(user.MustChangePassword), timeText(user.CreatedAt), timeText(user.UpdatedAt))
	if isUnique(err) {
		return ErrConflict
	}
	return err
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (UserAuth, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT id,username,display_name,password_hash,role,enabled,must_change_password,created_at,updated_at,last_login_at FROM users WHERE username=? COLLATE NOCASE`, username)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

func (s *Store) GetUserByID(ctx context.Context, id string) (UserAuth, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT id,username,display_name,password_hash,role,enabled,must_change_password,created_at,updated_at,last_login_at FROM users WHERE id=?`, id)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

func (s *Store) ListUsers(ctx context.Context) ([]model.User, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,username,display_name,password_hash,role,enabled,must_change_password,created_at,updated_at,last_login_at FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u.User)
	}
	return out, rows.Err()
}

func (s *Store) SetUserEnabled(ctx context.Context, username string, enabled bool, now time.Time) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE users SET enabled=?,updated_at=? WHERE username=? COLLATE NOCASE`, boolInt(enabled), timeText(now), username)
	if err != nil {
		return err
	}
	return requireAffected(res)
}

func (s *Store) SetUserRole(ctx context.Context, username string, role model.Role, now time.Time) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE users SET role=?,updated_at=? WHERE username=? COLLATE NOCASE`, role, timeText(now), username)
	if err != nil {
		return err
	}
	return requireAffected(res)
}

func (s *Store) SetPassword(ctx context.Context, username, passwordHash string, mustChange bool, now time.Time) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE users SET password_hash=?,must_change_password=?,updated_at=? WHERE username=? COLLATE NOCASE`, passwordHash, boolInt(mustChange), timeText(now), username)
	if err != nil {
		return err
	}
	return requireAffected(res)
}

func (s *Store) UpdateLastLogin(ctx context.Context, userID string, now time.Time) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET last_login_at=?,updated_at=? WHERE id=?`, timeText(now), timeText(now), userID)
	return err
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
func requireAffected(res sqlite.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
func isUnique(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "UNIQUE constraint failed") || strings.Contains(err.Error(), "constraint failed"))
}
