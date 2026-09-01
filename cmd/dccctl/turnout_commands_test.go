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
)

func TestTurnoutsCommandListsSimpleAndCompoundTurnouts(t *testing.T) {
	app, server := turnoutCommandFixture(t)
	defer server.Close()
	cmd := newTurnoutsCommand(app)
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := "ID\tNAME\tKIND\tDESIRED\tREPORTED\tPENDING\tQUALITY\n" +
		"T1\tSimple\tsimple\tstraight\tstraight\tno\tstation\n" +
		"T3\tEntrée gare\tthree_way\tright\tstraight\tyes\tphysical\n"
	if out.String() != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestTurnoutCommandListsAndCommandsCompoundPositions(t *testing.T) {
	app, server := turnoutCommandFixture(t)
	defer server.Close()

	positions := newTurnoutCommand(app)
	var out bytes.Buffer
	positions.SetOut(&out)
	positions.SetArgs([]string{"T3", "--positions"})
	if err := positions.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if out.String() != "left\tGauche\nstraight\tDirect\nright\tDroite\n" {
		t.Fatalf("positions output=%q", out.String())
	}

	command := newTurnoutCommand(app)
	out.Reset()
	command.SetOut(&out)
	command.SetArgs([]string{"T3", "right"})
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if out.String() != "T3\tright\n" {
		t.Fatalf("command output=%q", out.String())
	}
}

func TestTurnoutCommandCommandsSimplePosition(t *testing.T) {
	app, server := turnoutCommandFixture(t)
	defer server.Close()
	cmd := newTurnoutCommand(app)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"T1", "diverging"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if out.String() != "T1\tdiverging\n" {
		t.Fatalf("command output=%q", out.String())
	}
}

func TestTurnoutCommandRejectsInvalidPositionWithValidList(t *testing.T) {
	app, server := turnoutCommandFixture(t)
	defer server.Close()
	cmd := newTurnoutCommand(app)
	cmd.SetArgs([]string{"T3", "diverging"})
	err := cmd.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "valid positions: left, straight, right") {
		t.Fatalf("error=%v", err)
	}
}

func turnoutCommandFixture(t *testing.T) (*commandContext, *httptest.Server) {
	t.Helper()
	turnouts := []model.Turnout{
		{
			ID: "T1", Name: "Simple", Kind: model.TurnoutKindSimple,
			Positions:       []model.TurnoutPositionDefinition{{ID: "straight"}, {ID: "diverging"}},
			DesiredPosition: "straight", ReportedPosition: "straight", Quality: "station",
		},
		{
			ID: "T3", Name: "Entrée gare", Kind: model.TurnoutKindThreeWay,
			Positions:       []model.TurnoutPositionDefinition{{ID: "left", Label: "Gauche"}, {ID: "straight", Label: "Direct"}, {ID: "right", Label: "Droite"}},
			DesiredPosition: "right", ReportedPosition: "straight", Pending: true, Quality: "physical",
		},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/turnouts":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": turnouts})
		case r.Method == http.MethodPut && (r.URL.Path == "/api/v1/turnouts/T3" || r.URL.Path == "/api/v1/turnouts/T1"):
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			want := map[string]string{"/api/v1/turnouts/T3": "right", "/api/v1/turnouts/T1": "diverging"}[r.URL.Path]
			if body["position"] != want || body["state"] != "" {
				t.Errorf("request body=%v", body)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	return &commandContext{client: client.New(server.URL)}, server
}
