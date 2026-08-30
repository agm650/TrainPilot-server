package dccex

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/station"
)

const testReconnectInterval = 10 * time.Millisecond

type fakeTCPServer struct {
	t           *testing.T
	mu          sync.Mutex
	address     string
	listener    net.Listener
	connections map[net.Conn]struct{}
	commands    []string
	accepts     int
	wg          sync.WaitGroup
}

func newFakeTCPServer(t *testing.T) *fakeTCPServer {
	t.Helper()
	s := &fakeTCPServer{t: t, connections: make(map[net.Conn]struct{})}
	s.Start()
	t.Cleanup(s.Stop)
	return s
}

func (s *fakeTCPServer) Start() {
	s.t.Helper()
	s.mu.Lock()
	address := s.address
	alreadyStarted := s.listener != nil
	s.mu.Unlock()
	if alreadyStarted {
		s.t.Fatal("fake DCC-EX server is already started")
	}
	if address == "" {
		address = "127.0.0.1:0"
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		s.t.Fatalf("start fake DCC-EX server: %v", err)
	}
	s.mu.Lock()
	s.listener = listener
	s.address = listener.Addr().String()
	s.mu.Unlock()
	s.wg.Add(1)
	go s.acceptLoop(listener)
}

func (s *fakeTCPServer) Stop() {
	s.mu.Lock()
	listener := s.listener
	s.listener = nil
	connections := make([]net.Conn, 0, len(s.connections))
	for connection := range s.connections {
		connections = append(connections, connection)
	}
	s.mu.Unlock()
	if listener != nil {
		_ = listener.Close()
	}
	for _, connection := range connections {
		if tcp, ok := connection.(*net.TCPConn); ok {
			_ = tcp.SetLinger(0)
		}
		_ = connection.Close()
	}
	s.wg.Wait()
}

func (s *fakeTCPServer) acceptLoop(listener net.Listener) {
	defer s.wg.Done()
	for {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.listener != listener {
			s.mu.Unlock()
			_ = connection.Close()
			continue
		}
		s.connections[connection] = struct{}{}
		s.accepts++
		s.wg.Add(1)
		s.mu.Unlock()
		go s.readCommands(connection)
	}
}

func (s *fakeTCPServer) readCommands(connection net.Conn) {
	defer s.wg.Done()
	defer func() {
		s.mu.Lock()
		delete(s.connections, connection)
		s.mu.Unlock()
		_ = connection.Close()
	}()
	scanner := bufio.NewScanner(connection)
	for scanner.Scan() {
		command := strings.TrimSpace(scanner.Text())
		if command == "" {
			continue
		}
		s.mu.Lock()
		s.commands = append(s.commands, command)
		s.mu.Unlock()
	}
}

func (s *fakeTCPServer) SendFrame(frame string) {
	s.t.Helper()
	s.mu.Lock()
	connections := make([]net.Conn, 0, len(s.connections))
	for connection := range s.connections {
		connections = append(connections, connection)
	}
	s.mu.Unlock()
	if len(connections) != 1 {
		s.t.Fatalf("active fake-server connections=%d want 1", len(connections))
	}
	if _, err := connections[0].Write([]byte(frame + "\n")); err != nil {
		s.t.Fatalf("send frame %q: %v", frame, err)
	}
}

func (s *fakeTCPServer) Commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.commands...)
}

func (s *fakeTCPServer) ActiveConnections() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.connections)
}

func (s *fakeTCPServer) AcceptCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.accepts
}

func eventually(t *testing.T, timeout time.Duration, description string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !condition() {
		t.Fatalf("timeout waiting for %s", description)
	}
}

func newConnectedDriver(t *testing.T, server *fakeTCPServer, offlineAfter time.Duration) *Driver {
	t.Helper()
	driver := NewTCP(server.address, offlineAfter)
	driver.reconnectInterval = testReconnectInterval
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := driver.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = driver.Close() })
	eventually(t, time.Second, "initial DCC-EX connection", func() bool { return server.ActiveConnections() == 1 })
	return driver
}

