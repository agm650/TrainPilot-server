package topology

import (
	"testing"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/model/topologyfixture"
)

func TestSignalMetadataPreservesLegacyPhysicalRevision(t *testing.T) {
	layout := topologyfixture.PassingStation()
	before, err := DefinitionFromLayout(layout)
	if err != nil {
		t.Fatal(err)
	}
	layout.SignalProfiles = []model.SignalProfile{model.SNCFBAL3Profile()}
	after, err := DefinitionFromLayout(layout)
	if err != nil {
		t.Fatal(err)
	}
	if before.Revision != after.Revision {
		t.Fatal("optional signal metadata changed the checksum recomputed by legacy graph clients")
	}
	if before.SignallingRevision != "" || after.SignallingRevision == "" {
		t.Fatal("missing independent signalling revision")
	}
	after.SignalProfiles[0].Name = "Changed"
	if _, err := BuildDefinition(after, layout.Turnouts); err == nil {
		t.Fatal("modified signalling metadata passed revision validation")
	}
}
