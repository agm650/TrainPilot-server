package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/sqlite"
)

func (s *Store) ListLocomotives(ctx context.Context) ([]model.Locomotive, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name,dcc_address,address_kind,speed_steps,manufacturer,model FROM locomotives ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Locomotive
	for rows.Next() {
		var x model.Locomotive
		if err := rows.Scan(&x.ID, &x.Name, &x.DCCAddress, &x.AddressKind, &x.SpeedSteps, &x.Manufacturer, &x.Model); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Store) GetLocomotive(ctx context.Context, id string) (model.Locomotive, error) {
	var x model.Locomotive
	err := s.DB.QueryRowContext(ctx, `SELECT id,name,dcc_address,address_kind,speed_steps,manufacturer,model FROM locomotives WHERE id=?`, id).Scan(&x.ID, &x.Name, &x.DCCAddress, &x.AddressKind, &x.SpeedSteps, &x.Manufacturer, &x.Model)
	if errors.Is(err, sql.ErrNoRows) {
		return x, ErrNotFound
	}
	return x, err
}
func (s *Store) CreateLocomotive(ctx context.Context, x model.Locomotive) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO locomotives(id,name,dcc_address,address_kind,speed_steps,manufacturer,model) VALUES(?,?,?,?,?,?,?)`,
		x.ID, x.Name, x.DCCAddress, x.AddressKind, x.SpeedSteps, x.Manufacturer, x.Model)
	if isUnique(err) {
		return ErrConflict
	}
	return err
}

func (s *Store) UpdateLocomotive(ctx context.Context, x model.Locomotive) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE locomotives SET name=?,dcc_address=?,address_kind=?,speed_steps=?,manufacturer=?,model=? WHERE id=?`,
		x.Name, x.DCCAddress, x.AddressKind, x.SpeedSteps, x.Manufacturer, x.Model, x.ID)
	if err != nil {
		return err
	}
	return requireAffected(res)
}

