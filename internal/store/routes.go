package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/sqlite"
	"github.com/agm650/TrainPilot-server/internal/topology"
)

func (s *Store) ListRoutes(ctx context.Context) (out []model.Route, err error) {
	started := time.Now()
	defer func() { s.observe("list_routes", started, err) }()
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name,state,reserved_by_session FROM routes ORDER BY name,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r model.Route
		if err := rows.Scan(&r.ID, &r.Name, &r.State, &r.ReservedBySession); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	err = rows.Err()
	return out, err
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
func (s *Store) RouteBlockIDs(ctx context.Context, id string) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT block_id FROM route_blocks WHERE route_id=? ORDER BY block_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var blockID string
		if err := rows.Scan(&blockID); err != nil {
			return nil, err
		}
		ids = append(ids, blockID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		if _, err := s.GetRoute(ctx, id); err != nil {
			return nil, err
		}
	}
	return ids, nil
}
func (s *Store) RouteHasActiveConflict(ctx context.Context, id string) (bool, error) {
	var count int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM route_conflicts rc JOIN routes r ON r.id=rc.conflict_route_id WHERE rc.route_id=? AND r.state IN ('reserved','active')`, id).Scan(&count)
	return count > 0, err
}
func (s *Store) ValidateRouteActivation(ctx context.Context, id, sessionID string) error {
	var state, reservedBySession string
	var conflict int
	err := s.DB.QueryRowContext(ctx, `
		SELECT r.state,
		       r.reserved_by_session,
		       EXISTS (
		           SELECT 1
		           FROM route_conflicts rc
		           JOIN routes conflicting ON conflicting.id=rc.conflict_route_id
		           WHERE rc.route_id=r.id AND conflicting.state IN ('reserved','active')
		       )
		FROM routes r
		WHERE r.id=?`, id).Scan(&state, &reservedBySession, &conflict)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if state != "reserved" || reservedBySession != sessionID {
		return ErrNotFound
	}
	if conflict != 0 {
		return ErrRouteConflict
	}
	return nil
}
func (s *Store) ReserveRoute(ctx context.Context, id, sessionID string) (err error) {
	started := time.Now()
	defer func() { s.observe("reserve_route", started, err) }()
	res, err := s.DB.ExecContext(ctx, `UPDATE routes SET state='reserved',reserved_by_session=? WHERE id=? AND state='idle'`, sessionID, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		err = ErrConflict
		return err
	}
	return nil
}
func (s *Store) ActivateRoute(ctx context.Context, id, sessionID string) (err error) {
	started := time.Now()
	defer func() { s.observe("activate_route", started, err) }()
	res, err := s.DB.ExecContext(ctx, `UPDATE routes SET state='active' WHERE id=? AND state='reserved' AND reserved_by_session=?`, id, sessionID)
	if err != nil {
		return err
	}
	err = requireAffected(res)
	return err
}
func (s *Store) ReleaseRoute(ctx context.Context, id, sessionID string) (err error) {
	started := time.Now()
	defer func() { s.observe("release_route", started, err) }()
	res, err := s.DB.ExecContext(ctx, `UPDATE routes SET state='idle',reserved_by_session='' WHERE id=? AND reserved_by_session=?`, id, sessionID)
	if err != nil {
		return err
	}
	err = requireAffected(res)
	return err
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
	blocks, err := s.ListBlockDefinitions(ctx)
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
	providers, err := s.ListOccupancyProviders(ctx)
	if err != nil {
		return model.LayoutDefinition{}, err
	}
	occupancyMappings, err := s.ListOccupancySensorMappings(ctx)
	if err != nil {
		return model.LayoutDefinition{}, err
	}
	topology, err := s.GetTopologyDefinition(ctx)
	if err != nil {
		return model.LayoutDefinition{}, err
	}
	presentation, err := s.GetLayoutPresentation(ctx)
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
		if err := s.DB.QueryRowContext(ctx, `SELECT entry_node_id,exit_node_id FROM routes WHERE id=?`, route.ID).Scan(&def.EntryNodeID, &def.ExitNodeID); err != nil {
			return model.LayoutDefinition{}, err
		}
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
	return model.LayoutDefinition{
		Presentation:            &presentation,
		Blocks:                  blocks,
		Turnouts:                turnouts,
		Routes:                  defs,
		FeedbackMappings:        mappings,
		OccupancyProviders:      providers,
		OccupancySensorMappings: occupancyMappings,
		TopologyNodes:           topology.TopologyNodes,
		TrackSections:           topology.TrackSections,
		TurnoutTopologies:       topology.TurnoutTopologies,
	}, nil
}

func (s *Store) ImportLayout(ctx context.Context, layout model.LayoutDefinition, replace bool) error {
	normalizedTurnouts := make([]model.Turnout, len(layout.Turnouts))
	for index, turnout := range layout.Turnouts {
		normalized, err := model.NormalizeTurnout(turnout)
		if err != nil {
			return err
		}
		normalizedTurnouts[index] = normalized
	}
	validatedLayout := layout
	validatedLayout.Turnouts = normalizedTurnouts
	graph, err := topology.Build(validatedLayout)
	if err != nil {
		return err
	}
	if err := topology.RouteValidationErrors(topology.ValidateRouteDefinitions(graph, layout.Routes, normalizedTurnouts)); err != nil {
		return err
	}
	if err := validateTurnoutAddressOwnership(normalizedTurnouts); err != nil {
		return err
	}

	return s.DB.WithTransaction(ctx, func(tx *sqlite.Tx) error {
		currentResources, err := layoutPresentationResources(ctx, tx)
		if err != nil {
			return err
		}
		resources, err := effectivePresentationResources(currentResources, validatedLayout, replace)
		if err != nil {
			return err
		}
		currentPresentation, err := readLayoutPresentation(ctx, tx)
		if err != nil {
			return err
		}
		presentation, err := effectiveLayoutPresentation(currentPresentation, layout.Presentation, replace)
		if err != nil {
			return err
		}
		if err := model.ValidateLayoutPresentation(presentation, resources); err != nil {
			return err
		}
		if err := rejectPendingTurnoutConfiguration(ctx, tx, normalizedTurnouts, replace); err != nil {
			return err
		}
		if err := rejectOmittedBlockResourceChanges(ctx, tx, layout, replace); err != nil {
			return err
		}
		if !replace {
			if err := rejectExistingTurnoutAddressConflicts(ctx, tx, normalizedTurnouts); err != nil {
				return err
			}
		}
		if replace {
			if err := clearTopologyDefinition(ctx, tx); err != nil {
				return err
			}
			for _, q := range []string{`DELETE FROM route_conflicts`, `DELETE FROM route_turnouts`, `DELETE FROM route_blocks`, `DELETE FROM routes`, `DELETE FROM occupancy_sensor_mappings`, `DELETE FROM turnouts`, `DELETE FROM blocks`} {
				if _, err := tx.ExecContext(ctx, q); err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM occupancy_providers`); err != nil {
				return err
			}
		}
		for _, provider := range layout.OccupancyProviders {
			if err := model.ValidateOccupancyProvider(provider); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO occupancy_providers(id,type,priority,required,stale_after_ns,freshness_required)
				VALUES(?,?,?,?,?,?)
				ON CONFLICT(id) DO UPDATE SET type=excluded.type,priority=excluded.priority,required=excluded.required,
					stale_after_ns=excluded.stale_after_ns,freshness_required=excluded.freshness_required`,
				provider.ID, provider.Type, provider.Priority, boolInt(provider.Required), int64(provider.StaleAfter), boolInt(provider.FreshnessRequired)); err != nil {
				return err
			}
		}
		for _, b := range layout.Blocks {
			if _, err := tx.ExecContext(ctx, `INSERT INTO blocks(id,name,occupied) VALUES(?,?,0) ON CONFLICT(id) DO UPDATE SET name=excluded.name`, b.ID, b.Name); err != nil {
				return err
			}
		}
		for _, turnout := range normalizedTurnouts {
			if err := upsertTurnout(ctx, tx, turnout); err != nil {
				return err
			}
		}
		if err := upsertTopologyDefinition(ctx, tx, layout); err != nil {
			return err
		}
		if err := replaceBlockMemberships(ctx, tx, layout.Blocks); err != nil {
			return err
		}
		if layout.Presentation != nil || replace {
			if err := replaceLayoutPresentation(ctx, tx, presentation); err != nil {
				return err
			}
		}
		for _, m := range layout.FeedbackMappings {
			providerType := "current-detection"
			if m.Provider == "simulator" {
				providerType = "simulator"
			}
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO occupancy_providers(id,type,priority,required,stale_after_ns,freshness_required) VALUES(?,?,100,1,0,0)`, m.Provider, providerType); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO occupancy_sensor_mappings(provider_id,sensor_id,block_id) VALUES(?,CAST(? AS TEXT),?) ON CONFLICT(provider_id,sensor_id) DO UPDATE SET block_id=excluded.block_id`, m.Provider, m.Address, m.BlockID); err != nil {
				return err
			}
		}
		for _, mapping := range layout.OccupancySensorMappings {
			if err := model.ValidateOccupancySensorMapping(mapping); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO occupancy_sensor_mappings(provider_id,sensor_id,block_id,required,priority)
				VALUES(?,?,?,?,?)
				ON CONFLICT(provider_id,sensor_id) DO UPDATE SET block_id=excluded.block_id,required=excluded.required,priority=excluded.priority`,
				mapping.ProviderID, mapping.SensorID, mapping.BlockID, nullableBool(mapping.Required), nullableInt(mapping.Priority)); err != nil {
				return err
			}
		}
		// First pass: create every route so conflict foreign keys can resolve.
		for _, r := range layout.Routes {
			if _, err := tx.ExecContext(ctx, `INSERT INTO routes(id,name,state,reserved_by_session,entry_node_id,exit_node_id) VALUES(?,?,'idle','',?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,state='idle',reserved_by_session='',entry_node_id=excluded.entry_node_id,exit_node_id=excluded.exit_node_id`, r.ID, r.Name, r.EntryNodeID, r.ExitNodeID); err != nil {
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

func rejectOmittedBlockResourceChanges(ctx context.Context, tx *sqlite.Tx, layout model.LayoutDefinition, replace bool) error {
	if replace {
		return nil
	}
	includedBlocks := make(map[string]bool, len(layout.Blocks))
	for _, block := range layout.Blocks {
		includedBlocks[block.ID] = true
	}
	for _, section := range layout.TrackSections {
		owner, exists, err := blockOwnerForResource(ctx, tx, `SELECT block_id FROM block_track_sections WHERE track_section_id=?`, section.ID)
		if err != nil {
			return err
		}
		if exists && !includedBlocks[owner] {
			return fmt.Errorf("%w: track section %q belongs to omitted block %q", ErrConflict, section.ID, owner)
		}
	}
	for _, turnout := range layout.TurnoutTopologies {
		owner, exists, err := blockOwnerForResource(ctx, tx, `SELECT block_id FROM block_turnouts WHERE turnout_id=?`, turnout.TurnoutID)
		if err != nil {
			return err
		}
		if exists && !includedBlocks[owner] {
			return fmt.Errorf("%w: turnout %q belongs to omitted block %q", ErrConflict, turnout.TurnoutID, owner)
		}
	}
	return nil
}

func blockOwnerForResource(ctx context.Context, tx *sqlite.Tx, query, resourceID string) (string, bool, error) {
	rows, err := tx.QueryContext(ctx, query, resourceID)
	if err != nil {
		return "", false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return "", false, rows.Err()
	}
	var owner string
	if err := rows.Scan(&owner); err != nil {
		return "", false, err
	}
	return owner, true, rows.Err()
}

func replaceBlockMemberships(ctx context.Context, tx *sqlite.Tx, blocks []model.BlockDefinition) error {
	for _, block := range blocks {
		for _, query := range []string{
			`DELETE FROM block_track_sections WHERE block_id=?`,
			`DELETE FROM block_turnouts WHERE block_id=?`,
		} {
			if _, err := tx.ExecContext(ctx, query, block.ID); err != nil {
				return err
			}
		}
	}
	for _, block := range blocks {
		for _, sectionID := range block.TrackSectionIDs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO block_track_sections(block_id,track_section_id) VALUES(?,?)`, block.ID, sectionID); err != nil {
				return fmt.Errorf("store block %q track section %q: %w", block.ID, sectionID, err)
			}
		}
		for _, turnoutID := range block.TurnoutIDs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO block_turnouts(block_id,turnout_id) VALUES(?,?)`, block.ID, turnoutID); err != nil {
				return fmt.Errorf("store block %q turnout %q: %w", block.ID, turnoutID, err)
			}
		}
	}
	return nil
}

