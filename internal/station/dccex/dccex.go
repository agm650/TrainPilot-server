package dccex

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/agm650/TrainPilot-server/internal/station"
)

const (
	defaultReconnectInterval = time.Second
	defaultDialTimeout       = 2 * time.Second
)

type dialFunc func(context.Context, string) (net.Conn, error)

type Driver struct {
	address           string
	mu                sync.Mutex
	writeMu           sync.Mutex
	conn              net.Conn
	connectionID      uint64
	reconnecting      bool
	closed            bool
	dial              dialFunc
	reconnectInterval time.Duration
	feedback          chan station.FeedbackEvent
	statusEvents      chan station.Status
	health            station.HealthTracker
	runCtx            context.Context
	cancel            context.CancelFunc
	closeOnce         sync.Once
	wg                sync.WaitGroup
}

var _ station.HealthProvider = (*Driver)(nil)
var _ station.StatusEventProvider = (*Driver)(nil)

func NewTCP(address string, offlineAfter time.Duration) *Driver {
	runCtx, cancel := context.WithCancel(context.Background())
	dialer := &net.Dialer{Timeout: defaultDialTimeout}
	return &Driver{
		address: address,
		dial: func(ctx context.Context, address string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp", address)
		},
		reconnectInterval: defaultReconnectInterval,
		feedback:          make(chan station.FeedbackEvent, 64),
		statusEvents:      make(chan station.Status, 16),
		health:            station.NewHealthTracker(offlineAfter),
		runCtx:            runCtx,
		cancel:            cancel,
	}
}
func (d *Driver) Connect(ctx context.Context) error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return station.ErrOffline
	}
	if d.conn != nil {
		d.mu.Unlock()
		return errors.New("DCC-EX driver is already connected")
	}
	d.mu.Unlock()

	c, err := d.dial(ctx, d.address)
	if err != nil {
		return err
	}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		_ = c.Close()
		return station.ErrOffline
	}
	if d.conn != nil {
		d.mu.Unlock()
		_ = c.Close()
		return errors.New("DCC-EX driver is already connected")
	}
	d.conn = c
	d.connectionID++
	connectionID := d.connectionID
	d.health.Connected()
	d.health.ValidResponse()
	d.wg.Add(1)
	d.mu.Unlock()
	d.publishHealthStatus()
	go d.readLoop(c, connectionID)
	return nil
}
func (d *Driver) Close() error {
	var closeErr error
	d.closeOnce.Do(func() {
		d.mu.Lock()
		d.closed = true
		c := d.conn
		d.conn = nil
		d.mu.Unlock()
		d.cancel()
		if c != nil {
			closeErr = c.Close()
		}
		d.wg.Wait()
	})
	return closeErr
}
func (d *Driver) Health() station.Health { return d.health.Health() }
func (d *Driver) Capabilities() station.Capabilities {
	return station.Capabilities{Driver: "dccex", TrackPower: true, LocomotiveControl: true, Functions: 69, MaxFunctionNumber: 68, AccessoryControl: true, Feedback: true}
}
func (d *Driver) send(ctx context.Context, command string) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	d.mu.Lock()
	c := d.conn
	connectionID := d.connectionID
	d.mu.Unlock()
	if c == nil {
		return station.ErrOffline
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.SetWriteDeadline(deadline)
	} else {
		_ = c.SetWriteDeadline(time.Time{})
	}
	if _, err := io.WriteString(c, command); err != nil {
		d.connectionLost(c, connectionID)
		return fmt.Errorf("%w: DCC-EX write failed: %v", station.ErrOffline, err)
	}
	return nil
}
func (d *Driver) SetTrackPower(ctx context.Context, on bool) error {
	if on {
		return d.send(ctx, "<1>\n")
	}
	return d.send(ctx, "<0>\n")
}
func (d *Driver) EmergencyStop(ctx context.Context) error { return d.send(ctx, "<!>\n") }
func (d *Driver) SetLocoSpeed(ctx context.Context, address int, speed float64, direction station.Direction) error {
	if speed < 0 || speed > 1 {
		return fmt.Errorf("speed out of range")
	}
	if !direction.Valid() {
		return station.ErrUnsupported
	}
	step := int(math.Round(speed * 126))
	dir := 0
	if direction == station.Forward {
		dir = 1
	}
	return d.send(ctx, fmt.Sprintf("<t %d %d %d>\n", address, step, dir))
}
func (d *Driver) SetLocoFunction(ctx context.Context, address, fn int, on bool) error {
	if fn < 0 || fn > d.Capabilities().MaxFunctionNumber {
		return station.ErrUnsupported
	}
	state := 0
	if on {
		state = 1
	}
	return d.send(ctx, fmt.Sprintf("<F %d %d %d>\n", address, fn, state))
}
func (d *Driver) SetAccessory(ctx context.Context, address int, state string) error {
	activate := 0
	if state == "diverging" || state == "thrown" || state == "on" {
		activate = 1
	}
	return d.send(ctx, fmt.Sprintf("<a %d 0 %d>\n", address, activate))
}
func (d *Driver) Feedback() <-chan station.FeedbackEvent { return d.feedback }
func (d *Driver) StatusEvents() <-chan station.Status    { return d.statusEvents }
func (d *Driver) readLoop(c net.Conn, connectionID uint64) {
	defer d.wg.Done()
	scanner := bufio.NewScanner(c)
	scanner.Split(splitFrames)
	for scanner.Scan() {
		frame := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(frame, "<") || !strings.HasSuffix(frame, ">") {
			continue
		}
		if !d.recordValidResponse(connectionID) {
			return
		}
		d.handleFrame(frame)
	}
	d.connectionLost(c, connectionID)
}

