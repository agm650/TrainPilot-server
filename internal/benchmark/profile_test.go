package benchmark

import (
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
