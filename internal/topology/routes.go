package topology

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/agm650/TrainPilot-server/internal/model"
)

type ValidationSeverity string

const (
	ValidationSeverityError   ValidationSeverity = "error"
	ValidationSeverityWarning ValidationSeverity = "warning"

	IssueRouteNoPath                     = "route_no_path"
	IssueRouteMissingBlock               = "route_missing_block"
	IssueRouteMissingTurnoutPosition     = "route_missing_turnout_position"
	IssueRouteExtraBlock                 = "route_extra_block"
	IssueRouteExtraTurnout               = "route_extra_turnout"
	IssueRoutePossibleUndeclaredConflict = "route_possible_undeclared_conflict"
)

var ErrInvalidRouteDefinition = errors.New("invalid topological route definition")

type ValidationIssue struct {
	Severity   ValidationSeverity `json:"severity"`
	Code       string             `json:"code"`
	Message    string             `json:"message"`
	ResourceID string             `json:"resourceId,omitempty"`
}

type RouteDefinitionValidationError struct {
	Issues []ValidationIssue
}

func (e *RouteDefinitionValidationError) Error() string {
	if len(e.Issues) == 0 {
		return ErrInvalidRouteDefinition.Error()
	}
	return fmt.Sprintf("%s: %s", ErrInvalidRouteDefinition, e.Issues[0].Message)
}

func (e *RouteDefinitionValidationError) Unwrap() error { return ErrInvalidRouteDefinition }

// RouteValidationErrors returns a blocking error containing only error-level
// issues. Warnings remain available to callers through ValidateRouteDefinitions.
func RouteValidationErrors(issues []ValidationIssue) error {
	errorsOnly := make([]ValidationIssue, 0)
	for _, issue := range issues {
		if issue.Severity == ValidationSeverityError {
			errorsOnly = append(errorsOnly, issue)
		}
	}
	if len(errorsOnly) == 0 {
		return nil
	}
	return &RouteDefinitionValidationError{Issues: errorsOnly}
}

type validatedRoute struct {
	definition model.RouteDefinition
	resources  map[string]bool
}

// ValidateRouteDefinitions checks the configuration-time relationship between
// declared routes and the static topology. It never reads or changes runtime
// reservation, occupancy, or turnout state.
func ValidateRouteDefinitions(graph *Graph, definitions []model.RouteDefinition, turnouts []model.Turnout) []ValidationIssue {
	if graph == nil {
		return []ValidationIssue{{
			Severity: ValidationSeverityError, Code: IssueRouteNoPath,
			Message: "route topology graph is unavailable",
		}}
	}
	routes := append([]model.RouteDefinition(nil), definitions...)
	sort.Slice(routes, func(i, j int) bool { return routes[i].ID < routes[j].ID })
	turnoutsByID := make(map[string]model.Turnout, len(turnouts))
	for _, turnout := range turnouts {
		turnoutsByID[turnout.ID] = turnout
	}

	issues := make([]ValidationIssue, 0)
	validated := make([]validatedRoute, 0, len(routes))
	for _, route := range routes {
		if route.EntryNodeID == "" && route.ExitNodeID == "" {
			continue
		}
		path, routeIssues, found := validateRouteDefinition(graph, route, turnoutsByID)
		issues = append(issues, routeIssues...)
		if found {
			validated = append(validated, validatedRoute{definition: route, resources: resourcesFromPath(graph, path)})
		}
	}
	issues = append(issues, undeclaredConflictIssues(validated)...)
	sortValidationIssues(issues)
	return issues
}

