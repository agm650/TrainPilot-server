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

// GetLayoutPresentation returns a stable, empty definition for older layouts.
func (s *Store) GetLayoutPresentation(ctx context.Context) (model.LayoutPresentation, error) {
	p := model.EmptyLayoutPresentation()
	err := s.DB.QueryRowContext(ctx, `SELECT coordinate_system,grid_spacing FROM layout_presentation WHERE id=1`).Scan(&p.CoordinateSystem, &p.GridSpacing)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return model.LayoutPresentation{}, fmt.Errorf("read layout presentation: %w", err)
	}

	rows, err := s.DB.QueryContext(ctx, `SELECT node_id,x,y FROM layout_node_positions ORDER BY node_id`)
	if err != nil {
		return model.LayoutPresentation{}, err
	}
	for rows.Next() {
		var node model.LayoutNodePosition
		if err := rows.Scan(&node.NodeID, &node.X, &node.Y); err != nil {
			rows.Close()
			return model.LayoutPresentation{}, err
		}
		p.Nodes = append(p.Nodes, node)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return model.LayoutPresentation{}, err
	}
	rows.Close()

	rows, err = s.DB.QueryContext(ctx, `SELECT track_section_id,segments_json FROM layout_track_paths ORDER BY track_section_id`)
	if err != nil {
		return model.LayoutPresentation{}, err
	}
	for rows.Next() {
		var path model.LayoutTrackPath
		var encoded string
		if err := rows.Scan(&path.TrackSectionID, &encoded); err != nil {
			rows.Close()
			return model.LayoutPresentation{}, err
		}
		if err := json.Unmarshal([]byte(encoded), &path.Segments); err != nil {
			rows.Close()
			return model.LayoutPresentation{}, fmt.Errorf("decode track path %q: %w", path.TrackSectionID, err)
		}
		p.TrackSections = append(p.TrackSections, path)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return model.LayoutPresentation{}, err
	}
	rows.Close()

	rows, err = s.DB.QueryContext(ctx, `SELECT turnout_id,x,y,rotation_degrees,mirrored FROM layout_turnout_positions ORDER BY turnout_id`)
	if err != nil {
		return model.LayoutPresentation{}, err
	}
	for rows.Next() {
		var turnout model.LayoutTurnoutPosition
		var mirrored int
		if err := rows.Scan(&turnout.TurnoutID, &turnout.X, &turnout.Y, &turnout.RotationDegrees, &mirrored); err != nil {
			rows.Close()
			return model.LayoutPresentation{}, err
		}
		turnout.Mirrored = mirrored != 0
		p.Turnouts = append(p.Turnouts, turnout)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return model.LayoutPresentation{}, err
	}
	rows.Close()

	rows, err = s.DB.QueryContext(ctx, `SELECT block_id,color,opacity FROM layout_block_styles ORDER BY block_id`)
	if err != nil {
		return model.LayoutPresentation{}, err
	}
	for rows.Next() {
		var block model.LayoutBlockStyle
		if err := rows.Scan(&block.BlockID, &block.Color, &block.Opacity); err != nil {
			rows.Close()
			return model.LayoutPresentation{}, err
		}
		p.Blocks = append(p.Blocks, block)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return model.LayoutPresentation{}, err
	}
	rows.Close()
	return p, nil
}

// ReplaceLayoutPresentation replaces the drawing without changing topology or
// runtime state. Validation and replacement share one transaction.
func (s *Store) ReplaceLayoutPresentation(ctx context.Context, p model.LayoutPresentation) error {
	return s.DB.WithTransaction(ctx, func(tx *sqlite.Tx) error {
		return replaceLayoutPresentation(ctx, tx, p)
	})
}

