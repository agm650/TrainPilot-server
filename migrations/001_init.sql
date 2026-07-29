PRAGMA foreign_keys=ON;

CREATE TABLE IF NOT EXISTS users (
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
);

CREATE TABLE IF NOT EXISTS sessions (
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
);

CREATE INDEX IF NOT EXISTS idx_sessions_access ON sessions(access_token_hash);
CREATE INDEX IF NOT EXISTS idx_sessions_refresh ON sessions(refresh_token_hash);

CREATE TABLE IF NOT EXISTS locomotives (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  dcc_address INTEGER NOT NULL,
  address_kind TEXT NOT NULL DEFAULT 'short',
  speed_steps INTEGER NOT NULL DEFAULT 128,
  manufacturer TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS control_leases (
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
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_one_live_lease_per_loco
  ON control_leases(locomotive_id)
  WHERE state IN ('active','stopping');

CREATE TABLE IF NOT EXISTS blocks (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  occupied INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS feedback_mappings (
  provider TEXT NOT NULL,
  address INTEGER NOT NULL,
  block_id TEXT NOT NULL REFERENCES blocks(id) ON DELETE CASCADE,
  PRIMARY KEY(provider, address)
);

CREATE TABLE IF NOT EXISTS turnouts (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  dcc_address INTEGER NOT NULL,
  desired_state TEXT NOT NULL DEFAULT 'straight',
  reported_state TEXT NOT NULL DEFAULT 'unknown'
);

CREATE TABLE IF NOT EXISTS routes (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  state TEXT NOT NULL DEFAULT 'idle',
  reserved_by_session TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS route_blocks (
  route_id TEXT NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
  block_id TEXT NOT NULL REFERENCES blocks(id),
  PRIMARY KEY(route_id, block_id)
);

CREATE TABLE IF NOT EXISTS route_turnouts (
  route_id TEXT NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
  turnout_id TEXT NOT NULL REFERENCES turnouts(id),
  required_state TEXT NOT NULL,
  PRIMARY KEY(route_id, turnout_id)
);

CREATE TABLE IF NOT EXISTS route_conflicts (
  route_id TEXT NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
  conflict_route_id TEXT NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
  PRIMARY KEY(route_id, conflict_route_id)
);

CREATE TABLE IF NOT EXISTS audit_events (
  id TEXT PRIMARY KEY,
  occurred_at TEXT NOT NULL,
  actor_type TEXT NOT NULL,
  actor_id TEXT NOT NULL DEFAULT '',
  action TEXT NOT NULL,
  target_type TEXT NOT NULL DEFAULT '',
  target_id TEXT NOT NULL DEFAULT '',
  source TEXT NOT NULL DEFAULT '',
  details_json TEXT NOT NULL DEFAULT '{}'
);
