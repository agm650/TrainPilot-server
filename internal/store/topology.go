package store

import (
	"context"
	"fmt"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/sqlite"
)

func (s *Store) ListTopologyNodes(ctx context.Context) ([]model.TopologyNode, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name,kind FROM topology_nodes ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var nodes []model.TopologyNode
	for rows.Next() {
		var node model.TopologyNode
		if err := rows.Scan(&node.ID, &node.Name, &node.Kind); err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

func (s *Store) ListTrackSections(ctx context.Context) ([]model.TrackSection, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name,node_a_id,node_b_id,length_mm FROM track_sections ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sections []model.TrackSection
	for rows.Next() {
		var section model.TrackSection
		if err := rows.Scan(&section.ID, &section.Name, &section.NodeAID, &section.NodeBID, &section.LengthMM); err != nil {
			return nil, err
		}
		sections = append(sections, section)
	}
	return sections, rows.Err()
}

func (s *Store) ListTurnoutTopologies(ctx context.Context) ([]model.TurnoutTopology, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT turnout_id FROM turnout_topologies ORDER BY turnout_id`)
	if err != nil {
		return nil, err
	}
	var topologies []model.TurnoutTopology
	indexes := map[string]int{}
	for rows.Next() {
		var topology model.TurnoutTopology
		if err := rows.Scan(&topology.TurnoutID); err != nil {
			rows.Close()
			return nil, err
		}
		indexes[topology.TurnoutID] = len(topologies)
		topologies = append(topologies, topology)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	rows, err = s.DB.QueryContext(ctx, `SELECT turnout_id,port_id,node_id FROM turnout_topology_ports ORDER BY turnout_id,ordinal`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var turnoutID string
		var port model.TurnoutPort
		if err := rows.Scan(&turnoutID, &port.ID, &port.NodeID); err != nil {
			rows.Close()
			return nil, err
		}
		index, exists := indexes[turnoutID]
		if !exists {
			rows.Close()
			return nil, fmt.Errorf("turnout topology port references unknown topology %q", turnoutID)
		}
		topologies[index].Ports = append(topologies[index].Ports, port)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	positionIndexes := map[topologyPositionKey]int{}
	rows, err = s.DB.QueryContext(ctx, `SELECT turnout_id,position_id FROM turnout_topology_positions ORDER BY turnout_id,ordinal`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var turnoutID string
		var position model.TurnoutTopologyPosition
		if err := rows.Scan(&turnoutID, &position.PositionID); err != nil {
			rows.Close()
			return nil, err
		}
		index, exists := indexes[turnoutID]
		if !exists {
			rows.Close()
			return nil, fmt.Errorf("turnout topology position references unknown topology %q", turnoutID)
		}
		positionIndexes[topologyPositionKey{turnoutID: turnoutID, positionID: position.PositionID}] = len(topologies[index].Positions)
		topologies[index].Positions = append(topologies[index].Positions, position)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	rows, err = s.DB.QueryContext(ctx, `SELECT turnout_id,position_id,port_a_id,port_b_id FROM turnout_topology_connections ORDER BY turnout_id,position_id,ordinal`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var turnoutID, positionID string
		var connection model.PortConnection
		if err := rows.Scan(&turnoutID, &positionID, &connection.PortAID, &connection.PortBID); err != nil {
			return nil, err
		}
		topologyIndex, exists := indexes[turnoutID]
		if !exists {
			return nil, fmt.Errorf("turnout topology connection references unknown topology %q", turnoutID)
		}
		positionIndex, exists := positionIndexes[topologyPositionKey{turnoutID: turnoutID, positionID: positionID}]
		if !exists {
			return nil, fmt.Errorf("turnout topology connection references unknown position %q on turnout %q", positionID, turnoutID)
		}
		topologies[topologyIndex].Positions[positionIndex].Connections = append(
			topologies[topologyIndex].Positions[positionIndex].Connections,
			connection,
		)
	}
	return topologies, rows.Err()
}

func (s *Store) GetTopologyDefinition(ctx context.Context) (model.LayoutDefinition, error) {
	nodes, err := s.ListTopologyNodes(ctx)
	if err != nil {
		return model.LayoutDefinition{}, err
	}
	sections, err := s.ListTrackSections(ctx)
	if err != nil {
		return model.LayoutDefinition{}, err
	}
	turnoutTopologies, err := s.ListTurnoutTopologies(ctx)
	if err != nil {
		return model.LayoutDefinition{}, err
	}
	return model.LayoutDefinition{
		TopologyNodes:     nodes,
		TrackSections:     sections,
		TurnoutTopologies: turnoutTopologies,
	}, nil
}

func (s *Store) ReplaceTopologyDefinition(ctx context.Context, definition model.LayoutDefinition) error {
	turnouts, err := s.ListTurnouts(ctx)
	if err != nil {
		return err
	}
	validated := definition
	validated.Turnouts = turnouts
	if err := model.ValidateTopologyDefinition(validated); err != nil {
		return err
	}
	return s.DB.WithTransaction(ctx, func(tx *sqlite.Tx) error {
		if err := clearTopologyDefinition(ctx, tx); err != nil {
			return err
		}
		return upsertTopologyDefinition(ctx, tx, definition)
	})
}

func clearTopologyDefinition(ctx context.Context, tx *sqlite.Tx) error {
	for _, query := range []string{
		`DELETE FROM track_sections`,
		`DELETE FROM turnout_topologies`,
		`DELETE FROM topology_nodes`,
	} {
		if _, err := tx.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	return nil
}

func upsertTopologyDefinition(ctx context.Context, tx *sqlite.Tx, definition model.LayoutDefinition) error {
	for _, node := range definition.TopologyNodes {
		if _, err := tx.ExecContext(ctx, `INSERT INTO topology_nodes(id,name,kind) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,kind=excluded.kind`, node.ID, node.Name, node.Kind); err != nil {
			return fmt.Errorf("store topology node %q: %w", node.ID, err)
		}
	}
	for _, section := range definition.TrackSections {
		if _, err := tx.ExecContext(ctx, `INSERT INTO track_sections(id,name,node_a_id,node_b_id,length_mm) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,node_a_id=excluded.node_a_id,node_b_id=excluded.node_b_id,length_mm=excluded.length_mm`, section.ID, section.Name, section.NodeAID, section.NodeBID, section.LengthMM); err != nil {
			return fmt.Errorf("store track section %q: %w", section.ID, err)
		}
	}
	for _, topology := range definition.TurnoutTopologies {
		if _, err := tx.ExecContext(ctx, `INSERT INTO turnout_topologies(turnout_id) VALUES(?) ON CONFLICT(turnout_id) DO NOTHING`, topology.TurnoutID); err != nil {
			return fmt.Errorf("store topology for turnout %q: %w", topology.TurnoutID, err)
		}
		for _, query := range []string{
			`DELETE FROM turnout_topology_connections WHERE turnout_id=?`,
			`DELETE FROM turnout_topology_positions WHERE turnout_id=?`,
			`DELETE FROM turnout_topology_ports WHERE turnout_id=?`,
		} {
			if _, err := tx.ExecContext(ctx, query, topology.TurnoutID); err != nil {
				return fmt.Errorf("replace topology for turnout %q: %w", topology.TurnoutID, err)
			}
		}
		for ordinal, port := range topology.Ports {
			if _, err := tx.ExecContext(ctx, `INSERT INTO turnout_topology_ports(turnout_id,port_id,node_id,ordinal) VALUES(?,?,?,?)`, topology.TurnoutID, port.ID, port.NodeID, ordinal); err != nil {
				return fmt.Errorf("store turnout %q topology port %q: %w", topology.TurnoutID, port.ID, err)
			}
		}
		for positionOrdinal, position := range topology.Positions {
			if _, err := tx.ExecContext(ctx, `INSERT INTO turnout_topology_positions(turnout_id,position_id,ordinal) VALUES(?,?,?)`, topology.TurnoutID, position.PositionID, positionOrdinal); err != nil {
				return fmt.Errorf("store turnout %q topology position %q: %w", topology.TurnoutID, position.PositionID, err)
			}
			for connectionOrdinal, connection := range position.Connections {
				if _, err := tx.ExecContext(ctx, `INSERT INTO turnout_topology_connections(turnout_id,position_id,port_a_id,port_b_id,ordinal) VALUES(?,?,?,?,?)`, topology.TurnoutID, position.PositionID, connection.PortAID, connection.PortBID, connectionOrdinal); err != nil {
					return fmt.Errorf("store turnout %q topology position %q connection: %w", topology.TurnoutID, position.PositionID, err)
				}
			}
		}
	}
	return nil
}

type topologyPositionKey struct {
	turnoutID  string
	positionID string
}
