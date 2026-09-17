package model_test

import (
	"errors"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/model/topologyfixture"
)

func TestValidateBlockDefinitionsAcceptsEmptyAndUniqueMemberships(t *testing.T) {
	layout := topologyfixture.Simple()
	layout.TopologyNodes = append(layout.TopologyNodes,
		model.TopologyNode{ID: "outer-a", Kind: model.TopologyNodeBoundary},
		model.TopologyNode{ID: "outer-b", Kind: model.TopologyNodeBoundary},
	)
	layout.TrackSections = []model.TrackSection{
		{ID: "a", NodeAID: layout.TurnoutTopologies[0].Ports[0].NodeID, NodeBID: "outer-a"},
		{ID: "b", NodeAID: layout.TurnoutTopologies[0].Ports[1].NodeID, NodeBID: "outer-b"},
	}
	layout.Blocks = []model.BlockDefinition{
		{ID: "legacy", Name: "Legacy"},
		{ID: "detected", Name: "Detected", TrackSectionIDs: []string{"a"}, TurnoutIDs: []string{layout.Turnouts[0].ID}},
	}
	if err := model.ValidateBlockDefinitions(layout); err != nil {
		t.Fatal(err)
	}
}

func TestValidateBlockDefinitionsRejectsInvalidMemberships(t *testing.T) {
	base := topologyfixture.Simple()
	base.TopologyNodes = append(base.TopologyNodes,
		model.TopologyNode{ID: "outer", Kind: model.TopologyNodeBoundary},
	)
	base.TrackSections = []model.TrackSection{{
		ID: "section", NodeAID: base.TurnoutTopologies[0].Ports[0].NodeID, NodeBID: "outer",
	}}
	tests := []struct {
		name   string
		blocks []model.BlockDefinition
	}{
		{name: "missing identity", blocks: []model.BlockDefinition{{ID: "block"}}},
		{name: "duplicate block", blocks: []model.BlockDefinition{{ID: "block", Name: "A"}, {ID: "block", Name: "B"}}},
		{name: "unknown section", blocks: []model.BlockDefinition{{ID: "block", Name: "Block", TrackSectionIDs: []string{"missing"}}}},
		{name: "unknown turnout", blocks: []model.BlockDefinition{{ID: "block", Name: "Block", TurnoutIDs: []string{"missing"}}}},
		{name: "section twice", blocks: []model.BlockDefinition{
			{ID: "a", Name: "A", TrackSectionIDs: []string{"section"}},
			{ID: "b", Name: "B", TrackSectionIDs: []string{"section"}},
		}},
		{name: "turnout twice", blocks: []model.BlockDefinition{
			{ID: "a", Name: "A", TurnoutIDs: []string{base.Turnouts[0].ID}},
			{ID: "b", Name: "B", TurnoutIDs: []string{base.Turnouts[0].ID}},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			layout := base
			layout.Blocks = test.blocks
			if err := model.ValidateBlockDefinitions(layout); !errors.Is(err, model.ErrInvalidBlockDefinition) {
				t.Fatalf("ValidateBlockDefinitions() error = %v, want ErrInvalidBlockDefinition", err)
			}
		})
	}
}
