package store

import (
	"context"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/sqlite"
)

func deleteUnretainedSignalSymbols(ctx context.Context, tx *sqlite.Tx, signals []model.SignalDefinition) error {
	retained := map[string]bool{}
	for _, signal := range signals {
		retained[signal.ID] = true
	}
	rows, err := tx.QueryContext(ctx, `SELECT signal_id FROM layout_signal_positions`)
	if err != nil {
		return err
	}
	remove := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if !retained[id] {
			remove = append(remove, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range remove {
		if _, err := tx.ExecContext(ctx, `DELETE FROM layout_signal_positions WHERE signal_id=?`, id); err != nil {
			return err
		}
	}
	return nil
}
