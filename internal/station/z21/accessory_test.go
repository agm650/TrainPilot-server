package z21

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/station"
)

func TestAccessoryAddressConversion(t *testing.T) {
	for _, tc := range []struct {
		linear int
		faddr  uint16
	}{
		{1, 0x0000},
		{4, 0x0003},
		{5, 0x0004},
		{8, 0x0007},
		{9, 0x0008},
		{2040, 0x07f7},
	} {
		t.Run(strconv.Itoa(tc.linear), func(t *testing.T) {
			got, err := linearAddressToFAddress(tc.linear)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.faddr {
				t.Fatalf("linear %d: FAdr=%#04x want %#04x", tc.linear, got, tc.faddr)
			}
			roundTrip, err := fAddressToLinearAddress(got)
			if err != nil {
				t.Fatal(err)
			}
			if roundTrip != tc.linear {
				t.Fatalf("FAdr %#04x: linear=%d want %d", got, roundTrip, tc.linear)
			}
		})
	}
	for _, address := range []int{0, 2041} {
		if _, err := linearAddressToFAddress(address); !errors.Is(err, station.ErrInvalidAccessoryAddress) {
			t.Fatalf("linear %d error=%v", address, err)
		}
	}
	if _, err := fAddressToLinearAddress(2040); !errors.Is(err, station.ErrInvalidAccessoryAddress) {
		t.Fatalf("FAdr 2040 error=%v", err)
	}
}

func TestAccessoryPackets(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  []byte
		want []byte
	}{
		{
			"position1 activate",
			turnoutCommandPacket(0, station.AccessoryPosition1, true),
			[]byte{0x09, 0x00, 0x40, 0x00, 0x53, 0x00, 0x00, 0xa8, 0xfb},
		},
		{
			"position1 deactivate",
			turnoutCommandPacket(0, station.AccessoryPosition1, false),
			[]byte{0x09, 0x00, 0x40, 0x00, 0x53, 0x00, 0x00, 0xa0, 0xf3},
		},
		{
			"position2 activate",
			turnoutCommandPacket(0, station.AccessoryPosition2, true),
			[]byte{0x09, 0x00, 0x40, 0x00, 0x53, 0x00, 0x00, 0xa9, 0xfa},
		},
		{
			"position2 deactivate",
			turnoutCommandPacket(0, station.AccessoryPosition2, false),
			[]byte{0x09, 0x00, 0x40, 0x00, 0x53, 0x00, 0x00, 0xa1, 0xf2},
		},
		{
			"get info",
			turnoutInfoRequestPacket(0),
			[]byte{0x08, 0x00, 0x40, 0x00, 0x43, 0x00, 0x00, 0x43},
		},
		{
			"broadcast flags",
			broadcastFlagsPacket(),
			[]byte{0x08, 0x00, 0x50, 0x00, 0x03, 0x00, 0x00, 0x00},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !bytes.Equal(tc.got, tc.want) {
				t.Fatalf("packet=% x want=% x", tc.got, tc.want)
			}
		})
	}
}

func TestParseTurnoutInfoStates(t *testing.T) {
	for _, tc := range []struct {
		name     string
		zz       byte
		state    station.AccessoryReportState
		position station.AccessoryPosition
	}{
		{"unknown", 0x00, station.AccessoryReportUnknown, ""},
		{"position1", 0x01, station.AccessoryReportKnown, station.AccessoryPosition1},
		{"position2", 0x02, station.AccessoryReportKnown, station.AccessoryPosition2},
		{"invalid", 0x03, station.AccessoryReportInvalid, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			driver := New("unused", station.DefaultOfflineAfter, DefaultAccessoryPulse)
			driver.parse(xbus(lanXGetTurnoutInfo, 0x00, 0x03, tc.zz))
			select {
			case event := <-driver.AccessoryStateEvents():
				if event.Address != 4 || event.State != tc.state || event.Position != tc.position || event.Quality != station.AccessoryReportStation || event.ObservedAt.IsZero() {
					t.Fatalf("event=%+v", event)
				}
			default:
				t.Fatal("turnout event was not published")
			}
		})
	}
}

func TestConcurrentAccessoryGetsAreCorrelated(t *testing.T) {
	server := newUDPServer(t)
	driver := newConnectedTestDriver(t, server, DefaultAccessoryPulse)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	type result struct {
		event station.AccessoryStateEvent
		err   error
	}
	results := make(chan result, 2)
	for _, address := range []int{1, 5} {
		address := address
		go func() {
			event, err := driver.getAccessoryState(ctx, address)
			results <- result{event: event, err: err}
		}()
	}

	requests := map[uint16]*net.UDPAddr{}
	for len(requests) < 2 {
		datagram := server.next(t)
		if len(datagram.data) >= 8 && datagram.data[4] == lanXGetTurnoutInfo {
			fAddress := uint16(datagram.data[5])<<8 | uint16(datagram.data[6])
			requests[fAddress] = datagram.from
		}
	}
	server.send(t, requests[4], xbus(lanXGetTurnoutInfo, 0x00, 0x04, 0x02))
	server.send(t, requests[0], xbus(lanXGetTurnoutInfo, 0x00, 0x00, 0x01))

	got := map[int]station.AccessoryPosition{}
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		got[result.event.Address] = result.event.Position
	}
	if got[1] != station.AccessoryPosition1 || got[5] != station.AccessoryPosition2 {
		t.Fatalf("correlated results=%v", got)
	}
}

