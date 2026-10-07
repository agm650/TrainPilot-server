package main

import (
	"encoding/json"
	"fmt"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/topology"
	"github.com/spf13/cobra"
)

func newTopologyCommand(a *commandContext) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "topology",
		Short: "Show the persisted physical topology",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			definition, err := a.client.Topology(cmd.Context())
			if err != nil {
				return err
			}
			if asJSON {
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(definition)
			}
			graph, err := a.topologyGraph(cmd, definition)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Nodes: %d\nTrack sections: %d\nTurnouts: %d\nBlocks: %d\nComponents: %d\n",
				len(definition.Nodes), len(definition.TrackSections), len(definition.TurnoutTopologies),
				len(definition.Blocks), len(graph.ConnectedComponents()))
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "write the canonical topology as JSON")
	cmd.AddCommand(newTopologyValidateCommand(a))
	return cmd
}

func newTopologyValidateCommand(a *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate the persisted topology and revision",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			definition, err := a.client.Topology(cmd.Context())
			if err != nil {
				return err
			}
			if _, err := a.topologyGraph(cmd, definition); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Topology valid: %s\n", definition.Revision)
			return nil
		},
	}
}

func (a *commandContext) topologyGraph(cmd *cobra.Command, definition model.TopologyDefinition) (*topology.Graph, error) {
	turnouts, err := a.client.Turnouts(cmd.Context())
	if err != nil {
		return nil, err
	}
	return topology.BuildDefinition(definition, turnouts)
}
