package benchmark

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMonitoringDashboardsContainValidJSON(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "deploy", "monitoring", "grafana", "dashboards", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 4 {
		t.Fatalf("dashboard count=%d", len(paths))
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var dashboard struct {
			Title         string `json:"title"`
			SchemaVersion int    `json:"schemaVersion"`
			Panels        []any  `json:"panels"`
		}
		if err := json.Unmarshal(data, &dashboard); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if dashboard.Title == "" || dashboard.SchemaVersion != 39 || len(dashboard.Panels) == 0 {
			t.Fatalf("%s: incomplete dashboard", path)
		}
	}
}

func TestSoakPrometheusRulesAreStructurallyValid(t *testing.T) {
	paths := []string{
		filepath.Join("..", "..", "deploy", "monitoring", "prometheus", "trainpilot-soak-recording-rules.yml"),
		filepath.Join("..", "..", "deploy", "monitoring", "prometheus", "trainpilot-soak-alerting-rules.yml"),
	}
	type rule struct {
		Record string `yaml:"record"`
		Alert  string `yaml:"alert"`
		Expr   string `yaml:"expr"`
	}
	type ruleFile struct {
		Groups []struct {
			Name  string `yaml:"name"`
			Rules []rule `yaml:"rules"`
		} `yaml:"groups"`
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var document ruleFile
		if err := yaml.Unmarshal(data, &document); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if len(document.Groups) == 0 {
			t.Fatalf("%s: no groups", path)
		}
		for _, group := range document.Groups {
			if group.Name == "" || len(group.Rules) == 0 {
				t.Fatalf("%s: incomplete group", path)
			}
			for _, current := range group.Rules {
				if (current.Record == "") == (current.Alert == "") || current.Expr == "" {
					t.Fatalf("%s: invalid rule in group %s", path, group.Name)
				}
			}
		}
	}
}

func TestPrometheusExampleLoadsSoakRules(t *testing.T) {
	path := filepath.Join("..", "..", "deploy", "monitoring", "prometheus", "prometheus.example.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var configuration struct {
		RuleFiles []string `yaml:"rule_files"`
	}
	if err := yaml.Unmarshal(data, &configuration); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"trainpilot-soak-recording-rules.yml": false,
		"trainpilot-soak-alerting-rules.yml":  false,
	}
	for _, name := range configuration.RuleFiles {
		if _, exists := want[name]; exists {
			want[name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Fatalf("missing rule file %q", name)
		}
	}
}