func waitForConnectivity(t *testing.T, driver *Driver, connectivity station.Connectivity, timeout time.Duration) {
	t.Helper()
	eventually(t, timeout, "DCC-EX connectivity "+string(connectivity), func() bool {
		return driver.Health().Connectivity == connectivity
	})
}

func waitForFeedback(t *testing.T, feedback <-chan station.FeedbackEvent, timeout time.Duration) station.FeedbackEvent {
	t.Helper()
	select {
	case event := <-feedback:
		return event
	case <-time.After(timeout):
		t.Fatal("timeout waiting for DCC-EX feedback")
		return station.FeedbackEvent{}
	}
}

func waitForStatusConnectivity(t *testing.T, statuses <-chan station.Status, connectivity station.Connectivity, timeout time.Duration) station.Status {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case status := <-statuses:
			if status.Connectivity == connectivity {
				return status
			}
		case <-timer.C:
			t.Fatalf("timeout waiting for DCC-EX status event %s", connectivity)
			return station.Status{}
		}
	}
}

func TestCapabilitiesAndCommandBounds(t *testing.T) {
	d := NewTCP("unused", station.DefaultOfflineAfter)
	t.Cleanup(func() { _ = d.Close() })
	caps := d.Capabilities()
	if caps.Functions != 69 || caps.MaxFunctionNumber != 68 {
		t.Fatalf("capabilities=%+v", caps)
	}
	if err := d.SetLocoSpeed(context.Background(), 3, 0.5, station.Direction("sideways")); err != station.ErrUnsupported {
		t.Fatalf("invalid direction error=%v", err)
	}
	if err := d.SetLocoFunction(context.Background(), 3, 69, true); err != station.ErrUnsupported {
		t.Fatalf("out-of-range function error=%v", err)
	}
}

func TestInitialConnectionReportsOnline(t *testing.T) {
	server := newFakeTCPServer(t)
	driver := newConnectedDriver(t, server, 100*time.Millisecond)
	health := driver.Health()
	if health.Connectivity != station.Online || health.LastSeen == nil {
		t.Fatalf("health after connect=%+v", health)
	}
	status := waitForStatusConnectivity(t, driver.StatusEvents(), station.Online, time.Second)
	if status.LastSeen == nil {
		t.Fatalf("online status event=%+v", status)
	}
}

func TestInitialConnectionFailureDoesNotReconnect(t *testing.T) {
	driver := NewTCP("unused", 100*time.Millisecond)
	driver.reconnectInterval = testReconnectInterval
	var attempts atomic.Int64
	driver.dial = func(context.Context, string) (net.Conn, error) {
		attempts.Add(1)
		return nil, errors.New("initial dial failed")
	}
	t.Cleanup(func() { _ = driver.Close() })
	if err := driver.Connect(context.Background()); err == nil {
		t.Fatal("expected initial connection error")
	}
	time.Sleep(3 * testReconnectInterval)
	if got := attempts.Load(); got != 1 {
		t.Fatalf("initial failure started reconnection attempts=%d", got)
	}
}

func TestConnectionLossBecomesDegradedThenOffline(t *testing.T) {
	server := newFakeTCPServer(t)
	driver := newConnectedDriver(t, server, 150*time.Millisecond)
	server.Stop()
	waitForConnectivity(t, driver, station.Degraded, 100*time.Millisecond)
	waitForConnectivity(t, driver, station.Offline, 500*time.Millisecond)
}

func TestReconnectBeforeOffline(t *testing.T) {
	server := newFakeTCPServer(t)
	driver := newConnectedDriver(t, server, 500*time.Millisecond)
	server.Stop()
	waitForConnectivity(t, driver, station.Degraded, 100*time.Millisecond)
	server.Start()
	waitForConnectivity(t, driver, station.Online, 500*time.Millisecond)
	eventually(t, time.Second, "one active reconnected socket", func() bool { return server.ActiveConnections() == 1 })
}

