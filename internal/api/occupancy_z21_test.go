package api

import (
	"context"
	"encoding/binary"
	"net"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/auth"
	"github.com/agm650/TrainPilot-server/internal/client"
	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/service"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/station/z21"
	"github.com/agm650/TrainPilot-server/internal/store"
	"github.com/agm650/TrainPilot-server/internal/transfer"
)

func TestZ21StartupOccupancyReachesRESTAndWebSocket(t *testing.T) {
	for _, earlyReply := range []bool{true, false} {
		name := "reply_after_client_connects"
		if earlyReply {
			name = "reply_before_feedback_consumer_starts"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			fake := newZ21OccupancyServer(t)
			driver := z21.New(fake.conn.LocalAddr().String(), station.DefaultOfflineAfter, time.Millisecond)
			defer driver.Close()
			if err := driver.Connect(ctx); err != nil {
				t.Fatal(err)
			}
			if earlyReply {
				fake.replyToScan(t)
				deadline := time.Now().Add(time.Second)
				for len(driver.Feedback()) < 2 && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				if got := len(driver.Feedback()); got != 2 {
					t.Fatalf("buffered startup frames=%d want 2", got)
				}
			}
			httpServer, apiClient, token, railway := newZ21OccupancyHTTPFixture(t, ctx, driver)
			want := map[string]model.OccupancyState{
				"block-a": model.OccupancyOccupied, // R-BUS input 80.
				"block-b": model.OccupancyFree,     // R-BUS input 81.
				"block-c": model.OccupancyOccupied, // R-BUS input 160.
			}
			if earlyReply {
				waitForStartupOccupancy(t, railway, want)
			}
			websocket := dialTestWebSocket(t, httpServer.URL, token)
			defer websocket.close()
			snapshot := readTestSnapshot(t, websocket)
			if !earlyReply {
				for _, block := range snapshot.Payload.Blocks {
					if block.Occupancy.State != model.OccupancyUnknown {
						t.Fatalf("before detector reply: block=%+v", block)
					}
				}
				fake.replyToScan(t)
				changed := make(map[string]model.OccupancyState)
				for len(changed) < len(want) {
					var event struct {
						Type    string `json:"type"`
						Payload struct {
							BlockID string               `json:"blockId"`
							State   model.OccupancyState `json:"state"`
						} `json:"payload"`
					}
					websocket.readJSON(t, &event)
					if event.Type == "block.occupancy.changed" {
						changed[event.Payload.BlockID] = event.Payload.State
						if expected, ok := want[event.Payload.BlockID]; !ok || expected != event.Payload.State {
							t.Fatalf("unexpected occupancy event=%+v", event)
						}
					}
				}
				websocket.writeJSON(t, map[string]string{"type": "client.snapshot_request"})
				snapshot = readTestSnapshot(t, websocket)
			}
			assertStartupBlocks(t, snapshot.Payload.Blocks, want)
			blocks, err := apiClient.Blocks(ctx)
			if err != nil {
				t.Fatal(err)
			}
			assertStartupBlocks(t, blocks, want)
		})
	}
}

func assertStartupBlocks(t *testing.T, blocks []model.Block, want map[string]model.OccupancyState) {
	t.Helper()
	if len(blocks) != len(want) {
		t.Fatalf("blocks=%d want %d", len(blocks), len(want))
	}
	for _, block := range blocks {
		if block.Occupancy.State != want[block.ID] || block.Occupied != (want[block.ID] == model.OccupancyOccupied) {
			t.Fatalf("block=%+v want state=%s", block, want[block.ID])
		}
	}
}

func waitForStartupOccupancy(t *testing.T, railway *service.RailwayService, want map[string]model.OccupancyState) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		matched := true
		for id, state := range want {
			matched = matched && railway.OccupancyService().BlockState(id).State == state
		}
		if matched {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("startup detector observations did not reach block occupancy")
}

