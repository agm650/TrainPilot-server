package scripts_test

import (
	"os/exec"
	"strings"
	"testing"
)

func TestTurnoutHardwareScriptDryRun(t *testing.T) {
	command := exec.Command(
		"bash", "test-turnouts.sh",
		"--server", "http://127.0.0.1:8080",
		"--username", "dispatcher",
		"--turnout", "T1",
		"--positions", "straight,diverging",
		"--cycles", "1",
		"--external-check",
		"--offline-check",
		"--dry-run",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, output)
	}
	for _, expected := range []string{
		"turnout T1 straight",
		"turnout T1 diverging",
		"expected refusal",
		"without issuing an accessory command",
		"PASS campaign completed",
	} {
		if !strings.Contains(string(output), expected) {
			t.Errorf("dry-run output does not contain %q\n%s", expected, output)
		}
	}
}

func TestTurnoutHardwareScriptRequiresExplicitRiskAcknowledgement(t *testing.T) {
	command := exec.Command(
		"bash", "test-turnouts.sh",
		"--server", "http://127.0.0.1:8080",
		"--username", "dispatcher",
		"--turnout", "T1",
		"--positions", "straight,diverging",
		"--cycles", "1",
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("script accepted hardware commands without acknowledgement\n%s", output)
	}
	if !strings.Contains(string(output), "Refusing real commands") {
		t.Fatalf("unexpected refusal message\n%s", output)
	}
}
