package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/sqlite"
)

func (s *Store) migrateSignalSchema(ctx context.Context) error {
	return s.DB.WithTransaction(ctx, func(tx *sqlite.Tx) error {
		for _, query := range []string{
			`CREATE TABLE IF NOT EXISTS signal_profiles(id TEXT PRIMARY KEY, definition_json TEXT NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS signals(
			 id TEXT PRIMARY KEY,
			 profile_id TEXT NOT NULL REFERENCES signal_profiles(id) ON DELETE RESTRICT,
			 block_id TEXT NOT NULL REFERENCES blocks(id) DEFERRABLE INITIALLY DEFERRED,
			 section_id TEXT NOT NULL REFERENCES track_sections(id) DEFERRABLE INITIALLY DEFERRED,
			 entry_node_id TEXT NOT NULL REFERENCES topology_nodes(id) DEFERRABLE INITIALLY DEFERRED,
			 downstream_signal_id TEXT REFERENCES signals(id) DEFERRABLE INITIALLY DEFERRED,
			 endpoint_kind TEXT NOT NULL,
			 linear_address INTEGER NOT NULL CHECK(linear_address BETWEEN 1 AND 2040),
			 definition_json TEXT NOT NULL,
			 UNIQUE(endpoint_kind,linear_address)
			)`,
			`CREATE TABLE IF NOT EXISTS layout_signal_positions(
			 signal_id TEXT PRIMARY KEY REFERENCES signals(id) DEFERRABLE INITIALLY DEFERRED,
			 x REAL NOT NULL,y REAL NOT NULL,rotation_degrees REAL NOT NULL,mirrored INTEGER NOT NULL
			)`,
		} {
			if _, err := tx.ExecContext(ctx, query); err != nil {
				return err
			}
		}
		return nil
	})
}

type signalReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func readSignalProfiles(ctx context.Context, reader signalReader) ([]model.SignalProfile, error) {
	rows, err := reader.QueryContext(ctx, `SELECT definition_json FROM signal_profiles ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []model.SignalProfile{}
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var value model.SignalProfile
		if err := json.Unmarshal(data, &value); err != nil {
			return nil, err
		}
		if err := model.ValidateSignalProfile(value); err != nil {
			return nil, err
		}
		result = append(result, model.NormalizeSignalProfile(value))
	}
	return result, rows.Err()
}

func readSignals(ctx context.Context, reader signalReader) ([]model.SignalDefinition, error) {
	rows, err := reader.QueryContext(ctx, `SELECT definition_json FROM signals ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []model.SignalDefinition{}
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var value model.SignalDefinition
		if err := json.Unmarshal(data, &value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (s *Store) ListSignalProfiles(ctx context.Context) ([]model.SignalProfile, error) {
	return readSignalProfiles(ctx, s.DB)
}
func (s *Store) ListSignals(ctx context.Context) ([]model.SignalDefinition, error) {
	return readSignals(ctx, s.DB)
}

// SignalConfiguration reads both collections in the same database snapshot.
func (s *Store) SignalConfiguration(ctx context.Context) (profiles []model.SignalProfile, signals []model.SignalDefinition, err error) {
	err = s.DB.WithTransaction(ctx, func(tx *sqlite.Tx) error {
		var readErr error
		profiles, readErr = readSignalProfiles(ctx, tx)
		if readErr != nil {
			return readErr
		}
		signals, readErr = readSignals(ctx, tx)
		return readErr
	})
	return
}
func (s *Store) SignalProfile(ctx context.Context, id string) (model.SignalProfile, error) {
	values, err := s.ListSignalProfiles(ctx)
	if err != nil {
		return model.SignalProfile{}, err
	}
	for _, v := range values {
		if v.ID == id {
			return v, nil
		}
	}
	return model.SignalProfile{}, ErrNotFound
}
func (s *Store) Signal(ctx context.Context, id string) (model.SignalDefinition, error) {
	values, err := s.ListSignals(ctx)
	if err != nil {
		return model.SignalDefinition{}, err
	}
	for _, v := range values {
		if v.ID == id {
			return v, nil
		}
	}
	return model.SignalDefinition{}, ErrNotFound
}

func upsertSignalProfile(ctx context.Context, tx *sqlite.Tx, p model.SignalProfile) error {
	if err := model.ValidateSignalProfile(p); err != nil {
		return err
	}
	data, err := json.Marshal(model.NormalizeSignalProfile(p))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO signal_profiles(id,definition_json) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET definition_json=excluded.definition_json`, p.ID, string(data))
	return err
}

func upsertSignal(ctx context.Context, tx *sqlite.Tx, value model.SignalDefinition, profiles []model.SignalProfile) error {
	var profile model.SignalProfile
	for _, p := range profiles {
		if p.ID == value.ProfileID {
			profile = p
			break
		}
	}
	if profile.ID == "" {
		return fmt.Errorf("%w: signal profile %q", ErrNotFound, value.ProfileID)
	}
	normalized, err := model.NormalizeSignalDefinition(value, profile)
	if err != nil {
		return err
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return err
	}
	var downstream any
	if normalized.Downstream.Kind == model.SignalDownstreamSignal {
		downstream = normalized.Downstream.SignalID
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO signals(id,profile_id,block_id,section_id,entry_node_id,downstream_signal_id,endpoint_kind,linear_address,definition_json)
	 VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET profile_id=excluded.profile_id,block_id=excluded.block_id,section_id=excluded.section_id,entry_node_id=excluded.entry_node_id,downstream_signal_id=excluded.downstream_signal_id,endpoint_kind=excluded.endpoint_kind,linear_address=excluded.linear_address,definition_json=excluded.definition_json`, normalized.ID, normalized.ProfileID, normalized.ProtectedBlockID, normalized.Placement.TrackSectionID, normalized.Placement.EntryNodeID, downstream, normalized.Endpoint.Kind, normalized.Endpoint.LinearAddress, string(data))
	return err
}

func validateStoredSignalProfiles(ctx context.Context, tx *sqlite.Tx) error {
	profiles, err := readSignalProfiles(ctx, tx)
	if err != nil {
		return err
	}
	signals, err := readSignals(ctx, tx)
	if err != nil {
		return err
	}
	for _, signal := range signals {
		found := false
		for _, profile := range profiles {
			if signal.ProfileID == profile.ID {
				found = true
				if err := model.ValidateSignalDefinition(signal, profile); err != nil {
					return fmt.Errorf("%w: %w", ErrConflict, err)
				}
				break
			}
		}
		if !found {
			return fmt.Errorf("%w: missing signal profile", ErrConflict)
		}
	}
	return validateStoredSignalLayout(ctx, tx, profiles, signals)
}

func (s *Store) SetSignalProfile(ctx context.Context, p model.SignalProfile) error {
	return signalStoreError(s.DB.WithTransaction(ctx, func(tx *sqlite.Tx) error {
		if err := upsertSignalProfile(ctx, tx, p); err != nil {
			return err
		}
		return validateStoredSignalProfiles(ctx, tx)
	}))
}
func (s *Store) SetSignal(ctx context.Context, value model.SignalDefinition) error {
	return signalStoreError(s.DB.WithTransaction(ctx, func(tx *sqlite.Tx) error {
		profiles, err := readSignalProfiles(ctx, tx)
		if err != nil {
			return err
		}
		if err := upsertSignal(ctx, tx, value, profiles); err != nil {
			return err
		}
		return validateStoredSignalProfiles(ctx, tx)
	}))
}

func deleteSignalRow(ctx context.Context, tx *sqlite.Tx, table, id string) error {
	if table != "signals" && table != "signal_profiles" {
		return ErrConflict
	}
	if table == "signals" {
		if _, err := tx.ExecContext(ctx, `DELETE FROM layout_signal_positions WHERE signal_id=?`, id); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE id=?", id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *Store) DeleteSignal(ctx context.Context, id string) error {
	return signalStoreError(s.DB.WithTransaction(ctx, func(tx *sqlite.Tx) error { return deleteSignalRow(ctx, tx, "signals", id) }))
}
func (s *Store) DeleteSignalProfile(ctx context.Context, id string) error {
	return signalStoreError(s.DB.WithTransaction(ctx, func(tx *sqlite.Tx) error { return deleteSignalRow(ctx, tx, "signal_profiles", id) }))
}

// A single transaction can create cyclic downstream references. Deferred FKs
// check their final state; no intermediate configuration is published.
func replaceSignalDefinitions(ctx context.Context, tx *sqlite.Tx, profiles []model.SignalProfile, signals []model.SignalDefinition, replace bool) error {
	if replace {
		if err := deleteUnretainedSignalSymbols(ctx, tx, signals); err != nil {
			return err
		}
		for _, query := range []string{`DELETE FROM signals`, `DELETE FROM signal_profiles`} {
			if _, err := tx.ExecContext(ctx, query); err != nil {
				return err
			}
		}
	}
	seen := map[string]bool{}
	for _, p := range profiles {
		if seen[p.ID] {
			return ErrConflict
		}
		seen[p.ID] = true
		if err := upsertSignalProfile(ctx, tx, p); err != nil {
			return err
		}
	}
	allProfiles, err := readSignalProfiles(ctx, tx)
	if err != nil {
		return err
	}
	seen = map[string]bool{}
	for _, value := range signals {
		if seen[value.ID] {
			return ErrConflict
		}
		seen[value.ID] = true
		if err := upsertSignal(ctx, tx, value, allProfiles); err != nil {
			return signalStoreError(err)
		}
	}
	return validateStoredSignalProfiles(ctx, tx)
}

func (s *Store) ReplaceSignalDefinitions(ctx context.Context, profiles []model.SignalProfile, signals []model.SignalDefinition, replace bool) error {
	return signalStoreError(s.DB.WithTransaction(ctx, func(tx *sqlite.Tx) error { return replaceSignalDefinitions(ctx, tx, profiles, signals, replace) }))
}

func signalStoreError(err error) error {
	var constraint interface{ Code() int }
	if errors.As(err, &constraint) && constraint.Code()&0xff == 19 {
		return fmt.Errorf("%w: %w", ErrConflict, err)
	}
	return err
}
