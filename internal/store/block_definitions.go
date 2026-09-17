package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/agm650/TrainPilot-server/internal/model"
)

func (s *Store) ListBlockDefinitions(ctx context.Context) ([]model.BlockDefinition, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name FROM blocks ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var blocks []model.BlockDefinition
	indexes := map[string]int{}
	for rows.Next() {
		var block model.BlockDefinition
		if err := rows.Scan(&block.ID, &block.Name); err != nil {
			rows.Close()
			return nil, err
		}
		indexes[block.ID] = len(blocks)
		blocks = append(blocks, block)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	rows, err = s.DB.QueryContext(ctx, `SELECT block_id,track_section_id FROM block_track_sections ORDER BY block_id,track_section_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var blockID, sectionID string
		if err := rows.Scan(&blockID, &sectionID); err != nil {
			rows.Close()
			return nil, err
		}
		blocks[indexes[blockID]].TrackSectionIDs = append(blocks[indexes[blockID]].TrackSectionIDs, sectionID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	rows, err = s.DB.QueryContext(ctx, `SELECT block_id,turnout_id FROM block_turnouts ORDER BY block_id,turnout_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var blockID, turnoutID string
		if err := rows.Scan(&blockID, &turnoutID); err != nil {
			return nil, err
		}
		blocks[indexes[blockID]].TurnoutIDs = append(blocks[indexes[blockID]].TurnoutIDs, turnoutID)
	}
	return blocks, rows.Err()
}

func (s *Store) BlockForTrackSection(ctx context.Context, sectionID string) (model.BlockDefinition, error) {
	return s.blockForResource(ctx, `SELECT b.id,b.name FROM blocks b JOIN block_track_sections r ON r.block_id=b.id WHERE r.track_section_id=?`, sectionID)
}

func (s *Store) BlockForTurnout(ctx context.Context, turnoutID string) (model.BlockDefinition, error) {
	return s.blockForResource(ctx, `SELECT b.id,b.name FROM blocks b JOIN block_turnouts r ON r.block_id=b.id WHERE r.turnout_id=?`, turnoutID)
}

func (s *Store) ResourcesForBlock(ctx context.Context, blockID string) (model.BlockDefinition, error) {
	var block model.BlockDefinition
	err := s.DB.QueryRowContext(ctx, `SELECT id,name FROM blocks WHERE id=?`, blockID).Scan(&block.ID, &block.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return block, ErrNotFound
	}
	if err != nil {
		return block, err
	}
	return s.loadBlockResources(ctx, block)
}

func (s *Store) blockForResource(ctx context.Context, query, resourceID string) (model.BlockDefinition, error) {
	var block model.BlockDefinition
	err := s.DB.QueryRowContext(ctx, query, resourceID).Scan(&block.ID, &block.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return block, ErrNotFound
	}
	if err != nil {
		return block, err
	}
	return s.loadBlockResources(ctx, block)
}

func (s *Store) loadBlockResources(ctx context.Context, block model.BlockDefinition) (model.BlockDefinition, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT track_section_id FROM block_track_sections WHERE block_id=? ORDER BY track_section_id`, block.ID)
	if err != nil {
		return block, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return block, err
		}
		block.TrackSectionIDs = append(block.TrackSectionIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return block, err
	}
	rows.Close()

	rows, err = s.DB.QueryContext(ctx, `SELECT turnout_id FROM block_turnouts WHERE block_id=? ORDER BY turnout_id`, block.ID)
	if err != nil {
		return block, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return block, err
		}
		block.TurnoutIDs = append(block.TurnoutIDs, id)
	}
	return block, rows.Err()
}
