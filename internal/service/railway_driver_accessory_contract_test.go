package service

import (
	"bufio"
	"context"
	"encoding/binary"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/model/turnoutfixture"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/station/dccex"
	"github.com/agm650/TrainPilot-server/internal/station/simulator"
	"github.com/agm650/TrainPilot-server/internal/station/z21"
	"github.com/agm650/TrainPilot-server/internal/store"
)

type accessoryDriverFactory struct {
	name    string
	quality station.AccessoryReportQuality
	new     func(*testing.T, context.Context) station.CommandStation
}

func TestRailwayServiceUsesCommonLogicalFixturesWithEveryDriver(t *testing.T) {
	drivers := []accessoryDriverFactory{
		{
			name: "simulator", quality: station.AccessoryReportPhysical,
			new: func(t *testing.T, ctx context.Context) station.CommandStation {
				driver := simulator.New()
				if err := driver.Connect(ctx); err != nil {
					t.Fatal(err)
				}
				return driver
			},
		},
		{
			name: "z21", quality: station.AccessoryReportStation,
			new: func(t *testing.T, ctx context.Context) station.CommandStation {
				server := newServiceZ21AccessoryServer(t)
				driver := z21.New(server.address(), station.DefaultOfflineAfter, time.Millisecond)
				if err := driver.Connect(ctx); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = driver.Close() })
				return driver
			},
		},
		{
			name: "dccex", quality: station.AccessoryReportAssumed,
			new: func(t *testing.T, ctx context.Context) station.CommandStation {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = listener.Close() })
				go drainDCCEXCommands(listener)
				driver := dccex.NewTCP(listener.Addr().String(), station.DefaultOfflineAfter)
				if err := driver.Connect(ctx); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = driver.Close() })
				return driver
			},
		},
	}

	for _, driverFactory := range drivers {
		driverFactory := driverFactory
		for _, definition := range turnoutfixture.All() {
			definition := definition
			t.Run(driverFactory.name+"/"+definition.ID, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				t.Cleanup(cancel)
				driver := driverFactory.new(t, ctx)
				if !driver.Capabilities().AccessoryControl {
					t.Fatalf("driver %s does not announce accessoryControl", driverFactory.name)
				}
				// Begin without a claimed physical position. The first command must
				// therefore drive every endpoint and establish the driver report.
				definition.DesiredPosition = ""
				definition.ReportedPosition = ""
				definition.Quality = ""
				db, err := store.Open(":memory:")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = db.Close() })
				if err := db.ImportLayout(ctx, model.LayoutDefinition{Turnouts: []model.Turnout{definition}}, false); err != nil {
					t.Fatal(err)
				}
				bus := events.New()
				railway := NewRailwayService(db, driver, bus, 250*time.Millisecond)
				railway.StartFeedback(ctx)
				targets := append([]model.TurnoutPositionDefinition(nil), definition.Positions[1:]...)
				targets = append(targets, definition.Positions[0])
				for _, target := range targets {
					if err := railway.SetTurnout(ctx, model.User{Role: model.RoleDispatcher}, definition.ID, target.ID); err != nil {
						t.Fatalf("set %s: %v", target.ID, err)
					}
					stored, err := db.GetTurnout(ctx, definition.ID)
					if err != nil {
						t.Fatal(err)
					}
					if stored.ReportedPosition != target.ID || stored.Pending || stored.CommandStatus != model.TurnoutCommandSucceeded || stored.Quality != driverFactory.quality {
						t.Fatalf("target %s state=%+v", target.ID, stored)
					}
				}
			})
		}
	}
}

func TestRailwayServiceCoversEveryDoubleSlipTransitionPair(t *testing.T) {
	definition := turnoutfixture.DoubleSlip()
	for _, from := range definition.Positions {
		for _, to := range definition.Positions {
			if from.ID == to.ID {
				continue
			}
			t.Run(from.ID+"_to_"+to.ID, func(t *testing.T) {
				fixture := turnoutfixture.DoubleSlip()
				fixture.DesiredPosition = from.ID
				fixture.ReportedPosition = from.ID
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				db, _, railway := newAccessoryRailwayService(t, ctx, fixture)
				defer db.Close()
				if err := railway.SetTurnout(ctx, model.User{Role: model.RoleDispatcher}, fixture.ID, to.ID); err != nil {
					t.Fatal(err)
				}
				stored, err := db.GetTurnout(ctx, fixture.ID)
				if err != nil || stored.ReportedPosition != to.ID || stored.Pending {
					t.Fatalf("state=%+v err=%v", stored, err)
				}
			})
		}
	}
}

