package topology_test

import (
	"errors"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/model/topologyfixture"
	"github.com/agm650/TrainPilot-server/internal/topology"
)

func TestValidateRouteDefinitionsKeepsLegacyRoutesCompatible(t *testing.T) {
	layout := routeLineLayout()
	layout.Routes = []model.RouteDefinition{{
		ID: "legacy", Name: "Legacy", BlockIDs: []string{"line-block"}, TurnoutStates: map[string]string{},
	}}
	if issues := validateRoutes(t, layout); len(issues) != 0 {
		t.Fatalf("legacy route issues=%+v", issues)
	}
}

func TestValidateRouteDefinitionsAcceptsPhysicalRoutes(t *testing.T) {
	for _, test := range []struct {
		name   string
		layout model.LayoutDefinition
		route  model.RouteDefinition
	}{
		{
			name:   "simple section",
			layout: routeLineLayout(),
			route:  topologicalRoute("line", "a", "b", []string{"line-block"}, nil),
		},
		{
			name:   "simple turnout",
			layout: topologyfixture.Simple(),
			route:  topologicalRoute("simple", "simple-stem", "simple-diverging", nil, map[string]string{"simple": "diverging"}),
		},
		{
			name:   "three way turnout",
			layout: topologyfixture.ThreeWay(),
			route:  topologicalRoute("triple", "triple-stem", "triple-right", nil, map[string]string{"triple": "right"}),
		},
		{
			name:   "double slip compatible position",
			layout: topologyfixture.DoubleSlip(),
			route:  topologicalRoute("double-slip", "double-slip-a", "double-slip-c", nil, map[string]string{"double-slip": "route_b"}),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.layout.Routes = []model.RouteDefinition{test.route}
			if issues := validateRoutes(t, test.layout); len(issues) != 0 {
				t.Fatalf("issues=%+v", issues)
			}
		})
	}
}

