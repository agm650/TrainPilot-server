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

func TestServerURLIsSanitizedForReports(t *testing.T) {
	got := sanitizedServerURL("https://user:password@example.test/api?token=secret#fragment")
	if got != "https://example.test/api" {
		t.Fatalf("url=%q", got)
	}
}

func TestGeneratedRunIDIsAUUID(t *testing.T) {
	id, err := newRunID()
	if err != nil {
		t.Fatal(err)
	}
	if !validRunID(id) {
		t.Fatalf("run ID=%q", id)
	}
}

func TestBenchmarkSchemasContainValidJSON(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "benchmarks", "schema", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 4 {
		t.Fatalf("schema count=%d", len(paths))
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var schema map[string]any
		if err := json.Unmarshal(data, &schema); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
}
