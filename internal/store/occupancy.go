package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/agm650/TrainPilot-server/internal/model"
)

func (s *Store) SetOccupancyProvider(ctx context.Context, provider model.OccupancyProvider) error {
	if err := model.ValidateOccupancyProvider(provider); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO occupancy_providers(id,type,priority,required,stale_after_ns,freshness_required)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			type=excluded.type,
			priority=excluded.priority,
			required=excluded.required,
			stale_after_ns=excluded.stale_after_ns,
			freshness_required=excluded.freshness_required`,
		provider.ID, provider.Type, provider.Priority, boolInt(provider.Required), int64(provider.StaleAfter), boolInt(provider.FreshnessRequired))
	return err
}

func (s *Store) OccupancyProvider(ctx context.Context, id string) (model.OccupancyProvider, error) {
	var provider model.OccupancyProvider
	var required, freshnessRequired int
	var staleAfter int64
	err := s.DB.QueryRowContext(ctx, `
		SELECT id,type,priority,required,stale_after_ns,freshness_required
		FROM occupancy_providers WHERE id=?`, id).Scan(
		&provider.ID, &provider.Type, &provider.Priority, &required, &staleAfter, &freshnessRequired)
	if errors.Is(err, sql.ErrNoRows) {
		return model.OccupancyProvider{}, ErrNotFound
	}
	if err != nil {
		return model.OccupancyProvider{}, err
	}
	provider.Required = required != 0
	provider.StaleAfter = time.Duration(staleAfter)
	provider.FreshnessRequired = freshnessRequired != 0
	return provider, nil
}

func (s *Store) ListOccupancyProviders(ctx context.Context) ([]model.OccupancyProvider, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id,type,priority,required,stale_after_ns,freshness_required
		FROM occupancy_providers ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var providers []model.OccupancyProvider
	for rows.Next() {
		var provider model.OccupancyProvider
		var required, freshnessRequired int
		var staleAfter int64
		if err := rows.Scan(&provider.ID, &provider.Type, &provider.Priority, &required, &staleAfter, &freshnessRequired); err != nil {
			return nil, err
		}
		provider.Required = required != 0
		provider.StaleAfter = time.Duration(staleAfter)
		provider.FreshnessRequired = freshnessRequired != 0
		providers = append(providers, provider)
	}
	return providers, rows.Err()
}

func (s *Store) SetOccupancySensorMapping(ctx context.Context, mapping model.OccupancySensorMapping) error {
	if err := model.ValidateOccupancySensorMapping(mapping); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO occupancy_sensor_mappings(provider_id,sensor_id,block_id,required,priority)
		VALUES(?,?,?,?,?)
		ON CONFLICT(provider_id,sensor_id) DO UPDATE SET
			block_id=excluded.block_id,
			required=excluded.required,
			priority=excluded.priority`,
		mapping.ProviderID, mapping.SensorID, mapping.BlockID, nullableBool(mapping.Required), nullableInt(mapping.Priority))
	return err
}

func (s *Store) OccupancySensorMapping(ctx context.Context, providerID, sensorID string) (model.OccupancySensorMapping, error) {
	var mapping model.OccupancySensorMapping
	var required, priority sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `
		SELECT provider_id,sensor_id,block_id,required,priority
		FROM occupancy_sensor_mappings
		WHERE provider_id=? AND sensor_id=?`, providerID, sensorID).Scan(
		&mapping.ProviderID, &mapping.SensorID, &mapping.BlockID, &required, &priority)
	if errors.Is(err, sql.ErrNoRows) {
		return model.OccupancySensorMapping{}, ErrNotFound
	}
	if err != nil {
		return model.OccupancySensorMapping{}, err
	}
	mapping.Required = boolPointer(required)
	mapping.Priority = intPointer(priority)
	return mapping, nil
}

func (s *Store) ListOccupancySensorMappings(ctx context.Context) ([]model.OccupancySensorMapping, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT provider_id,sensor_id,block_id,required,priority
		FROM occupancy_sensor_mappings ORDER BY provider_id,sensor_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var mappings []model.OccupancySensorMapping
	for rows.Next() {
		var mapping model.OccupancySensorMapping
		var required, priority sql.NullInt64
		if err := rows.Scan(&mapping.ProviderID, &mapping.SensorID, &mapping.BlockID, &required, &priority); err != nil {
			return nil, err
		}
		mapping.Required = boolPointer(required)
		mapping.Priority = intPointer(priority)
		mappings = append(mappings, mapping)
	}
	return mappings, rows.Err()
}

func (s *Store) ResolveOccupancySensorMapping(ctx context.Context, providerID, sensorID string) (model.ResolvedOccupancySensorMapping, error) {
	provider, err := s.OccupancyProvider(ctx, providerID)
	if err != nil {
		return model.ResolvedOccupancySensorMapping{}, err
	}
	mapping, err := s.OccupancySensorMapping(ctx, providerID, sensorID)
	if err != nil {
		return model.ResolvedOccupancySensorMapping{}, err
	}
	return mapping.Resolve(provider)
}

func nullableBool(value *bool) any {
	if value == nil {
		return nil
	}
	return boolInt(*value)
}

func nullableInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func boolPointer(value sql.NullInt64) *bool {
	if !value.Valid {
		return nil
	}
	result := value.Int64 != 0
	return &result
}

func intPointer(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	result := int(value.Int64)
	return &result
}
