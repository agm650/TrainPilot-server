package api

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/auth"
	"github.com/agm650/TrainPilot-server/internal/client"
	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/model/turnoutfixture"
	"github.com/agm650/TrainPilot-server/internal/service"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/station/dccex"
	"github.com/agm650/TrainPilot-server/internal/station/z21"
	"github.com/agm650/TrainPilot-server/internal/store"
	"github.com/agm650/TrainPilot-server/internal/transfer"
)

func TestCompoundTurnoutHTTPToDriverAndWebSocket(t *testing.T) {
	tests := []struct {
		name    string
		quality station.AccessoryReportQuality
		station func(*testing.T) station.CommandStation
	}{
		{
			name: "z21", quality: station.AccessoryReportStation,
			station: func(t *testing.T) station.CommandStation {
				fake := newAPIZ21AccessoryServer(t)
				return z21.New(fake.address(), station.DefaultOfflineAfter, time.Millisecond)
			},
		},
		{
			name: "dccex", quality: station.AccessoryReportAssumed,
			station: func(t *testing.T) station.CommandStation {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = listener.Close() })
				go drainAPIDCCEX(listener)
				return dccex.NewTCP(listener.Addr().String(), station.DefaultOfflineAfter)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			driver := test.station(t)
			if err := driver.Connect(ctx); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = driver.Close() })
			httpServer, apiClient, token := newTurnoutDriverHTTPFixture(t, ctx, driver)
			websocket := dialTestWebSocket(t, httpServer.URL, token)
			defer websocket.close()
			readTestSnapshot(t, websocket)

			done := make(chan error, 1)
			go func() { done <- apiClient.SetTurnout(ctx, turnoutfixture.ThreeWay().ID, "right") }()
			commanded := false
			completed := false
			for attempts := 0; attempts < 8 && !completed; attempts++ {
				var event struct {
					Type    string `json:"type"`
					Payload struct {
						TurnoutID        string                         `json:"turnoutId"`
						TargetPosition   string                         `json:"targetPosition"`
						ReportedPosition string                         `json:"reportedPosition"`
						Pending          bool                           `json:"pending"`
						ReportQuality    station.AccessoryReportQuality `json:"reportQuality"`
					} `json:"payload"`
				}
				websocket.readJSON(t, &event)
				if event.Type == "turnout.commanded" && event.Payload.TurnoutID == "triple" && event.Payload.TargetPosition == "right" {
					commanded = true
				}
				if event.Type == "turnout.state.changed" && event.Payload.TurnoutID == "triple" && !event.Payload.Pending && event.Payload.ReportedPosition == "right" {
					if event.Payload.ReportQuality != test.quality {
						t.Fatalf("quality=%q want %q", event.Payload.ReportQuality, test.quality)
					}
					completed = true
				}
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if !commanded || !completed {
				t.Fatalf("websocket flow commanded=%v completed=%v", commanded, completed)
			}
		})
	}
}

func newTurnoutDriverHTTPFixture(t *testing.T, ctx context.Context, driver station.CommandStation) (*httptest.Server, *client.Client, string) {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.SeedDemo(ctx); err != nil {
		t.Fatal(err)
	}
	definition := turnoutfixture.ThreeWay()
	definition.DesiredPosition = ""
	definition.ReportedPosition = ""
	definition.Quality = ""
	if err := db.ImportLayout(ctx, model.LayoutDefinition{Turnouts: []model.Turnout{definition}}, false); err != nil {
		t.Fatal(err)
	}

	clk := clock.Real{}
	users := service.NewUserServiceWithPasswordParams(db, clk, auth.PasswordParams{Iterations: 100_000, SaltLength: 16, KeyLength: 32})
	if _, err := users.Create(ctx, "dispatcher", "Dispatcher", "correct-horse-1", model.RoleDispatcher, false, false); err != nil {
		t.Fatal(err)
	}
	authService := service.NewAuthService(db, users, clk, 15*time.Minute, time.Hour)
	bus := events.New()
	railway := service.NewRailwayService(db, driver, bus, 500*time.Millisecond)
	railway.StartFeedback(ctx)
	control := service.NewControlService(db, driver, bus, clk, 15*time.Second, time.Second, time.Hour)
	routes := service.NewRouteService(db, railway, bus)
	server := New(authService, control, railway, routes, transfer.New(db, bus, clk), db, bus, driver, nil, false)
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)
	apiClient := client.New(httpServer.URL)
	pair, err := apiClient.Login(ctx, "dispatcher", "correct-horse-1", "turnout-driver-e2e")
	if err != nil {
		t.Fatal(err)
	}
	return httpServer, apiClient, pair.AccessToken
}

