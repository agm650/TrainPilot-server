package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/service"
)

func TestStatePersistsSessionAndLeaseWithPrivatePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dccctl", "state.json")
	state, err := loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	profile := state.profile("http://localhost:8080", "alice")
	now := time.Now().UTC().Truncate(time.Second)
	profile.setTokens(service.TokenPair{
		AccessToken: "access", RefreshToken: "refresh", SessionID: "session-1",
		AccessExpiresAt: now.Add(time.Minute), RefreshExpiresAt: now.Add(time.Hour),
	})
	profile.setLease(model.ControlLease{ID: "lease-1", LocomotiveID: "loco-1"})
	if err := saveState(path, state); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("permissions=%o want 600", got)
	}
	loaded, err := loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	got := loaded.profile("http://localhost:8080", "alice")
	if got.SessionID != "session-1" || got.Leases["loco-1"].ID != "lease-1" {
		t.Fatalf("loaded profile=%+v", got)
	}
}

func TestProfilesAreSeparatedByServerAndUsername(t *testing.T) {
	state := &cliState{Profiles: make(map[string]*savedProfile)}
	a := state.profile("http://server-a", "alice")
	b := state.profile("http://server-b", "alice")
	c := state.profile("http://server-a", "bob")
	a.Leases["loco"] = savedLease{ID: "lease-a"}
	if b.Leases["loco"].ID != "" || c.Leases["loco"].ID != "" {
		t.Fatal("lease leaked between profiles")
	}
}
