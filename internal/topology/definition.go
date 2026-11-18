package topology

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/agm650/TrainPilot-server/internal/model"
)

type definitionContent struct {
	SignalProfiles    []model.SignalProfile    `json:"signalProfiles,omitempty"`
	Signals           []model.SignalDefinition `json:"signals,omitempty"`
	Nodes             []model.TopologyNode     `json:"nodes"`
	TrackSections     []model.TrackSection     `json:"trackSections"`
	TurnoutTopologies []model.TurnoutTopology  `json:"turnoutTopologies"`
	Blocks            []model.BlockDefinition  `json:"blocks"`
}

// DefinitionFromLayout validates and canonicalizes the public topology. Its
// revision depends only on persisted physical topology and block membership.
func DefinitionFromLayout(layout model.LayoutDefinition) (model.TopologyDefinition, error) {
	graph, err := Build(layout)
	if err != nil {
		return model.TopologyDefinition{}, err
	}
	if err := model.ValidateSignalDefinitions(layout); err != nil {
		return model.TopologyDefinition{}, err
	}
	content := definitionContent{
		SignalProfiles: slices.Clone(layout.SignalProfiles), Signals: slices.Clone(layout.Signals),
		Nodes:             graph.Nodes(),
		TrackSections:     graph.TrackSections(),
		TurnoutTopologies: graph.TurnoutTopologies(),
		Blocks:            graph.Blocks(),
	}
	slices.SortFunc(content.SignalProfiles, func(a, b model.SignalProfile) int { return cmp.Compare(a.ID, b.ID) })
	slices.SortFunc(content.Signals, func(a, b model.SignalDefinition) int { return cmp.Compare(a.ID, b.ID) })
	for i, p := range content.SignalProfiles {
		content.SignalProfiles[i] = model.NormalizeSignalProfile(p)
	}
	for i, value := range content.Signals {
		for _, p := range content.SignalProfiles {
			if value.ProfileID == p.ID {
				value, _ = model.NormalizeSignalDefinition(value, p)
				break
			}
		}
		content.Signals[i] = value
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
		SignalProfiles: content.SignalProfiles, Signals: content.Signals,
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
		SignalProfiles: definition.SignalProfiles, Signals: definition.Signals,
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