func newZ21OccupancyHTTPFixture(t *testing.T, parent context.Context, driver station.CommandStation, extraMappings ...model.FeedbackMapping) (*httptest.Server, *client.Client, string, *service.RailwayService) {
	t.Helper()
	server, c, token, railway, _ := newZ21OccupancyHTTPFixtureWithAPI(t, parent, driver, extraMappings...)
	return server, c, token, railway
}

func newZ21OccupancyHTTPFixtureWithAPI(t *testing.T, parent context.Context, driver station.CommandStation, extraMappings ...model.FeedbackMapping) (*httptest.Server, *client.Client, string, *service.RailwayService, *Server) {
	t.Helper()
	ctx, cancel := context.WithCancel(parent)
	db, err := store.Open(":memory:")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = db.Close() })
	if err := db.SeedDemo(ctx); err != nil {
		t.Fatal(err)
	}
	provider, err := db.OccupancyProvider(ctx, "simulator")
	if err != nil {
		t.Fatal(err)
	}
	provider.Required = false
	if err := db.SetOccupancyProvider(ctx, provider); err != nil {
		t.Fatal(err)
	}
	for address, blockID := range map[int]string{80: "block-a", 81: "block-b", 160: "block-c"} {
		if err := db.SetFeedbackMapping(ctx, "z21-rbus", address, blockID); err != nil {
			t.Fatal(err)
		}
	}
	for _, mapping := range extraMappings {
		if err := db.SetFeedbackMapping(ctx, mapping.Provider, mapping.Address, mapping.BlockID); err != nil {
			t.Fatal(err)
		}
	}
	clk := clock.Real{}
	users := service.NewUserServiceWithPasswordParams(db, clk, auth.PasswordParams{Iterations: 100_000, SaltLength: 16, KeyLength: 32})
	if _, err := users.Create(ctx, "dispatcher", "Dispatcher", "correct-horse-1", model.RoleDispatcher, false, false); err != nil {
		t.Fatal(err)
	}
	authService := service.NewAuthService(db, users, clk, 15*time.Minute, time.Hour)
	bus := events.New()
	railway := service.NewRailwayService(db, driver, bus)
	// This fake central is initially powered ON. Seed that known fixture state
	// before consuming its queued initial replies; power-cycle tests start unknown.
	if err := railway.OccupancyService().SetTrackPowerAvailable(ctx, true); err != nil {
		t.Fatal(err)
	}
	railway.StartFeedback(ctx)
	control := service.NewControlService(db, driver, bus, clk, time.Minute, time.Second, time.Hour)
	control.SetOccupancyProvider(railway.OccupancyService(), "z21-rbus")
	routes := service.NewRouteService(db, railway, bus)
	api := New(authService, control, railway, routes, transfer.New(db, bus, clk), db, bus, driver, nil, false)
	httpServer := httptest.NewServer(api.Handler())
	t.Cleanup(httpServer.Close)
	apiClient := client.New(httpServer.URL)
	pair, err := apiClient.Login(ctx, "dispatcher", "correct-horse-1", "occupancy-startup")
	if err != nil {
		t.Fatal(err)
	}
	return httpServer, apiClient, pair.AccessToken, railway, api
}

type z21OccupancyRequest struct {
	group byte
	from  *net.UDPAddr
}

type z21OccupancyServer struct {
	conn           *net.UDPConn
	requests       chan z21OccupancyRequest
	done           chan struct{}
	drop           atomic.Bool
	powerOff       atomic.Bool
	ignorePowerOn  atomic.Bool
	omitXStatus    atomic.Bool
	systemProbes   chan struct{}
	signalSupport  atomic.Bool
	signalCommands chan station.ExtendedAccessoryCommand
}

