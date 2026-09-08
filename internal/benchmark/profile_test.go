package benchmark

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const validProfileYAML = `
schema_version: 1
name: test
warmup: 1s
duration: 2s
seed: 650
clients:
  users: 2
  websockets: 1
  active_locomotives: 1
rates:
  throttle_per_second: 2
behavior:
  reconnect_probability: 0.01
  snapshot_probability: 0.02
`

func TestDecodeProfileAppliesDefaultsAndRejectsUnknownFields(t *testing.T) {
	profile, err := DecodeProfile(strings.NewReader(validProfileYAML))
	if err != nil {
		t.Fatal(err)
	}
	if profile.SchemaVersion != ProfileSchemaVersion || profile.OperationTimeout.Duration != 5*time.Second || profile.Clients.Workers != 16 {
		t.Fatalf("profile=%+v", profile)
	}
	_, err = DecodeProfile(strings.NewReader(validProfileYAML + "unknown: true\n"))
	if err == nil || !strings.Contains(err.Error(), "field unknown not found") {
		t.Fatalf("unknown field error=%v", err)
	}
}

func TestProfileValidation(t *testing.T) {
	profile, err := DecodeProfile(strings.NewReader(validProfileYAML))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		change func(*Profile)
	}{
		{"schema", func(p *Profile) { p.SchemaVersion = 2 }},
		{"duration", func(p *Profile) { p.Duration.Duration = 0 }},
		{"users", func(p *Profile) { p.Clients.Users = 0 }},
		{"rate", func(p *Profile) { p.Rates.ThrottlePerSecond = -1 }},
		{"probability", func(p *Profile) { p.Behavior.ReconnectProbability = 2 }},
		{"drop probability", func(p *Profile) { p.Behavior.DropEventProbability = 2 }},
		{"empty expected error", func(p *Profile) { p.ExpectedErrors = []ExpectedErrorRule{{Operation: "throttle"}} }},
		{"invalid expected status", func(p *Profile) {
			p.ExpectedErrors = []ExpectedErrorRule{{Operation: "throttle", HTTPStatuses: []int{200}}}
		}},
		{"invalid expected kind", func(p *Profile) {
			p.ExpectedErrors = []ExpectedErrorRule{{Operation: "throttle", Kinds: []string{"other"}}}
		}},
		{"partial expected window", func(p *Profile) {
			from := Duration{Duration: time.Second}
			p.ExpectedErrors = []ExpectedErrorRule{{Operation: "throttle", HTTPStatuses: []int{503}, From: &from}}
		}},
		{"expected window outside measurement", func(p *Profile) {
			from := Duration{Duration: time.Second}
			to := Duration{Duration: 3 * time.Second}
			p.ExpectedErrors = []ExpectedErrorRule{{Operation: "throttle", HTTPStatuses: []int{503}, From: &from, To: &to}}
		}},
		{"unbounded health error", func(p *Profile) {
			p.ExpectedErrors = []ExpectedErrorRule{{Operation: "health", HTTPStatuses: []int{503}}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := profile
			test.change(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestProfileAcceptsWindowedPlannedOutage(t *testing.T) {
	profile, err := DecodeProfile(strings.NewReader(validProfileYAML))
	if err != nil {
		t.Fatal(err)
	}
	from := Duration{Duration: 500 * time.Millisecond}
	to := Duration{Duration: 1500 * time.Millisecond}
	profile.ExpectedErrors = []ExpectedErrorRule{{Operation: "health", Kinds: []string{"network"}, From: &from, To: &to}}
	if err := profile.Validate(); err != nil {
		t.Fatal(err)
	}
	if !profile.HasPlannedOutage() || !profile.HasOperation("health") {
		t.Fatal("planned outage was not detected")
	}
}

func TestScheduleOffsetsAreDeterministic(t *testing.T) {
	first := scheduleOffsets(12.5, 2*time.Second, 650)
	second := scheduleOffsets(12.5, 2*time.Second, 650)
	if len(first) == 0 || len(first) != len(second) {
		t.Fatalf("offset counts %d and %d", len(first), len(second))
	}
	for index := range first {
		if first[index] != second[index] {
			t.Fatalf("offset %d differs: %v != %v", index, first[index], second[index])
		}
	}
	third := scheduleOffsets(12.5, 2*time.Second, 651)
	if len(third) == 0 || first[0] == third[0] {
		t.Fatalf("different seed produced same first offset: %v", first[0])
	}
}

func TestFeedbackRateRequiresFixtureTarget(t *testing.T) {
	profile, err := DecodeProfile(strings.NewReader(validProfileYAML))
	if err != nil {
		t.Fatal(err)
	}
	profile.Rates.FeedbackPerSecond = 1
	if err := ValidateFixtureForProfile(profile, Fixture{}); err == nil {
		t.Fatal("expected fixture validation error")
	}
	fixture := Fixture{SchemaVersion: FixtureSchemaVersion, FeedbackTargets: []FeedbackTarget{{Source: "simulator", Kind: "occupancy", Address: 1, BlockID: "block-a"}}}
	if err := ValidateFixtureForProfile(profile, fixture); err != nil {
		t.Fatal(err)
	}
}

func TestLoadFixtureRejectsDuplicateSelectorsAndInvalidDataset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.json")
	data := `{"schemaVersion":1,"dataset":{"preset":"small","locomotives":0,"blocks":20,"turnouts":10,"routes":10}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFixture(path); err == nil || !strings.Contains(err.Error(), "resource counts") {
		t.Fatalf("error=%v", err)
	}
	data = `{"schemaVersion":1,"locomotiveIds":["same","same"]}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFixture(path); err == nil || !strings.Contains(err.Error(), "duplicates") {
		t.Fatalf("error=%v", err)
	}
}

func TestProfileBurstsAreValidatedAndAffectSafetyFlags(t *testing.T) {
	profile, err := DecodeProfile(strings.NewReader(validProfileYAML))
	if err != nil {
		t.Fatal(err)
	}
	profile.Rates.ThrottlePerSecond = 0
	profile.Clients.ActiveLocomotives = 0
	profile.Bursts = []BurstProfile{{At: Duration{Duration: 1500 * time.Millisecond}, Operation: "feedback", Count: 250}}
	if err := profile.Validate(); err != nil {
		t.Fatal(err)
	}
	if !profile.HasSimulatorOperations() || profile.HasActiveOperations() {
		t.Fatalf("unexpected safety flags: simulator=%t active=%t", profile.HasSimulatorOperations(), profile.HasActiveOperations())
	}
	profile.Bursts[0].Operation = "unknown"
	if err := profile.Validate(); err == nil {
		t.Fatal("expected unsupported burst operation error")
	}
}
