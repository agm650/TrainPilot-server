package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	bench "github.com/agm650/TrainPilot-server/internal/benchmark"
	"github.com/spf13/cobra"
)

var version = "dev"

const benchmarkMetricsFinalScrapeWait = 3 * time.Second

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
	command.AddCommand(newValidateProfileCommand(), newGenerateFixtureCommand(), newRunCommand(), newEnrichReportCommand(), newValidateReportCommand(), newCompareCommand(), newAnalyzeSoakCommand())
	return command
}

func newGenerateFixtureCommand() *cobra.Command {
	var output string
	command := &cobra.Command{
		Use:   "generate-fixture <small|medium|large|xlarge>",
		Short: "Generate deterministic import archives and benchmark selectors",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			if err := bench.WriteDataset(output, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(command.OutOrStdout(), "fixture %q written to %s\n", args[0], output)
			return nil
		},
	}
	command.Flags().StringVar(&output, "output", "", "output directory")
	_ = command.MarkFlagRequired("output")
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
	warmup              time.Duration
	seed                int64
	output              string
	allowActive         bool
	allowSimulatorAPI   bool
	allowRealHardware   bool
	allowPlannedOutage  bool
	simulatorScenario   string
	metricsListen       string
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
	command.Flags().DurationVar(&flags.warmup, "warmup", 0, "override warm-up duration")
	command.Flags().Int64Var(&flags.seed, "seed", 0, "override deterministic seed")
	command.Flags().StringVar(&flags.output, "output", "", "JSON report path")
	command.Flags().BoolVar(&flags.allowActive, "allow-active-commands", false, "allow leases, track power, throttle, functions, accessories, and routes")
	command.Flags().BoolVar(&flags.allowSimulatorAPI, "allow-simulator-api", false, "allow event injection through the simulator test API")
	command.Flags().BoolVar(&flags.allowRealHardware, "allow-real-hardware", false, "confirm that active commands may target a non-simulator driver")
	command.Flags().BoolVar(&flags.allowPlannedOutage, "allow-planned-outage", false, "allow a profile to declare a bounded server outage")
	command.Flags().StringVar(&flags.simulatorScenario, "simulator-scenario", "", "simulator scenario JSON run in wall-clock time during measurement")
	command.Flags().StringVar(&flags.metricsListen, "metrics-listen", "", "expose optional benchmark metrics on this address")
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
	if command.Flags().Changed("warmup") {
		profile.Warmup.Duration = flags.warmup
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
	var simulatorScenario *bench.SimulatorScenario
	if flags.simulatorScenario != "" {
		simulatorScenario, err = bench.LoadSimulatorScenario(flags.simulatorScenario)
		if err != nil {
			return err
		}
	}
	var liveMetrics *bench.LiveMetrics
	var metricsServer *benchmarkMetricsServer
	if flags.metricsListen != "" {
		liveMetrics = bench.NewLiveMetrics()
		metricsServer, err = startBenchmarkMetricsServer(flags.metricsListen, liveMetrics.Handler())
		if err != nil {
			return err
		}
		defer metricsServer.closeAfter(command.Context(), benchmarkMetricsFinalScrapeWait)
		fmt.Fprintf(command.ErrOrStderr(), "Benchmark metrics listening on http://%s/metrics\n", metricsServer.address())
	}
	report, err := bench.Run(command.Context(), bench.RunOptions{
		Server: flags.server, Profile: profile, Fixture: fixture, Credentials: credentials,
		AllowActiveCommands: flags.allowActive, AllowSimulatorAPI: flags.allowSimulatorAPI,
		AllowRealHardware: flags.allowRealHardware, BenchmarkVersion: version,
		AllowPlannedOutage: flags.allowPlannedOutage,
		SimulatorScenario:  simulatorScenario,
		LiveMetrics:        liveMetrics,
		OnMeasurementStart: func(startedAt time.Time) {
			fmt.Fprintf(command.ErrOrStderr(), "Measurement started at %s\n", startedAt.Format(time.RFC3339Nano))
		},
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
		return errors.New("benchmark functional failure")
	}
	return nil
}

type benchmarkMetricsServer struct {
	listener net.Listener
	server   *http.Server
	done     chan error
}

func startBenchmarkMetricsServer(address string, handler http.Handler) (*benchmarkMetricsServer, error) {
	if address == "" {
		return nil, nil
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("listen for benchmark metrics: %w", err)
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", handler)
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	result := &benchmarkMetricsServer{listener: listener, server: server, done: make(chan error, 1)}
	go func() {
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		result.done <- err
		close(result.done)
	}()
	return result, nil
}

func (s *benchmarkMetricsServer) address() string {
	if s == nil {
		return ""
	}
	return s.listener.Addr().String()
}

func (s *benchmarkMetricsServer) close() error {
	if s == nil {
		return nil
	}
	return errors.Join(s.server.Close(), <-s.done)
}

func (s *benchmarkMetricsServer) closeAfter(ctx context.Context, delay time.Duration) error {
	if s == nil {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
	return s.close()
}

func newEnrichReportCommand() *cobra.Command {
	var metadataPath string
	var metricsPath string
	var outputPath string
	command := &cobra.Command{
		Use:   "enrich-report <report.json>",
		Short: "Attach external hardware and system metrics to a report",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			if metadataPath == "" && metricsPath == "" {
				return errors.New("provide --metadata or --metrics")
			}
			report, err := bench.LoadReport(args[0])
			if err != nil {
				return err
			}
			if metadataPath != "" {
				metadata, err := bench.LoadRunMetadata(metadataPath)
				if err != nil {
					return err
				}
				bench.ApplyRunMetadata(&report, metadata)
			}
			if metricsPath != "" {
				metrics, err := bench.LoadSystemMetrics(metricsPath)
				if err != nil {
					return err
				}
				bench.ApplySystemMetrics(&report, metrics)
			}
			bench.ApplyReportPolicy(&report)
			destination := outputPath
			if destination == "" {
				destination = args[0]
			}
			if err := bench.WriteReport(destination, report); err != nil {
				return err
			}
			fmt.Fprintf(command.OutOrStdout(), "Enriched report: %s\n", destination)
			return nil
		},
	}
	command.Flags().StringVar(&metadataPath, "metadata", "", "versioned hardware and server metadata JSON")
	command.Flags().StringVar(&metricsPath, "metrics", "", "versioned external system metrics summary JSON")
	command.Flags().StringVar(&outputPath, "output", "", "output report path; defaults to replacing the input atomically")
	return command
}

func newValidateReportCommand() *cobra.Command {
	var publication bool
	command := &cobra.Command{
		Use:   "validate-report <report.json>",
		Short: "Validate a benchmark report",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			report, err := bench.LoadReport(args[0])
			if err != nil {
				return err
			}
			if publication {
				if err := bench.ValidateReportForPublication(report); err != nil {
					return err
				}
			}
			fmt.Fprintf(command.OutOrStdout(), "Report is valid: %s\n", args[0])
			return nil
		},
	}
	command.Flags().BoolVar(&publication, "publication", false, "require all metadata needed for a published baseline")
	return command
}

func newCompareCommand() *cobra.Command {
	var baselinePaths []string
	var candidatePaths []string
	var outputPath string
	command := &cobra.Command{
		Use:   "compare",
		Short: "Compare medians from two groups of at least three reports",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			baseline, err := bench.LoadReports(baselinePaths)
			if err != nil {
				return fmt.Errorf("baseline: %w", err)
			}
			candidate, err := bench.LoadReports(candidatePaths)
			if err != nil {
				return fmt.Errorf("candidate: %w", err)
			}
			comparison, err := bench.CompareReports(baseline, candidate)
			if err != nil {
				return err
			}
			bench.WriteComparisonSummary(command.OutOrStdout(), comparison)
			if err := bench.WriteComparison(outputPath, comparison); err != nil {
				return err
			}
			if outputPath != "" {
				fmt.Fprintf(command.OutOrStdout(), "Comparison report: %s\n", outputPath)
			}
			if comparison.Result == "FAIL" {
				return errors.New("candidate has functional failures")
			}
			return nil
		},
	}
	command.Flags().StringArrayVar(&baselinePaths, "baseline", nil, "baseline report path; repeat at least three times")
	command.Flags().StringArrayVar(&candidatePaths, "candidate", nil, "candidate report path; repeat at least three times")
	command.Flags().StringVar(&outputPath, "output", "", "optional versioned comparison JSON path")
	return command
}

func newAnalyzeSoakCommand() *cobra.Command {
	thresholds := bench.DefaultSoakThresholds()
	var prometheusURL string
	var instance string
	var output string
	var scrapeInterval time.Duration
	command := &cobra.Command{
		Use:   "analyze-soak <report.json>",
		Short: "Analyze a soak run using Prometheus recording rules",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			report, err := bench.LoadReport(args[0])
			if err != nil {
				return err
			}
			analysis, err := bench.AnalyzeSoak(command.Context(), report, bench.SoakAnalysisOptions{
				PrometheusURL: prometheusURL, Instance: instance, Window: 30 * time.Minute,
				ScrapeInterval: scrapeInterval, Thresholds: thresholds,
			})
			if err != nil {
				return err
			}
			if err := bench.WriteSoakAnalysis(output, analysis); err != nil {
				return err
			}
			fmt.Fprintf(command.OutOrStdout(), "Soak analysis: %s\nResult: %s\n", output, analysis.OverallResult)
			return nil
		},
	}
	command.Flags().StringVar(&prometheusURL, "prometheus", "http://127.0.0.1:9090", "Prometheus base URL")
	command.Flags().StringVar(&instance, "instance", "", "TrainPilot Prometheus instance label")
	command.Flags().StringVar(&output, "output", "", "versioned soak analysis JSON path")
	command.Flags().DurationVar(&scrapeInterval, "scrape-interval", 15*time.Second, "Prometheus scrape interval used for coverage")
	command.Flags().Float64Var(&thresholds.RSSBytesPerHour, "warn-rss-bytes-per-hour", thresholds.RSSBytesPerHour, "RSS slope warning threshold")
	command.Flags().Float64Var(&thresholds.HeapBytesPerHour, "warn-heap-bytes-per-hour", thresholds.HeapBytesPerHour, "Go heap slope warning threshold")
	command.Flags().Float64Var(&thresholds.HeapObjectsPerHour, "warn-heap-objects-per-hour", thresholds.HeapObjectsPerHour, "heap object slope warning threshold")
	command.Flags().Float64Var(&thresholds.GoroutinesPerHour, "warn-goroutines-per-hour", thresholds.GoroutinesPerHour, "goroutine slope warning threshold")
	command.Flags().Float64Var(&thresholds.ThreadsPerHour, "warn-threads-per-hour", thresholds.ThreadsPerHour, "thread slope warning threshold")
	command.Flags().Float64Var(&thresholds.FileDescriptorsHour, "warn-fds-per-hour", thresholds.FileDescriptorsHour, "file descriptor slope warning threshold")
	command.Flags().Float64Var(&thresholds.SQLiteBytesPerHour, "warn-sqlite-bytes-per-hour", thresholds.SQLiteBytesPerHour, "SQLite file growth warning threshold")
	command.Flags().Float64Var(&thresholds.CPUIncreasePct, "warn-cpu-increase-percent", thresholds.CPUIncreasePct, "CPU relative warning threshold")
	command.Flags().Float64Var(&thresholds.LatencyIncreasePct, "warn-latency-increase-percent", thresholds.LatencyIncreasePct, "p95/p99 relative warning threshold")
	command.Flags().Float64Var(&thresholds.MinimumCoverageRatio, "minimum-coverage", thresholds.MinimumCoverageRatio, "minimum Prometheus scrape coverage ratio")
	_ = command.MarkFlagRequired("instance")
	_ = command.MarkFlagRequired("output")
	return command
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