func validateTurnoutAddressOwnership(turnouts []model.Turnout) error {
	owners := make(map[int]string)
	for _, turnout := range turnouts {
		for _, endpoint := range turnout.Endpoints {
			if owner, exists := owners[endpoint.LinearAddress]; exists && owner != turnout.ID {
				return fmt.Errorf("%w: linear address %d is assigned to turnouts %q and %q", ErrAccessoryAddressConflict, endpoint.LinearAddress, owner, turnout.ID)
			}
			owners[endpoint.LinearAddress] = turnout.ID
		}
	}
	return nil
}

func rejectPendingTurnoutConfiguration(ctx context.Context, tx *sqlite.Tx, turnouts []model.Turnout, replace bool) error {
	updated := make(map[string]bool, len(turnouts))
	for _, turnout := range turnouts {
		updated[turnout.ID] = true
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM turnouts WHERE pending<>0 ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		if replace || updated[id] {
			return fmt.Errorf("%w: turnout %q", ErrTurnoutConfigurationPending, id)
		}
	}
	return rows.Err()
}

func rejectExistingTurnoutAddressConflicts(ctx context.Context, tx *sqlite.Tx, turnouts []model.Turnout) error {
	replaced := make(map[string]bool, len(turnouts))
	for _, turnout := range turnouts {
		replaced[turnout.ID] = true
	}
	owners := make(map[int]string)
	rows, err := tx.QueryContext(ctx, `SELECT turnout_id,linear_address FROM turnout_endpoints ORDER BY linear_address,turnout_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var owner string
		var address int
		if err := rows.Scan(&owner, &address); err != nil {
			return err
		}
		if replaced[owner] {
			continue
		}
		if previous, exists := owners[address]; exists && previous != owner {
			return fmt.Errorf("%w: linear address %d is assigned to turnouts %q and %q", ErrAccessoryAddressConflict, address, previous, owner)
		}
		owners[address] = owner
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, turnout := range turnouts {
		for _, endpoint := range turnout.Endpoints {
			if owner, exists := owners[endpoint.LinearAddress]; exists && owner != turnout.ID {
				return fmt.Errorf("%w: linear address %d is assigned to turnouts %q and %q", ErrAccessoryAddressConflict, endpoint.LinearAddress, owner, turnout.ID)
			}
			owners[endpoint.LinearAddress] = turnout.ID
		}
	}
	return nil
}

func upsertTurnout(ctx context.Context, tx *sqlite.Tx, turnout model.Turnout) error {
	legacyAddress := turnout.Endpoints[0].LinearAddress
	legacyDesired := legacyState(turnout.DesiredPosition)
	legacyReported := legacyState(turnout.ReportedPosition)
	if _, err := tx.ExecContext(ctx, `INSERT INTO turnouts(id,name,dcc_address,desired_state,reported_state,kind,desired_position,reported_position,pending,reported_status,quality,command_status) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,dcc_address=excluded.dcc_address,desired_state=excluded.desired_state,reported_state=excluded.reported_state,kind=excluded.kind,desired_position=excluded.desired_position,reported_position=excluded.reported_position,pending=excluded.pending,reported_status=excluded.reported_status,quality=excluded.quality,command_status=excluded.command_status`, turnout.ID, turnout.Name, legacyAddress, legacyDesired, legacyReported, turnout.Kind, turnout.DesiredPosition, turnout.ReportedPosition, boolInt(turnout.Pending), turnout.ReportedStatus, turnout.Quality, turnout.CommandStatus); err != nil {
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
