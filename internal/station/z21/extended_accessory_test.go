package z21

import (
	"bytes"
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/station/contracttest"
)

func TestExtendedAccessoryReferencePacketsAndLimits(t *testing.T) {
	for _, tc := range []struct {
		command station.ExtendedAccessoryCommand
		want    []byte
	}{
		{station.ExtendedAccessoryCommand{Address: 1, Aspect: 5}, []byte{0x0a, 0, 0x40, 0, 0x54, 0, 4, 5, 0, 0x55}},
		{station.ExtendedAccessoryCommand{Address: 2040, Aspect: 255}, []byte{0x0a, 0, 0x40, 0, 0x54, 7, 0xfb, 0xff, 0, 0x57}},
	} {
		got, err := extendedAccessoryPacket(tc.command)
		if err != nil || !bytes.Equal(got, tc.want) {
			t.Fatalf("got % x, err %v; want % x", got, err, tc.want)
		}
	}
	query, err := extendedInfoPacket(1)
	if err != nil || !bytes.Equal(query, []byte{9, 0, 0x40, 0, 0x44, 0, 4, 0, 0x40}) {
		t.Fatal(query, err)
	}
	for _, address := range []int{-1, 0, 2041, 2044} {
		if _, err := extendedRawAddress(address); !errors.Is(err, station.ErrInvalidExtendedAccessoryAddress) {
			t.Fatal(address, err)
		}
	}
	for _, aspect := range []int{-1, 256} {
		if _, err := extendedAccessoryPacket(station.ExtendedAccessoryCommand{Address: 1, Aspect: aspect}); !errors.Is(err, station.ErrInvalidExtendedAccessoryAspect) {
			t.Fatal(err)
		}
	}
}

func TestExtendedAccessoryCapabilityRequiresObservedCompatibleFirmware(t *testing.T) {
	for _, tc := range []struct {
		major, minor byte
		supported    bool
	}{
		{1, 0x39, false}, {1, 0x40, true}, {1, 0x43, true}, {2, 0, true}, {1, 0x4a, false},
	} {
		d := New("unused", station.DefaultOfflineAfter, time.Millisecond)
		if d.Capabilities().ExtendedAccessoryControl {
			t.Fatal("unknown firmware advertised support")
		}
		d.parse(xbus(0xf3, 0x0a, tc.major, tc.minor))
		if got := d.Capabilities().ExtendedAccessoryControl; got != tc.supported {
			t.Fatal(tc, got)
		}
	}
	server := newUDPServer(t)
	d := New(server.address(), station.DefaultOfflineAfter, time.Millisecond)
	if err := d.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var probe udpDatagram
	for range 8 {
		packet := server.next(t)
		if bytes.Equal(packet.data, []byte{7, 0, 0x40, 0, 0xf1, 0x0a, 0xfb}) {
			probe = packet
			break
		}
	}
	if probe.from == nil {
		t.Fatal("firmware query missing at startup")
	}
	server.send(t, probe.from, xbus(0xf3, 0x0a, 1, 0x40))
	deadline := time.Now().Add(time.Second)
	for !d.Capabilities().ExtendedAccessoryControl && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !d.Capabilities().ExtendedAccessoryControl {
		t.Fatal("firmware reply did not enable capability")
	}
}

