package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/client"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/model/topologyfixture"
	"github.com/agm650/TrainPilot-server/internal/topology"
)

func TestTopologyCommandSummaries(t *testing.T) {
	for _, test := range []struct {
		name   string
		layout model.LayoutDefinition
		want   string
	}{
		{"empty", model.LayoutDefinition{}, "Nodes: 0\nTrack sections: 0\nTurnouts: 0\nBlocks: 0\nComponents: 0\n"},
		{"network", topologyfixture.PassingStation(), "Nodes: 12\nTrack sections: 6\nTurnouts: 3\nBlocks: 5\nComponents: 1\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			app, server, _ := topologyCommandFixture(t, test.layout)
			defer server.Close()
			cmd := newTopologyCommand(app)
			var out bytes.Buffer
			cmd.SetOut(&out)
			if err := cmd.ExecuteContext(context.Background()); err != nil {
				t.Fatal(err)
			}
			if out.String() != test.want {
				t.Fatalf("output=%q want=%q", out.String(), test.want)
			}
		})
	}
}

func TestTopologyCommandJSONIsStable(t *testing.T) {
	layout := topologyfixture.ThreeWay()
	app, server, definition := topologyCommandFixture(t, layout)
	defer server.Close()
	cmd := newTopologyCommand(app)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--json"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	want, err := json.MarshalIndent(definition, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if out.String() != string(want)+"\n" {
		t.Fatalf("output=%q want=%q", out.String(), string(want)+"\n")
	}
}

func TestTopologyValidateCommand(t *testing.T) {
	layout := topologyfixture.DoubleSlip()
	app, server, definition := topologyCommandFixture(t, layout)
	defer server.Close()
	cmd := newTopologyCommand(app)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"validate"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if out.String() != "Topology valid: "+definition.Revision+"\n" {
		t.Fatalf("output=%q", out.String())
	}
}

func TestTopologyValidateCommandRejectsInvalidFixture(t *testing.T) {
	layout := topologyfixture.ThreeWay()
	app, server, _ := topologyCommandFixture(t, layout)
	defer server.Close()
	app.client.BaseURL = server.URL + "/invalid"
	cmd := newTopologyCommand(app)
	cmd.SetArgs([]string{"validate"})
	err := cmd.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "invalid kind") {
		t.Fatalf("error=%v", err)
	}
}

func topologyCommandFixture(t *testing.T, layout model.LayoutDefinition) (*commandContext, *httptest.Server, model.TopologyDefinition) {
	t.Helper()
	definition, err := topology.DefinitionFromLayout(layout)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		invalid := strings.HasPrefix(r.URL.Path, "/invalid/")
		path := strings.TrimPrefix(r.URL.Path, "/invalid")
		switch path {
		case "/api/v1/topology":
			response := definition
			if invalid && len(response.Nodes) > 0 {
				response.Nodes[0].Kind = "invalid"
			}
			_ = json.NewEncoder(w).Encode(response)
		case "/api/v1/turnouts":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": layout.Turnouts})
		default:
			http.NotFound(w, r)
		}
	}))
	return &commandContext{client: client.New(server.URL)}, server, definition
}
