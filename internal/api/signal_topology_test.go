package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/topology"
)

func TestSignalDefinitionsReachTopologyClients(t *testing.T) {
	f := newWebsocketFixture(t)
	ctx := context.Background()
	p := model.SNCFBAL3Profile()
	l := model.LayoutDefinition{TopologyNodes: []model.TopologyNode{{ID: "a", Kind: model.TopologyNodeBoundary}, {ID: "b", Kind: model.TopologyNodeBoundary}}, TrackSections: []model.TrackSection{{ID: "ab", Name: "AB", NodeAID: "a", NodeBID: "b"}}, Blocks: []model.BlockDefinition{{ID: "block", Name: "Block", TrackSectionIDs: []string{"ab"}}}, SignalProfiles: []model.SignalProfile{p}, Signals: []model.SignalDefinition{{ID: "s1", Name: "Signal", ProfileID: p.ID, Placement: model.SignalPlacement{TrackSectionID: "ab", EntryNodeID: "a"}, ProtectedBlockID: "block", Downstream: model.SignalDownstream{Kind: model.SignalDownstreamTerminal, Aspect: "S"}, Endpoint: model.SignalEndpoint{Kind: model.SignalEndpointDCCExtended, LinearAddress: 1, AspectCodes: map[model.SignalAspectID]int{"S": 0, "A": 7, "VL": 16}}}}}
	if err := f.api.store.ImportLayout(ctx, l, true); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodGet, f.server.URL+"/api/v1/topology", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+f.accessToken)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatal(response.Status)
	}
	var definition model.TopologyDefinition
	if err := json.NewDecoder(response.Body).Decode(&definition); err != nil {
		t.Fatal(err)
	}
	if len(definition.Signals) != 1 || len(definition.SignalProfiles) != 1 || definition.Signals[0].Endpoint.AspectCodes["A"] != 7 {
		t.Fatal(definition)
	}
	if _, err := topology.BuildDefinition(definition, nil); err != nil {
		t.Fatal("client cannot validate revision", err)
	}
	ws := dialTestWebSocket(t, f.server.URL, f.accessToken)
	defer ws.close()
	snapshot := readTestSnapshot(t, ws)
	if snapshot.Payload.TopologyRevision != definition.Revision {
		t.Fatal("REST and WebSocket revisions differ")
	}
}
