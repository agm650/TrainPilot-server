package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	bench "github.com/agm650/TrainPilot-server/internal/benchmark"
	"github.com/spf13/cobra"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	command := newRootCommand()
	command.SetContext(ctx)
	if err := command.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	command := &cobra.Command{
		Use:           "trainpilot-bench",
		Short:         "Generate reproducible load against a TrainPilot server",
		Version:       version,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	command.AddCommand(newValidateProfileCommand(), newRunCommand())
	return command
}

func newValidateProfileCommand() *cobra.Command {
	var fixturePath string
	command := &cobra.Command{
		Use:   "validate-profile <profile.yaml>",
		Short: "Validate a versioned benchmark profile and its fixture",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			profile, err := bench.LoadProfile(args[0])
			if err != nil {
				return err
			}
			resolvedFixture := fixturePath
			if resolvedFixture == "" && profile.Fixture != "" {
				resolvedFixture = resolveRelative(args[0], profile.Fixture)
			}
			var fixture bench.Fixture
			if resolvedFixture != "" {
				fixture, err = bench.LoadFixture(resolvedFixture)
				if err != nil {
					return err
				}
			}
			if err := bench.ValidateFixtureForProfile(profile, fixture); err != nil {
				return err
			}
			fmt.Fprintf(command.OutOrStdout(), "profile %q is valid\n", profile.Name)
			return nil
		},
	}
	command.Flags().StringVar(&fixturePath, "fixture", "", "override fixture JSON path")
	return command
}

type runFlags struct {
	server              string
	profile             string
	fixture             string
	credentials         string
	environmentAccounts []string
	duration            time.Duration
	seed                int64
	output              string
	allowActive         bool
	allowSimulatorAPI   bool
	allowRealHardware   bool
}

func newRunCommand() *cobra.Command {
	flags := &runFlags{}
	command := &cobra.Command{
		Use:   "run",
		Short: "Run a benchmark and write a versioned JSON report",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return executeRun(command, flags)
		},
	}
	command.Flags().StringVar(&flags.server, "server", "http://127.0.0.1:8080", "TrainPilot server URL")
	command.Flags().StringVar(&flags.profile, "profile", "", "benchmark profile YAML path")
	command.Flags().StringVar(&flags.fixture, "fixture", "", "override fixture JSON path")
	command.Flags().StringVar(&flags.credentials, "credentials", "", "credentials JSON path")
	command.Flags().StringSliceVar(&flags.environmentAccounts, "credential", nil, "credential mapping username=ENV_VAR; repeat for multiple accounts")
	command.Flags().DurationVar(&flags.duration, "duration", 0, "override measured duration")
	command.Flags().Int64Var(&flags.seed, "seed", 0, "override deterministic seed")
	command.Flags().StringVar(&flags.output, "output", "", "JSON report path")
	command.Flags().BoolVar(&flags.allowActive, "allow-active-commands", false, "allow leases, track power, throttle, functions, accessories, and routes")
	command.Flags().BoolVar(&flags.allowSimulatorAPI, "allow-simulator-api", false, "allow event injection through the simulator test API")
	command.Flags().BoolVar(&flags.allowRealHardware, "allow-real-hardware", false, "confirm that active commands may target a non-simulator driver")
	_ = command.MarkFlagRequired("profile")
	_ = command.MarkFlagRequired("output")
	return command
}

func executeRun(command *cobra.Command, flags *runFlags) error {
	profile, err := bench.LoadProfile(flags.profile)
	if err != nil {
		return err
	}
	if command.Flags().Changed("duration") {
		profile.Duration.Duration = flags.duration
	}
	if command.Flags().Changed("seed") {
		profile.Seed = flags.seed
	}
	if err := profile.Validate(); err != nil {
		return fmt.Errorf("profile override: %w", err)
	}
	fixturePath := flags.fixture
	if fixturePath == "" && profile.Fixture != "" {
		fixturePath = resolveRelative(flags.profile, profile.Fixture)
	}
	var fixture bench.Fixture
	if fixturePath != "" {
		fixture, err = bench.LoadFixture(fixturePath)
		if err != nil {
			return err
		}
	}
	if flags.fixture != "" {
		profile.Fixture = flags.fixture
	}
	credentials, err := loadRunCredentials(flags)
	if err != nil {
		return err
	}
	report, err := bench.Run(command.Context(), bench.RunOptions{
		Server: flags.server, Profile: profile, Fixture: fixture, Credentials: credentials,
		AllowActiveCommands: flags.allowActive, AllowSimulatorAPI: flags.allowSimulatorAPI,
		AllowRealHardware: flags.allowRealHardware, BenchmarkVersion: version,
	})
	if err != nil {
		return err
	}
	if err := bench.WriteReport(flags.output, report); err != nil {
		return err
	}
	bench.WriteConsoleSummary(command.OutOrStdout(), report)
	fmt.Fprintf(command.OutOrStdout(), "Report: %s\n", flags.output)
	if report.OverallResult == "FAIL" {
		return errors.New("benchmark invariant violation")
	}
	return nil
}

func loadRunCredentials(flags *runFlags) ([]bench.Credential, error) {
	if flags.credentials != "" && len(flags.environmentAccounts) > 0 {
		return nil, errors.New("use either --credentials or --credential, not both")
	}
	if flags.credentials != "" {
		return bench.LoadCredentials(flags.credentials)
	}
	if len(flags.environmentAccounts) > 0 {
		return bench.ParseEnvironmentCredentials(flags.environmentAccounts, os.LookupEnv)
	}
	return nil, errors.New("--credentials or at least one --credential is required")
}

func resolveRelative(profilePath, value string) string {
	if filepath.IsAbs(value) {
		return value
	}
	return filepath.Join(filepath.Dir(profilePath), value)
}
