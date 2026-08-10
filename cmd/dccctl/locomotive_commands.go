package main

import (
	"fmt"
	"strconv"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/spf13/cobra"
)

func newLocomotivesCommand(a *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "locomotives",
		Short: "List locomotives",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			items, err := a.client.Locomotives(cmd.Context())
			if err != nil {
				return err
			}
			for _, l := range items {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%d\t%s\n", l.ID, l.DCCAddress, l.Name)
			}
			return nil
		},
	}
}

func newLocomotiveShowCommand(a *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "locomotive-show <locomotive-id>",
		Short: "Show a locomotive",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			l, err := a.client.Locomotive(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			printLocomotive(cmd, l)
			return nil
		},
	}
}

func newLocomotiveAddCommand(a *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "locomotive-add <name> <dcc-address> [short|long] [14|28|128] [manufacturer] [model]",
		Short: "Add a locomotive",
		Args:  cobra.RangeArgs(2, 6),
		RunE: func(cmd *cobra.Command, args []string) error {
			input, err := parseLocomotiveInput(args)
			if err != nil {
				return err
			}
			l, err := a.client.CreateLocomotive(cmd.Context(), input)
			if err != nil {
				return err
			}
			printLocomotive(cmd, l)
			return nil
		},
	}
}

func newLocomotiveUpdateCommand(a *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "locomotive-update <id> <name> <dcc-address> [short|long] [14|28|128] [manufacturer] [model]",
		Short: "Update a locomotive",
		Args:  cobra.RangeArgs(3, 7),
		RunE: func(cmd *cobra.Command, args []string) error {
			input, err := parseLocomotiveInput(args[1:])
			if err != nil {
				return err
			}
			l, err := a.client.UpdateLocomotive(cmd.Context(), args[0], input)
			if err != nil {
				return err
			}
			printLocomotive(cmd, l)
			return nil
		},
	}
}

func newLocomotiveDeleteCommand(a *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "locomotive-delete <locomotive-id>",
		Short: "Delete a locomotive",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.client.DeleteLocomotive(cmd.Context(), args[0])
		},
	}
}

func parseLocomotiveInput(args []string) (model.LocomotiveInput, error) {
	address, err := strconv.Atoi(args[1])
	if err != nil {
		return model.LocomotiveInput{}, fmt.Errorf("invalid DCC address: %w", err)
	}
	kind := "short"
	if address >= 128 {
		kind = "long"
	}
	if len(args) > 2 {
		kind = args[2]
	}
	steps := 128
	if len(args) > 3 {
		steps, err = strconv.Atoi(args[3])
		if err != nil {
			return model.LocomotiveInput{}, fmt.Errorf("invalid speed steps: %w", err)
		}
	}
	manufacturer := ""
	if len(args) > 4 {
		manufacturer = args[4]
	}
	modelName := ""
	if len(args) > 5 {
		modelName = args[5]
	}
	return model.LocomotiveInput{Name: args[0], DCCAddress: address, AddressKind: kind, SpeedSteps: steps, Manufacturer: manufacturer, Model: modelName}, nil
}

func printLocomotive(cmd *cobra.Command, l model.Locomotive) {
	fmt.Fprintf(cmd.OutOrStdout(), "%s\t%d\t%s\t%s\t%d\t%s\t%s\n", l.ID, l.DCCAddress, l.AddressKind, l.Name, l.SpeedSteps, l.Manufacturer, l.Model)
}
