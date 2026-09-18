package topology

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/agm650/TrainPilot-server/internal/model"
)

type definitionContent struct {
	Nodes             []model.TopologyNode    `json:"nodes"`
	TrackSections     []model.TrackSection    `json:"trackSections"`
	TurnoutTopologies []model.TurnoutTopology `json:"turnoutTopologies"`
	Blocks            []model.BlockDefinition `json:"blocks"`
}

// DefinitionFromLayout validates and canonicalizes the public topology. Its
// revision depends only on persisted physical topology and block membership.
func DefinitionFromLayout(layout model.LayoutDefinition) (model.TopologyDefinition, error) {
	graph, err := Build(layout)
	if err != nil {
		return model.TopologyDefinition{}, err
	}
	content := definitionContent{
		Nodes:             graph.Nodes(),
		TrackSections:     graph.TrackSections(),
		TurnoutTopologies: graph.TurnoutTopologies(),
		Blocks:            graph.Blocks(),
	}
	for topologyIndex := range content.TurnoutTopologies {
		item := &content.TurnoutTopologies[topologyIndex]
		if item.Ports == nil {
			item.Ports = []model.TurnoutPort{}
		}
		if item.Positions == nil {
			item.Positions = []model.TurnoutTopologyPosition{}
		}
		for positionIndex := range item.Positions {
			if item.Positions[positionIndex].Connections == nil {
				item.Positions[positionIndex].Connections = []model.PortConnection{}
			}
		}
	}
	for index := range content.Blocks {
		if content.Blocks[index].TrackSectionIDs == nil {
			content.Blocks[index].TrackSectionIDs = []string{}
		}
		if content.Blocks[index].TurnoutIDs == nil {
			content.Blocks[index].TurnoutIDs = []string{}
		}
	}
	encoded, err := json.Marshal(content)
	if err != nil {
		return model.TopologyDefinition{}, fmt.Errorf("encode topology revision: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return model.TopologyDefinition{
		Revision:          hex.EncodeToString(digest[:]),
		Nodes:             content.Nodes,
		TrackSections:     content.TrackSections,
		TurnoutTopologies: content.TurnoutTopologies,
		Blocks:            content.Blocks,
	}, nil
}

// BuildDefinition validates a public topology response against the referenced
// turnout definitions and returns its static graph.
func BuildDefinition(definition model.TopologyDefinition, turnouts []model.Turnout) (*Graph, error) {
	layout := model.LayoutDefinition{
		Blocks:            definition.Blocks,
		Turnouts:          turnouts,
		TopologyNodes:     definition.Nodes,
		TrackSections:     definition.TrackSections,
		TurnoutTopologies: definition.TurnoutTopologies,
	}
	canonical, err := DefinitionFromLayout(layout)
	if err != nil {
		return nil, err
	}
	if definition.Revision != canonical.Revision {
		return nil, fmt.Errorf("%w: topology revision mismatch", ErrInvalidGraph)
	}
	return Build(layout)
}