func (d *Driver) handleFrame(frame string) {
	var id int
	var state int
	if _, err := fmt.Sscanf(frame, "<Q %d>", &id); err == nil {
		d.publish(station.FeedbackEvent{Source: "dccex", Kind: "sensor", Address: id, Active: true})
		return
	}
	if _, err := fmt.Sscanf(frame, "<q %d>", &id); err == nil {
		d.publish(station.FeedbackEvent{Source: "dccex", Kind: "sensor", Address: id, Active: false})
		return
	}
	if _, err := fmt.Sscanf(frame, "<S %d %d>", &id, &state); err == nil {
		d.publish(station.FeedbackEvent{Source: "dccex", Kind: "sensor", Address: id, Active: state != 0})
	}
}

func (d *Driver) recordValidResponse(connectionID uint64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.conn == nil || d.connectionID != connectionID {
		return false
	}
	d.health.ValidResponse()
	return true
}

func (d *Driver) connectionLost(c net.Conn, connectionID uint64) {
	d.mu.Lock()
	if d.conn == nil || d.connectionID != connectionID {
		d.mu.Unlock()
		_ = c.Close()
		return
	}
	d.conn = nil
	d.health.CommunicationError()
	startReconnect := !d.closed && !d.reconnecting
	if startReconnect {
		d.reconnecting = true
		d.wg.Add(1)
	}
	d.mu.Unlock()
	_ = c.Close()
	d.publishHealthStatus()
	if startReconnect {
		go d.reconnectLoop()
	}
}

func (d *Driver) reconnectLoop() {
	defer d.wg.Done()
	connected := false
	defer func() {
		if !connected {
			d.mu.Lock()
			d.reconnecting = false
			d.mu.Unlock()
		}
	}()

	for {
		c, err := d.dial(d.runCtx, d.address)
		if err == nil {
			d.mu.Lock()
			if d.closed || d.runCtx.Err() != nil {
				d.mu.Unlock()
				_ = c.Close()
				return
			}
			d.conn = c
			d.connectionID++
			connectionID := d.connectionID
			d.reconnecting = false
			connected = true
			d.health.ValidResponse()
			d.wg.Add(1)
			d.mu.Unlock()
			d.publishHealthStatus()
			go d.readLoop(c, connectionID)
			return
		}
		if d.runCtx.Err() != nil {
			return
		}
		d.health.CommunicationError()
		d.publishHealthStatus()

		interval := d.reconnectInterval
		if interval <= 0 {
			interval = defaultReconnectInterval
		}
		timer := time.NewTimer(interval)
		select {
		case <-d.runCtx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
			d.publishHealthStatus()
		}
	}
}

func (d *Driver) publishHealthStatus() {
	health := d.health.Health()
	status := station.Status{Connectivity: health.Connectivity, LastSeen: health.LastSeen, TrackPower: "unknown"}
	select {
	case d.statusEvents <- status:
	default:
	}
}
func (d *Driver) publish(e station.FeedbackEvent) {
	select {
	case d.feedback <- e:
	default:
	}
}
func splitFrames(data []byte, atEOF bool) (advance int, token []byte, err error) {
	start := strings.IndexByte(string(data), '<')
	if start < 0 {
		if atEOF {
			return len(data), nil, nil
		}
		return 0, nil, nil
	}
	end := strings.IndexByte(string(data[start:]), '>')
	if end < 0 {
		if atEOF {
			return len(data), data[start:], nil
		}
		return 0, nil, nil
	}
	end += start
	return end + 1, data[start : end+1], nil
}
