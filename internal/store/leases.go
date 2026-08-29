package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/agm650/TrainPilot-server/internal/model"
)

func scanLease(scanner interface{ Scan(...any) error }) (model.ControlLease, error) {
	var l model.ControlLease
	var acquired, renewed, expires string
	var release sql.NullString
	err := scanner.Scan(&l.ID, &l.LocomotiveID, &l.UserID, &l.SessionID, &l.State, &acquired, &renewed, &expires, &release, &l.ReleaseReason)
	if err != nil {
		return l, err
	}
	if l.AcquiredAt, err = parseTime(acquired); err != nil {
		return l, err
	}
	if l.RenewedAt, err = parseTime(renewed); err != nil {
		return l, err
	}
	if l.ExpiresAt, err = parseTime(expires); err != nil {
		return l, err
	}
	l.ReleaseAfter, err = nullableTime(release)
	return l, err
}
func (s *Store) CreateLease(ctx context.Context, l model.ControlLease) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO control_leases(id,locomotive_id,user_id,session_id,state,acquired_at,renewed_at,expires_at,release_after,release_reason) VALUES(?,?,?,?,?,?,?,?,?,?)`, l.ID, l.LocomotiveID, l.UserID, l.SessionID, l.State, timeText(l.AcquiredAt), timeText(l.RenewedAt), timeText(l.ExpiresAt), nil, l.ReleaseReason)
	if isUnique(err) {
		return ErrConflict
	}
	return err
}
func (s *Store) GetLease(ctx context.Context, id string) (model.ControlLease, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT id,locomotive_id,user_id,session_id,state,acquired_at,renewed_at,expires_at,release_after,release_reason FROM control_leases WHERE id=?`, id)
	l, err := scanLease(row)
	if errors.Is(err, sql.ErrNoRows) {
		return l, ErrNotFound
	}
	return l, err
}
func (s *Store) LiveLeaseForLoco(ctx context.Context, locoID string) (model.ControlLease, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT id,locomotive_id,user_id,session_id,state,acquired_at,renewed_at,expires_at,release_after,release_reason FROM control_leases WHERE locomotive_id=? AND state IN ('active','stopping')`, locoID)
	l, err := scanLease(row)
	if errors.Is(err, sql.ErrNoRows) {
		return l, ErrNotFound
	}
	return l, err
}

func (s *Store) LiveLeasesForSession(ctx context.Context, sessionID string) ([]model.ControlLease, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,locomotive_id,user_id,session_id,state,acquired_at,renewed_at,expires_at,release_after,release_reason FROM control_leases WHERE session_id=? AND state IN ('active','stopping') ORDER BY acquired_at`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	leases := make([]model.ControlLease, 0)
	for rows.Next() {
		lease, err := scanLease(rows)
		if err != nil {
			return nil, err
		}
		leases = append(leases, lease)
	}
	return leases, rows.Err()
}
func (s *Store) HeartbeatLease(ctx context.Context, id, sessionID string, renewed, expires time.Time) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE control_leases SET renewed_at=?,expires_at=? WHERE id=? AND session_id=? AND state='active' AND expires_at>?`, timeText(renewed), timeText(expires), id, sessionID, timeText(renewed))
	if err != nil {
		return err
	}
	return requireAffected(res)
}

// RenewActiveLeaseForCommand validates ownership and extends an unexpired lease
// in one statement. This prevents a command from reviving a lease which has
// already reached its inactivity deadline but has not yet been swept.
func (s *Store) RenewActiveLeaseForCommand(ctx context.Context, id, locomotiveID, sessionID string, now, expires time.Time) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE control_leases SET renewed_at=?,expires_at=? WHERE id=? AND locomotive_id=? AND session_id=? AND state='active' AND expires_at>?`,
		timeText(now), timeText(expires), id, locomotiveID, sessionID, timeText(now))
	if err != nil {
		return err
	}
	return requireAffected(res)
}
func (s *Store) ExpiredActiveLeases(ctx context.Context, now time.Time) ([]model.ControlLease, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,locomotive_id,user_id,session_id,state,acquired_at,renewed_at,expires_at,release_after,release_reason FROM control_leases WHERE state='active' AND expires_at<=?`, timeText(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ControlLease
	for rows.Next() {
		l, err := scanLease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
func (s *Store) MarkLeaseStopping(ctx context.Context, id, reason string, releaseAfter time.Time) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE control_leases SET state='stopping',release_reason=?,release_after=? WHERE id=? AND state='active'`, reason, timeText(releaseAfter), id)
	if err != nil {
		return err
	}
	return requireAffected(res)
}
func (s *Store) StoppingLeasesReady(ctx context.Context, now time.Time) ([]model.ControlLease, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,locomotive_id,user_id,session_id,state,acquired_at,renewed_at,expires_at,release_after,release_reason FROM control_leases WHERE state='stopping' AND release_after<=?`, timeText(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ControlLease
	for rows.Next() {
		l, err := scanLease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
func (s *Store) ReleaseLease(ctx context.Context, id, sessionID, reason string) error {
	q := `UPDATE control_leases SET state='released',release_reason=?,release_after=NULL WHERE id=? AND state IN ('active','stopping')`
	args := []any{reason, id}
	if sessionID != "" {
		q += ` AND session_id=?`
		args = append(args, sessionID)
	}
	res, err := s.DB.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	return requireAffected(res)
}
