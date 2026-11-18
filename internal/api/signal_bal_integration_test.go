package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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

func latestBALScan(t *testing.T, s *z21OccupancyServer, previous *net.UDPAddr) [2]z21OccupancyRequest {
	t.Helper()
	requests := s.scanAfter(t, previous)
	endpoint := requests[0].from.String()
	seen := [2]bool{true, true}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for !seen[0] || !seen[1] || len(s.requests) > 0 {
		select {
		case next := <-s.requests:
			if next.from.String() == previous.String() {
				continue
			}
			if next.from.String() != endpoint {
				endpoint = next.from.String()
				seen = [2]bool{}
			}
			if next.group > 1 {
				t.Fatal("invalid scan group", next.group)
			}
			requests[next.group] = next
			seen[next.group] = true
		case <-timer.C:
			t.Fatal("missing final synchronization group", endpoint, seen)
		}
	}
	return requests
}

func assertZ21BALWrites(t *testing.T, s *z21OccupancyServer, want map[int]int) {
	t.Helper()
	first, last := map[int]int{}, map[int]int{}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case c := <-s.signalCommands:
			if _, exists := first[c.Address]; !exists {
				first[c.Address] = c.Aspect
			}
			last[c.Address] = c.Aspect
			matched := len(last) == len(want)
			for address, code := range want {
				if last[address] != code {
					matched = false
				}
			}
			if matched {
				for address, code := range first {
					if code != 0 {
						t.Fatal("opening before restrictive synchronization", address, code)
					}
				}
				return
			}
		case <-timer.C:
			t.Fatal("missing extended packets", first, last, want)
		}
	}
}

func balIntegrationLayout(provider string, addresses []int) model.LayoutDefinition {
	l := model.LayoutDefinition{SignalProfiles: []model.SignalProfile{model.SNCFBAL3Profile()}}
	for i := 0; i < 4; i++ {
		kind := model.TopologyNodeJoint
		if i == 0 || i == 3 {
			kind = model.TopologyNodeBoundary
		}
		l.TopologyNodes = append(l.TopologyNodes, model.TopologyNode{ID: fmt.Sprint("n", i), Kind: kind})
	}
	for i, id := range []string{"a", "b", "c"} {
		l.TrackSections = append(l.TrackSections, model.TrackSection{ID: id, Name: id, NodeAID: fmt.Sprint("n", i), NodeBID: fmt.Sprint("n", i+1)})
		l.Blocks = append(l.Blocks, model.BlockDefinition{ID: id, Name: id, TrackSectionIDs: []string{id}})
		l.FeedbackMappings = append(l.FeedbackMappings, model.FeedbackMapping{Provider: provider, Address: addresses[i], BlockID: id})
		down := model.SignalDownstream{Kind: model.SignalDownstreamTerminal, Aspect: "S"}
		if i < 2 {
			down = model.SignalDownstream{Kind: model.SignalDownstreamSignal, SignalID: fmt.Sprint("s", i+2)}
		}
		l.Signals = append(l.Signals, model.SignalDefinition{ID: fmt.Sprint("s", i+1), Name: id, ProfileID: model.SignalProfileSNCFBAL3, Mode: model.SignalModeAutomatic, Placement: model.SignalPlacement{TrackSectionID: id, EntryNodeID: fmt.Sprint("n", i)}, ProtectedBlockID: id, Downstream: down, Endpoint: model.SignalEndpoint{Kind: model.SignalEndpointDCCExtended, LinearAddress: i + 1, AspectCodes: map[model.SignalAspectID]int{"S": 0, "A": 7, "VL": 16}}})
	}
	return l
}

