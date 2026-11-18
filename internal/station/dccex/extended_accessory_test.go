package dccex

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/station/contracttest"
)

func enableExtendedFirmware(t *testing.T, d *Driver, server *fakeTCPServer) {
	t.Helper()
	server.SendFrame("<iDCC-EX V-5.6.6 / MEGA / STANDARD G-test>")
	eventually(t, time.Second, "verified extended firmware", func() bool { return d.Capabilities().ExtendedAccessoryControl })
}

func TestExtendedAccessoryEncodingAndFiveBitLimits(t *testing.T) {
	for _, tc := range []struct {
		command station.ExtendedAccessoryCommand
		want    string
	}{
		{station.ExtendedAccessoryCommand{Address: 1, Aspect: 0}, "<A 1 0>\n"},
		{station.ExtendedAccessoryCommand{Address: 2040, Aspect: 31}, "<A 2040 31>\n"},
	} {
		got, err := extendedAccessoryCommand(tc.command)
		if err != nil || got != tc.want {
			t.Fatal(got, err)
		}
	}
	for _, aspect := range []int{-1, 32, 255, 256} {
		if _, err := extendedAccessoryCommand(station.ExtendedAccessoryCommand{Address: 1, Aspect: aspect}); !errors.Is(err, station.ErrInvalidExtendedAccessoryAspect) {
			t.Fatal(aspect, err)
		}
	}
	for _, address := range []int{0, 2041, 2044} {
		if _, err := extendedAccessoryCommand(station.ExtendedAccessoryCommand{Address: address}); !errors.Is(err, station.ErrInvalidExtendedAccessoryAddress) {
			t.Fatal(address, err)
		}
	}
}

func TestExtendedAccessoryCapabilityRequiresVerifiedFirmware(t *testing.T) {
	server := newFakeTCPServer(t)
	d := newConnectedDriver(t, server, station.DefaultOfflineAfter)
	command := station.ExtendedAccessoryCommand{Address: 1, Aspect: 0}
	if d.Capabilities().ExtendedAccessoryControl {
		t.Fatal("unknown firmware enabled support")
	}
	if err := d.SetExtendedAccessory(context.Background(), command); !errors.Is(err, station.ErrUnsupported) {
		t.Fatal(err)
	}
	for _, version := range []string{"5.6.5", "5.7.0", "5.6.6-dev", "invalid"} {
		server.SendFrame(fmt.Sprintf("<iDCC-EX V-%s / MEGA / STANDARD>", version))
		eventually(t, time.Second, "identity processed", func() bool { d.mu.Lock(); defer d.mu.Unlock(); return d.firmware == version })
		if d.Capabilities().ExtendedAccessoryControl {
			t.Fatal("unverified firmware advertised support", version)
		}
	}
	enableExtendedFirmware(t, d, server)
	if limits := d.Capabilities().ExtendedAccessoryLimits; limits == nil || limits.MinAddress != 1 || limits.MaxAddress != 2040 || limits.MinAspect != 0 || limits.MaxAspect != 31 {
		t.Fatal(limits)
	}
	if err := d.SetExtendedAccessory(context.Background(), station.ExtendedAccessoryCommand{Address: 1, Aspect: 32}); !errors.Is(err, station.ErrInvalidExtendedAccessoryAspect) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := d.SetExtendedAccessory(ctx, command); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(server.Commands()) != 0 {
		t.Fatal("rejected command was sent", server.Commands())
	}
}

