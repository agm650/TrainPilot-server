package topologyfixture

import "github.com/agm650/TrainPilot-server/internal/model"

// ReferenceLayout is a named physical topology used by regression tests and
// examples. The order returned by ReferenceLayouts is part of the fixture set.
type ReferenceLayout struct {
	Name   string
	Layout model.LayoutDefinition
}

// ReferenceLayouts returns the eight TOP-009 reference networks and the
// conceptual five-zone TrainPilot network.
func ReferenceLayouts() []ReferenceLayout {
	return []ReferenceLayout{
		{Name: "simple-line", Layout: SimpleLine()},
		{Name: "passing-loop", Layout: PassingLoop()},
		{Name: "three-way-yard", Layout: ThreeWayYard()},
		{Name: "double-slip-station", Layout: DoubleSlipStation()},
		{Name: "fixed-crossing", Layout: FixedCrossing()},
		{Name: "multi-section-block", Layout: MultiSectionBlock()},
		{Name: "undetected-section", Layout: UndetectedSection()},
		{Name: "loop", Layout: Loop()},
		{Name: "conceptual-five-detection-zones", Layout: ConceptualFiveDetectionZones()},
	}
}

// SimpleLine models buffer -- S1 -- joint -- S2 -- boundary.
func SimpleLine() model.LayoutDefinition {
	return model.LayoutDefinition{
		TopologyNodes: []model.TopologyNode{
			topologyNode("buffer", model.TopologyNodeBuffer),
			topologyNode("middle", model.TopologyNodeJoint),
			topologyNode("boundary", model.TopologyNodeBoundary),
		},
		TrackSections: []model.TrackSection{
			trackSection("s1", "buffer", "middle"),
			trackSection("s2", "middle", "boundary"),
		},
		Blocks: []model.BlockDefinition{
			block("block-s1", []string{"s1"}, nil),
			block("block-s2", []string{"s2"}, nil),
		},
	}
}

// PassingLoop models two station tracks between simple turnouts.
func PassingLoop() model.LayoutDefinition {
	west := model.NewSimpleTurnout("t1", "West turnout", 100, "straight", "straight")
	east := model.NewSimpleTurnout("t2", "East turnout", 101, "straight", "straight")
	layout := model.LayoutDefinition{
		Turnouts: []model.Turnout{west, east},
		TopologyNodes: []model.TopologyNode{
			topologyNode("west-boundary", model.TopologyNodeBoundary),
			topologyNode("upper-middle", model.TopologyNodeJoint),
			topologyNode("east-boundary", model.TopologyNodeBoundary),
		},
	}
	appendSimpleTurnoutTopology(&layout, west)
	appendSimpleTurnoutTopology(&layout, east)
	layout.TrackSections = []model.TrackSection{
		trackSection("s1", "west-boundary", "t1-stem"),
		trackSection("s3", "t1-diverging", "upper-middle"),
		trackSection("s4", "upper-middle", "t2-diverging"),
		trackSection("s5", "t1-straight", "t2-straight"),
		trackSection("s6", "t2-stem", "east-boundary"),
	}
	layout.Blocks = []model.BlockDefinition{
		block("block-west", []string{"s1"}, nil),
		block("block-upper", []string{"s3", "s4"}, nil),
		block("block-direct", []string{"s5"}, nil),
		block("block-east", []string{"s6"}, nil),
	}
	return layout
}

// ThreeWayYard models a three-way turnout followed by three buffer stops.
func ThreeWayYard() model.LayoutDefinition {
	layout := ThreeWay()
	layout.TopologyNodes = append(layout.TopologyNodes,
		topologyNode("yard-boundary", model.TopologyNodeBoundary),
		topologyNode("left-buffer", model.TopologyNodeBuffer),
		topologyNode("straight-buffer", model.TopologyNodeBuffer),
		topologyNode("right-buffer", model.TopologyNodeBuffer),
	)
	layout.TrackSections = []model.TrackSection{
		trackSection("yard-lead", "yard-boundary", "triple-stem"),
		trackSection("left-siding", "triple-left", "left-buffer"),
		trackSection("straight-siding", "triple-straight", "straight-buffer"),
		trackSection("right-siding", "triple-right", "right-buffer"),
	}
	layout.Blocks = []model.BlockDefinition{
		block("block-yard-lead", []string{"yard-lead"}, nil),
		block("block-left", []string{"left-siding"}, nil),
		block("block-straight", []string{"straight-siding"}, nil),
		block("block-right", []string{"right-siding"}, nil),
		block("block-yard-throat", nil, []string{"triple"}),
	}
	return layout
}

// DoubleSlipStation models a four-port double-slip crossing and four approaches.
func DoubleSlipStation() model.LayoutDefinition {
	layout := DoubleSlip()
	for _, portID := range []string{"a", "b", "c", "d"} {
		boundaryID := portID + "-boundary"
		layout.TopologyNodes = append(layout.TopologyNodes, topologyNode(boundaryID, model.TopologyNodeBoundary))
		layout.TrackSections = append(layout.TrackSections, trackSection("approach-"+portID, boundaryID, "double-slip-"+portID))
		layout.Blocks = append(layout.Blocks, block("block-approach-"+portID, []string{"approach-" + portID}, nil))
	}
	layout.Blocks = append(layout.Blocks, block("block-double-slip", nil, []string{"double-slip"}))
	return layout
}