func replaceLayoutPresentation(ctx context.Context, tx *sqlite.Tx, p model.LayoutPresentation) error {
	resources, err := layoutPresentationResources(ctx, tx)
	if err != nil {
		return err
	}
	p = model.NormalizeLayoutPresentation(p)
	if err := model.ValidateLayoutPresentation(p, resources); err != nil {
		return err
	}
	for _, table := range []string{"layout_track_paths", "layout_node_positions", "layout_turnout_positions", "layout_block_styles"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("clear %s: %w", table, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO layout_presentation(id,coordinate_system,grid_spacing) VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET coordinate_system=excluded.coordinate_system,grid_spacing=excluded.grid_spacing`, p.CoordinateSystem, p.GridSpacing); err != nil {
		return fmt.Errorf("store layout presentation metadata: %w", err)
	}
	for _, node := range p.Nodes {
		if _, err := tx.ExecContext(ctx, `INSERT INTO layout_node_positions(node_id,x,y) VALUES(?,?,?)`, node.NodeID, node.X, node.Y); err != nil {
			return fmt.Errorf("store node position %q: %w", node.NodeID, err)
		}
	}
	for _, path := range p.TrackSections {
		encoded, err := json.Marshal(path.Segments)
		if err != nil {
			return fmt.Errorf("encode track path %q: %w", path.TrackSectionID, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO layout_track_paths(track_section_id,segments_json) VALUES(?,?)`, path.TrackSectionID, string(encoded)); err != nil {
			return fmt.Errorf("store track path %q: %w", path.TrackSectionID, err)
		}
	}
	for _, turnout := range p.Turnouts {
		if _, err := tx.ExecContext(ctx, `INSERT INTO layout_turnout_positions(turnout_id,x,y,rotation_degrees,mirrored) VALUES(?,?,?,?,?)`, turnout.TurnoutID, turnout.X, turnout.Y, turnout.RotationDegrees, boolInt(turnout.Mirrored)); err != nil {
			return fmt.Errorf("store turnout position %q: %w", turnout.TurnoutID, err)
		}
	}
	for _, block := range p.Blocks {
		if _, err := tx.ExecContext(ctx, `INSERT INTO layout_block_styles(block_id,color,opacity) VALUES(?,?,?)`, block.BlockID, block.Color, block.Opacity); err != nil {
			return fmt.Errorf("store block style %q: %w", block.BlockID, err)
		}
	}
	return nil
}

func layoutPresentationResources(ctx context.Context, tx *sqlite.Tx) (model.LayoutDefinition, error) {
	var layout model.LayoutDefinition
	rows, err := tx.QueryContext(ctx, `SELECT id FROM topology_nodes`)
	if err != nil {
		return layout, err
	}
	for rows.Next() {
		var node model.TopologyNode
		if err := rows.Scan(&node.ID); err != nil {
			rows.Close()
			return layout, err
		}
		layout.TopologyNodes = append(layout.TopologyNodes, node)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return layout, err
	}
	rows.Close()

	rows, err = tx.QueryContext(ctx, `SELECT id,node_a_id,node_b_id FROM track_sections`)
	if err != nil {
		return layout, err
	}
	for rows.Next() {
		var section model.TrackSection
		if err := rows.Scan(&section.ID, &section.NodeAID, &section.NodeBID); err != nil {
			rows.Close()
			return layout, err
		}
		layout.TrackSections = append(layout.TrackSections, section)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return layout, err
	}
	rows.Close()

	rows, err = tx.QueryContext(ctx, `SELECT id FROM turnouts`)
	if err != nil {
		return layout, err
	}
	for rows.Next() {
		var turnout model.Turnout
		if err := rows.Scan(&turnout.ID); err != nil {
			rows.Close()
			return layout, err
		}
		layout.Turnouts = append(layout.Turnouts, turnout)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return layout, err
	}
	rows.Close()

	rows, err = tx.QueryContext(ctx, `SELECT id FROM blocks`)
	if err != nil {
		return layout, err
	}
	for rows.Next() {
		var block model.BlockDefinition
		if err := rows.Scan(&block.ID); err != nil {
			rows.Close()
			return layout, err
		}
		layout.Blocks = append(layout.Blocks, block)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return layout, err
	}
	rows.Close()
	return layout, nil
}
