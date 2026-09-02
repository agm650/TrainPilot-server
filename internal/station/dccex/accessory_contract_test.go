package dccex

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/station/contracttest"
)

func TestBasicAccessoryContract(t *testing.T) {
	contracttest.BasicAccessoryContract(t, func(t *testing.T) contracttest.AccessoryHarness {
		t.Helper()
		server := newFakeTCPServer(t)
		driver := newConnectedDriver(t, server, 500*time.Millisecond)
		contracttest.RequireAccessoryCapability(t, driver)
		return contracttest.AccessoryHarness{
			Station: driver,
			Commands: func() []station.AccessoryCommand {
				return parseContractAccessoryCommands(server.Commands())
			},
			GoOffline: func() {
				server.Stop()
				eventually(t, 200*time.Millisecond, "DCC-EX contract socket removal", func() bool {
					driver.mu.Lock()
					defer driver.mu.Unlock()
					return driver.conn == nil
				})
			},
			Reconnect: func() {
				server.Start()
				waitForConnectivity(t, driver, station.Online, 500*time.Millisecond)
				eventually(t, time.Second, "DCC-EX contract reconnection", func() bool {
					return server.ActiveConnections() == 1
				})
			},
			Settle: func() { time.Sleep(3 * testReconnectInterval) },
		}
	})
}

func parseContractAccessoryCommands(frames []string) []station.AccessoryCommand {
	commands := make([]station.AccessoryCommand, 0, len(frames))
	for _, frame := range frames {
		if !strings.HasPrefix(frame, "<a ") {
			continue
		}
		var address, rawPosition int
		if _, err := fmt.Sscanf(frame, "<a %d %d>", &address, &rawPosition); err != nil {
			continue
		}
		position := station.AccessoryPosition1
		if rawPosition == 1 {
			position = station.AccessoryPosition2
		}
		commands = append(commands, station.AccessoryCommand{Address: address, Position: position})
	}
	return commands
}

var _ station.CommandStation = (*Driver)(nil)