func (s *Store) DeleteLocomotive(ctx context.Context, id string) error {
	// Keep control history intact. A locomotive that has ever been referenced by
	// a lease cannot be physically removed; callers may update it instead.
	var leases int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM control_leases WHERE locomotive_id=?`, id).Scan(&leases); err != nil {
		return err
	}
	if leases > 0 {
		return ErrConflict
	}
	res, err := s.DB.ExecContext(ctx, `DELETE FROM locomotives WHERE id=?`, id)
	if err != nil {
		return err
	}
	return requireAffected(res)
}
func (s *Store) ListBlocks(ctx context.Context) ([]model.Block, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name,occupied FROM blocks ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Block
	for rows.Next() {
		var x model.Block
		var occ int
		if err := rows.Scan(&x.ID, &x.Name, &occ); err != nil {
			return nil, err
		}
		x.Occupied = occ != 0
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Store) SetBlockOccupied(ctx context.Context, id string, occupied bool) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE blocks SET occupied=? WHERE id=?`, boolInt(occupied), id)
	if err != nil {
		return err
	}
	return requireAffected(res)
}
func (s *Store) ListTurnouts(ctx context.Context) ([]model.Turnout, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name,dcc_address,desired_state,reported_state FROM turnouts ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Turnout
	for rows.Next() {
		var x model.Turnout
		if err := rows.Scan(&x.ID, &x.Name, &x.DCCAddress, &x.DesiredState, &x.ReportedState); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Store) GetTurnout(ctx context.Context, id string) (model.Turnout, error) {
	var x model.Turnout
	err := s.DB.QueryRowContext(ctx, `SELECT id,name,dcc_address,desired_state,reported_state FROM turnouts WHERE id=?`, id).Scan(&x.ID, &x.Name, &x.DCCAddress, &x.DesiredState, &x.ReportedState)
	if errors.Is(err, sql.ErrNoRows) {
		return x, ErrNotFound
	}
	return x, err
}
func (s *Store) SetTurnoutState(ctx context.Context, id, state string) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE turnouts SET desired_state=?,reported_state=? WHERE id=?`, state, state, id)
	if err != nil {
		return err
	}
	return requireAffected(res)
}

func (s *Store) SeedDemo(ctx context.Context) error {
	stmts := []struct {
		q    string
		args []any
	}{
		{`INSERT OR IGNORE INTO locomotives(id,name,dcc_address,address_kind,speed_steps,manufacturer,model) VALUES(?,?,?,?,?,?,?)`, []any{"loco-bb26001", "BB 26001", 2601, "long", 128, "Jouef", "BB 26000"}},
		{`INSERT OR IGNORE INTO locomotives(id,name,dcc_address,address_kind,speed_steps,manufacturer,model) VALUES(?,?,?,?,?,?,?)`, []any{"loco-cc72030", "CC 72030", 7203, "long", 128, "Roco", "CC 72000"}},
		{`INSERT OR IGNORE INTO blocks(id,name,occupied) VALUES(?,?,0)`, []any{"block-a", "Gare voie 1"}},
		{`INSERT OR IGNORE INTO blocks(id,name,occupied) VALUES(?,?,0)`, []any{"block-b", "Pleine voie"}},
		{`INSERT OR IGNORE INTO blocks(id,name,occupied) VALUES(?,?,0)`, []any{"block-c", "Gare voie 2"}},
		{`INSERT OR IGNORE INTO feedback_mappings(provider,address,block_id) VALUES(?,?,?)`, []any{"simulator", 1, "block-a"}},
		{`INSERT OR IGNORE INTO turnouts(id,name,dcc_address,desired_state,reported_state) VALUES(?,?,?,?,?)`, []any{"turnout-1", "Aiguille entrée", 1, "straight", "straight"}},
		{`INSERT OR IGNORE INTO routes(id,name,state,reserved_by_session) VALUES(?,?,?,?)`, []any{"route-a-b", "Voie 1 vers pleine voie", "idle", ""}},
		{`INSERT OR IGNORE INTO route_blocks(route_id,block_id) VALUES(?,?)`, []any{"route-a-b", "block-a"}},
		{`INSERT OR IGNORE INTO route_blocks(route_id,block_id) VALUES(?,?)`, []any{"route-a-b", "block-b"}},
		{`INSERT OR IGNORE INTO route_turnouts(route_id,turnout_id,required_state) VALUES(?,?,?)`, []any{"route-a-b", "turnout-1", "straight"}},
	}
	for _, st := range stmts {
		if _, err := s.DB.ExecContext(ctx, st.q, st.args...); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) BlockForFeedback(ctx context.Context, provider string, address int) (string, error) {
	var blockID string
	err := s.DB.QueryRowContext(ctx, `SELECT block_id FROM feedback_mappings WHERE (provider=? OR provider='*') AND address=? ORDER BY provider DESC LIMIT 1`, provider, address).Scan(&blockID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return blockID, err
}

func (s *Store) SetFeedbackMapping(ctx context.Context, provider string, address int, blockID string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO feedback_mappings(provider,address,block_id) VALUES(?,?,?) ON CONFLICT(provider,address) DO UPDATE SET block_id=excluded.block_id`, provider, address, blockID)
	return err
}

func (s *Store) ReplaceLocomotives(ctx context.Context, items []model.Locomotive, replace bool) error {
	if replace {
		var live int
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM control_leases WHERE state IN ('active','stopping')`).Scan(&live); err != nil {
			return err
		}
		if live > 0 {
			return ErrConflict
		}
	}
	return s.DB.WithTransaction(ctx, func(tx *sqlite.Tx) error {
		if replace {
			if _, err := tx.ExecContext(ctx, `DELETE FROM control_leases`); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM locomotives`); err != nil {
				return err
			}
		}
		for _, x := range items {
			if _, err := tx.ExecContext(ctx, `INSERT INTO locomotives(id,name,dcc_address,address_kind,speed_steps,manufacturer,model) VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,dcc_address=excluded.dcc_address,address_kind=excluded.address_kind,speed_steps=excluded.speed_steps,manufacturer=excluded.manufacturer,model=excluded.model`, x.ID, x.Name, x.DCCAddress, x.AddressKind, x.SpeedSteps, x.Manufacturer, x.Model); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) ListFeedbackMappings(ctx context.Context) ([]model.FeedbackMapping, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT provider,address,block_id FROM feedback_mappings ORDER BY provider,address`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.FeedbackMapping
	for rows.Next() {
		var x model.FeedbackMapping
		if err := rows.Scan(&x.Provider, &x.Address, &x.BlockID); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
