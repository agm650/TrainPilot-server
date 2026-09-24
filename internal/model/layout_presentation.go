package model

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

const (
	LayoutCoordinateSystem = "layout-units"
	DefaultGridSpacing     = 20
)

type LayoutPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type LayoutNodePosition struct {
	NodeID string  `json:"nodeId"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
}

type LayoutTrackSegment struct {
	Type     string       `json:"type"`
	Control1 *LayoutPoint `json:"control1,omitempty"`
	Control2 *LayoutPoint `json:"control2,omitempty"`
	To       *LayoutPoint `json:"to"`
}

type LayoutTrackPath struct {
	TrackSectionID string               `json:"trackSectionId"`
	Segments       []LayoutTrackSegment `json:"segments"`
}

type LayoutTurnoutPosition struct {
	TurnoutID       string  `json:"turnoutId"`
	X               float64 `json:"x"`
	Y               float64 `json:"y"`
	RotationDegrees float64 `json:"rotationDegrees"`
	Mirrored        bool    `json:"mirrored"`
}

type LayoutBlockStyle struct {
	BlockID string  `json:"blockId"`
	Color   string  `json:"color"`
	Opacity float64 `json:"opacity"`
}

// LayoutPresentation contains only drawing data. It does not define physical
// connectivity or operational state.
type LayoutPresentation struct {
	CoordinateSystem string                  `json:"coordinateSystem"`
	GridSpacing      float64                 `json:"gridSpacing"`
	Nodes            []LayoutNodePosition    `json:"nodes"`
	TrackSections    []LayoutTrackPath       `json:"trackSections"`
	Turnouts         []LayoutTurnoutPosition `json:"turnouts"`
	Blocks           []LayoutBlockStyle      `json:"blocks"`
}

var ErrInvalidLayoutPresentation = errors.New("invalid layout presentation")

func EmptyLayoutPresentation() LayoutPresentation {
	return LayoutPresentation{
		CoordinateSystem: LayoutCoordinateSystem,
		GridSpacing:      DefaultGridSpacing,
		Nodes:            []LayoutNodePosition{},
		TrackSections:    []LayoutTrackPath{},
		Turnouts:         []LayoutTurnoutPosition{},
		Blocks:           []LayoutBlockStyle{},
	}
}

// NormalizeLayoutPresentation supplies defaults for older layouts and omitted
// canvas metadata. Invalid non-default values are rejected by validation.
func NormalizeLayoutPresentation(p LayoutPresentation) LayoutPresentation {
	if p.CoordinateSystem == "" {
		p.CoordinateSystem = LayoutCoordinateSystem
	}
	if p.GridSpacing == 0 {
		p.GridSpacing = DefaultGridSpacing
	}
	if p.Nodes == nil {
		p.Nodes = []LayoutNodePosition{}
	}
	if p.TrackSections == nil {
		p.TrackSections = []LayoutTrackPath{}
	}
	if p.Turnouts == nil {
		p.Turnouts = []LayoutTurnoutPosition{}
	}
	if p.Blocks == nil {
		p.Blocks = []LayoutBlockStyle{}
	}
	return p
}

func ValidateLayoutPresentation(p LayoutPresentation, layout LayoutDefinition) error {
	invalid := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalidLayoutPresentation, fmt.Sprintf(format, args...))
	}
	p = NormalizeLayoutPresentation(p)
	if p.CoordinateSystem != LayoutCoordinateSystem {
		return invalid("unsupported coordinate system %q", p.CoordinateSystem)
	}
	if !finite(p.GridSpacing) || p.GridSpacing <= 0 {
		return invalid("grid spacing must be positive and finite")
	}

	nodes := make(map[string]bool, len(layout.TopologyNodes))
	for _, node := range layout.TopologyNodes {
		nodes[node.ID] = true
	}
	sections := make(map[string]TrackSection, len(layout.TrackSections))
	for _, section := range layout.TrackSections {
		sections[section.ID] = section
	}
	turnouts := make(map[string]bool, len(layout.Turnouts))
	for _, turnout := range layout.Turnouts {
		turnouts[turnout.ID] = true
	}
	blocks := make(map[string]bool, len(layout.Blocks))
	for _, block := range layout.Blocks {
		blocks[block.ID] = true
	}

	positions := make(map[string]LayoutPoint, len(p.Nodes))
	for _, node := range p.Nodes {
		if strings.TrimSpace(node.NodeID) == "" || !nodes[node.NodeID] {
			return invalid("unknown node %q", node.NodeID)
		}
		if _, exists := positions[node.NodeID]; exists {
			return invalid("duplicate node %q", node.NodeID)
		}
		position := LayoutPoint{X: node.X, Y: node.Y}
		if !finitePoint(position) {
			return invalid("node %q has non-finite coordinates", node.NodeID)
		}
		positions[node.NodeID] = position
	}

	paths := make(map[string]bool, len(p.TrackSections))
	for _, path := range p.TrackSections {
		section, exists := sections[path.TrackSectionID]
		if !exists || strings.TrimSpace(path.TrackSectionID) == "" {
			return invalid("unknown track section %q", path.TrackSectionID)
		}
		if paths[path.TrackSectionID] {
			return invalid("duplicate track section %q", path.TrackSectionID)
		}
		paths[path.TrackSectionID] = true
		if _, exists := positions[section.NodeAID]; !exists {
			return invalid("track section %q requires node %q position", path.TrackSectionID, section.NodeAID)
		}
		end, exists := positions[section.NodeBID]
		if !exists {
			return invalid("track section %q requires node %q position", path.TrackSectionID, section.NodeBID)
		}
		if len(path.Segments) == 0 {
			return invalid("track section %q requires a segment", path.TrackSectionID)
		}
		for index, segment := range path.Segments {
			if segment.To == nil || !finitePoint(*segment.To) {
				return invalid("track section %q segment %d has invalid endpoint", path.TrackSectionID, index)
			}
			switch segment.Type {
			case "line":
				if segment.Control1 != nil || segment.Control2 != nil {
					return invalid("track section %q line segment %d has controls", path.TrackSectionID, index)
				}
			case "cubic":
				if segment.Control1 == nil || segment.Control2 == nil || !finitePoint(*segment.Control1) || !finitePoint(*segment.Control2) {
					return invalid("track section %q cubic segment %d has invalid controls", path.TrackSectionID, index)
				}
			default:
				return invalid("track section %q segment %d has unknown type %q", path.TrackSectionID, index, segment.Type)
			}
		}
		if *path.Segments[len(path.Segments)-1].To != end {
			return invalid("track section %q path does not end at node %q", path.TrackSectionID, section.NodeBID)
		}
	}

	placedTurnouts := make(map[string]bool, len(p.Turnouts))
	for _, turnout := range p.Turnouts {
		if strings.TrimSpace(turnout.TurnoutID) == "" || !turnouts[turnout.TurnoutID] {
			return invalid("unknown turnout %q", turnout.TurnoutID)
		}
		if placedTurnouts[turnout.TurnoutID] {
			return invalid("duplicate turnout %q", turnout.TurnoutID)
		}
		placedTurnouts[turnout.TurnoutID] = true
		if !finite(turnout.X) || !finite(turnout.Y) || !finite(turnout.RotationDegrees) {
			return invalid("turnout %q has non-finite placement", turnout.TurnoutID)
		}
	}

	styledBlocks := make(map[string]bool, len(p.Blocks))
	for _, block := range p.Blocks {
		if strings.TrimSpace(block.BlockID) == "" || !blocks[block.BlockID] {
			return invalid("unknown block %q", block.BlockID)
		}
		if styledBlocks[block.BlockID] {
			return invalid("duplicate block %q", block.BlockID)
		}
		styledBlocks[block.BlockID] = true
		if !validLayoutColor(block.Color) {
			return invalid("block %q has invalid color %q", block.BlockID, block.Color)
		}
		if !finite(block.Opacity) || block.Opacity < 0 || block.Opacity > 1 {
			return invalid("block %q opacity is outside 0..1", block.BlockID)
		}
	}
	return nil
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func finitePoint(point LayoutPoint) bool { return finite(point.X) && finite(point.Y) }

func validLayoutColor(color string) bool {
	if len(color) != 7 || color[0] != '#' {
		return false
	}
	for _, digit := range color[1:] {
		if !((digit >= '0' && digit <= '9') || (digit >= 'A' && digit <= 'F') || (digit >= 'a' && digit <= 'f')) {
			return false
		}
	}
	return true
}
