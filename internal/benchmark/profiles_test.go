package benchmark

import (
	"path/filepath"
	"testing"
)

func TestVersionedProfilesAndFixturesAreValid(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "benchmarks", "profiles", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 20 {
		t.Fatalf("profile count=%d", len(paths))
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			profile, err := LoadProfile(path)
			if err != nil {
				t.Fatal(err)
			}
			if profile.Fixture == "" {
				return
			}
			fixture, err := LoadFixture(filepath.Join(filepath.Dir(path), profile.Fixture))
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateFixtureForProfile(profile, fixture); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCapacityProfilesDeclareNormalSafetyRefusals(t *testing.T) {
	profiles := map[string][]string{
		"small.yaml":           {"lease_acquire", "route"},
		"medium.yaml":          {"lease_acquire", "route"},
		"large.yaml":           {"lease_acquire", "route"},
		"xlarge.yaml":          {"lease_acquire", "route"},
		"ramp-burst.yaml":      {"route"},
		"soak-6h-medium.yaml":  {"lease_acquire", "route"},
		"soak-24h-medium.yaml": {"lease_acquire", "route"},
	}
	for name, operations := range profiles {
		t.Run(name, func(t *testing.T) {
			profile, err := LoadProfile(filepath.Join("..", "..", "benchmarks", "profiles", name))
			if err != nil {
				t.Fatal(err)
			}
			for _, operation := range operations {
				if !declaresUnboundedHTTPStatus(profile, operation, 409) {
					t.Errorf("missing unbounded expected HTTP 409 for %s", operation)
				}
			}
		})
	}
}

func declaresUnboundedHTTPStatus(profile Profile, operation string, status int) bool {
	for _, rule := range profile.ExpectedErrors {
		if rule.Operation != operation || rule.From != nil || rule.To != nil {
			continue
		}
		for _, candidate := range rule.HTTPStatuses {
			if candidate == status {
				return true
			}
		}
	}
	return false
}
