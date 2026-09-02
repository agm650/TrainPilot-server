package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/sqlite"
	"github.com/agm650/TrainPilot-server/internal/station"
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
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name,occupied FROM blocks ORDER BY name,id`)
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
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name,kind,desired_position,reported_position,pending,reported_status,quality,command_status,dcc_address,desired_state,reported_state FROM turnouts ORDER BY name,id`)
	if err != nil {
		return nil, err
	}
	var out []model.Turnout
	for rows.Next() {
		var x model.Turnout
		var pending int
		if err := rows.Scan(&x.ID, &x.Name, &x.Kind, &x.DesiredPosition, &x.ReportedPosition, &pending, &x.ReportedStatus, &x.Quality, &x.CommandStatus, &x.DCCAddress, &x.DesiredState, &x.ReportedState); err != nil {
			rows.Close()
			return nil, err
		}
		x.Pending = pending != 0
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for i := range out {
		if err := s.loadTurnoutDefinition(ctx, &out[i]); err != nil {
			return nil, err
		}
		normalized, err := model.NormalizeTurnout(out[i])
		if err != nil {
			return nil, fmt.Errorf("load turnout %q: %w", out[i].ID, err)
		}
		out[i] = normalized
	}
	return out, nil
}
func (s *Store) GetTurnout(ctx context.Context, id string) (model.Turnout, error) {
	var x model.Turnout
	var pending int
	err := s.DB.QueryRowContext(ctx, `SELECT id,name,kind,desired_position,reported_position,pending,reported_status,quality,command_status,dcc_address,desired_state,reported_state FROM turnouts WHERE id=?`, id).Scan(&x.ID, &x.Name, &x.Kind, &x.DesiredPosition, &x.ReportedPosition, &pending, &x.ReportedStatus, &x.Quality, &x.CommandStatus, &x.DCCAddress, &x.DesiredState, &x.ReportedState)
	if errors.Is(err, sql.ErrNoRows) {
		return x, ErrNotFound
	}
	if err != nil {
		return x, err
	}
	x.Pending = pending != 0
	if err := s.loadTurnoutDefinition(ctx, &x); err != nil {
		return model.Turnout{}, err
	}
	normalized, err := model.NormalizeTurnout(x)
	if err != nil {
		return model.Turnout{}, fmt.Errorf("load turnout %q: %w", x.ID, err)
	}
	return normalized, nil
}

func (s *Store) ListTurnoutsByAccessoryAddress(ctx context.Context, address int) ([]model.Turnout, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT turnout_id FROM turnout_endpoints WHERE linear_address=? ORDER BY turnout_id`, address)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	turnouts := make([]model.Turnout, 0, len(ids))
	for _, id := range ids {
		turnout, err := s.GetTurnout(ctx, id)
		if err != nil {
			return nil, err
		}
		turnouts = append(turnouts, turnout)
	}
	return turnouts, nil
}
func (s *Store) SetTurnoutState(ctx context.Context, id, position string) error {
	legacy := position
	if legacy != "straight" && legacy != "diverging" {
		legacy = "unknown"
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE turnouts SET desired_position=?,reported_position=?,pending=0,reported_status='known',quality='assumed',command_status='succeeded',desired_state=?,reported_state=? WHERE id=?`, position, position, legacy, legacy, id)
	if err != nil {
		return err
	}
	return requireAffected(res)
}

func (s *Store) SetTurnoutDesiredPosition(ctx context.Context, id, position string, pending bool) error {
	legacy := position
	if legacy != "straight" && legacy != "diverging" {
		legacy = "unknown"
	}
	status := model.TurnoutCommandIdle
	if pending {
		status = model.TurnoutCommandPending
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE turnouts SET desired_position=?,pending=?,command_status=?,desired_state=? WHERE id=?`, position, pending, status, legacy, id)
	if err != nil {
		return err
	}
	return requireAffected(res)
}

func (s *Store) SetTurnoutReportedPosition(ctx context.Context, id, position string, pending bool) error {
	legacy := position
	if legacy != "straight" && legacy != "diverging" {
		legacy = "unknown"
	}
	status := model.TurnoutCommandSucceeded
	if pending {
		status = model.TurnoutCommandPending
	}
	reportedStatus := station.AccessoryReportKnown
	if position == "" {
		reportedStatus = station.AccessoryReportUnknown
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE turnouts SET reported_position=?,pending=?,reported_status=?,command_status=?,reported_state=? WHERE id=?`, position, pending, reportedStatus, status, legacy, id)
	if err != nil {
		return err
	}
	return requireAffected(res)
}

func (s *Store) SetTurnoutObservation(ctx context.Context, id, position string, reportedStatus station.AccessoryReportState, quality station.AccessoryReportQuality) error {
	legacy := position
	if legacy != "straight" && legacy != "diverging" {
		legacy = "unknown"
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE turnouts SET reported_position=?,reported_status=?,quality=?,reported_state=? WHERE id=?`, position, reportedStatus, quality, legacy, id)
	if err != nil {
		return err
	}
	return requireAffected(res)
}

func (s *Store) SetTurnoutCommandResult(ctx context.Context, id string, pending bool, status model.TurnoutCommandStatus) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE turnouts SET pending=?,command_status=? WHERE id=?`, boolInt(pending), status, id)
	if err != nil {
		return err
	}
	return requireAffected(res)
}

