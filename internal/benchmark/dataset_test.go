package benchmark

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/store"
	"github.com/agm650/TrainPilot-server/internal/transfer"
)

func TestGenerateDatasetPresetsAreStableAndComplete(t *testing.T) {
	want := map[string]FixtureDataset{
		"small":  {Locomotives: 50, Blocks: 20, Turnouts: 10, Routes: 10},
		"medium": {Locomotives: 250, Blocks: 100, Turnouts: 50, Routes: 75},
		"large":  {Locomotives: 1000, Blocks: 250, Turnouts: 150, Routes: 200},
		"xlarge": {Locomotives: 5000, Blocks: 1000, Turnouts: 500, Routes: 1000},
	}
	for name, counts := range want {
		t.Run(name, func(t *testing.T) {
			first, err := GenerateDataset(name)
			if err != nil {
				t.Fatal(err)
			}
			second, err := GenerateDataset(name)
			if err != nil {
				t.Fatal(err)
			}
			if len(first.Locomotives) != counts.Locomotives || len(first.Layout.Blocks) != counts.Blocks ||
				len(first.Layout.Turnouts) != counts.Turnouts || len(first.Layout.Routes) != counts.Routes {
				t.Fatalf("unexpected counts: locomotives=%d blocks=%d turnouts=%d routes=%d",
					len(first.Locomotives), len(first.Layout.Blocks), len(first.Layout.Turnouts), len(first.Layout.Routes))
			}
			if first.Locomotives[0].ID != second.Locomotives[0].ID || first.Layout.Routes[0].ID != second.Layout.Routes[0].ID {
				t.Fatal("generated IDs are not stable")
			}
			if first.Fixture.Dataset == nil || first.Fixture.Dataset.Preset != name {
				t.Fatalf("fixture dataset=%+v", first.Fixture.Dataset)
			}
		})
	}
}

func TestWriteDatasetProducesDeterministicImportArchives(t *testing.T) {
	first := filepath.Join(t.TempDir(), "first")
	second := filepath.Join(t.TempDir(), "second")
	if err := WriteDataset(first, "small"); err != nil {
		t.Fatal(err)
	}
	if err := WriteDataset(second, "small"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"rolling-stock.dcclib", "layout.dcclayout", "fixture.json"} {
		firstData, err := os.ReadFile(filepath.Join(first, name))
		if err != nil {
			t.Fatal(err)
		}
		secondData, err := os.ReadFile(filepath.Join(second, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(firstData, secondData) {
			t.Fatalf("%s is not deterministic", name)
		}
	}
	fixture, err := LoadFixture(filepath.Join(first, "fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	if fixture.Dataset == nil || fixture.Dataset.Locomotives != 50 {
		t.Fatalf("fixture=%+v", fixture)
	}
}

func TestGenerateDatasetRejectsUnknownPreset(t *testing.T) {
	if _, err := GenerateDataset("huge"); err == nil {
		t.Fatal("expected unknown preset error")
	}
}

func TestGeneratedArchivesCanBeImported(t *testing.T) {
	directory := t.TempDir()
	if err := WriteDataset(directory, "small"); err != nil {
		t.Fatal(err)
	}
	database, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	service := transfer.New(database, events.New(), clock.Real{})
	admin := model.User{ID: "benchmark-admin", Role: model.RoleAdministrator}
	rollingStock, err := os.ReadFile(filepath.Join(directory, "rolling-stock.dcclib"))
	if err != nil {
		t.Fatal(err)
	}
	layout, err := os.ReadFile(filepath.Join(directory, "layout.dcclayout"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := service.ImportRollingStock(ctx, admin, rollingStock, true); err != nil {
		t.Fatal(err)
	}
	if err := service.ImportLayout(ctx, admin, layout, true); err != nil {
		t.Fatal(err)
	}
	locomotives, err := database.ListLocomotives(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gotLayout, err := database.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(locomotives) != 50 || len(gotLayout.Blocks) != 20 || len(gotLayout.Turnouts) != 10 || len(gotLayout.Routes) != 10 {
		t.Fatalf("imported counts: locomotives=%d blocks=%d turnouts=%d routes=%d",
			len(locomotives), len(gotLayout.Blocks), len(gotLayout.Turnouts), len(gotLayout.Routes))
	}
}

func TestCommittedDatasetsMatchGenerator(t *testing.T) {
	for _, name := range DatasetPresetNames() {
		t.Run(name, func(t *testing.T) {
			dataset, err := GenerateDataset(name)
			if err != nil {
				t.Fatal(err)
			}
			rollingStock, err := transfer.BuildRollingStockArchive(datasetCreatedAt, dataset.Locomotives)
			if err != nil {
				t.Fatal(err)
			}
			layout, err := transfer.BuildLayoutArchive(datasetCreatedAt, dataset.Layout)
			if err != nil {
				t.Fatal(err)
			}
			fixture, err := json.MarshalIndent(dataset.Fixture, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			fixture = append(fixture, '\n')
			generated := map[string][]byte{
				"rolling-stock.dcclib": rollingStock,
				"layout.dcclayout":     layout,
				"fixture.json":         fixture,
			}
			for filename, want := range generated {
				path := filepath.Join("..", "..", "benchmarks", "fixtures", name, filename)
				got, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("%s does not match deterministic generator", path)
				}
			}
		})
	}
}