func validateRouteDefinition(graph *Graph, route model.RouteDefinition, turnouts map[string]model.Turnout) (Path, []ValidationIssue, bool) {
	issues := make([]ValidationIssue, 0)
	seen := map[string]bool{}
	add := func(issue ValidationIssue) {
		key := issue.Code + "\x00" + issue.ResourceID
		if !seen[key] {
			seen[key] = true
			issues = append(issues, issue)
		}
	}
	if route.EntryNodeID == "" || route.ExitNodeID == "" {
		add(routeIssue(ValidationSeverityError, IssueRouteNoPath, route.ID,
			fmt.Sprintf("route %q requires both entryNodeId and exitNodeId", route.ID)))
		return Path{}, issues, false
	}
	if _, exists := graph.Node(route.EntryNodeID); !exists {
		add(routeIssue(ValidationSeverityError, IssueRouteNoPath, route.EntryNodeID,
			fmt.Sprintf("route %q references unknown entry node %q", route.ID, route.EntryNodeID)))
	}
	if _, exists := graph.Node(route.ExitNodeID); !exists {
		add(routeIssue(ValidationSeverityError, IssueRouteNoPath, route.ExitNodeID,
			fmt.Sprintf("route %q references unknown exit node %q", route.ID, route.ExitNodeID)))
	}
	if len(issues) != 0 {
		return Path{}, issues, false
	}

	required := make(map[string]string)
	for turnoutID, positionID := range route.TurnoutStates {
		turnout, exists := turnouts[turnoutID]
		if !exists {
			add(routeIssue(ValidationSeverityError, IssueRouteMissingTurnoutPosition, turnoutID,
				fmt.Sprintf("route %q references unknown turnout %q", route.ID, turnoutID)))
			continue
		}
		if _, exists := turnout.Position(positionID); !exists {
			add(routeIssue(ValidationSeverityError, IssueRouteMissingTurnoutPosition, turnoutID,
				fmt.Sprintf("route %q requests unknown position %q on turnout %q", route.ID, positionID, turnoutID)))
			continue
		}
		_, exists = graph.TurnoutTopology(turnoutID)
		if !exists {
			continue
		}
		required[turnoutID] = positionID
	}

	excluded := make(map[string]bool)
	for _, turnoutID := range graph.turnoutIDs {
		if _, declared := route.TurnoutStates[turnoutID]; !declared {
			excluded[turnoutID] = true
		}
	}
	path, found := graph.FindPath(route.EntryNodeID, route.ExitNodeID, PathConstraints{
		ExcludedTurnouts:         excluded,
		RequiredTurnoutPositions: required,
	})
	if !found {
		path, found = graph.FindPath(route.EntryNodeID, route.ExitNodeID, PathConstraints{
			RequiredTurnoutPositions: required,
		})
	}
	if !found {
		path, found = graph.FindPath(route.EntryNodeID, route.ExitNodeID, PathConstraints{})
	}
	if !found {
		add(routeIssue(ValidationSeverityError, IssueRouteNoPath, route.ID,
			fmt.Sprintf("route %q has no physical path from %q to %q", route.ID, route.EntryNodeID, route.ExitNodeID)))
		return Path{}, issues, false
	}

	traversedTurnouts := make(map[string]bool)
	traversedBlocks := make(map[string]bool)
	for _, traversal := range path.Traversals {
		switch {
		case traversal.TrackSection != nil:
			if block, exists := graph.BlockForTrackSection(traversal.TrackSection.TrackSectionID); exists {
				traversedBlocks[block.ID] = true
			}
		case traversal.Turnout != nil:
			turnout := traversal.Turnout
			traversedTurnouts[turnout.TurnoutID] = true
			declared, exists := route.TurnoutStates[turnout.TurnoutID]
			if !exists || !containsString(turnout.PossiblePositionIDs, declared) {
				detail := "is not declared"
				if exists {
					detail = fmt.Sprintf("is declared as %q", declared)
				}
				add(routeIssue(ValidationSeverityError, IssueRouteMissingTurnoutPosition, turnout.TurnoutID,
					fmt.Sprintf("route %q requires turnout %q in one of [%s], but it %s", route.ID, turnout.TurnoutID, strings.Join(turnout.PossiblePositionIDs, ", "), detail)))
			}
			if block, exists := graph.BlockForTurnout(turnout.TurnoutID); exists {
				traversedBlocks[block.ID] = true
			}
		}
	}

	declaredBlocks := stringSet(route.BlockIDs)
	for blockID := range traversedBlocks {
		if !declaredBlocks[blockID] {
			add(routeIssue(ValidationSeverityError, IssueRouteMissingBlock, blockID,
				fmt.Sprintf("route %q traverses block %q without declaring it", route.ID, blockID)))
		}
	}
	for blockID := range declaredBlocks {
		if !traversedBlocks[blockID] {
			add(routeIssue(ValidationSeverityWarning, IssueRouteExtraBlock, blockID,
				fmt.Sprintf("route %q declares non-traversed block %q as additional protection", route.ID, blockID)))
		}
	}
	for turnoutID := range route.TurnoutStates {
		if !traversedTurnouts[turnoutID] {
			add(routeIssue(ValidationSeverityWarning, IssueRouteExtraTurnout, turnoutID,
				fmt.Sprintf("route %q declares non-traversed turnout %q as additional protection", route.ID, turnoutID)))
		}
	}
	return path, issues, true
}

