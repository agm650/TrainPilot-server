package presentation

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/model"
)

func TestDefinitionCanonicalizesPresentationWithoutMutatingInput(t *testing.T) {
	empty, err := Definition(model.LayoutPresentation{})
	if err != nil {
		t.Fatal(err)
	}
	if empty.Revision != "66d1f79391497b6a68e617899f73e89ea16d900720a765039a2a15203677ad97" {
		t.Fatalf("empty presentation revision = %q", empty.Revision)
	}
	if empty.Nodes == nil || empty.TrackSections == nil || empty.Turnouts == nil || empty.Blocks == nil {
		t.Fatalf("empty presentation has nil arrays: %#v", empty)
	}
	point := func(x, y float64) *model.LayoutPoint { return &model.LayoutPoint{X: x, Y: y} }
	input := model.LayoutPresentation{
		CoordinateSystem: model.LayoutCoordinateSystem,
		GridSpacing:      20,
		Nodes: []model.LayoutNodePosition{
			{NodeID: "b", X: 20}, {NodeID: "a", X: 10},
		},
		TrackSections: []model.LayoutTrackPath{
			{TrackSectionID: "s2", Segments: []model.LayoutTrackSegment{{Type: "line", To: point(20, 0)}}},
			{TrackSectionID: "s1", Segments: []model.LayoutTrackSegment{{Type: "line", To: point(10, 0)}}},
		},
		Turnouts: []model.LayoutTurnoutPosition{{TurnoutID: "t2"}, {TurnoutID: "t1", Mirrored: true}},
		Blocks:   []model.LayoutBlockStyle{{BlockID: "b2", Color: "#000000"}, {BlockID: "b1", Color: "#FFFFFF"}},
	}
	before := append([]model.LayoutNodePosition(nil), input.Nodes...)
	first, err := Definition(input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(input.Nodes, before) || first.Nodes[0].NodeID != "a" || first.TrackSections[0].TrackSectionID != "s1" || first.Turnouts[0].TurnoutID != "t1" || first.Blocks[0].BlockID != "b1" {
		t.Fatalf("canonical order or input mutation: input=%#v output=%#v", input, first)
	}
	second, err := Definition(first.LayoutPresentation)
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision != second.Revision {
		t.Fatalf("reordered presentation changed revision: %q != %q", first.Revision, second.Revision)
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	if object["revision"] == nil || object["nodes"] == nil || object["layoutPresentation"] != nil {
		t.Fatalf("unexpected public JSON: %s", encoded)
	}
	input.Nodes[0].X++
	changed, err := Definition(input)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Revision == first.Revision {
		t.Fatal("node position change left presentation revision unchanged")
	}
}
