package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/agm650/TrainPilot-server/internal/sqlite"
)

type Store struct{ DB *sqlite.DB }

func Open(path string) (*Store, error) {
	db, err := sqlite.Open(path)
	if err != nil {
		return nil, err
	}
	s := &Store{DB: db}
	if err := s.Migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.DB.Close() }

func (s *Store) Migrate(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL UNIQUE COLLATE NOCASE,
			display_name TEXT NOT NULL DEFAULT '',
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			must_change_password INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			last_login_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id),
			client_id TEXT NOT NULL,
			client_name TEXT NOT NULL DEFAULT '',
			platform TEXT NOT NULL DEFAULT '',
			access_token_hash TEXT NOT NULL UNIQUE,
			refresh_token_hash TEXT NOT NULL UNIQUE,
			access_expires_at TEXT NOT NULL,
			refresh_expires_at TEXT NOT NULL,
			created_at TEXT NOT NULL,
			last_seen_at TEXT NOT NULL,
			revoked_at TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_access ON sessions(access_token_hash)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_refresh ON sessions(refresh_token_hash)`,
		`CREATE TABLE IF NOT EXISTS locomotives (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			dcc_address INTEGER NOT NULL,
			address_kind TEXT NOT NULL DEFAULT 'short',
			speed_steps INTEGER NOT NULL DEFAULT 128,
			manufacturer TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS control_leases (
			id TEXT PRIMARY KEY,
			locomotive_id TEXT NOT NULL REFERENCES locomotives(id),
			user_id TEXT NOT NULL REFERENCES users(id),
			session_id TEXT NOT NULL REFERENCES sessions(id),
			state TEXT NOT NULL,
			acquired_at TEXT NOT NULL,
			renewed_at TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			release_after TEXT,
			release_reason TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_one_live_lease_per_loco ON control_leases(locomotive_id) WHERE state IN ('active','stopping')`,
		`CREATE TABLE IF NOT EXISTS blocks (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			occupied INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS feedback_mappings (
			provider TEXT NOT NULL,
			address INTEGER NOT NULL,
			block_id TEXT NOT NULL REFERENCES blocks(id) ON DELETE CASCADE,
			PRIMARY KEY(provider, address)
		)`,
		`CREATE TABLE IF NOT EXISTS turnouts (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			dcc_address INTEGER NOT NULL,
			desired_state TEXT NOT NULL DEFAULT 'straight',
			reported_state TEXT NOT NULL DEFAULT 'unknown',
			kind TEXT NOT NULL DEFAULT 'simple',
			desired_position TEXT NOT NULL DEFAULT '',
			reported_position TEXT NOT NULL DEFAULT '',
			pending INTEGER NOT NULL DEFAULT 0,
			reported_status TEXT NOT NULL DEFAULT 'unknown',
			quality TEXT NOT NULL DEFAULT '',
			command_status TEXT NOT NULL DEFAULT 'idle'
		)`,
		`CREATE TABLE IF NOT EXISTS turnout_endpoints (
			turnout_id TEXT NOT NULL REFERENCES turnouts(id) ON DELETE CASCADE,
			endpoint_id TEXT NOT NULL,
			linear_address INTEGER NOT NULL CHECK(linear_address >= 1),
			inverted INTEGER NOT NULL DEFAULT 0,
			ordinal INTEGER NOT NULL,
			PRIMARY KEY(turnout_id, endpoint_id),
			UNIQUE(turnout_id, linear_address),
			UNIQUE(turnout_id, ordinal)
		)`,
		`CREATE TABLE IF NOT EXISTS turnout_positions (
			turnout_id TEXT NOT NULL REFERENCES turnouts(id) ON DELETE CASCADE,
			position_id TEXT NOT NULL,
			label TEXT NOT NULL DEFAULT '',
			ordinal INTEGER NOT NULL,
			PRIMARY KEY(turnout_id, position_id),
			UNIQUE(turnout_id, ordinal)
		)`,
		`CREATE TABLE IF NOT EXISTS turnout_position_endpoints (
			turnout_id TEXT NOT NULL,
			position_id TEXT NOT NULL,
			endpoint_id TEXT NOT NULL,
			required_position TEXT NOT NULL CHECK(required_position IN ('position1','position2')),
			PRIMARY KEY(turnout_id, position_id, endpoint_id),
			FOREIGN KEY(turnout_id, position_id) REFERENCES turnout_positions(turnout_id, position_id) ON DELETE CASCADE,
			FOREIGN KEY(turnout_id, endpoint_id) REFERENCES turnout_endpoints(turnout_id, endpoint_id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS routes (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			state TEXT NOT NULL DEFAULT 'idle',
			reserved_by_session TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS route_blocks (
			route_id TEXT NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
			block_id TEXT NOT NULL REFERENCES blocks(id),
			PRIMARY KEY(route_id, block_id)
		)`,
		`CREATE TABLE IF NOT EXISTS route_turnouts (
			route_id TEXT NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
			turnout_id TEXT NOT NULL REFERENCES turnouts(id),
			required_state TEXT NOT NULL,
			PRIMARY KEY(route_id, turnout_id)
		)`,
		`CREATE TABLE IF NOT EXISTS route_conflicts (
			route_id TEXT NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
			conflict_route_id TEXT NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
			PRIMARY KEY(route_id, conflict_route_id)
		)`,
		`CREATE TABLE IF NOT EXISTS audit_events (
			id TEXT PRIMARY KEY,
			occurred_at TEXT NOT NULL,
			actor_type TEXT NOT NULL,
			actor_id TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL,
			target_type TEXT NOT NULL DEFAULT '',
			target_id TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT '',
			details_json TEXT NOT NULL DEFAULT '{}'
		)`,
	}
	for i, stmt := range stmts {
		if _, err := s.DB.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
	}
	return s.migrateTurnoutSchema(ctx)
}

func (s *Store) migrateTurnoutSchema(ctx context.Context) error {
	columns, err := s.tableColumns(ctx, "turnouts")
	if err != nil {
		return fmt.Errorf("inspect turnout schema: %w", err)
	}
	initializeReportedStatus := !columns["reported_status"]
	initializeQuality := !columns["quality"]
	initializeCommandStatus := !columns["command_status"]
	additions := []struct {
		name string
		sql  string
	}{
		{"kind", `ALTER TABLE turnouts ADD COLUMN kind TEXT NOT NULL DEFAULT 'simple'`},
		{"desired_position", `ALTER TABLE turnouts ADD COLUMN desired_position TEXT NOT NULL DEFAULT ''`},
		{"reported_position", `ALTER TABLE turnouts ADD COLUMN reported_position TEXT NOT NULL DEFAULT ''`},
		{"pending", `ALTER TABLE turnouts ADD COLUMN pending INTEGER NOT NULL DEFAULT 0`},
		{"reported_status", `ALTER TABLE turnouts ADD COLUMN reported_status TEXT NOT NULL DEFAULT 'unknown'`},
		{"quality", `ALTER TABLE turnouts ADD COLUMN quality TEXT NOT NULL DEFAULT ''`},
		{"command_status", `ALTER TABLE turnouts ADD COLUMN command_status TEXT NOT NULL DEFAULT 'idle'`},
	}
	for _, addition := range additions {
		if columns[addition.name] {
			continue
		}
		if _, err := s.DB.ExecContext(ctx, addition.sql); err != nil {
			return fmt.Errorf("add turnouts.%s: %w", addition.name, err)
		}
	}

	type legacyTurnout struct {
		id, desired, reported string
		address               int
		needsMigration        bool
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,dcc_address,desired_state,reported_state FROM turnouts ORDER BY id`)
	if err != nil {
		return fmt.Errorf("list legacy turnouts: %w", err)
	}
	var legacy []legacyTurnout
	for rows.Next() {
		var item legacyTurnout
		if err := rows.Scan(&item.id, &item.address, &item.desired, &item.reported); err != nil {
			rows.Close()
			return fmt.Errorf("read legacy turnout: %w", err)
		}
		legacy = append(legacy, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("list legacy turnouts: %w", err)
	}
	rows.Close()
	for i := range legacy {
		var endpointCount int
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM turnout_endpoints WHERE turnout_id=?`, legacy[i].id).Scan(&endpointCount); err != nil {
			return fmt.Errorf("inspect turnout %q endpoints: %w", legacy[i].id, err)
		}
		legacy[i].needsMigration = endpointCount == 0
	}

	return s.DB.WithTransaction(ctx, func(tx *sqlite.Tx) error {
		if initializeReportedStatus {
			if _, err := tx.ExecContext(ctx, `UPDATE turnouts SET reported_status=CASE WHEN reported_position<>'' THEN 'known' ELSE 'unknown' END`); err != nil {
				return fmt.Errorf("initialize turnout runtime state: %w", err)
			}
		}
		if initializeQuality {
			if _, err := tx.ExecContext(ctx, `UPDATE turnouts SET quality=CASE WHEN reported_position<>'' THEN 'assumed' ELSE '' END`); err != nil {
				return fmt.Errorf("initialize turnout quality: %w", err)
			}
		}
		if initializeCommandStatus {
			if _, err := tx.ExecContext(ctx, `UPDATE turnouts SET command_status=CASE WHEN pending<>0 THEN 'pending' WHEN desired_position<>'' AND desired_position=reported_position THEN 'succeeded' ELSE 'idle' END`); err != nil {
				return fmt.Errorf("initialize turnout command status: %w", err)
			}
		}
		for _, item := range legacy {
			if !item.needsMigration {
				continue
			}
			desired := legacyPosition(item.desired)
			reported := legacyPosition(item.reported)
			reportedStatus := "unknown"
			quality := ""
			if reported != "" {
				reportedStatus = "known"
				quality = "assumed"
			}
			commandStatus := "idle"
			if desired != "" && desired == reported {
				commandStatus = "succeeded"
			}
			if _, err := tx.ExecContext(ctx, `UPDATE turnouts SET kind=CASE WHEN kind='' THEN 'simple' ELSE kind END,desired_position=CASE WHEN desired_position='' THEN ? ELSE desired_position END,reported_position=CASE WHEN reported_position='' THEN ? ELSE reported_position END,reported_status=?,quality=?,command_status=? WHERE id=?`, desired, reported, reportedStatus, quality, commandStatus, item.id); err != nil {
				return fmt.Errorf("migrate turnout %q state: %w", item.id, err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO turnout_endpoints(turnout_id,endpoint_id,linear_address,inverted,ordinal) VALUES(?,?,?,0,0)`, item.id, "main", item.address); err != nil {
				return fmt.Errorf("migrate turnout %q endpoint: %w", item.id, err)
			}
			for ordinal, position := range []struct {
				id       string
				required string
			}{{"straight", "position1"}, {"diverging", "position2"}} {
				if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO turnout_positions(turnout_id,position_id,label,ordinal) VALUES(?,?,?,?)`, item.id, position.id, "", ordinal); err != nil {
					return fmt.Errorf("migrate turnout %q position %q: %w", item.id, position.id, err)
				}
				if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO turnout_position_endpoints(turnout_id,position_id,endpoint_id,required_position) VALUES(?,?,?,?)`, item.id, position.id, "main", position.required); err != nil {
					return fmt.Errorf("migrate turnout %q vector %q: %w", item.id, position.id, err)
				}
			}
		}
		return nil
	})
}

func (s *Store) tableColumns(ctx context.Context, table string) (map[string]bool, error) {
	rows, err := s.DB.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}

func legacyPosition(value string) string {
	if value == "straight" || value == "diverging" {
		return value
	}
	return ""
}

func parseTime(value string) (time.Time, error) { return time.Parse(time.RFC3339Nano, value) }
func nullableTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	t, err := parseTime(value.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
func timeText(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("conflict")
