package simulator

import (
	"context"
	"sync"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/station/contracttest"
)

type contractRecordingSimulator struct {
	*Simulator
	mu       sync.Mutex
	commands []station.AccessoryCommand
}

func (s *contractRecordingSimulator) SetBasicAccessory(ctx context.Context, command station.AccessoryCommand) error {
	if err := s.Simulator.SetBasicAccessory(ctx, command); err != nil {
		return err
	}
	s.mu.Lock()
	s.commands = append(s.commands, command)
	s.mu.Unlock()
	return nil
}

func (s *contractRecordingSimulator) Commands() []station.AccessoryCommand {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]station.AccessoryCommand(nil), s.commands...)
}

func TestBasicAccessoryContract(t *testing.T) {
	contracttest.BasicAccessoryContract(t, func(t *testing.T) contracttest.AccessoryHarness {
		t.Helper()
		simulator := New()
		if err := simulator.Connect(context.Background()); err != nil {
			t.Fatal(err)
		}
		recorder := &contractRecordingSimulator{Simulator: simulator}
		contracttest.RequireAccessoryCapability(t, recorder)
		return contracttest.AccessoryHarness{
			Station:  recorder,
			Commands: recorder.Commands,
			GoOffline: func() {
				if err := simulator.SetConnectivity(station.Offline); err != nil {
					t.Fatal(err)
				}
			},
			Reconnect: func() {
				if err := simulator.SetConnectivity(station.Online); err != nil {
					t.Fatal(err)
				}
			},
		}
	})
}
