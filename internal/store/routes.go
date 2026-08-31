package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/sqlite"
)

func (s *Store) ListRoutes(ctx context.Context) ([]model.Route, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name,state,reserved_by_session FROM routes ORDER BY name,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Route
	for rows.Next() {
		var r model.Route
		if err := rows.Scan(&r.ID, &r.Name, &r.State, &r.ReservedBySession); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) GetRoute(ctx context.Context, id string) (model.Route, error) {
	var r model.Route
	err := s.DB.QueryRowContext(ctx, `SELECT id,name,state,reserved_by_session FROM routes WHERE id=?`, id).Scan(&r.ID, &r.Name, &r.State, &r.ReservedBySession)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}
func (s *Store) RouteBlocksOccupied(ctx context.Context, id string) (bool, error) {
	var count int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM route_blocks rb JOIN blocks b ON b.id=rb.block_id WHERE rb.route_id=? AND b.occupied=1`, id).Scan(&count)
	return count > 0, err
}
func (s *Store) RouteHasActiveConflict(ctx context.Context, id string) (bool, error) {
	var count int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM route_conflicts rc JOIN routes r ON r.id=rc.conflict_route_id WHERE rc.route_id=? AND r.state IN ('reserved','active')`, id).Scan(&count)
	return count > 0, err
}
func (s *Store) ReserveRoute(ctx context.Context, id, sessionID string) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE routes SET state='reserved',reserved_by_session=? WHERE id=? AND state='idle'`, sessionID, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrConflict
	}
	return nil
}
func (s *Store) ActivateRoute(ctx context.Context, id, sessionID string) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE routes SET state='active' WHERE id=? AND state='reserved' AND reserved_by_session=?`, id, sessionID)
	if err != nil {
		return err
	}
	return requireAffected(res)
}
func (s *Store) ReleaseRoute(ctx context.Context, id, sessionID string) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE routes SET state='idle',reserved_by_session='' WHERE id=? AND reserved_by_session=?`, id, sessionID)
	if err != nil {
		return err
	}
	return requireAffected(res)
}
func (s *Store) RouteTurnoutRequirements(ctx context.Context, id string) (map[string]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT turnout_id,required_state FROM route_turnouts WHERE route_id=?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var tid, state string
		if err := rows.Scan(&tid, &state); err != nil {
			return nil, err
		}
		out[tid] = state
	}
	return out, rows.Err()
}

func (s *Store) ExportLayout(ctx context.Context) (model.LayoutDefinition, error) {
	blocks, err := s.ListBlocks(ctx)
	if err != nil {
		return model.LayoutDefinition{}, err
	}
	turnouts, err := s.ListTurnouts(ctx)
	if err != nil {
		return model.LayoutDefinition{}, err
	}
	mappings, err := s.ListFeedbackMappings(ctx)
	if err != nil {
		return model.LayoutDefinition{}, err
	}
	routes, err := s.ListRoutes(ctx)
	if err != nil {
		return model.LayoutDefinition{}, err
	}
	defs := make([]model.RouteDefinition, 0, len(routes))
	for _, route := range routes {
		def := model.RouteDefinition{ID: route.ID, Name: route.Name, TurnoutStates: map[string]string{}}
		rows, err := s.DB.QueryContext(ctx, `SELECT block_id FROM route_blocks WHERE route_id=? ORDER BY block_id`, route.ID)
		if err != nil {
			return model.LayoutDefinition{}, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return model.LayoutDefinition{}, err
			}
			def.BlockIDs = append(def.BlockIDs, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return model.LayoutDefinition{}, err
		}
		rows.Close()
		def.TurnoutStates, err = s.RouteTurnoutRequirements(ctx, route.ID)
		if err != nil {
			return model.LayoutDefinition{}, err
		}
		rows, err = s.DB.QueryContext(ctx, `SELECT conflict_route_id FROM route_conflicts WHERE route_id=? ORDER BY conflict_route_id`, route.ID)
		if err != nil {
			return model.LayoutDefinition{}, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return model.LayoutDefinition{}, err
			}
			def.ConflictRouteIDs = append(def.ConflictRouteIDs, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return model.LayoutDefinition{}, err
		}
		rows.Close()
		defs = append(defs, def)
	}
	return model.LayoutDefinition{Blocks: blocks, Turnouts: turnouts, Routes: defs, FeedbackMappings: mappings}, nil
}

func (s *Store) ImportLayout(ctx context.Context, layout model.LayoutDefinition, replace bool) error {
	return s.DB.WithTransaction(ctx, func(tx *sqlite.Tx) error {
		if replace {
			for _, q := range []string{`DELETE FROM route_conflicts`, `DELETE FROM route_turnouts`, `DELETE FROM route_blocks`, `DELETE FROM routes`, `DELETE FROM feedback_mappings`, `DELETE FROM turnouts`, `DELETE FROM blocks`} {
				if _, err := tx.ExecContext(ctx, q); err != nil {
					return err
				}
			}
		}
		for _, b := range layout.Blocks {
			if _, err := tx.ExecContext(ctx, `INSERT INTO blocks(id,name,occupied) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,occupied=excluded.occupied`, b.ID, b.Name, boolInt(b.Occupied)); err != nil {
				return err
			}
		}
		for _, t := range layout.Turnouts {
			normalized, err := model.NormalizeTurnout(t)
			if err != nil {
				return err
			}
			if err := upsertTurnout(ctx, tx, normalized); err != nil {
				return err
			}
		}
		for _, m := range layout.FeedbackMappings {
			if _, err := tx.ExecContext(ctx, `INSERT INTO feedback_mappings(provider,address,block_id) VALUES(?,?,?) ON CONFLICT(provider,address) DO UPDATE SET block_id=excluded.block_id`, m.Provider, m.Address, m.BlockID); err != nil {
				return err
			}
		}
		// First pass: create every route so conflict foreign keys can resolve.
		for _, r := range layout.Routes {
			if _, err := tx.ExecContext(ctx, `INSERT INTO routes(id,name,state,reserved_by_session) VALUES(?,?,'idle','') ON CONFLICT(id) DO UPDATE SET name=excluded.name,state='idle',reserved_by_session=''`, r.ID, r.Name); err != nil {
				return err
			}
		}
		// Second pass: replace route relationships.
		for _, r := range layout.Routes {
			for _, q := range []string{`DELETE FROM route_blocks WHERE route_id=?`, `DELETE FROM route_turnouts WHERE route_id=?`, `DELETE FROM route_conflicts WHERE route_id=?`} {
				if _, err := tx.ExecContext(ctx, q, r.ID); err != nil {
					return err
				}
			}
			for _, blockID := range r.BlockIDs {
				if _, err := tx.ExecContext(ctx, `INSERT INTO route_blocks(route_id,block_id) VALUES(?,?)`, r.ID, blockID); err != nil {
					return err
				}
			}
			for turnoutID, state := range r.TurnoutStates {
				if _, err := tx.ExecContext(ctx, `INSERT INTO route_turnouts(route_id,turnout_id,required_state) VALUES(?,?,?)`, r.ID, turnoutID, state); err != nil {
					return err
				}
			}
			for _, conflictID := range r.ConflictRouteIDs {
				if _, err := tx.ExecContext(ctx, `INSERT INTO route_conflicts(route_id,conflict_route_id) VALUES(?,?)`, r.ID, conflictID); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func upsertTurnout(ctx context.Context, tx *sqlite.Tx, turnout model.Turnout) error {
	legacyAddress := turnout.Endpoints[0].LinearAddress
	legacyDesired := legacyState(turnout.DesiredPosition)
	legacyReported := legacyState(turnout.ReportedPosition)
	if _, err := tx.ExecContext(ctx, `INSERT INTO turnouts(id,name,dcc_address,desired_state,reported_state,kind,desired_position,reported_position,pending) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,dcc_address=excluded.dcc_address,desired_state=excluded.desired_state,reported_state=excluded.reported_state,kind=excluded.kind,desired_position=excluded.desired_position,reported_position=excluded.reported_position,pending=excluded.pending`, turnout.ID, turnout.Name, legacyAddress, legacyDesired, legacyReported, turnout.Kind, turnout.DesiredPosition, turnout.ReportedPosition, boolInt(turnout.Pending)); err != nil {
		return fmt.Errorf("store turnout %q: %w", turnout.ID, err)
	}
	for _, query := range []string{
		`DELETE FROM turnout_position_endpoints WHERE turnout_id=?`,
		`DELETE FROM turnout_positions WHERE turnout_id=?`,
		`DELETE FROM turnout_endpoints WHERE turnout_id=?`,
	} {
		if _, err := tx.ExecContext(ctx, query, turnout.ID); err != nil {
			return fmt.Errorf("replace turnout %q definition: %w", turnout.ID, err)
		}
	}
	for ordinal, endpoint := range turnout.Endpoints {
		if _, err := tx.ExecContext(ctx, `INSERT INTO turnout_endpoints(turnout_id,endpoint_id,linear_address,inverted,ordinal) VALUES(?,?,?,?,?)`, turnout.ID, endpoint.ID, endpoint.LinearAddress, boolInt(endpoint.Inverted), ordinal); err != nil {
			return fmt.Errorf("store turnout %q endpoint %q: %w", turnout.ID, endpoint.ID, err)
		}
	}
	for ordinal, position := range turnout.Positions {
		if _, err := tx.ExecContext(ctx, `INSERT INTO turnout_positions(turnout_id,position_id,label,ordinal) VALUES(?,?,?,?)`, turnout.ID, position.ID, position.Label, ordinal); err != nil {
			return fmt.Errorf("store turnout %q position %q: %w", turnout.ID, position.ID, err)
		}
		for endpointID, required := range position.Endpoints {
			if _, err := tx.ExecContext(ctx, `INSERT INTO turnout_position_endpoints(turnout_id,position_id,endpoint_id,required_position) VALUES(?,?,?,?)`, turnout.ID, position.ID, endpointID, required); err != nil {
				return fmt.Errorf("store turnout %q position %q endpoint %q: %w", turnout.ID, position.ID, endpointID, err)
			}
		}
	}
	return nil
}

func legacyState(position string) string {
	if position == "straight" || position == "diverging" {
		return position
	}
	return "unknown"
}