func TestReconnectAfterOffline(t *testing.T) {
	server := newFakeTCPServer(t)
	driver := newConnectedDriver(t, server, 60*time.Millisecond)
	statuses := driver.StatusEvents()
	waitForStatusConnectivity(t, statuses, station.Online, time.Second)
	server.Stop()
	waitForConnectivity(t, driver, station.Offline, 500*time.Millisecond)
	waitForStatusConnectivity(t, statuses, station.Degraded, time.Second)
	waitForStatusConnectivity(t, statuses, station.Offline, time.Second)
	server.Start()
	waitForConnectivity(t, driver, station.Online, 500*time.Millisecond)
	waitForStatusConnectivity(t, statuses, station.Online, time.Second)
}

func TestCommandDuringOutageIsRejectedAndNotReplayed(t *testing.T) {
	server := newFakeTCPServer(t)
	driver := newConnectedDriver(t, server, 500*time.Millisecond)
	server.Stop()
	eventually(t, 200*time.Millisecond, "driver socket removal", func() bool {
		driver.mu.Lock()
		defer driver.mu.Unlock()
		return driver.conn == nil
	})
	if err := driver.SetLocoSpeed(context.Background(), 42, 0.5, station.Forward); !errors.Is(err, station.ErrOffline) {
		t.Fatalf("outage command error=%v want station.ErrOffline", err)
	}
	if got := server.Commands(); len(got) != 0 {
		t.Fatalf("commands received during outage=%v", got)
	}

	server.Start()
	waitForConnectivity(t, driver, station.Online, 500*time.Millisecond)
	eventually(t, time.Second, "reconnected socket", func() bool { return server.ActiveConnections() == 1 })
	time.Sleep(3 * testReconnectInterval)
	if got := server.Commands(); len(got) != 0 {
		t.Fatalf("outage command was replayed after reconnect: %v", got)
	}
	if err := driver.SetLocoSpeed(context.Background(), 42, 0.25, station.Forward); err != nil {
		t.Fatal(err)
	}
	eventually(t, time.Second, "new throttle after reconnect", func() bool { return len(server.Commands()) == 1 })
	if got := server.Commands(); len(got) != 1 || got[0] != "<t 42 32 1>" {
		t.Fatalf("commands after reconnect=%v", got)
	}
}

func TestFeedbackContinuesOnOriginalChannelAfterReconnect(t *testing.T) {
	server := newFakeTCPServer(t)
	driver := newConnectedDriver(t, server, 500*time.Millisecond)
	feedback := driver.Feedback()
	initialLastSeen := driver.Health().LastSeen
	time.Sleep(2 * time.Millisecond)
	server.SendFrame("<Q 7>")
	first := waitForFeedback(t, feedback, time.Second)
	if first.Address != 7 || !first.Active {
		t.Fatalf("first feedback=%+v", first)
	}
	updatedLastSeen := driver.Health().LastSeen
	if initialLastSeen == nil || updatedLastSeen == nil || !updatedLastSeen.After(*initialLastSeen) {
		t.Fatalf("lastSeen was not updated by a valid frame: before=%v after=%v", initialLastSeen, updatedLastSeen)
	}

	server.Stop()
	waitForConnectivity(t, driver, station.Degraded, 100*time.Millisecond)
	server.Start()
	waitForConnectivity(t, driver, station.Online, 500*time.Millisecond)
	eventually(t, time.Second, "feedback reconnection", func() bool { return server.ActiveConnections() == 1 })
	server.SendFrame("<q 7>")
	second := waitForFeedback(t, feedback, time.Second)
	if second.Address != 7 || second.Active {
		t.Fatalf("second feedback=%+v", second)
	}
}

func TestMultipleReconnectCyclesDoNotDuplicateTraffic(t *testing.T) {
	server := newFakeTCPServer(t)
	driver := newConnectedDriver(t, server, 500*time.Millisecond)
	feedback := driver.Feedback()

	for cycle := 1; cycle <= 3; cycle++ {
		server.Stop()
		waitForConnectivity(t, driver, station.Degraded, 100*time.Millisecond)
		server.Start()
		waitForConnectivity(t, driver, station.Online, 500*time.Millisecond)
		eventually(t, time.Second, "single active socket", func() bool { return server.ActiveConnections() == 1 })

		if err := driver.SetLocoSpeed(context.Background(), 40+cycle, 0.25, station.Forward); err != nil {
			t.Fatal(err)
		}
		eventually(t, time.Second, "one command per reconnect cycle", func() bool { return len(server.Commands()) == cycle })
		server.SendFrame(fmt.Sprintf("<Q %d>", cycle))
		event := waitForFeedback(t, feedback, time.Second)
		if event.Address != cycle || !event.Active {
			t.Fatalf("cycle %d feedback=%+v", cycle, event)
		}
	}
	time.Sleep(3 * testReconnectInterval)
	if got := server.Commands(); len(got) != 3 {
		t.Fatalf("duplicated commands=%v", got)
	}
	select {
	case event := <-feedback:
		t.Fatalf("duplicated feedback=%+v", event)
	default:
	}
	if accepts := server.AcceptCount(); accepts != 4 {
		t.Fatalf("accepted connections=%d want 4", accepts)
	}
}

