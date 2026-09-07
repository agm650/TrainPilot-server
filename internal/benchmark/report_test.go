package benchmark

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReportDoesNotContainCredentialsAndUsesPrivatePermissions(t *testing.T) {
	report := Report{
		SchemaVersion: ReportSchemaVersion,
		Profile:       Profile{SchemaVersion: ProfileSchemaVersion, Name: "secret-free"},
		Server:        ServerMetadata{URL: "http://127.0.0.1:8080"},
		OverallResult: "PASS",
	}
	path := filepath.Join(t.TempDir(), "report.json")
	if err := WriteReport(path, report); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "password") || strings.Contains(string(data), "token") {
		t.Fatalf("report contains a credential field: %s", data)
	}
	var decoded Report
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SchemaVersion != ReportSchemaVersion {
		t.Fatalf("schemaVersion=%d", decoded.SchemaVersion)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions=%04o", info.Mode().Perm())
	}
}
