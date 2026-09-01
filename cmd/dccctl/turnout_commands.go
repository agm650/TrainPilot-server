package main

import (
	"fmt"
	"strings"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/spf13/cobra"
)

func newTurnoutsCommand(a *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "turnouts",
		Short: "List turnouts and their operational state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			items, err := a.client.Turnouts(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "ID\tNAME\tKIND\tDESIRED\tREPORTED\tPENDING\tQUALITY")
			for _, turnout := range items {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					turnout.ID, turnout.Name, turnout.Kind, displayTurnoutValue(turnout.DesiredPosition),
					displayTurnoutValue(turnout.ReportedPosition), yesNo(turnout.Pending), displayTurnoutValue(string(turnout.Quality)))
			}
			return nil
		},
	}
}

func newTurnoutCommand(a *commandContext) *cobra.Command {
	var showPositions bool
	cmd := &cobra.Command{
		Use:   "turnout <turnout-id> [position]",
		Short: "Show valid positions or command a turnout",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			turnout, err := findTurnout(cmd, a, args[0])
			if err != nil {
				return err
			}
			if len(args) == 1 {
				if !showPositions {
					return fmt.Errorf("position is required; valid positions: %s", validTurnoutPositions(turnout))
				}
				for _, position := range turnout.Positions {
					fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", position.ID, position.Label)
				}
				return nil
			}
			if showPositions {
				return fmt.Errorf("--positions cannot be used with a position argument")
			}
			if _, ok := turnout.Position(args[1]); !ok {
				return fmt.Errorf("invalid position %q for turnout %q; valid positions: %s", args[1], turnout.ID, validTurnoutPositions(turnout))
			}
			if err := a.client.SetTurnout(cmd.Context(), turnout.ID, args[1]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", turnout.ID, args[1])
			return nil
		},
	}
	cmd.Flags().BoolVar(&showPositions, "positions", false, "list valid logical positions")
	return cmd
}

func findTurnout(cmd *cobra.Command, a *commandContext, id string) (model.Turnout, error) {
	items, err := a.client.Turnouts(cmd.Context())
	if err != nil {
		return model.Turnout{}, err
	}
	for _, turnout := range items {
		if turnout.ID == id {
			return turnout, nil
		}
	}
	return model.Turnout{}, fmt.Errorf("turnout %q not found", id)
}

func validTurnoutPositions(turnout model.Turnout) string {
	positions := make([]string, 0, len(turnout.Positions))
	for _, position := range turnout.Positions {
		positions = append(positions, position.ID)
	}
	return strings.Join(positions, ", ")
}

func displayTurnoutValue(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
