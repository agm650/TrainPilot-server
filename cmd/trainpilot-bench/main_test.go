package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bench "github.com/agm650/TrainPilot-server/internal/benchmark"
)

func TestValidateProfileCommand(t *testing.T) {
	profilePath := filepath.Join(t.TempDir(), "profile.yaml")
	profile := []byte("schema_version: 1\nname: cli-test\nwarmup: 0s\nduration: 1s\nclients:\n  users: 1\n  websockets: 0\n  active_locomotives: 0\nrates: {}\nbehavior: {}\n")
	if err := os.WriteFile(profilePath, profile, 0o600); err != nil {
		t.Fatal(err)
	}
	command := newRootCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetArgs([]string{"validate-profile", profilePath})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `profile "cli-test" is valid`) {
		t.Fatalf("output=%q", output.String())
	}
}

func TestCompareCommandAcceptsMoreThanThreeReports(t *testing.T) {
	directory := t.TempDir()
	hardware := &bench.HardwareMetadata{Model: "test-host"}
	arguments := []string{"compare"}
	for groupIndex, group := range []string{"baseline", "candidate"} {
		for index := 0; index < 4; index++ {
			path := filepath.Join(directory, group+string(rune('a'+index))+".json")
			latency := 10.0
			if groupIndex == 1 {
				latency = 12
			}
			report := bench.Report{
				SchemaVersion: bench.ReportSchemaVersion, RunID: fmt.Sprintf("00000000-0000-4000-8000-%012d", groupIndex*100+index),
				Profile: bench.Profile{Name: "medium"}, ProfileSHA256: strings.Repeat("a", 64), Hardware: hardware,
				Operations: map[string]bench.OperationSummary{"throttle": {Latency: bench.LatencySummary{
					P50Milliseconds: latency / 2, P90Milliseconds: latency, P95Milliseconds: latency,
					P99Milliseconds: latency, MaxMilliseconds: latency,
				}}},
				OverallResult: "PASS",
			}
			if err := bench.WriteReport(path, report); err != nil {
				t.Fatal(err)
			}
			arguments = append(arguments, "--"+group, path)
		}
	}
	command := newRootCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetArgs(arguments)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Comparison: WARN (4 baseline, 4 candidate runs)") {
		t.Fatalf("output=%q", output.String())
	}
}

func TestEnrichAndPublicationValidationCommands(t *testing.T) {
	directory := t.TempDir()
	input := filepath.Join(directory, "input.json")
	output := filepath.Join(directory, "enriched.json")
	report := bench.Report{
		SchemaVersion: bench.ReportSchemaVersion, RunID: "00000000-0000-4000-8000-000000000001",
		BenchmarkVersion: "test", StartedAt: time.Now().UTC(), EndedAt: time.Now().UTC(), Duration: "1m", Warmup: "1s",
		Profile: bench.Profile{SchemaVersion: bench.ProfileSchemaVersion, Name: "medium"}, ProfileSHA256: strings.Repeat("a", 64),
		Server: bench.ServerMetadata{URL: "http://example.test", ServerVersion: "test"}, Operations: map[string]bench.OperationSummary{},
		ClientHost:    bench.ClientHostMetadata{Hostname: "generator", OS: "linux", Arch: "amd64", CPUs: 4, GoVersion: "go1.26"},
		OverallResult: "PASS",
	}
	if err := bench.WriteReport(input, report); err != nil {
		t.Fatal(err)
	}
	command := newRootCommand()
	command.SetArgs([]string{
		"enrich-report", input,
		"--metadata", filepath.Join("..", "..", "benchmarks", "examples", "run-metadata.json"),
		"--metrics", filepath.Join("..", "..", "benchmarks", "examples", "system-metrics.json"),
		"--output", output,
	})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	command = newRootCommand()
	command.SetArgs([]string{"validate-report", "--publication", output})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestRunCommandRequiresCredentials(t *testing.T) {
	profilePath := filepath.Join(t.TempDir(), "profile.yaml")
	profile := []byte("schema_version: 1\nname: cli-test\nwarmup: 0s\nduration: 1s\nclients:\n  users: 1\n  websockets: 0\n  active_locomotives: 0\nrates: {}\nbehavior: {}\n")
	if err := os.WriteFile(profilePath, profile, 0o600); err != nil {
		t.Fatal(err)
	}
	command := newRootCommand()
	command.SetArgs([]string{"run", "--profile", profilePath, "--output", filepath.Join(t.TempDir(), "report.json")})
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "--credentials or at least one --credential is required") {
		t.Fatalf("error=%v", err)
	}
}

func TestGenerateFixtureCommand(t *testing.T) {
	outputDirectory := filepath.Join(t.TempDir(), "small")
	command := newRootCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetArgs([]string{"generate-fixture", "small", "--output", outputDirectory})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `fixture "small" written`) {
		t.Fatalf("output=%q", output.String())
	}
	for _, name := range []string{"rolling-stock.dcclib", "layout.dcclayout", "fixture.json"} {
		if _, err := os.Stat(filepath.Join(outputDirectory, name)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}
