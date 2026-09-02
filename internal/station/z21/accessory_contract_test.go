package z21

import (
	"context"
	"encoding/binary"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/station/contracttest"
)

type contractUDPServer struct {
	conn *net.UDPConn
	mu   sync.Mutex
	seen []station.AccessoryCommand
	done chan struct{}
}

func newContractUDPServer(t *testing.T) *contractUDPServer {
	t.Helper()
	connection, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	server := &contractUDPServer{conn: connection, done: make(chan struct{})}
	go server.run()
	t.Cleanup(func() {
		_ = connection.Close()
		<-server.done
	})
	return server
}

func (s *contractUDPServer) run() {
	defer close(s.done)
	buffer := make([]byte, 2048)
	positions := make(map[uint16]station.AccessoryPosition)
	for {
		count, from, err := s.conn.ReadFromUDP(buffer)
		if err != nil {
			return
		}
		packet := append([]byte(nil), buffer[:count]...)
		if len(packet) < 8 || binary.LittleEndian.Uint16(packet[2:4]) != lanXHeader {
			continue
		}
		fAddress := uint16(packet[5])<<8 | uint16(packet[6])
		switch packet[4] {
		case lanXSetTurnout:
			if packet[7]&0x08 == 0 {
				continue
			}
			position := station.AccessoryPosition1
			if packet[7]&0x01 != 0 {
				position = station.AccessoryPosition2
			}
			positions[fAddress] = position
			address, err := fAddressToLinearAddress(fAddress)
			if err == nil {
				s.mu.Lock()
				s.seen = append(s.seen, station.AccessoryCommand{Address: address, Position: position})
				s.mu.Unlock()
			}
		case lanXGetTurnoutInfo:
			zz := byte(0x01)
			if positions[fAddress] == station.AccessoryPosition2 {
				zz = 0x02
			}
			_, _ = s.conn.WriteToUDP(xbus(lanXGetTurnoutInfo, byte(fAddress>>8), byte(fAddress), zz), from)
		}
	}
}

func (s *contractUDPServer) address() string { return s.conn.LocalAddr().String() }

func (s *contractUDPServer) commands() []station.AccessoryCommand {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]station.AccessoryCommand(nil), s.seen...)
}

func TestBasicAccessoryContract(t *testing.T) {
	contracttest.BasicAccessoryContract(t, func(t *testing.T) contracttest.AccessoryHarness {
		t.Helper()
		server := newContractUDPServer(t)
		driver := New(server.address(), 5*time.Millisecond, time.Millisecond)
		if err := driver.Connect(context.Background()); err != nil {
			t.Fatal(err)
		}
		driver.health.ValidResponse()
		t.Cleanup(func() { _ = driver.Close() })
		contracttest.RequireAccessoryCapability(t, driver)
		return contracttest.AccessoryHarness{
			Station:  driver,
			Commands: server.commands,
			GoOffline: func() {
				driver.health.CommunicationError()
				deadline := time.Now().Add(time.Second)
				for driver.Health().Connectivity != station.Offline {
					if time.Now().After(deadline) {
						t.Fatal("z21 did not become offline")
					}
				}
			},
			Reconnect: driver.health.ValidResponse,
			Settle:    func() { time.Sleep(5 * time.Millisecond) },
		}
	})
}
