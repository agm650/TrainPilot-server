package main

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/spf13/cobra"
)

func newAcquireCommand(a *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "acquire <locomotive-id>",
		Short: "Acquire exclusive control of a locomotive",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			lease, err := a.client.Acquire(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			a.profile.setLease(lease)
			if err := saveState(a.statePath, a.state); err != nil {
				return fmt.Errorf("save lease: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), lease.ID)
			return nil
		},
	}
}

func newThrottleCommand(a *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "throttle <locomotive-id> <speed-0..100> [forward|reverse]",
		Short: "Set locomotive speed and direction",
		Args:  cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			lease, err := a.savedLease(args[0])
			if err != nil {
				return err
			}
			speed, err := strconv.Atoi(args[1])
			if err != nil || speed < 0 || speed > 100 {
				return errors.New("speed must be an integer between 0 and 100")
			}
			direction := station.Forward
			if len(args) == 3 {
				direction = station.Direction(args[2])
				if direction != station.Forward && direction != station.Reverse {
					return errors.New("direction must be forward or reverse")
				}
			}
			if err := a.client.Throttle(cmd.Context(), args[0], lease.ID, speed, direction); err != nil {
				a.forgetRejectedLease(args[0], err)
				return err
			}
			return nil
		},
	}
}

func newFunctionCommand(a *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "function <locomotive-id> <function-0..68> <true|false>",
		Short: "Enable or disable a locomotive function",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			lease, err := a.savedLease(args[0])
			if err != nil {
				return err
			}
			function, err := strconv.Atoi(args[1])
			if err != nil || function < 0 || function > 68 {
				return errors.New("function number must be between 0 and 68")
			}
			enabled, err := strconv.ParseBool(args[2])
			if err != nil {
				return errors.New("function state must be true or false")
			}
			if err := a.client.Function(cmd.Context(), args[0], lease.ID, function, enabled); err != nil {
				a.forgetRejectedLease(args[0], err)
				return err
			}
			return nil
		},
	}
}

func newReleaseCommand(a *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "release <locomotive-id>",
		Short: "Release control of a locomotive",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			lease, err := a.savedLease(args[0])
			if err != nil {
				return err
			}
			if err := a.client.Release(cmd.Context(), lease.ID); err != nil {
				a.forgetRejectedLease(args[0], err)
				return err
			}
			delete(a.profile.Leases, args[0])
			if err := saveState(a.statePath, a.state); err != nil {
				return fmt.Errorf("save lease state: %w", err)
			}
			return nil
		},
	}
}

func newPowerCommand(a *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use: "power", Short: "Manage track power", Args: cobra.NoArgs,
	}
	cmd.AddCommand(
		&cobra.Command{
			Use: "on", Short: "Enable track power", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error { return a.client.SetTrackPower(cmd.Context(), true) },
		},
		&cobra.Command{
			Use: "off", Short: "Disable track power", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error { return a.client.SetTrackPower(cmd.Context(), false) },
		},
		&cobra.Command{
			Use: "status", Short: "Report track-power status", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				status, err := a.client.StationStatus(cmd.Context())
				if err != nil {
					return err
				}
				lastSeen := "unknown"
				if status.LastSeen != nil {
					lastSeen = status.LastSeen.Format(time.RFC3339Nano)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "connectivity: %s\nlast-seen: %s\ntrack-power: %s\nemergency-stop: %t\nshort-circuit: %t\nprogramming-mode: %t\nmain-current-ma: %d\nfiltered-main-current-ma: %d\ntemperature-c: %d\nsupply-voltage-mv: %d\ntrack-voltage-mv: %d\nhigh-temperature: %t\npower-lost: %t\nexternal-short-circuit: %t\ninternal-short-circuit: %t\n", status.Connectivity, lastSeen, status.TrackPower, status.EmergencyStop, status.ShortCircuit, status.ProgrammingMode, status.MainCurrentMilliAmps, status.FilteredMainCurrentMilliAmps, status.TemperatureCelsius, status.SupplyVoltageMilliVolts, status.TrackVoltageMilliVolts, status.HighTemperature, status.PowerLost, status.ExternalShortCircuit, status.InternalShortCircuit)
				return nil
			},
		},
	)
	return cmd
}

func newEmergencyStopCommand(a *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "emergency-stop",
		Short: "Send a global emergency-stop command",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.client.EmergencyStop(cmd.Context())
		},
	}
}