func TestExtendedAccessoryContract(t *testing.T) {
	contracttest.ExtendedAccessoryContract(t, func(t *testing.T) contracttest.ExtendedAccessoryHarness {
		server := newFakeTCPServer(t)
		d := newConnectedDriver(t, server, station.DefaultOfflineAfter)
		enableExtendedFirmware(t, d, server)
		return contracttest.ExtendedAccessoryHarness{Station: d, Commands: func() []station.ExtendedAccessoryCommand {
			deadline := time.Now().Add(time.Second)
			for len(server.Commands()) < 2 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			result := []station.ExtendedAccessoryCommand{}
			for _, line := range server.Commands() {
				var address, aspect int
				if _, err := fmt.Sscanf(line, "<A %d %d>", &address, &aspect); err == nil {
					result = append(result, station.ExtendedAccessoryCommand{Address: address, Aspect: aspect})
				}
			}
			return result
		}}
	})
}

func TestExtendedAccessoryReconnectRequiresNewIdentityWithoutReplay(t *testing.T) {
	server := newFakeTCPServer(t)
	d := newConnectedDriver(t, server, 500*time.Millisecond)
	enableExtendedFirmware(t, d, server)
	if err := d.SetExtendedAccessory(context.Background(), station.ExtendedAccessoryCommand{Address: 1, Aspect: 16}); err != nil {
		t.Fatal(err)
	}
	eventually(t, time.Second, "initial extended command", func() bool { return len(server.Commands()) == 1 })
	d.mu.Lock()
	oldID := d.connectionID
	d.mu.Unlock()
	server.Stop()
	eventually(t, time.Second, "identity invalidation", func() bool { return !d.Capabilities().ExtendedAccessoryControl })
	server.Start()
	waitForConnectivity(t, d, station.Online, time.Second)
	eventually(t, time.Second, "recovered TCP", func() bool { return server.ActiveConnections() == 1 })
	d.handleControlFrame("<iDCC-EX V-5.6.6 / MEGA>", oldID)
	if d.Capabilities().ExtendedAccessoryControl {
		t.Fatal("old connection enabled support")
	}
	enableExtendedFirmware(t, d, server)
	if len(server.Commands()) != 1 {
		t.Fatal("opening replayed", server.Commands())
	}
	if err := d.SetExtendedAccessory(context.Background(), station.ExtendedAccessoryCommand{Address: 1, Aspect: 0}); err != nil {
		t.Fatal(err)
	}
	eventually(t, time.Second, "new restrictive command", func() bool { return len(server.Commands()) == 2 })
	if got := server.Commands(); got[0] != "<A 1 16>" || got[1] != "<A 1 0>" {
		t.Fatal(got)
	}
}

func TestExtendedAccessoryTimeoutDuringWriteAndWhileQueued(t *testing.T) {
	for _, queued := range []bool{false, true} {
		t.Run(fmt.Sprintf("queued=%v", queued), func(t *testing.T) {
			client, peer := net.Pipe()
			defer peer.Close()
			d := NewTCP("unused", station.DefaultOfflineAfter)
			d.conn = client
			d.connectionID = 1
			d.firmware = verifiedExtendedFirmware
			d.health.Connected()
			d.health.ValidResponse()
			d.dial = func(context.Context, string) (net.Conn, error) { return nil, errors.New("unavailable") }
			defer d.Close()
			if queued {
				d.writeMu.Lock()
				defer d.writeMu.Unlock()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
			defer cancel()
			if err := d.SetExtendedAccessory(ctx, station.ExtendedAccessoryCommand{Address: 1, Aspect: 0}); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
		})
	}
}

func TestObservedMainPowerIsIndependentFromProgrammingTrack(t *testing.T) {
	server := newFakeTCPServer(t)
	d := newConnectedDriver(t, server, station.DefaultOfflineAfter)
	for _, tc := range []struct{ frame, want string }{{"<p1 PROG>", "unknown"}, {"<p1 A>", "unknown"}, {"<p1 MAIN>", "on"}, {"<p0 A>", "unknown"}, {"<p0>", "off"}, {"<p1 JOIN>", "on"}} {
		server.SendFrame(tc.frame)
		eventually(t, time.Second, "observed power "+tc.want, func() bool {
			status, err := d.Status(context.Background())
			return err == nil && status.TrackPower == tc.want
		})
	}
}