func TestValidateRouteDefinitionsReportsStableErrors(t *testing.T) {
	for _, test := range []struct {
		name         string
		layout       model.LayoutDefinition
		route        model.RouteDefinition
		wantCode     string
		wantResource string
	}{
		{
			name: "no path", layout: routeLineLayout(),
			route:    topologicalRoute("disconnected", "a", "isolated", nil, nil),
			wantCode: topology.IssueRouteNoPath, wantResource: "disconnected",
		},
		{
			name: "missing block", layout: routeLineLayout(),
			route:    topologicalRoute("missing-block", "a", "b", nil, nil),
			wantCode: topology.IssueRouteMissingBlock, wantResource: "line-block",
		},
		{
			name: "missing turnout position", layout: topologyfixture.Simple(),
			route:    topologicalRoute("missing-turnout", "simple-stem", "simple-diverging", nil, nil),
			wantCode: topology.IssueRouteMissingTurnoutPosition, wantResource: "simple",
		},
		{
			name: "incorrect turnout position", layout: topologyfixture.Simple(),
			route:    topologicalRoute("wrong-turnout", "simple-stem", "simple-diverging", nil, map[string]string{"simple": "straight"}),
			wantCode: topology.IssueRouteMissingTurnoutPosition, wantResource: "simple",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.layout.Routes = []model.RouteDefinition{test.route}
			issues := validateRoutes(t, test.layout)
			assertIssue(t, issues, topology.ValidationSeverityError, test.wantCode, test.wantResource)
			err := topology.RouteValidationErrors(issues)
			if !errors.Is(err, topology.ErrInvalidRouteDefinition) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestValidateRouteDefinitionsAllowsAdditionalProtectionWithWarnings(t *testing.T) {
	line := routeLineLayout()
	line.Routes = []model.RouteDefinition{
		topologicalRoute("extra-block", "a", "b", []string{"line-block", "protect-block"}, nil),
	}
	issues := validateRoutes(t, line)
	assertIssue(t, issues, topology.ValidationSeverityWarning, topology.IssueRouteExtraBlock, "protect-block")
	if err := topology.RouteValidationErrors(issues); err != nil {
		t.Fatalf("warning blocked validation: %v", err)
	}

	withTurnout := routeLineLayout()
	simple := topologyfixture.Simple()
	withTurnout.TopologyNodes = append(withTurnout.TopologyNodes, simple.TopologyNodes...)
	withTurnout.Turnouts = simple.Turnouts
	withTurnout.TurnoutTopologies = simple.TurnoutTopologies
	withTurnout.Routes = []model.RouteDefinition{
		topologicalRoute("extra-turnout", "a", "b", []string{"line-block"}, map[string]string{"simple": "straight"}),
	}
	issues = validateRoutes(t, withTurnout)
	assertIssue(t, issues, topology.ValidationSeverityWarning, topology.IssueRouteExtraTurnout, "simple")
	if err := topology.RouteValidationErrors(issues); err != nil {
		t.Fatalf("warning blocked validation: %v", err)
	}
}

func TestValidateRouteDefinitionsWarnsAboutDirectionalUndeclaredConflicts(t *testing.T) {
	layout := routeLineLayout()
	first := topologicalRoute("first", "a", "b", []string{"line-block"}, nil)
	second := topologicalRoute("second", "b", "a", []string{"line-block"}, nil)
	layout.Routes = []model.RouteDefinition{second, first}
	issues := validateRoutes(t, layout)
	assertIssue(t, issues, topology.ValidationSeverityWarning, topology.IssueRoutePossibleUndeclaredConflict, "first")
	assertIssue(t, issues, topology.ValidationSeverityWarning, topology.IssueRoutePossibleUndeclaredConflict, "second")

	first.ConflictRouteIDs = []string{"second"}
	second.ConflictRouteIDs = []string{"first"}
	layout.Routes = []model.RouteDefinition{first, second}
	if issues := validateRoutes(t, layout); len(issues) != 0 {
		t.Fatalf("declared conflict issues=%+v", issues)
	}
}

func TestValidateRouteDefinitionsTreatsSharedBlockAsPotentialConflict(t *testing.T) {
	layout := model.LayoutDefinition{
		TopologyNodes: []model.TopologyNode{
			{ID: "a", Kind: model.TopologyNodeBoundary},
			{ID: "b", Kind: model.TopologyNodeJoint},
			{ID: "c", Kind: model.TopologyNodeBoundary},
		},
		TrackSections: []model.TrackSection{
			{ID: "ab", Name: "AB", NodeAID: "a", NodeBID: "b"},
			{ID: "bc", Name: "BC", NodeAID: "b", NodeBID: "c"},
		},
		Blocks: []model.BlockDefinition{{
			ID: "shared", Name: "Shared", TrackSectionIDs: []string{"ab", "bc"},
		}},
	}
	layout.Routes = []model.RouteDefinition{
		topologicalRoute("first", "a", "b", []string{"shared"}, nil),
		topologicalRoute("second", "b", "c", []string{"shared"}, nil),
	}
	issues := validateRoutes(t, layout)
	assertIssue(t, issues, topology.ValidationSeverityWarning, topology.IssueRoutePossibleUndeclaredConflict, "first")
	assertIssue(t, issues, topology.ValidationSeverityWarning, topology.IssueRoutePossibleUndeclaredConflict, "second")
}

func routeLineLayout() model.LayoutDefinition {
	return model.LayoutDefinition{
		TopologyNodes: []model.TopologyNode{
			{ID: "a", Kind: model.TopologyNodeBoundary},
			{ID: "b", Kind: model.TopologyNodeBoundary},
			{ID: "isolated", Kind: model.TopologyNodeBoundary},
		},
		TrackSections: []model.TrackSection{{ID: "line", Name: "Line", NodeAID: "a", NodeBID: "b"}},
		Blocks: []model.BlockDefinition{
			{ID: "line-block", Name: "Line", TrackSectionIDs: []string{"line"}},
			{ID: "protect-block", Name: "Additional protection"},
		},
	}
}

func topologicalRoute(id, entry, exit string, blocks []string, turnouts map[string]string) model.RouteDefinition {
	if turnouts == nil {
		turnouts = map[string]string{}
	}
	return model.RouteDefinition{
		ID: id, Name: id, EntryNodeID: entry, ExitNodeID: exit,
		BlockIDs: blocks, TurnoutStates: turnouts,
	}
}

func validateRoutes(t *testing.T, layout model.LayoutDefinition) []topology.ValidationIssue {
	t.Helper()
	graph, err := topology.Build(layout)
	if err != nil {
		t.Fatal(err)
	}
	return topology.ValidateRouteDefinitions(graph, layout.Routes, layout.Turnouts)
}

func assertIssue(t *testing.T, issues []topology.ValidationIssue, severity topology.ValidationSeverity, code, resourceID string) {
	t.Helper()
	for _, issue := range issues {
		if issue.Severity == severity && issue.Code == code && issue.ResourceID == resourceID {
			return
		}
	}
	t.Fatalf("missing issue severity=%q code=%q resource=%q in %+v", severity, code, resourceID, issues)
}