func TestRailwayServiceAcceptsExternalZ21AccessoryReport(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := newServiceZ21AccessoryServer(t)
	driver := z21.New(server.address(), station.DefaultOfflineAfter, time.Millisecond)
	if err := driver.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer driver.Close()
	definition := turnoutfixture.Simple()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.ImportLayout(ctx, model.LayoutDefinition{Turnouts: []model.Turnout{definition}}, false); err != nil {
		t.Fatal(err)
	}
	railway := NewRailwayService(db, driver, events.New(), 250*time.Millisecond)
	railway.StartFeedback(ctx)
	server.sendPosition(t, definition.Endpoints[0].LinearAddress, station.AccessoryPosition2)
	waitForTurnoutPosition(t, ctx, db, definition.ID, "diverging", false)
	stored, err := db.GetTurnout(ctx, definition.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.DesiredPosition != "straight" || stored.Quality != station.AccessoryReportStation || stored.ReportedStatus != station.AccessoryReportKnown {
		t.Fatalf("external z21 report state=%+v", stored)
	}
}

type serviceZ21AccessoryServer struct {
	conn      *net.UDPConn
	done      chan struct{}
	mu        sync.Mutex
	client    *net.UDPAddr
	positions map[uint16]byte
}

func newServiceZ21AccessoryServer(t *testing.T) *serviceZ21AccessoryServer {
	t.Helper()
	connection, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	server := &serviceZ21AccessoryServer{conn: connection, done: make(chan struct{}), positions: make(map[uint16]byte)}
	go server.run()
	t.Cleanup(func() {
		_ = connection.Close()
		<-server.done
	})
	return server
}

func (s *serviceZ21AccessoryServer) address() string { return s.conn.LocalAddr().String() }

func (s *serviceZ21AccessoryServer) run() {
	defer close(s.done)
	buffer := make([]byte, 2048)
	for {
		count, from, err := s.conn.ReadFromUDP(buffer)
		if err != nil {
			return
		}
		s.mu.Lock()
		s.client = from
		s.mu.Unlock()
		packet := append([]byte(nil), buffer[:count]...)
		if len(packet) < 8 || binary.LittleEndian.Uint16(packet[2:4]) != 0x0040 {
			continue
		}
		address := uint16(packet[5])<<8 | uint16(packet[6])
		switch packet[4] {
		case 0x53:
			if packet[7]&0x08 != 0 {
				s.mu.Lock()
				s.positions[address] = (packet[7] & 0x01) + 1
				s.mu.Unlock()
			}
		case 0x43:
			s.mu.Lock()
			state := s.positions[address]
			s.mu.Unlock()
			if state == 0 {
				state = 1
			}
			_, _ = s.conn.WriteToUDP(serviceZ21XBus(0x43, byte(address>>8), byte(address), state), from)
		}
	}
}

func (s *serviceZ21AccessoryServer) sendPosition(t *testing.T, linearAddress int, position station.AccessoryPosition) {
	t.Helper()
	state := byte(1)
	if position == station.AccessoryPosition2 {
		state = 2
	}
	fAddress := uint16(linearAddress - 1)
	deadline := time.Now().Add(time.Second)
	for {
		s.mu.Lock()
		client := s.client
		s.mu.Unlock()
		if client != nil {
			if _, err := s.conn.WriteToUDP(serviceZ21XBus(0x43, byte(fAddress>>8), byte(fAddress), state), client); err != nil {
				t.Fatal(err)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("z21 client address was not observed")
		}
		time.Sleep(time.Millisecond)
	}
}

func serviceZ21XBus(payload ...byte) []byte {
	checksum := byte(0)
	for _, value := range payload {
		checksum ^= value
	}
	record := make([]byte, 4+len(payload)+1)
	binary.LittleEndian.PutUint16(record[0:2], uint16(len(record)))
	binary.LittleEndian.PutUint16(record[2:4], 0x0040)
	copy(record[4:], payload)
	record[len(record)-1] = checksum
	return record
}

func drainDCCEXCommands(listener net.Listener) {
	connection, err := listener.Accept()
	if err != nil {
		return
	}
	defer connection.Close()
	scanner := bufio.NewScanner(connection)
	for scanner.Scan() {
	}
}
