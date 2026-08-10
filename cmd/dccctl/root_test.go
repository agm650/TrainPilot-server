package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootCommandContainsExistingCLICommands(t *testing.T) {
	cmd, err := newRootCommand()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"locomotives", "locomotive-show", "locomotive-add", "locomotive-update", "locomotive-delete",
		"acquire", "throttle", "function", "release", "power", "emergency-stop",
		"export-rolling-stock", "import-rolling-stock", "export-layout", "import-layout",
	}
	for _, name := range want {
		found, _, err := cmd.Find([]string{name})
		if err != nil || found.Name() != name {
			t.Errorf("command %q not found: found=%v err=%v", name, found, err)
		}
	}
	for _, name := range []string{"on", "off", "status"} {
		found, _, err := cmd.Find([]string{"power", name})
		if err != nil || found.Name() != name {
			t.Errorf("power command %q not found: found=%v err=%v", name, found, err)
		}
	}
}

func TestHelpDoesNotRequireAuthentication(t *testing.T) {
	cmd, err := newRootCommand()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"throttle", "--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "speed-0..100") {
		t.Fatalf("unexpected help output: %s", out.String())
	}
}

func TestCobraArgumentValidationRunsBeforeAuthentication(t *testing.T) {
	cmd, err := newRootCommand()
	if err != nil {
		t.Fatal(err)
	}
	cmd.SetArgs([]string{"function", "loco-only"})
	err = cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "accepts 3 arg") {
		t.Fatalf("error=%v", err)
	}
}

func TestParseLocomotiveInput(t *testing.T) {
	input, err := parseLocomotiveInput([]string{"BB 26001", "2601", "long", "128", "Alstom", "Prima"})
	if err != nil {
		t.Fatal(err)
	}
	if input.Name != "BB 26001" || input.DCCAddress != 2601 || input.AddressKind != "long" || input.SpeedSteps != 128 || input.Manufacturer != "Alstom" || input.Model != "Prima" {
		t.Fatalf("input=%+v", input)
	}
}