func awaitBALREST(t *testing.T, c *client.Client, want ...model.SignalAspectID) []model.Signal {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var signals []model.Signal
	for time.Now().Before(deadline) {
		var err error
		signals, err = c.Signals(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		matched := len(signals) == len(want)
		for i, v := range signals {
			if i >= len(want) || v.State.Pending || v.State.CommandedAspect == nil || *v.State.CommandedAspect != want[i] || v.State.DesiredAspect != want[i] {
				matched = false
				break
			}
		}
		if matched {
			return signals
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("REST signals did not converge: %+v", signals)
	return nil
}

func assertBALSnapshot(t *testing.T, snapshot systemSnapshot, want ...model.SignalAspectID) {
	t.Helper()
	if len(snapshot.Payload.Signals) != len(want) {
		t.Fatal(snapshot.Payload.Signals)
	}
	for i, v := range snapshot.Payload.Signals {
		if v.State.CommandedAspect == nil || *v.State.CommandedAspect != want[i] || v.State.DesiredAspect != want[i] || v.State.Pending {
			t.Fatal(v.State)
		}
	}
}

func TestAutomaticBALSimulatorRESTWebSocketAndImport(t *testing.T) {
	f := newDetailedHTTPFixture(t)
	ctx := context.Background()
	if err := f.db.ImportLayout(ctx, balIntegrationLayout("simulator", []int{1, 2, 3}), true); err != nil {
		t.Fatal(err)
	}
	runtime := service.NewSignalService(f.db, f.simulator, f.bus, f.occupancy)
	f.control.SetOccupancyProvider(f.occupancy, "simulator")
	f.control.SetSignalService(runtime)
	f.api.SetSignalService(runtime)
	f.api.transfer.SetSignalService(runtime)
	f.control.Start()
	defer f.control.Close()
	if err := runtime.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if err := f.dispatcher.SetTrackPower(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.viewer.StationStatus(ctx); err != nil {
		t.Fatal(err)
	}
	awaitBALREST(t, f.viewer, "S", "S", "S")
	ws := dialTestWebSocket(t, f.server.URL, f.viewer.AccessToken)
	defer ws.close()
	initial := readTestSnapshot(t, ws)
	assertBALSnapshot(t, initial, "S", "S", "S")
	// Drive the existing simulator test API, not the signal runtime directly.
	for _, address := range []int{1, 2, 3} {
		_, err := f.administrator.Do(ctx, http.MethodPut, "/test/v1/simulator/feedback", map[string]any{"source": "simulator", "kind": "block", "address": address, "active": false, "emit": true}, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	signals := awaitBALREST(t, f.viewer, "VL", "VL", "A")
	for _, v := range signals {
		if v.State.CalculationRevision == 0 || v.State.OccupancyRevision == 0 {
			t.Fatal(v.State)
		}
	}
	seen := false
	for !seen {
		var e struct {
			Type     string          `json:"type"`
			Sequence uint64          `json:"sequence"`
			Payload  json.RawMessage `json:"payload"`
		}
		ws.readJSON(t, &e)
		if e.Sequence <= initial.Sequence {
			t.Fatal("stale event", e)
		}
		if e.Type == "signal.aspect.changed" {
			var state model.SignalState
			if err := json.Unmarshal(e.Payload, &state); err != nil {
				t.Fatal(err)
			}
			seen = state.ID == "s1" && state.CommandedAspect != nil && *state.CommandedAspect == "VL" && !state.Pending
		}
	}
	archive, err := f.administrator.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.administrator.ImportLayout(ctx, archive, true); err != nil {
		t.Fatal(err)
	}
	awaitBALREST(t, f.viewer, "S", "S", "S")
	blocks, err := f.viewer.Blocks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range blocks {
		if v.Occupancy.State != model.OccupancyUnknown {
			t.Fatal("import reused detector state", v)
		}
	}
	reconnected := dialTestWebSocket(t, f.server.URL, f.viewer.AccessToken)
	defer reconnected.close()
	assertBALSnapshot(t, readTestSnapshot(t, reconnected), "S", "S", "S")
}

// The UDP fixture speaks the real driver protocol, with no physical central.
func TestAutomaticBALZ21RecoveryWithoutTrainMovement(t *testing.T) {
	for _, powerCut := range []bool{false, true} {
		t.Run(fmt.Sprint("DCC=", powerCut), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			fake := newZ21OccupancyServer(t)
			fake.signalSupport.Store(true)
			driver := z21.New(fake.conn.LocalAddr().String(), station.DefaultOfflineAfter, time.Millisecond)
			defer driver.Close()
			if err := driver.Connect(ctx); err != nil {
				t.Fatal(err)
			}
			db, err := store.Open(filepath.Join(t.TempDir(), "bal.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { cancel(); _ = db.Close() }()
			if err := db.ImportLayout(ctx, balIntegrationLayout("z21-rbus", []int{80, 81, 160}), true); err != nil {
				t.Fatal(err)
			}
			bus := events.New()
			railway := service.NewRailwayService(db, driver, bus)
			if err := railway.OccupancyService().SetTrackPowerAvailable(ctx, true); err != nil {
				t.Fatal(err)
			}
			railway.StartFeedback(ctx)
			control := service.NewControlService(db, driver, bus, clock.Real{}, time.Minute, time.Second, time.Hour)
			control.SetOccupancyProvider(railway.OccupancyService(), "z21-rbus")
			runtime := service.NewSignalService(db, driver, bus, railway.OccupancyService())
			control.SetSignalService(runtime)
			if err := runtime.Start(ctx); err != nil {
				t.Fatal(err)
			}
			defer runtime.Close()
			users := service.NewUserServiceWithPasswordParams(db, clock.Real{}, auth.PasswordParams{Iterations: 100_000, SaltLength: 16, KeyLength: 32})
			if _, err := users.Create(ctx, "dispatcher", "Dispatcher", "correct-horse-1", model.RoleDispatcher, false, false); err != nil {
				t.Fatal(err)
			}
			authSvc := service.NewAuthService(db, users, clock.Real{}, time.Minute, time.Hour)
			api := New(authSvc, control, railway, service.NewRouteService(db, railway, bus), transfer.New(db, bus, clock.Real{}), db, bus, driver, nil, false)
			api.SetSignalService(runtime)
			server := httptest.NewServer(api.Handler())
			defer server.Close()
			c := client.New(server.URL)
			if _, err := c.Login(ctx, "dispatcher", "correct-horse-1", "bal"); err != nil {
				t.Fatal(err)
			}
			if _, err := c.StationStatus(ctx); err != nil {
				t.Fatal(err)
			}
			startup := fake.scanAfter(t, nil)
			awaitBALREST(t, c, "S", "S", "S")
			for group, request := range startup {
				payload := make([]byte, 11)
				payload[0] = byte(group)
				if _, err := fake.conn.WriteToUDP(apiZ21LAN(0x0080, payload), request.from); err != nil {
					t.Fatal(err)
				}
			}
			signals := awaitBALREST(t, c, "VL", "VL", "A")
			assertZ21BALWrites(t, fake, map[int]int{1: 16, 2: 16, 3: 7})
			connected := dialTestWebSocket(t, server.URL, c.AccessToken)
			defer connected.close()
			assertBALSnapshot(t, readTestSnapshot(t, connected), "VL", "VL", "A")
			for _, v := range signals {
				if v.State.ReportQuality != nil && *v.State.ReportQuality == station.AccessoryReportPhysical {
					t.Fatal("invented physical confirmation", v)
				}
			}
			if powerCut {
				if err := c.SetTrackPower(ctx, false); err != nil {
					t.Fatal(err)
				}
				if err := c.SetTrackPower(ctx, true); err != nil {
					t.Fatal(err)
				}
			} else {
				fake.drop.Store(true)
				if _, err := c.StationStatus(ctx); err != nil {
					t.Fatal(err)
				}
				fake.drop.Store(false)
				if _, err := c.StationStatus(ctx); err != nil {
					t.Fatal(err)
				}
			}
			awaitBALREST(t, c, "S", "S", "S")
			assertZ21BALWrites(t, fake, map[int]int{1: 0, 2: 0, 3: 0})
			// Network restoration can open a transport epoch followed by a
			// power-observation epoch. Stop writes require rediscovered firmware,
			// so all initialization datagrams for that final epoch are now read.
			// Drain earlier scans and answer the latest complete endpoint only.
			requests := latestBALScan(t, fake, startup[0].from)
			// Only the first group is restored; c must remain unknown and at stop.
			payload := make([]byte, 11)
			if _, err := fake.conn.WriteToUDP(apiZ21LAN(0x0080, payload), requests[0].from); err != nil {
				t.Fatal(err)
			}
			awaitBALREST(t, c, "A", "S", "S")
			// The train is now on input 81, with no physical-change broadcast.
			payload[0], payload[1] = 1, 1
			if _, err := fake.conn.WriteToUDP(apiZ21LAN(0x0080, payload), requests[1].from); err != nil {
				t.Fatal(err)
			}
			awaitBALREST(t, c, "A", "S", "A")
			connected.writeJSON(t, map[string]any{"type": "client.snapshot_request"})
			found := false
			for i := 0; i < 128; i++ {
				var snapshot systemSnapshot
				connected.readJSON(t, &snapshot)
				if snapshot.Type == "system.snapshot" {
					assertBALSnapshot(t, snapshot, "A", "S", "A")
					found = true
					break
				}
			}
			if !found {
				t.Fatal("existing client did not recover a full signal snapshot")
			}
			blocks, err := c.Blocks(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range blocks {
				if v.ID == "b" && v.Occupancy.State != model.OccupancyOccupied {
					t.Fatal(v)
				}
			}
			ws := dialTestWebSocket(t, server.URL, c.AccessToken)
			defer ws.close()
			assertBALSnapshot(t, readTestSnapshot(t, ws), "A", "S", "A")
		})
	}
}