// FixedCrossing models two tracks that cross visually without sharing a node.
func FixedCrossing() model.LayoutDefinition {
	return model.LayoutDefinition{
		TopologyNodes: []model.TopologyNode{
			topologyNode("west", model.TopologyNodeBoundary),
			topologyNode("east", model.TopologyNodeBoundary),
			topologyNode("north", model.TopologyNodeBoundary),
			topologyNode("south", model.TopologyNodeBoundary),
		},
		TrackSections: []model.TrackSection{
			trackSection("west-east", "west", "east"),
			trackSection("north-south", "north", "south"),
		},
		Blocks: []model.BlockDefinition{
			block("block-west-east", []string{"west-east"}, nil),
			block("block-north-south", []string{"north-south"}, nil),
		},
	}
}

// MultiSectionBlock models one detection block spanning track and a turnout.
func MultiSectionBlock() model.LayoutDefinition {
	layout := Simple()
	for _, portID := range []string{"stem", "straight", "diverging"} {
		boundaryID := portID + "-boundary"
		layout.TopologyNodes = append(layout.TopologyNodes, topologyNode(boundaryID, model.TopologyNodeBoundary))
		layout.TrackSections = append(layout.TrackSections, trackSection("section-"+portID, boundaryID, "simple-"+portID))
	}
	layout.Blocks = []model.BlockDefinition{
		block("block-complete-junction", []string{"section-stem", "section-straight", "section-diverging"}, []string{"simple"}),
	}
	return layout
}

// UndetectedSection contains a valid physical section with no block owner.
func UndetectedSection() model.LayoutDefinition {
	layout := SimpleLine()
	layout.Blocks = layout.Blocks[:1]
	return layout
}

// Loop models a complete fixed-track cycle.
func Loop() model.LayoutDefinition {
	return model.LayoutDefinition{
		TopologyNodes: []model.TopologyNode{
			topologyNode("a", model.TopologyNodeJoint),
			topologyNode("b", model.TopologyNodeJoint),
			topologyNode("c", model.TopologyNodeJoint),
			topologyNode("d", model.TopologyNodeJoint),
		},
		TrackSections: []model.TrackSection{
			trackSection("ab", "a", "b"),
			trackSection("bc", "b", "c"),
			trackSection("cd", "c", "d"),
			trackSection("da", "d", "a"),
		},
		Blocks: []model.BlockDefinition{
			block("block-loop", []string{"ab", "bc", "cd", "da"}, nil),
		},
	}
}

// ConceptualFiveDetectionZones records only the five planned R-BUS zones.
// The sections are deliberately disconnected because no confirmed physical
// track plan or turnout geometry is available in the repository.
func ConceptualFiveDetectionZones() model.LayoutDefinition {
	layout := model.LayoutDefinition{}
	zoneIDs := []string{"outer-1", "outer-2", "outer-3", "inner-1", "inner-2"}
	for _, zoneID := range zoneIDs {
		nodeAID := zoneID + "-a"
		nodeBID := zoneID + "-b"
		sectionID := "section-" + zoneID
		layout.TopologyNodes = append(layout.TopologyNodes,
			topologyNode(nodeAID, model.TopologyNodeBoundary),
			topologyNode(nodeBID, model.TopologyNodeBoundary),
		)
		layout.TrackSections = append(layout.TrackSections, trackSection(sectionID, nodeAID, nodeBID))
		layout.Blocks = append(layout.Blocks, block("block-"+zoneID, []string{sectionID}, nil))
	}
	return layout
}

func appendSimpleTurnoutTopology(layout *model.LayoutDefinition, turnout model.Turnout) {
	for _, portID := range []string{"stem", "straight", "diverging"} {
		layout.TopologyNodes = append(layout.TopologyNodes, topologyNode(turnout.ID+"-"+portID, model.TopologyNodeJoint))
	}
	layout.TurnoutTopologies = append(layout.TurnoutTopologies, model.TurnoutTopology{
		TurnoutID: turnout.ID,
		Ports: []model.TurnoutPort{
			{ID: "stem", NodeID: turnout.ID + "-stem"},
			{ID: "straight", NodeID: turnout.ID + "-straight"},
			{ID: "diverging", NodeID: turnout.ID + "-diverging"},
		},
		Positions: []model.TurnoutTopologyPosition{
			{PositionID: "straight", Connections: []model.PortConnection{connection("stem", "straight")}},
			{PositionID: "diverging", Connections: []model.PortConnection{connection("stem", "diverging")}},
		},
	})
}

func topologyNode(id string, kind model.TopologyNodeKind) model.TopologyNode {
	return model.TopologyNode{ID: id, Name: id, Kind: kind}
}

func trackSection(id, nodeAID, nodeBID string) model.TrackSection {
	return model.TrackSection{ID: id, Name: id, NodeAID: nodeAID, NodeBID: nodeBID}
}

func block(id string, sectionIDs, turnoutIDs []string) model.BlockDefinition {
	return model.BlockDefinition{
		ID:              id,
		Name:            id,
		TrackSectionIDs: append([]string(nil), sectionIDs...),
		TurnoutIDs:      append([]string(nil), turnoutIDs...),
	}
}
