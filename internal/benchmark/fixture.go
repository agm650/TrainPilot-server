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
	LocomotiveIDs          []string                `json:"locomotiveIds,omitempty"`
	FeedbackTargets        []FeedbackTarget        `json:"feedbackTargets,omitempty"`
	IncompatibleRoutePairs []IncompatibleRoutePair `json:"incompatibleRoutePairs,omitempty"`
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
	for index, target := range fixture.FeedbackTargets {
		if target.Source == "" || target.Kind == "" || target.Address < 0 || target.BlockID == "" {
			return Fixture{}, fmt.Errorf("feedbackTargets[%d] is incomplete", index)
		}
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
	return nil
}
