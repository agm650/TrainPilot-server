package model

import (
	"errors"
	"math"
	"testing"
)

func TestValidateLayoutPresentation(t *testing.T) {
	layout := LayoutDefinition{
		TopologyNodes: []TopologyNode{{ID: "a"}, {ID: "b"}},
		TrackSections: []TrackSection{{ID: "section", NodeAID: "a", NodeBID: "b"}},
		Turnouts:      []Turnout{{ID: "turnout"}},
		Blocks:        []BlockDefinition{{ID: "block"}},
	}
	point := func(x, y float64) *LayoutPoint { return &LayoutPoint{X: x, Y: y} }
	valid := func() LayoutPresentation {
		return LayoutPresentation{
			CoordinateSystem: LayoutCoordinateSystem,
			GridSpacing:      20,
			Nodes: []LayoutNodePosition{
				{NodeID: "a", X: 10, Y: 20},
				{NodeID: "b", X: 50, Y: 40},
			},
			TrackSections: []LayoutTrackPath{{
				TrackSectionID: "section",
				Segments: []LayoutTrackSegment{
					{Type: "line", To: point(20, 20)},
					{Type: "cubic", Control1: point(30, 20), Control2: point(40, 40), To: point(50, 40)},
				},
			}},
			Turnouts: []LayoutTurnoutPosition{{TurnoutID: "turnout", X: 30, Y: 30, RotationDegrees: 90, Mirrored: true}},
			Blocks:   []LayoutBlockStyle{{BlockID: "block", Color: "#33AADD", Opacity: 0.3}},
		}
	}
	if err := ValidateLayoutPresentation(LayoutPresentation{}, layout); err != nil {
		t.Fatalf("empty presentation: %v", err)
	}
	if err := ValidateLayoutPresentation(valid(), layout); err != nil {
		t.Fatalf("valid presentation: %v", err)
	}

	cases := []struct {
		name   string
		change func(*LayoutPresentation)
	}{
		{"pixels", func(p *LayoutPresentation) { p.CoordinateSystem = "pixels" }},
		{"negative grid", func(p *LayoutPresentation) { p.GridSpacing = -1 }},
		{"infinite grid", func(p *LayoutPresentation) { p.GridSpacing = math.Inf(1) }},
		{"unknown node", func(p *LayoutPresentation) { p.Nodes[0].NodeID = "missing" }},
		{"duplicate node", func(p *LayoutPresentation) { p.Nodes[1].NodeID = "a" }},
		{"nonfinite node", func(p *LayoutPresentation) { p.Nodes[0].X = math.NaN() }},
		{"unknown section", func(p *LayoutPresentation) { p.TrackSections[0].TrackSectionID = "missing" }},
		{"duplicate section", func(p *LayoutPresentation) { p.TrackSections = append(p.TrackSections, p.TrackSections[0]) }},
		{"missing start position", func(p *LayoutPresentation) { p.Nodes = p.Nodes[1:] }},
		{"missing endpoint", func(p *LayoutPresentation) { p.TrackSections[0].Segments[1].To = nil }},
		{"wrong endpoint", func(p *LayoutPresentation) { p.TrackSections[0].Segments[1].To = point(49, 40) }},
		{"unknown segment", func(p *LayoutPresentation) { p.TrackSections[0].Segments[0].Type = "arc" }},
		{"line controls", func(p *LayoutPresentation) { p.TrackSections[0].Segments[0].Control1 = point(12, 20) }},
		{"missing cubic control", func(p *LayoutPresentation) { p.TrackSections[0].Segments[1].Control2 = nil }},
		{"nonfinite cubic control", func(p *LayoutPresentation) { p.TrackSections[0].Segments[1].Control1 = point(math.Inf(1), 20) }},
		{"unknown turnout", func(p *LayoutPresentation) { p.Turnouts[0].TurnoutID = "missing" }},
		{"duplicate turnout", func(p *LayoutPresentation) { p.Turnouts = append(p.Turnouts, p.Turnouts[0]) }},
		{"nonfinite rotation", func(p *LayoutPresentation) { p.Turnouts[0].RotationDegrees = math.NaN() }},
		{"unknown block", func(p *LayoutPresentation) { p.Blocks[0].BlockID = "missing" }},
		{"duplicate block", func(p *LayoutPresentation) { p.Blocks = append(p.Blocks, p.Blocks[0]) }},
		{"invalid color", func(p *LayoutPresentation) { p.Blocks[0].Color = "red" }},
		{"invalid opacity", func(p *LayoutPresentation) { p.Blocks[0].Opacity = 1.1 }},
		{"nonfinite opacity", func(p *LayoutPresentation) { p.Blocks[0].Opacity = math.NaN() }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			p := valid()
			test.change(&p)
			if err := ValidateLayoutPresentation(p, layout); !errors.Is(err, ErrInvalidLayoutPresentation) {
				t.Fatalf("validation error = %v", err)
			}
		})
	}
}