func (s *Store) loadTurnoutDefinition(ctx context.Context, turnout *model.Turnout) error {
	endpointRows, err := s.DB.QueryContext(ctx, `SELECT endpoint_id,linear_address,inverted FROM turnout_endpoints WHERE turnout_id=? ORDER BY ordinal,endpoint_id`, turnout.ID)
	if err != nil {
		return err
	}
	for endpointRows.Next() {
		var endpoint model.AccessoryEndpoint
		var inverted int
		if err := endpointRows.Scan(&endpoint.ID, &endpoint.LinearAddress, &inverted); err != nil {
			endpointRows.Close()
			return err
		}
		endpoint.Inverted = inverted != 0
		turnout.Endpoints = append(turnout.Endpoints, endpoint)
	}
	if err := endpointRows.Err(); err != nil {
		endpointRows.Close()
		return err
	}
	endpointRows.Close()

	positionRows, err := s.DB.QueryContext(ctx, `SELECT position_id,label FROM turnout_positions WHERE turnout_id=? ORDER BY ordinal,position_id`, turnout.ID)
	if err != nil {
		return err
	}
	for positionRows.Next() {
		var position model.TurnoutPositionDefinition
		if err := positionRows.Scan(&position.ID, &position.Label); err != nil {
			positionRows.Close()
			return err
		}
		position.Endpoints = map[string]model.AccessoryPosition{}
		turnout.Positions = append(turnout.Positions, position)
	}
	if err := positionRows.Err(); err != nil {
		positionRows.Close()
		return err
	}
	positionRows.Close()
	for i := range turnout.Positions {
		vectorRows, err := s.DB.QueryContext(ctx, `SELECT endpoint_id,required_position FROM turnout_position_endpoints WHERE turnout_id=? AND position_id=? ORDER BY endpoint_id`, turnout.ID, turnout.Positions[i].ID)
		if err != nil {
			return err
		}
		for vectorRows.Next() {
			var endpointID string
			var position model.AccessoryPosition
			if err := vectorRows.Scan(&endpointID, &position); err != nil {
				vectorRows.Close()
				return err
			}
			turnout.Positions[i].Endpoints[endpointID] = position
		}
		if err := vectorRows.Err(); err != nil {
			vectorRows.Close()
			return err
		}
		vectorRows.Close()
	}
	return nil
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
		{`INSERT OR IGNORE INTO turnouts(id,name,dcc_address,desired_state,reported_state,kind,desired_position,reported_position,pending,reported_status,quality,command_status) VALUES(?,?,?,?,?,'simple','straight','straight',0,'known','assumed','succeeded')`, []any{"turnout-1", "Aiguille entrée", 1, "straight", "straight"}},
		{`INSERT OR IGNORE INTO turnout_endpoints(turnout_id,endpoint_id,linear_address,inverted,ordinal) SELECT 'turnout-1','main',1,0,0 WHERE NOT EXISTS(SELECT 1 FROM turnout_endpoints WHERE turnout_id='turnout-1')`, nil},
		{`INSERT OR IGNORE INTO turnout_positions(turnout_id,position_id,label,ordinal) SELECT 'turnout-1','straight','',0 WHERE EXISTS(SELECT 1 FROM turnouts t JOIN turnout_endpoints e ON e.turnout_id=t.id WHERE t.id='turnout-1' AND t.kind='simple' AND e.endpoint_id='main')`, nil},
		{`INSERT OR IGNORE INTO turnout_positions(turnout_id,position_id,label,ordinal) SELECT 'turnout-1','diverging','',1 WHERE EXISTS(SELECT 1 FROM turnouts t JOIN turnout_endpoints e ON e.turnout_id=t.id WHERE t.id='turnout-1' AND t.kind='simple' AND e.endpoint_id='main')`, nil},
		{`INSERT OR IGNORE INTO turnout_position_endpoints(turnout_id,position_id,endpoint_id,required_position) SELECT 'turnout-1','straight','main','position1' WHERE EXISTS(SELECT 1 FROM turnout_positions WHERE turnout_id='turnout-1' AND position_id='straight') AND EXISTS(SELECT 1 FROM turnout_endpoints WHERE turnout_id='turnout-1' AND endpoint_id='main')`, nil},
		{`INSERT OR IGNORE INTO turnout_position_endpoints(turnout_id,position_id,endpoint_id,required_position) SELECT 'turnout-1','diverging','main','position2' WHERE EXISTS(SELECT 1 FROM turnout_positions WHERE turnout_id='turnout-1' AND position_id='diverging') AND EXISTS(SELECT 1 FROM turnout_endpoints WHERE turnout_id='turnout-1' AND endpoint_id='main')`, nil},
		{`INSERT OR IGNORE INTO routes(id,name,state,reserved_by_session) VALUES(?,?,?,?)`, []any{"route-a-b", "Voie 1 vers pleine voie", "idle", ""}},
		{`INSERT OR IGNORE INTO route_blocks(route_id,block_id) VALUES(?,?)`, []any{"route-a-b", "block-a"}},
		{`INSERT OR IGNORE INTO route_blocks(route_id,block_id) VALUES(?,?)`, []any{"route-a-b", "block-b"}},
		{`INSERT OR IGNORE INTO route_turnouts(route_id,turnout_id,required_state) SELECT 'route-a-b','turnout-1','straight' WHERE EXISTS(SELECT 1 FROM turnout_positions WHERE turnout_id='turnout-1' AND position_id='straight')`, nil},
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
