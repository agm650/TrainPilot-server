// Package contracttest contains reusable behavioral contracts for command
// station implementations. It is intended for tests only.
package contracttest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/station"
)

type AccessoryHarness struct {
	Station   station.CommandStation
	Commands  func() []station.AccessoryCommand
	GoOffline func()
	Reconnect func()
	Settle    func()
}

type AccessoryFactory func(*testing.T) AccessoryHarness

func BasicAccessoryContract(t *testing.T, factory AccessoryFactory) {
	t.Helper()
	for _, position := range []station.AccessoryPosition{station.AccessoryPosition1, station.AccessoryPosition2} {
		position := position
		t.Run(string(position), func(t *testing.T) {
			harness := factory(t)
			command := station.AccessoryCommand{Address: 10, Position: position}
			if err := harness.Station.SetBasicAccessory(context.Background(), command); err != nil {
				t.Fatal(err)
			}
			commands := waitForCommands(t, harness.Commands, 1)
			if len(commands) != 1 || commands[0] != command {
				t.Fatalf("commands=%+v want %+v", commands, command)
			}
		})
	}

	t.Run("validation", func(t *testing.T) {
		harness := factory(t)
		for _, test := range []struct {
			command station.AccessoryCommand
			want    error
		}{
			{station.AccessoryCommand{Address: 0, Position: station.AccessoryPosition1}, station.ErrInvalidAccessoryAddress},
			{station.AccessoryCommand{Address: station.MaxBasicAccessoryAddress + 1, Position: station.AccessoryPosition1}, station.ErrInvalidAccessoryAddress},
			{station.AccessoryCommand{Address: 10, Position: station.AccessoryPosition("invalid")}, station.ErrInvalidAccessoryPosition},
		} {
			if err := harness.Station.SetBasicAccessory(context.Background(), test.command); !errors.Is(err, test.want) {
				t.Errorf("command=%+v error=%v want %v", test.command, err, test.want)
			}
		}
		if commands := harness.Commands(); len(commands) != 0 {
			t.Fatalf("invalid commands reached station: %+v", commands)
		}
	})

	t.Run("offline_no_replay", func(t *testing.T) {
		harness := factory(t)
		initial := station.AccessoryCommand{Address: 10, Position: station.AccessoryPosition1}
		if err := harness.Station.SetBasicAccessory(context.Background(), initial); err != nil {
			t.Fatal(err)
		}
		waitForCommands(t, harness.Commands, 1)
		harness.GoOffline()
		refused := station.AccessoryCommand{Address: 11, Position: station.AccessoryPosition2}
		if err := harness.Station.SetBasicAccessory(context.Background(), refused); !errors.Is(err, station.ErrOffline) {
			t.Fatalf("offline error=%v want station.ErrOffline", err)
		}
		harness.Reconnect()
		if harness.Settle != nil {
			harness.Settle()
		}
		accepted := station.AccessoryCommand{Address: 12, Position: station.AccessoryPosition2}
		if err := harness.Station.SetBasicAccessory(context.Background(), accepted); err != nil {
			t.Fatal(err)
		}
		commands := waitForCommands(t, harness.Commands, 2)
		counts := commandCounts(commands)
		if counts[refused] != 0 || counts[initial] != 1 || counts[accepted] != 1 || len(commands) != 2 {
			t.Fatalf("offline command was replayed or traffic duplicated: %+v", commands)
		}
	})

	t.Run("concurrent_commands", func(t *testing.T) {
		harness := factory(t)
		const count = 100
		var wg sync.WaitGroup
		errs := make(chan error, count)
		want := make(map[station.AccessoryCommand]int)
		for index := range count {
			position := station.AccessoryPosition1
			if index%2 == 1 {
				position = station.AccessoryPosition2
			}
			command := station.AccessoryCommand{Address: index%20 + 1, Position: position}
			want[command]++
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs <- harness.Station.SetBasicAccessory(context.Background(), command)
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		commands := waitForCommands(t, harness.Commands, count)
		got := commandCounts(commands)
		if len(commands) != count || len(got) != len(want) {
			t.Fatalf("commands=%d distinct=%d want %d/%d", len(commands), len(got), count, len(want))
		}
		for command, expected := range want {
			if got[command] != expected {
				t.Errorf("command %+v count=%d want %d", command, got[command], expected)
			}
		}
	})
}

func waitForCommands(t *testing.T, commands func() []station.AccessoryCommand, count int) []station.AccessoryCommand {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		current := commands()
		if len(current) >= count {
			return current
		}
		if time.Now().After(deadline) {
			t.Fatalf("received %d accessory commands, want %d", len(current), count)
		}
		time.Sleep(time.Millisecond)
	}
}

func commandCounts(commands []station.AccessoryCommand) map[station.AccessoryCommand]int {
	counts := make(map[station.AccessoryCommand]int)
	for _, command := range commands {
		counts[command]++
	}
	return counts
}

func RequireAccessoryCapability(t *testing.T, stationDriver station.CommandStation) {
	t.Helper()
	if !stationDriver.Capabilities().AccessoryControl {
		t.Fatal(fmt.Errorf("driver %q does not announce accessoryControl", stationDriver.Capabilities().Driver))
	}
}