func TestCloseDuringReconnectStopsFurtherDials(t *testing.T) {
	server := newFakeTCPServer(t)
	driver := NewTCP(server.address, 500*time.Millisecond)
	driver.reconnectInterval = testReconnectInterval
	baseDial := driver.dial
	var attempts atomic.Int64
	driver.dial = func(ctx context.Context, address string) (net.Conn, error) {
		attempts.Add(1)
		return baseDial(ctx, address)
	}
	if err := driver.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	server.Stop()
	eventually(t, 500*time.Millisecond, "reconnection dial", func() bool { return attempts.Load() >= 2 })
	if err := driver.Close(); err != nil {
		t.Fatal(err)
	}
	if err := driver.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	afterClose := attempts.Load()
	if err := driver.Connect(context.Background()); !errors.Is(err, station.ErrOffline) {
		t.Fatalf("Connect after Close error=%v want station.ErrOffline", err)
	}
	if got := attempts.Load(); got != afterClose {
		t.Fatalf("Connect dialed after Close: before=%d after=%d", afterClose, got)
	}
	server.Start()
	time.Sleep(5 * testReconnectInterval)
	if got := attempts.Load(); got != afterClose {
		t.Fatalf("dial attempts continued after Close: before=%d after=%d", afterClose, got)
	}
	if active := server.ActiveConnections(); active != 0 {
		t.Fatalf("connections after Close=%d", active)
	}
}

type failingWriteConn struct {
	net.Conn
	err error
}

func (c *failingWriteConn) Write([]byte) (int, error) { return 0, c.err }

func TestWriteErrorInvalidatesConnectionAndStartsReconnect(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	forcedErr := errors.New("forced write failure")
	driver := NewTCP("unused", 500*time.Millisecond)
	driver.reconnectInterval = testReconnectInterval
	var attempts atomic.Int64
	driver.dial = func(context.Context, string) (net.Conn, error) {
		if attempts.Add(1) == 1 {
			return &failingWriteConn{Conn: clientConn, err: forcedErr}, nil
		}
		return nil, errors.New("station unavailable")
	}
	if err := driver.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = driver.Close()
		_ = serverConn.Close()
	})

	err := driver.SetLocoSpeed(context.Background(), 42, 0.5, station.Forward)
	if !errors.Is(err, station.ErrOffline) || !strings.Contains(err.Error(), forcedErr.Error()) {
		t.Fatalf("write error=%v", err)
	}
	waitForConnectivity(t, driver, station.Degraded, 100*time.Millisecond)
	eventually(t, 500*time.Millisecond, "reconnect after write error", func() bool { return attempts.Load() >= 2 })
	driver.mu.Lock()
	connection := driver.conn
	driver.mu.Unlock()
	if connection != nil {
		t.Fatal("failed write connection was not invalidated")
	}
}

func TestThrottleCommand(t *testing.T) {
	server := newFakeTCPServer(t)
	driver := newConnectedDriver(t, server, station.DefaultOfflineAfter)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := driver.SetLocoSpeed(ctx, 42, 0.5, station.Forward); err != nil {
		t.Fatal(err)
	}
	eventually(t, time.Second, "throttle command", func() bool { return len(server.Commands()) == 1 })
	if got := server.Commands(); len(got) != 1 || got[0] != "<t 42 63 1>" {
		t.Fatalf("commands=%v", got)
	}
}