func undeclaredConflictIssues(routes []validatedRoute) []ValidationIssue {
	var issues []ValidationIssue
	for firstIndex := 0; firstIndex < len(routes); firstIndex++ {
		for secondIndex := firstIndex + 1; secondIndex < len(routes); secondIndex++ {
			first, second := routes[firstIndex], routes[secondIndex]
			if !resourceSetsOverlap(first.resources, second.resources) {
				continue
			}
			if !containsString(first.definition.ConflictRouteIDs, second.definition.ID) {
				issues = append(issues, routeIssue(ValidationSeverityWarning, IssueRoutePossibleUndeclaredConflict, first.definition.ID,
					fmt.Sprintf("route %q shares physical resources with route %q but does not declare it as a conflict", first.definition.ID, second.definition.ID)))
			}
			if !containsString(second.definition.ConflictRouteIDs, first.definition.ID) {
				issues = append(issues, routeIssue(ValidationSeverityWarning, IssueRoutePossibleUndeclaredConflict, second.definition.ID,
					fmt.Sprintf("route %q shares physical resources with route %q but does not declare it as a conflict", second.definition.ID, first.definition.ID)))
			}
		}
	}
	return issues
}

func resourcesFromPath(graph *Graph, path Path) map[string]bool {
	resources := make(map[string]bool)
	for _, traversal := range path.Traversals {
		if traversal.TrackSection != nil {
			sectionID := traversal.TrackSection.TrackSectionID
			resources["section:"+sectionID] = true
			if block, exists := graph.BlockForTrackSection(sectionID); exists {
				resources["block:"+block.ID] = true
			}
		}
		if traversal.Turnout != nil {
			turnoutID := traversal.Turnout.TurnoutID
			resources["turnout:"+turnoutID] = true
			if block, exists := graph.BlockForTurnout(turnoutID); exists {
				resources["block:"+block.ID] = true
			}
		}
	}
	return resources
}

func resourceSetsOverlap(first, second map[string]bool) bool {
	for resource := range first {
		if second[resource] {
			return true
		}
	}
	return false
}

func stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func routeIssue(severity ValidationSeverity, code, resourceID, message string) ValidationIssue {
	return ValidationIssue{Severity: severity, Code: code, ResourceID: resourceID, Message: message}
}

func sortValidationIssues(issues []ValidationIssue) {
	sort.Slice(issues, func(i, j int) bool {
		if issues[i].Severity != issues[j].Severity {
			return issues[i].Severity == ValidationSeverityError
		}
		if issues[i].Code != issues[j].Code {
			return issues[i].Code < issues[j].Code
		}
		if issues[i].ResourceID != issues[j].ResourceID {
			return issues[i].ResourceID < issues[j].ResourceID
		}
		return issues[i].Message < issues[j].Message
	})
}
