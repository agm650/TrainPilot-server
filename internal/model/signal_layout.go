package model

import (
	"errors"
	"fmt"
	"sort"
)

var ErrInvalidSignalLayout = errors.New("invalid signal layout")

type SignalLayoutValidationError struct{ SignalID, Message string }

func (e *SignalLayoutValidationError) Error() string {
	return fmt.Sprintf("%s: signal %q: %s", ErrInvalidSignalLayout, e.SignalID, e.Message)
}
func (e *SignalLayoutValidationError) Unwrap() error { return ErrInvalidSignalLayout }

// ValidateSignalDefinitions validates complete configuration only. It never
// derives occupancy, aspects or movement authority from topology coverage.
func ValidateSignalDefinitions(layout LayoutDefinition) error {
	invalid := func(id, message string) error { return &SignalLayoutValidationError{SignalID: id, Message: message} }
	profiles := map[string]SignalProfile{}
	for _, p := range layout.SignalProfiles {
		if _, found := profiles[p.ID]; found {
			return invalid("", "duplicate profile "+p.ID)
		}
		if err := ValidateSignalProfile(p); err != nil {
			return invalid("", err.Error())
		}
		profiles[p.ID] = p
	}
	sections := map[string]TrackSection{}
	incident := map[string][]string{}
	for _, section := range layout.TrackSections {
		sections[section.ID] = section
		incident[section.NodeAID] = append(incident[section.NodeAID], section.ID)
		incident[section.NodeBID] = append(incident[section.NodeBID], section.ID)
	}
	owners := map[string]string{}
	for _, b := range layout.Blocks {
		for _, id := range b.TrackSectionIDs {
			owners[id] = b.ID
		}
	}
	signals := map[string]SignalDefinition{}
	endpoints := map[struct {
		kind    SignalEndpointKind
		address int
	}]bool{}
	placements := map[SignalPlacement]bool{}
	for _, value := range layout.Signals {
		if _, found := signals[value.ID]; found {
			return invalid(value.ID, "duplicate signal id")
		}
		p, found := profiles[value.ProfileID]
		if !found {
			return invalid(value.ID, "unknown profile")
		}
		value, err := NormalizeSignalDefinition(value, p)
		if err != nil {
			return invalid(value.ID, err.Error())
		}
		section, found := sections[value.Placement.TrackSectionID]
		if !found || (value.Placement.EntryNodeID != section.NodeAID && value.Placement.EntryNodeID != section.NodeBID) {
			return invalid(value.ID, "placement is not an oriented section endpoint")
		}
		if owners[section.ID] != value.ProtectedBlockID {
			return invalid(value.ID, "section is not owned by protected block")
		}
		key := struct {
			kind    SignalEndpointKind
			address int
		}{value.Endpoint.Kind, value.Endpoint.LinearAddress}
		if endpoints[key] || placements[value.Placement] {
			return invalid(value.ID, "duplicate endpoint or oriented placement")
		}
		endpoints[key] = true
		placements[value.Placement] = true
		signals[value.ID] = value
	}
	for _, value := range layout.Signals {
		current := signals[value.ID]
		first := sections[current.Placement.TrackSectionID]
		node := first.NodeBID
		if node == current.Placement.EntryNodeID {
			node = first.NodeAID
		}
		visited := map[string]bool{first.ID: true}
		for {
			next := ""
			for _, id := range incident[node] {
				if owners[id] == current.ProtectedBlockID && !visited[id] {
					if next != "" {
						return invalid(current.ID, "ambiguous continuation in protected block")
					}
					next = id
				}
			}
			if next == "" {
				if current.Downstream.Kind == SignalDownstreamTerminal {
					break
				}
				target, found := signals[current.Downstream.SignalID]
				if !found || target.Placement.EntryNodeID != node || (visited[target.Placement.TrackSectionID] && target.ID != current.ID) {
					return invalid(current.ID, "downstream is missing, discontinuous or oppositely oriented")
				}
				break
			}
			visited[next] = true
			section := sections[next]
			if section.NodeAID == node {
				node = section.NodeBID
			} else {
				node = section.NodeAID
			}
		}
	}
	return nil
}

// UnsignalledBlockIDs exposes incomplete signalling coverage without implying
// that the corresponding blocks are free or safe to traverse.
func UnsignalledBlockIDs(layout LayoutDefinition) []string {
	protected := map[string]bool{}
	for _, signal := range layout.Signals {
		protected[signal.ProtectedBlockID] = true
	}
	result := []string{}
	for _, block := range layout.Blocks {
		if !protected[block.ID] {
			result = append(result, block.ID)
		}
	}
	sort.Strings(result)
	return result
}
