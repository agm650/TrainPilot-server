package topology

import (
	"testing"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/model/topologyfixture"
)

func TestDefinitionRevisionIsCanonicalAndIgnoresRuntimeState(t *testing.T) {
	layout := topologyfixture.PassingStation()
	first, err := DefinitionFromLayout(layout)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Revision) != 64 {
		t.Fatalf("revision=%q", first.Revision)
	}

	reordered := layout
	reordered.TopologyNodes[0], reordered.TopologyNodes[1] = reordered.TopologyNodes[1], reordered.TopologyNodes[0]
	reordered.Turnouts[0].ReportedPosition = "diverging"
	reordered.Turnouts[0].Pending = true
	second, err := DefinitionFromLayout(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if second.Revision != first.Revision {
		t.Fatalf("runtime or input order changed revision: %s != %s", second.Revision, first.Revision)
	}

	changed := layout
	changed.TrackSections[0].Name = "Changed"
	third, err := DefinitionFromLayout(changed)
	if err != nil {
		t.Fatal(err)
	}
	if third.Revision == first.Revision {
		t.Fatal("topology change did not change revision")
	}
}

func TestBuildDefinitionRejectsInvalidOrStaleResponse(t *testing.T) {
	layout := topologyfixture.ThreeWay()
	definition, err := DefinitionFromLayout(layout)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := BuildDefinition(definition, layout.Turnouts)
	if err != nil || len(graph.ConnectedComponents()) != 1 {
		t.Fatalf("graph=%v err=%v", graph, err)
	}

	definition.Revision = "stale"
	if _, err := BuildDefinition(definition, layout.Turnouts); err == nil {
		t.Fatal("stale revision accepted")
	}
	definition.Revision = ""
	definition.Nodes = append(definition.Nodes, model.TopologyNode{ID: "invalid", Kind: "invalid"})
	if _, err := BuildDefinition(definition, layout.Turnouts); err == nil {
		t.Fatal("invalid topology accepted")
	}
}

func TestDefinitionUsesJSONArraysForEmptyCollections(t *testing.T) {
	definition, err := DefinitionFromLayout(model.LayoutDefinition{
		Blocks: []model.BlockDefinition{{ID: "legacy", Name: "Legacy"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if definition.Nodes == nil || definition.TrackSections == nil || definition.TurnoutTopologies == nil || definition.Blocks[0].TrackSectionIDs == nil || definition.Blocks[0].TurnoutIDs == nil {
		t.Fatalf("definition contains nil collections: %+v", definition)
	}
}
