package transfer

import (
	"context"
	"errors"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/service"
	"github.com/agm650/TrainPilot-server/internal/sqlite"
	"github.com/agm650/TrainPilot-server/internal/store"
	"github.com/agm650/TrainPilot-server/internal/topology"
	sqliteDriver "modernc.org/sqlite"
	sqliteLib "modernc.org/sqlite/lib"
)

type LayoutDiagnostic struct {
	Code         string `json:"code"`
	ResourceType string `json:"resourceType,omitempty"`
	ResourceID   string `json:"resourceId,omitempty"`
	Message      string `json:"message"`
}

type LayoutValidationResult struct {
	Valid    bool               `json:"valid"`
	Errors   []LayoutDiagnostic `json:"errors"`
	Warnings []LayoutDiagnostic `json:"warnings"`
}

func newLayoutValidationResult() LayoutValidationResult {
	return LayoutValidationResult{Valid: true, Errors: []LayoutDiagnostic{}, Warnings: []LayoutDiagnostic{}}
}

// ValidateLayout runs the import checks against current database state. The
// store executes its import transaction and rolls it back before returning.
func (s *Service) ValidateLayout(ctx context.Context, user model.User, data []byte, replace bool) (LayoutValidationResult, error) {
	result := newLayoutValidationResult()
	if !service.Allowed(user.Role, service.PermissionConfigure) {
		return result, service.ErrPermissionDenied
	}
	var doc LayoutDocument
	if err := readArchive(data, "layout", "layout.json", &doc); err != nil {
		result.Errors = append(result.Errors, LayoutDiagnostic{Code: "invalid_archive", Message: err.Error()})
		result.Valid = false
		return result, nil
	}
	if err := validateLayout(&doc.Layout); err != nil {
		result.Errors = append(result.Errors, layoutDiagnostics(err)...)
		result.Valid = false
		return result, nil
	}
	if err := s.store.ValidateLayoutImport(ctx, doc.Layout, replace); err != nil {
		if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, sqlite.ErrRollbackFailed) || !isLayoutValidationError(err) {
			return result, err
		}
		result.Errors = append(result.Errors, layoutDiagnostics(err)...)
		result.Valid = false
		return result, nil
	}
	graph, err := topology.Build(doc.Layout)
	if err != nil {
		return result, err // already validated by the import pipeline
	}
	for _, issue := range topology.ValidateRouteDefinitions(graph, doc.Layout.Routes, doc.Layout.Turnouts) {
		if issue.Severity == topology.ValidationSeverityWarning {
			result.Warnings = append(result.Warnings, LayoutDiagnostic{
				Code: issue.Code, ResourceType: "route", ResourceID: issue.ResourceID, Message: issue.Message,
			})
		}
	}
	return result, nil
}

func isLayoutValidationError(err error) bool {
	if errors.Is(err, store.ErrConflict) || errors.Is(err, model.ErrInvalidLayoutPresentation) ||
		errors.Is(err, model.ErrInvalidTurnout) || errors.Is(err, model.ErrInvalidTopology) ||
		errors.Is(err, model.ErrInvalidBlockDefinition) || errors.Is(err, topology.ErrInvalidGraph) ||
		errors.Is(err, topology.ErrInvalidRouteDefinition) {
		return true
	}
	var sqliteErr *sqliteDriver.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code()&0xff == sqliteLib.SQLITE_CONSTRAINT
}

func layoutDiagnostics(err error) []LayoutDiagnostic {
	var presentationErr *model.LayoutPresentationValidationError
	if errors.As(err, &presentationErr) {
		return []LayoutDiagnostic{{Code: presentationErr.Code, ResourceType: presentationErr.ResourceType, ResourceID: presentationErr.ResourceID, Message: presentationErr.Message}}
	}
	var routeErr *topology.RouteDefinitionValidationError
	if errors.As(err, &routeErr) {
		out := make([]LayoutDiagnostic, 0, len(routeErr.Issues))
		for _, issue := range routeErr.Issues {
			out = append(out, LayoutDiagnostic{Code: issue.Code, ResourceType: "route", ResourceID: issue.ResourceID, Message: issue.Message})
		}
		return out
	}
	code := "layout_invalid"
	switch {
	case errors.Is(err, store.ErrAccessoryAddressConflict):
		code = "accessory_address_conflict"
	case errors.Is(err, store.ErrTurnoutConfigurationPending):
		code = "turnout_configuration_pending"
	case errors.Is(err, model.ErrInvalidBlockDefinition):
		code = "block_membership_invalid"
	case errors.Is(err, model.ErrInvalidTopology), errors.Is(err, topology.ErrInvalidGraph):
		code = "topology_invalid"
	case errors.Is(err, model.ErrInvalidTurnout):
		code = "turnout_configuration_invalid"
	case errors.Is(err, model.ErrInvalidLayoutPresentation):
		code = "layout_presentation_invalid"
	case errors.Is(err, store.ErrConflict):
		code = "layout_import_conflict"
	}
	return []LayoutDiagnostic{{Code: code, Message: err.Error()}}
}
