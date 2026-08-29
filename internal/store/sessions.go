package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/agm650/TrainPilot-server/internal/model"
)

func (s *Store) CreateSession(ctx context.Context, sess model.Session) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO sessions(id,user_id,client_id,client_name,platform,access_token_hash,refresh_token_hash,access_expires_at,refresh_expires_at,created_at,last_seen_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`, sess.ID, sess.UserID, sess.ClientID, sess.ClientName, sess.Platform, sess.AccessHash, sess.RefreshHash,
		timeText(sess.AccessExpiry), timeText(sess.RefreshExpiry), timeText(sess.CreatedAt), timeText(sess.LastSeenAt))
	return err
}

func scanSession(scanner interface{ Scan(...any) error }) (model.Session, error) {
	var sess model.Session
	var accessExp, refreshExp, created, seen string
	var revoked sql.NullString
	err := scanner.Scan(&sess.ID, &sess.UserID, &sess.ClientID, &sess.ClientName, &sess.Platform, &sess.AccessHash, &sess.RefreshHash, &accessExp, &refreshExp, &created, &seen, &revoked)
	if err != nil {
		return sess, err
	}
	var e error
	if sess.AccessExpiry, e = parseTime(accessExp); e != nil {
		return sess, e
	}
	if sess.RefreshExpiry, e = parseTime(refreshExp); e != nil {
		return sess, e
	}
	if sess.CreatedAt, e = parseTime(created); e != nil {
		return sess, e
	}
	if sess.LastSeenAt, e = parseTime(seen); e != nil {
		return sess, e
	}
	sess.RevokedAt, e = nullableTime(revoked)
	return sess, e
}

func (s *Store) SessionByAccessHash(ctx context.Context, hash string) (model.Session, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT id,user_id,client_id,client_name,platform,access_token_hash,refresh_token_hash,access_expires_at,refresh_expires_at,created_at,last_seen_at,revoked_at FROM sessions WHERE access_token_hash=?`, hash)
	sess, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return sess, ErrNotFound
	}
	return sess, err
}

func (s *Store) SessionByID(ctx context.Context, id string) (model.Session, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT id,user_id,client_id,client_name,platform,access_token_hash,refresh_token_hash,access_expires_at,refresh_expires_at,created_at,last_seen_at,revoked_at FROM sessions WHERE id=?`, id)
	sess, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return sess, ErrNotFound
	}
	return sess, err
}

func (s *Store) SessionByRefreshHash(ctx context.Context, hash string) (model.Session, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT id,user_id,client_id,client_name,platform,access_token_hash,refresh_token_hash,access_expires_at,refresh_expires_at,created_at,last_seen_at,revoked_at FROM sessions WHERE refresh_token_hash=?`, hash)
	sess, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return sess, ErrNotFound
	}
	return sess, err
}

func (s *Store) RotateSessionTokens(ctx context.Context, id, accessHash, refreshHash string, accessExp, refreshExp, now time.Time) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE sessions SET access_token_hash=?,refresh_token_hash=?,access_expires_at=?,refresh_expires_at=?,last_seen_at=? WHERE id=? AND revoked_at IS NULL`, accessHash, refreshHash, timeText(accessExp), timeText(refreshExp), timeText(now), id)
	if err != nil {
		return err
	}
	return requireAffected(res)
}

func (s *Store) TouchSession(ctx context.Context, id string, now time.Time) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE sessions SET last_seen_at=? WHERE id=? AND revoked_at IS NULL`, timeText(now), id)
	return err
}

func (s *Store) RevokeSession(ctx context.Context, id string, now time.Time) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE sessions SET revoked_at=? WHERE id=? AND revoked_at IS NULL`, timeText(now), id)
	if err != nil {
		return err
	}
	return requireAffected(res)
}

func (s *Store) RevokeUserSessions(ctx context.Context, userID string, now time.Time) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE sessions SET revoked_at=? WHERE user_id=? AND revoked_at IS NULL`, timeText(now), userID)
	return err
}
