package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