type apiZ21AccessoryServer struct {
	conn *net.UDPConn
	done chan struct{}
}

func newAPIZ21AccessoryServer(t *testing.T) *apiZ21AccessoryServer {
	t.Helper()
	connection, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	server := &apiZ21AccessoryServer{conn: connection, done: make(chan struct{})}
	go server.run()
	t.Cleanup(func() {
		_ = connection.Close()
		<-server.done
	})
	return server
}

func (s *apiZ21AccessoryServer) address() string { return s.conn.LocalAddr().String() }

func (s *apiZ21AccessoryServer) run() {
	defer close(s.done)
	buffer := make([]byte, 2048)
	positions := make(map[uint16]byte)
	for {
		count, from, err := s.conn.ReadFromUDP(buffer)
		if err != nil {
			return
		}
		packet := buffer[:count]
		if len(packet) < 4 {
			continue
		}
		header := binary.LittleEndian.Uint16(packet[2:4])
		if header == 0x0085 {
			_, _ = s.conn.WriteToUDP(apiZ21LAN(0x0084, make([]byte, 16)), from)
			continue
		}
		if header != 0x0040 || len(packet) < 7 {
			continue
		}
		if packet[4] == 0x21 && packet[5] == 0x24 {
			_, _ = s.conn.WriteToUDP(apiZ21XBus(0x62, 0x22, 0x00), from)
			continue
		}
		if len(packet) < 8 {
			continue
		}
		address := uint16(packet[5])<<8 | uint16(packet[6])
		switch packet[4] {
		case 0x53:
			if packet[7]&0x08 != 0 {
				positions[address] = (packet[7] & 0x01) + 1
			}
		case 0x43:
			state := positions[address]
			if state == 0 {
				state = 1
			}
			_, _ = s.conn.WriteToUDP(apiZ21XBus(0x43, byte(address>>8), byte(address), state), from)
		}
	}
}

func apiZ21XBus(payload ...byte) []byte {
	checksum := byte(0)
	for _, value := range payload {
		checksum ^= value
	}
	packet := make([]byte, 4+len(payload)+1)
	binary.LittleEndian.PutUint16(packet[0:2], uint16(len(packet)))
	binary.LittleEndian.PutUint16(packet[2:4], 0x0040)
	copy(packet[4:], payload)
	packet[len(packet)-1] = checksum
	return packet
}

func apiZ21LAN(header uint16, payload []byte) []byte {
	packet := make([]byte, 4+len(payload))
	binary.LittleEndian.PutUint16(packet[0:2], uint16(len(packet)))
	binary.LittleEndian.PutUint16(packet[2:4], header)
	copy(packet[4:], payload)
	return packet
}

func drainAPIDCCEX(listener net.Listener) {
	connection, err := listener.Accept()
	if err != nil {
		return
	}
	defer connection.Close()
	scanner := bufio.NewScanner(connection)
	for scanner.Scan() {
		if !strings.HasPrefix(scanner.Text(), "<a ") {
			continue
		}
		var address, position int
		_, _ = fmt.Sscanf(scanner.Text(), "<a %d %d>", &address, &position)
	}
}