func newZ21OccupancyServer(t *testing.T) *z21OccupancyServer {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	s := &z21OccupancyServer{conn: conn, requests: make(chan z21OccupancyRequest, 16), done: make(chan struct{}), signalCommands: make(chan station.ExtendedAccessoryCommand, 128), systemProbes: make(chan struct{}, 32)}
	t.Cleanup(func() { _ = conn.Close(); <-s.done })
	go func() {
		defer close(s.done)
		buffer := make([]byte, 2048)
		aspects := map[int]int{}
		for {
			n, from, err := conn.ReadFromUDP(buffer)
			if err != nil {
				return
			}
			packet := buffer[:n]
			if s.drop.Load() {
				continue
			}
			if len(packet) < 4 {
				continue
			}
			switch binary.LittleEndian.Uint16(packet[2:4]) {
			case 0x0081:
				if len(packet) == 5 {
					s.requests <- z21OccupancyRequest{group: packet[4], from: from}
				}
			case 0x0085:
				payload := make([]byte, 16)
				if s.powerOff.Load() {
					payload[12] = 0x02
				}
				_, _ = conn.WriteToUDP(apiZ21LAN(0x0084, payload), from)
				select {
				case s.systemProbes <- struct{}{}:
				default:
				}
			case 0x0040:
				if s.signalSupport.Load() && len(packet) >= 7 {
					switch packet[4] {
					case 0xf1:
						if packet[5] == 0x0a {
							_, _ = conn.WriteToUDP(apiZ21XBus(0xf3, 0x0a, 1, 0x40), from)
						}
					case 0x54:
						if len(packet) == 10 {
							address := int(packet[5])<<8 | int(packet[6])
							aspects[address] = int(packet[7])
							select {
							case s.signalCommands <- station.ExtendedAccessoryCommand{Address: address - 3, Aspect: int(packet[7])}:
							default:
							}
						}
					case 0x44:
						if len(packet) == 9 {
							address := int(packet[5])<<8 | int(packet[6])
							code, known := aspects[address]
							status := byte(0)
							if !known {
								status = 0xff
							}
							_, _ = conn.WriteToUDP(apiZ21XBus(0x44, packet[5], packet[6], byte(code), status), from)
						}
					}
				}
				if len(packet) == 7 && packet[4] == 0x21 && packet[5] == 0x24 && !s.omitXStatus.Load() {
					status := byte(0)
					if s.powerOff.Load() {
						status = 2
					}
					_, _ = conn.WriteToUDP(apiZ21XBus(0x62, 0x22, status), from)
				}
				if len(packet) == 7 && packet[4] == 0x21 && (packet[5] == 0x80 || packet[5] == 0x81) {
					if packet[5] != 0x81 || !s.ignorePowerOn.Load() {
						s.powerOff.Store(packet[5] == 0x80)
					}
				}
			}
		}
	}()
	return s
}

// Retries can remain queued while the HTTP fixture initializes. Recovery must
// answer a complete scan addressed to the renewed endpoint, not those retries.
func (s *z21OccupancyServer) scanAfter(t *testing.T, previous *net.UDPAddr) [2]z21OccupancyRequest {
	t.Helper()
	var requests [2]z21OccupancyRequest
	var received [2]bool
	var endpoint string
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for !received[0] || !received[1] {
		select {
		case request := <-s.requests:
			if previous != nil && request.from.String() == previous.String() {
				continue
			}
			if request.group >= byte(len(requests)) {
				t.Fatalf("unexpected R-BUS group %d", request.group)
			}
			if endpoint == "" {
				endpoint = request.from.String()
			} else if endpoint != request.from.String() {
				t.Fatalf("R-BUS scan changed endpoint: %s -> %s", endpoint, request.from)
			}
			requests[request.group] = request
			received[request.group] = true
		case <-timer.C:
			t.Fatalf("incomplete R-BUS scan: received=%v endpoint=%s previous=%v", received, endpoint, previous)
		}
	}
	return requests
}

func (s *z21OccupancyServer) replyToScan(t *testing.T) [2]z21OccupancyRequest {
	t.Helper()
	requests := s.scanAfter(t, nil)
	for group, request := range requests {
		payload := make([]byte, 11)
		payload[0], payload[10] = byte(group), 0x80 // Inputs 80 and 160 occupied.
		if _, err := s.conn.WriteToUDP(apiZ21LAN(0x0080, payload), request.from); err != nil {
			t.Fatal(err)
		}
	}
	return requests
}
