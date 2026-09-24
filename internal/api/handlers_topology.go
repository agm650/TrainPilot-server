package api

import (
	"context"
	"net/http"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/presentation"
	"github.com/agm650/TrainPilot-server/internal/topology"
)

func (s *Server) getTopology(w http.ResponseWriter, r *http.Request) {
	turnouts, err := s.store.ListTurnouts(r.Context())
	if err != nil {
		writeOperationProblem(w, err, "topology_read_failed")
		return
	}
	definition, err := s.topologyDefinition(r.Context(), turnouts)
	if err != nil {
		writeOperationProblem(w, err, "topology_read_failed")
		return
	}
	writeJSON(w, http.StatusOK, definition)
}

func (s *Server) topologyDefinition(ctx context.Context, turnouts []model.Turnout) (model.TopologyDefinition, error) {
	layout, err := s.store.GetTopologyDefinition(ctx)
	if err != nil {
		return model.TopologyDefinition{}, err
	}
	blocks, err := s.store.ListBlockDefinitions(ctx)
	if err != nil {
		return model.TopologyDefinition{}, err
	}
	layout.Turnouts = turnouts
	layout.Blocks = blocks
	return topology.DefinitionFromLayout(layout)
}

func (s *Server) getLayoutPresentation(w http.ResponseWriter, r *http.Request) {
	definition, err := s.layoutPresentationDefinition(r.Context())
	if err != nil {
		writeOperationProblem(w, err, "layout_presentation_read_failed")
		return
	}
	writeJSON(w, http.StatusOK, definition)
}

func (s *Server) layoutPresentationDefinition(ctx context.Context) (model.LayoutPresentationDefinition, error) {
	value, err := s.store.GetLayoutPresentation(ctx)
	if err != nil {
		return model.LayoutPresentationDefinition{}, err
	}
	return presentation.Definition(value)
}
