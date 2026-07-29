package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type scenario struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Given       []string `json:"given"`
	When        []string `json:"when"`
	Then        []string `json:"then"`
}

func TestScenarioFilesAreComplete(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "contract-tests", "scenarios", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 5 {
		t.Fatalf("expected at least 5 scenarios, got %d", len(files))
	}
	seen := map[string]bool{}
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var s scenario
		if err := json.Unmarshal(b, &s); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if s.ID == "" || len(s.Given) == 0 || len(s.When) == 0 || len(s.Then) == 0 {
			t.Fatalf("incomplete scenario %s", file)
		}
		if seen[s.ID] {
			t.Fatalf("duplicate scenario id %s", s.ID)
		}
		seen[s.ID] = true
	}
}