func TestExtendedAccessoryCommandQueriesStationWithoutInventingConfirmation(t *testing.T) {
	server := newUDPServer(t)
	d := newConnectedTestDriver(t, server, time.Hour)
	d.parse(xbus(0xf3, 0x0a, 1, 0x40))
	if err := d.SetExtendedAccessory(context.Background(), station.ExtendedAccessoryCommand{Address: 1, Aspect: 255}); err != nil {
		t.Fatal(err)
	}
	set, query := server.next(t), server.next(t)
	if set.data[4] != 0x54 || set.data[7] != 255 || query.data[4] != 0x44 {
		t.Fatal(set, query)
	}
	select {
	case e := <-d.ExtendedAccessoryStateEvents():
		t.Fatal("write fabricated report", e)
	default:
	}
	server.send(t, query.from, xbus(0x44, 0, 4, 255, 0))
	select {
	case e := <-d.ExtendedAccessoryStateEvents():
		if e.Address != 1 || e.Aspect == nil || *e.Aspect != 255 || e.Quality != station.AccessoryReportStation || e.State != station.AccessoryReportKnown {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("station report missing")
	}
	if packet, ok := server.nextWithin(20 * time.Millisecond); ok {
		t.Fatalf("unexpected pulse/replay: % x", packet.data)
	}
}

func TestExtendedAccessoryReportsValidateChecksumStatusAndConnectionEpoch(t *testing.T) {
	server := newUDPServer(t)
	d := newConnectedTestDriver(t, server, time.Millisecond)
	d.parse(xbus(0xf3, 0x0a, 1, 0x40))
	for _, status := range []byte{0xff, 0x02} {
		d.parse(xbus(0x44, 0, 4, 16, status))
		e := <-d.ExtendedAccessoryStateEvents()
		if e.Aspect != nil || e.State == station.AccessoryReportKnown || e.Quality != station.AccessoryReportStation {
			t.Fatal(e)
		}
	}
	bad := xbus(0x44, 0, 4, 16, 0)
	bad[len(bad)-1] ^= 1
	d.parse(bad)
	for _, raw := range []uint16{0, 3, 2044, 2047} {
		d.parse(xbus(0x44, byte(raw>>8), byte(raw), 16, 0))
	}
	select {
	case e := <-d.ExtendedAccessoryStateEvents():
		t.Fatal("invalid report accepted", e)
	default:
	}
	d.mu.Lock()
	old, generation, power := d.conn, d.feedbackGeneration, d.feedbackPowerGeneration
	d.mu.Unlock()
	d.parse(xbus(0x44, 0, 4, 16, 0))
	d.loseConnection(old)
	if d.Capabilities().ExtendedAccessoryControl {
		t.Fatal("old firmware capability retained")
	}
	d.parseConnection(old, generation, power, xbus(0x44, 0, 4, 16, 0))
	d.parseConnection(old, generation, power, xbus(0xf3, 0x0a, 1, 0x40))
	select {
	case e := <-d.ExtendedAccessoryStateEvents():
		t.Fatal("old connection report retained", e)
	default:
	}
	if d.Capabilities().ExtendedAccessoryControl {
		t.Fatal("retired connection enabled capability")
	}
}

func TestExtendedAccessoryCancellationAndUnavailableTransportSendNothing(t *testing.T) {
	server := newUDPServer(t)
	d := newConnectedTestDriver(t, server, time.Millisecond)
	command := station.ExtendedAccessoryCommand{Address: 1, Aspect: 0}
	if err := d.SetExtendedAccessory(context.Background(), command); !errors.Is(err, station.ErrUnsupported) {
		t.Fatal(err)
	}
	d.parse(xbus(0xf3, 0x0a, 1, 0x40))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := d.SetExtendedAccessory(ctx, command); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	if err := d.SetExtendedAccessory(expired, command); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	d.health.CommunicationError()
	if err := d.SetExtendedAccessory(context.Background(), command); !errors.Is(err, station.ErrOffline) {
		t.Fatal(err)
	}
	if packet, ok := server.nextWithin(20 * time.Millisecond); ok {
		t.Fatal("rejected command reached transport", packet)
	}
}

func TestExtendedAccessoryContract(t *testing.T) {
	contracttest.ExtendedAccessoryContract(t, func(t *testing.T) contracttest.ExtendedAccessoryHarness {
		server := newUDPServer(t)
		d := newConnectedTestDriver(t, server, time.Millisecond)
		d.parse(xbus(0xf3, 0x0a, 1, 0x40))
		return contracttest.ExtendedAccessoryHarness{Station: d, Commands: func() []station.ExtendedAccessoryCommand {
			result := []station.ExtendedAccessoryCommand{}
			deadline := time.Now().Add(time.Second)
			for len(result) < 2 && time.Now().Before(deadline) {
				packet, ok := server.nextWithin(10 * time.Millisecond)
				if !ok {
					continue
				}
				if len(packet.data) == 10 && packet.data[4] == 0x54 {
					raw := int(packet.data[5])<<8 | int(packet.data[6])
					result = append(result, station.ExtendedAccessoryCommand{Address: raw - 3, Aspect: int(packet.data[7])})
				}
			}
			return result
		}}
	})
}

func TestExtendedAccessoryRecoveryDoesNotReplayPreviousOpening(t *testing.T) {
	server := newUDPServer(t)
	d := newConnectedTestDriver(t, server, time.Millisecond)
	d.parse(xbus(0xf3, 0x0a, 1, 0x40))
	if err := d.SetExtendedAccessory(context.Background(), station.ExtendedAccessoryCommand{Address: 1, Aspect: 16}); err != nil {
		t.Fatal(err)
	}
	server.next(t)
	server.next(t)
	d.mu.Lock()
	old := d.conn
	d.started = true
	d.mu.Unlock()
	d.loseConnection(old)
	if err := d.ensureConnection(context.Background()); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		packet := server.next(t)
		if len(packet.data) > 4 && packet.data[4] == 0x54 {
			t.Fatal("opening replayed", packet)
		}
	}
	d.mu.Lock()
	current := d.conn
	d.mu.Unlock()
	if current.LocalAddr().(*net.UDPAddr).Port == old.LocalAddr().(*net.UDPAddr).Port {
		t.Fatal("reused old endpoint")
	}
	d.parseConnection(old, 1, 0, xbus(0xf3, 0x0a, 1, 0x40))
	if d.Capabilities().ExtendedAccessoryControl {
		t.Fatal("retired firmware reply accepted")
	}
	d.parse(xbus(0xf3, 0x0a, 1, 0x40))
	if err := d.SetExtendedAccessory(context.Background(), station.ExtendedAccessoryCommand{Address: 1, Aspect: 0}); err != nil {
		t.Fatal(err)
	}
	if got := server.next(t); got.data[4] != 0x54 || got.data[7] != 0 {
		t.Fatal(got)
	}
}
