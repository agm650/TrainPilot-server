package benchmark

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

const FixtureSchemaVersion = 1

type Fixture struct {
	SchemaVersion          int                     `json:"schemaVersion"`
	Dataset                *FixtureDataset         `json:"dataset,omitempty"`
	LocomotiveIDs          []string                `json:"locomotiveIds,omitempty"`
	FeedbackTargets        []FeedbackTarget        `json:"feedbackTargets,omitempty"`
	IncompatibleRoutePairs []IncompatibleRoutePair `json:"incompatibleRoutePairs,omitempty"`
}

type FixtureDataset struct {
	Preset      string `json:"preset"`
	Locomotives int    `json:"locomotives"`
	Blocks      int    `json:"blocks"`
	Turnouts    int    `json:"turnouts"`
	Routes      int    `json:"routes"`
}

type FeedbackTarget struct {
	Source  string `json:"source"`
	Kind    string `json:"kind"`
	Address int    `json:"address"`
	BlockID string `json:"blockId"`
}

type IncompatibleRoutePair struct {
	First  string `json:"first"`
	Second string `json:"second"`
}

func LoadFixture(path string) (Fixture, error) {
	f, err := os.Open(path)
	if err != nil {
		return Fixture{}, fmt.Errorf("open fixture: %w", err)
	}
	defer f.Close()
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	var fixture Fixture
	if err := decoder.Decode(&fixture); err != nil {
		return Fixture{}, fmt.Errorf("decode fixture: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Fixture{}, err
	}
	if fixture.SchemaVersion != FixtureSchemaVersion {
		return Fixture{}, fmt.Errorf("unsupported fixture schemaVersion %d (want %d)", fixture.SchemaVersion, FixtureSchemaVersion)
	}
	if fixture.Dataset != nil {
		if fixture.Dataset.Preset == "" {
			return Fixture{}, errors.New("dataset.preset is required")
		}
		if fixture.Dataset.Locomotives <= 0 || fixture.Dataset.Blocks <= 0 || fixture.Dataset.Turnouts <= 0 || fixture.Dataset.Routes <= 0 {
			return Fixture{}, errors.New("dataset resource counts must be greater than zero")
		}
	}
	locomotiveIDs := make(map[string]struct{}, len(fixture.LocomotiveIDs))
	for index, id := range fixture.LocomotiveIDs {
		if id == "" {
			return Fixture{}, fmt.Errorf("locomotiveIds[%d] is empty", index)
		}
		if _, exists := locomotiveIDs[id]; exists {
			return Fixture{}, fmt.Errorf("locomotiveIds[%d] duplicates %q", index, id)
		}
		locomotiveIDs[id] = struct{}{}
	}
	feedbackTargets := make(map[string]struct{}, len(fixture.FeedbackTargets))
	for index, target := range fixture.FeedbackTargets {
		if target.Source == "" || target.Kind == "" || target.Address < 0 || target.BlockID == "" {
			return Fixture{}, fmt.Errorf("feedbackTargets[%d] is incomplete", index)
		}
		key := fmt.Sprintf("%s:%s:%d", target.Source, target.Kind, target.Address)
		if _, exists := feedbackTargets[key]; exists {
			return Fixture{}, fmt.Errorf("feedbackTargets[%d] duplicates %q", index, key)
		}
		feedbackTargets[key] = struct{}{}
	}
	for index, pair := range fixture.IncompatibleRoutePairs {
		if pair.First == "" || pair.Second == "" || pair.First == pair.Second {
			return Fixture{}, fmt.Errorf("incompatibleRoutePairs[%d] is invalid", index)
		}
	}
	return fixture, nil
}

func ValidateFixtureForProfile(profile Profile, fixture Fixture) error {
	if profile.HasSimulatorOperations() && len(fixture.FeedbackTargets) == 0 {
		return errors.New("a positive feedback rate requires fixture.feedbackTargets")
	}
	if len(fixture.LocomotiveIDs) > 0 && profile.Clients.ActiveLocomotives > len(fixture.LocomotiveIDs) {
		return fmt.Errorf("profile requests %d active locomotives, but fixture selects %d", profile.Clients.ActiveLocomotives, len(fixture.LocomotiveIDs))
	}
	if profile.HasOperation("route_contention") && len(fixture.IncompatibleRoutePairs) == 0 {
		return errors.New("route contention requires fixture.incompatibleRoutePairs")
	}
	return nil
}
