package benchmark

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/transfer"
)

var datasetCreatedAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

type DatasetPreset struct {
	Name              string
	Locomotives       int
	Blocks            int
	Turnouts          int
	Routes            int
	ActiveLocomotives int
}

type GeneratedDataset struct {
	Preset      DatasetPreset
	Fixture     Fixture
	Locomotives []model.Locomotive
	Layout      model.LayoutDefinition
}

var datasetPresets = map[string]DatasetPreset{
	"small":  {Name: "small", Locomotives: 50, Blocks: 20, Turnouts: 10, Routes: 10, ActiveLocomotives: 3},
	"medium": {Name: "medium", Locomotives: 250, Blocks: 100, Turnouts: 50, Routes: 75, ActiveLocomotives: 10},
	"large":  {Name: "large", Locomotives: 1000, Blocks: 250, Turnouts: 150, Routes: 200, ActiveLocomotives: 25},
	"xlarge": {Name: "xlarge", Locomotives: 5000, Blocks: 1000, Turnouts: 500, Routes: 1000, ActiveLocomotives: 50},
}

func DatasetPresetNames() []string {
	names := make([]string, 0, len(datasetPresets))
	for name := range datasetPresets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func GenerateDataset(name string) (GeneratedDataset, error) {
	preset, exists := datasetPresets[name]
	if !exists {
		return GeneratedDataset{}, fmt.Errorf("unknown fixture preset %q (choose %v)", name, DatasetPresetNames())
	}
	dataset := GeneratedDataset{Preset: preset}
	for index := 1; index <= preset.Locomotives; index++ {
		addressKind := "long"
		if index <= 127 {
			addressKind = "short"
		}
		dataset.Locomotives = append(dataset.Locomotives, model.Locomotive{
			ID: fmt.Sprintf("benchmark-loco-%04d", index), Name: fmt.Sprintf("Benchmark locomotive %04d", index),
			DCCAddress: index, AddressKind: addressKind, SpeedSteps: 128,
		})
	}
	for index := 1; index <= preset.Blocks; index++ {
		blockID := fmt.Sprintf("benchmark-block-%04d", index)
		dataset.Layout.Blocks = append(dataset.Layout.Blocks, model.BlockDefinition{ID: blockID, Name: fmt.Sprintf("Benchmark block %04d", index)})
		dataset.Layout.FeedbackMappings = append(dataset.Layout.FeedbackMappings, model.FeedbackMapping{
			Provider: "simulator", Address: index, BlockID: blockID,
		})
	}
	for index := 1; index <= preset.Turnouts; index++ {
		dataset.Layout.Turnouts = append(dataset.Layout.Turnouts, model.NewSimpleTurnout(
			fmt.Sprintf("benchmark-turnout-%04d", index), fmt.Sprintf("Benchmark turnout %04d", index), index, "", "",
		))
	}
	for index := 1; index <= preset.Routes; index++ {
		pairIndex := (index - 1) / 2
		position := "straight"
		if index%2 == 0 {
			position = "diverging"
		}
		route := model.RouteDefinition{
			ID: fmt.Sprintf("benchmark-route-%04d", index), Name: fmt.Sprintf("Benchmark route %04d", index),
			BlockIDs:      []string{fmt.Sprintf("benchmark-block-%04d", pairIndex%preset.Blocks+1)},
			TurnoutStates: map[string]string{fmt.Sprintf("benchmark-turnout-%04d", pairIndex%preset.Turnouts+1): position},
		}
		mate := index + 1
		if index%2 == 0 {
			mate = index - 1
		}
		if mate >= 1 && mate <= preset.Routes {
			route.ConflictRouteIDs = []string{fmt.Sprintf("benchmark-route-%04d", mate)}
		}
		dataset.Layout.Routes = append(dataset.Layout.Routes, route)
	}
	dataset.Fixture = fixtureForDataset(preset)
	return dataset, nil
}

func fixtureForDataset(preset DatasetPreset) Fixture {
	fixture := Fixture{
		SchemaVersion: FixtureSchemaVersion,
		Dataset: &FixtureDataset{
			Preset: preset.Name, Locomotives: preset.Locomotives, Blocks: preset.Blocks,
			Turnouts: preset.Turnouts, Routes: preset.Routes,
		},
	}
	for index := 1; index <= preset.ActiveLocomotives; index++ {
		fixture.LocomotiveIDs = append(fixture.LocomotiveIDs, fmt.Sprintf("benchmark-loco-%04d", index))
	}
	feedbackTargetCount := min(preset.Blocks, 32)
	for index := 1; index <= feedbackTargetCount; index++ {
		fixture.FeedbackTargets = append(fixture.FeedbackTargets, FeedbackTarget{
			Source: "simulator", Kind: "occupancy", Address: index,
			BlockID: fmt.Sprintf("benchmark-block-%04d", index),
		})
	}
	pairCount := min(preset.Routes/2, 16)
	for index := 0; index < pairCount; index++ {
		fixture.IncompatibleRoutePairs = append(fixture.IncompatibleRoutePairs, IncompatibleRoutePair{
			First: fmt.Sprintf("benchmark-route-%04d", index*2+1), Second: fmt.Sprintf("benchmark-route-%04d", index*2+2),
		})
	}
	return fixture
}

func WriteDataset(directory, name string) error {
	dataset, err := GenerateDataset(name)
	if err != nil {
		return err
	}
	rollingStock, err := transfer.BuildRollingStockArchive(datasetCreatedAt, dataset.Locomotives)
	if err != nil {
		return fmt.Errorf("build rolling-stock archive: %w", err)
	}
	layout, err := transfer.BuildLayoutArchive(datasetCreatedAt, dataset.Layout)
	if err != nil {
		return fmt.Errorf("build layout archive: %w", err)
	}
	fixture, err := json.MarshalIndent(dataset.Fixture, "", "  ")
	if err != nil {
		return fmt.Errorf("encode fixture: %w", err)
	}
	fixture = append(fixture, '\n')
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create fixture directory: %w", err)
	}
	files := []struct {
		name string
		data []byte
	}{
		{"rolling-stock.dcclib", rollingStock},
		{"layout.dcclayout", layout},
		{"fixture.json", fixture},
	}
	for _, file := range files {
		if err := writeDatasetFile(filepath.Join(directory, file.name), file.data); err != nil {
			return err
		}
	}
	return nil
}

func writeDatasetFile(path string, data []byte) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".trainpilot-fixture-*")
	if err != nil {
		return fmt.Errorf("create fixture file: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write fixture file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("publish fixture file: %w", err)
	}
	return nil
}
