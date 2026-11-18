package store

import (
	"context"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/sqlite"
)

func validateStoredSignalLayout(ctx context.Context, tx *sqlite.Tx, profiles []model.SignalProfile, signals []model.SignalDefinition) error {
	if len(signals) == 0 {
		return nil
	}
	layout, err := layoutPresentationResources(ctx, tx)
	if err != nil {
		return err
	}
	layout.SignalProfiles = profiles
	layout.Signals = signals
	indexes := map[string]int{}
	for i, b := range layout.Blocks {
		indexes[b.ID] = i
	}
	rows, err := tx.QueryContext(ctx, `SELECT block_id,track_section_id FROM block_track_sections`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var blockID, sectionID string
		if err := rows.Scan(&blockID, &sectionID); err != nil {
			return err
		}
		if i, found := indexes[blockID]; found {
			layout.Blocks[i].TrackSectionIDs = append(layout.Blocks[i].TrackSectionIDs, sectionID)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	return model.ValidateSignalDefinitions(layout)
}
