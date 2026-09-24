package presentation

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/agm650/TrainPilot-server/internal/model"
)

// Definition canonicalizes persisted drawing data before computing its
// revision. Runtime state and physical topology are intentionally absent.
func Definition(value model.LayoutPresentation) (model.LayoutPresentationDefinition, error) {
	value = model.NormalizeLayoutPresentation(value)
	value.Nodes = slices.Clone(value.Nodes)
	value.TrackSections = slices.Clone(value.TrackSections)
	value.Turnouts = slices.Clone(value.Turnouts)
	value.Blocks = slices.Clone(value.Blocks)
	slices.SortFunc(value.Nodes, func(a, b model.LayoutNodePosition) int { return cmp.Compare(a.NodeID, b.NodeID) })
	slices.SortFunc(value.TrackSections, func(a, b model.LayoutTrackPath) int { return cmp.Compare(a.TrackSectionID, b.TrackSectionID) })
	slices.SortFunc(value.Turnouts, func(a, b model.LayoutTurnoutPosition) int { return cmp.Compare(a.TurnoutID, b.TurnoutID) })
	slices.SortFunc(value.Blocks, func(a, b model.LayoutBlockStyle) int { return cmp.Compare(a.BlockID, b.BlockID) })
	encoded, err := json.Marshal(value)
	if err != nil {
		return model.LayoutPresentationDefinition{}, fmt.Errorf("encode layout presentation revision: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return model.LayoutPresentationDefinition{
		Revision:           hex.EncodeToString(digest[:]),
		LayoutPresentation: value,
	}, nil
}
