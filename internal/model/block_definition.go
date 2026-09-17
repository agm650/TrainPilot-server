package model

import (
	"errors"
	"fmt"
)

var ErrInvalidBlockDefinition = errors.New("invalid block definition")

// ValidateBlockDefinitions validates block identities, resource references,
// and the rule that one physical resource belongs to at most one block.
// Connectivity is validated by the topology graph, which owns graph semantics.
func ValidateBlockDefinitions(layout LayoutDefinition) error {
	sections := make(map[string]bool, len(layout.TrackSections))
	for _, section := range layout.TrackSections {
		sections[section.ID] = true
	}
	turnouts := make(map[string]bool, len(layout.TurnoutTopologies))
	for _, turnout := range layout.TurnoutTopologies {
		turnouts[turnout.TurnoutID] = true
	}

	blocks := make(map[string]bool, len(layout.Blocks))
	sectionOwners := make(map[string]string)
	turnoutOwners := make(map[string]string)
	for index, block := range layout.Blocks {
		if block.ID == "" || block.Name == "" {
			return fmt.Errorf("%w: block %d requires id and name", ErrInvalidBlockDefinition, index)
		}
		if blocks[block.ID] {
			return fmt.Errorf("%w: duplicate block %q", ErrInvalidBlockDefinition, block.ID)
		}
		blocks[block.ID] = true
		for _, sectionID := range block.TrackSectionIDs {
			if !sections[sectionID] {
				return fmt.Errorf("%w: block %q references unknown track section %q", ErrInvalidBlockDefinition, block.ID, sectionID)
			}
			if owner, exists := sectionOwners[sectionID]; exists {
				return fmt.Errorf("%w: track section %q belongs to blocks %q and %q", ErrInvalidBlockDefinition, sectionID, owner, block.ID)
			}
			sectionOwners[sectionID] = block.ID
		}
		for _, turnoutID := range block.TurnoutIDs {
			if !turnouts[turnoutID] {
				return fmt.Errorf("%w: block %q references unknown turnout topology %q", ErrInvalidBlockDefinition, block.ID, turnoutID)
			}
			if owner, exists := turnoutOwners[turnoutID]; exists {
				return fmt.Errorf("%w: turnout %q belongs to blocks %q and %q", ErrInvalidBlockDefinition, turnoutID, owner, block.ID)
			}
			turnoutOwners[turnoutID] = block.ID
		}
	}
	return nil
}
