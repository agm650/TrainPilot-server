package benchmark

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/station/simulator/scenario"
)

func TestBenchmarkSimulatorScenariosAreValid(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "benchmarks", "scenarios", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 6 {
		t.Fatalf("scenario count=%d", len(paths))
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if _, err := scenario.Load(file); err != nil {
				t.Fatal(err)
			}
		})
	}
}