func TestSpontaneousAccessoryBroadcastIsPublished(t *testing.T) {
	server := newUDPServer(t)
	driver := newConnectedTestDriver(t, server, DefaultAccessoryPulse)
	server.send(t, driver.conn.LocalAddr().(*net.UDPAddr), xbus(lanXGetTurnoutInfo, 0x00, 0x08, 0x02))
	select {
	case event := <-driver.AccessoryStateEvents():
		if event.Address != 9 || event.Position != station.AccessoryPosition2 || event.State != station.AccessoryReportKnown {
			t.Fatalf("event=%+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for spontaneous turnout event")
	}
}

func TestSetBasicAccessoryPulseAndConfirmation(t *testing.T) {
	server := newUDPServer(t)
	driver := newConnectedTestDriver(t, server, 5*time.Millisecond)
	result := make(chan error, 1)
	go func() {
		result <- driver.SetBasicAccessory(context.Background(), station.AccessoryCommand{Address: 5, Position: station.AccessoryPosition2})
	}()

	activate := server.next(t)
	deactivate := server.next(t)
	get := server.next(t)
	if want := turnoutCommandPacket(4, station.AccessoryPosition2, true); !bytes.Equal(activate.data, want) {
		t.Fatalf("activate=% x want=% x", activate.data, want)
	}
	if want := turnoutCommandPacket(4, station.AccessoryPosition2, false); !bytes.Equal(deactivate.data, want) {
		t.Fatalf("deactivate=% x want=% x", deactivate.data, want)
	}
	if want := turnoutInfoRequestPacket(4); !bytes.Equal(get.data, want) {
		t.Fatalf("get=% x want=% x", get.data, want)
	}
	server.send(t, get.from, xbus(lanXGetTurnoutInfo, 0x00, 0x04, 0x02))
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-driver.AccessoryStateEvents():
		if event.Address != 5 || event.Position != station.AccessoryPosition2 {
			t.Fatalf("event=%+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for confirmation")
	}
}

func TestSetBasicAccessoryCancellationStillDeactivates(t *testing.T) {
	server := newUDPServer(t)
	driver := newConnectedTestDriver(t, server, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- driver.SetBasicAccessory(ctx, station.AccessoryCommand{Address: 1, Position: station.AccessoryPosition1})
	}()
	activate := server.next(t)
	cancel()
	deactivate := server.next(t)
	if !bytes.Equal(activate.data, turnoutCommandPacket(0, station.AccessoryPosition1, true)) {
		t.Fatalf("activate=% x", activate.data)
	}
	if !bytes.Equal(deactivate.data, turnoutCommandPacket(0, station.AccessoryPosition1, false)) {
		t.Fatalf("deactivate=% x", deactivate.data)
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v want context.Canceled", err)
	}
	if datagram, ok := server.nextWithin(30 * time.Millisecond); ok {
		t.Fatalf("unexpected replay/request after cancellation: % x", datagram.data)
	}
}

func TestOfflineAccessoryCommandSendsNothing(t *testing.T) {
	server := newUDPServer(t)
	driver := New(server.address(), station.DefaultOfflineAfter, time.Millisecond)
	err := driver.SetBasicAccessory(context.Background(), station.AccessoryCommand{Address: 1, Position: station.AccessoryPosition1})
	if !errors.Is(err, station.ErrOffline) {
		t.Fatalf("error=%v want station.ErrOffline", err)
	}
	if datagram, ok := server.nextWithin(30 * time.Millisecond); ok {
		t.Fatalf("offline command sent packet: % x", datagram.data)
	}
}

func TestConnectConfiguresAccessoryBroadcasts(t *testing.T) {
	server := newUDPServer(t)
	driver := New(server.address(), station.DefaultOfflineAfter, DefaultAccessoryPulse)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := driver.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = driver.Close() })
	packet := server.next(t)
	if want := broadcastFlagsPacket(); !bytes.Equal(packet.data, want) {
		t.Fatalf("first connect packet=% x want=% x", packet.data, want)
	}
}

type udpDatagram struct {
	data []byte
	from *net.UDPAddr
}

type udpTestServer struct {
	conn    *net.UDPConn
	packets chan udpDatagram
}

func newUDPServer(t *testing.T) *udpTestServer {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	server := &udpTestServer{conn: conn, packets: make(chan udpDatagram, 32)}
	t.Cleanup(func() { _ = conn.Close() })
	go func() {
		buffer := make([]byte, 2048)
		for {
			n, from, readErr := conn.ReadFromUDP(buffer)
			if readErr != nil {
				return
			}
			packet := append([]byte(nil), buffer[:n]...)
			server.packets <- udpDatagram{data: packet, from: from}
		}
	}()
	return server
}

func (s *udpTestServer) address() string { return s.conn.LocalAddr().String() }

func (s *udpTestServer) next(t *testing.T) udpDatagram {
	t.Helper()
	select {
	case packet := <-s.packets:
		return packet
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for UDP packet")
		return udpDatagram{}
	}
}

func (s *udpTestServer) nextWithin(duration time.Duration) (udpDatagram, bool) {
	select {
	case packet := <-s.packets:
		return packet, true
	case <-time.After(duration):
		return udpDatagram{}, false
	}
}

func (s *udpTestServer) send(t *testing.T, destination *net.UDPAddr, packet []byte) {
	t.Helper()
	if _, err := s.conn.WriteToUDP(packet, destination); err != nil {
		t.Fatal(err)
	}
}

func newConnectedTestDriver(t *testing.T, server *udpTestServer, pulse time.Duration) *Driver {
	t.Helper()
	remote, err := net.ResolveUDPAddr("udp", server.address())
	if err != nil {
		t.Fatal(err)
	}
	connection, err := net.DialUDP("udp", nil, remote)
	if err != nil {
		t.Fatal(err)
	}
	driver := New(server.address(), station.DefaultOfflineAfter, pulse)
	driver.conn = connection
	driver.health.Connected()
	driver.health.ValidResponse()
	go driver.readLoop(connection)
	t.Cleanup(func() { _ = driver.Close() })
	return driver
}
