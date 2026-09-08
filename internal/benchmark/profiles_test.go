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
	if len(paths) != 18 {
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
