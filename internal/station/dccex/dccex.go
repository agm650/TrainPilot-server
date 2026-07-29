package dccex

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math"
	"net"
	"strings"
	"sync"

	"github.com/agm650/TrainPilot-server/internal/station"
)

type Driver struct {
	address  string
	mu       sync.Mutex
	conn     net.Conn
	feedback chan station.FeedbackEvent
	done     chan struct{}
}

func NewTCP(address string) *Driver {
	return &Driver{address: address, feedback: make(chan station.FeedbackEvent, 64), done: make(chan struct{})}
}
func (d *Driver) Connect(ctx context.Context) error {
	c, err := (&net.Dialer{}).DialContext(ctx, "tcp", d.address)
	if err != nil {
		return err
	}
	d.mu.Lock()
	d.conn = c
	d.mu.Unlock()
	go d.readLoop(c)
	return nil
}
func (d *Driver) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	select {
	case <-d.done:
	default:
		close(d.done)
	}
	if d.conn != nil {
		return d.conn.Close()
	}
	return nil
}
func (d *Driver) Capabilities() station.Capabilities {
	return station.Capabilities{Driver: "dccex", TrackPower: true, LocomotiveControl: true, Functions: 68, AccessoryControl: true, Feedback: true}
}
func (d *Driver) send(ctx context.Context, command string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn == nil {
		return fmt.Errorf("DCC-EX is not connected")
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = d.conn.SetWriteDeadline(deadline)
	}
	_, err := io.WriteString(d.conn, command)
	return err
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
	step := int(math.Round(speed * 126))
	dir := 0
	if direction == station.Forward {
		dir = 1
	}
	return d.send(ctx, fmt.Sprintf("<t %d %d %d>\n", address, step, dir))
}
func (d *Driver) SetLocoFunction(ctx context.Context, address, fn int, on bool) error {
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
func (d *Driver) readLoop(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Split(splitFrames)
	for scanner.Scan() {
		frame := strings.TrimSpace(scanner.Text())
		var id int
		var state int
		if _, err := fmt.Sscanf(frame, "<Q %d>", &id); err == nil {
			d.publish(station.FeedbackEvent{Source: "dccex", Kind: "sensor", Address: id, Active: true})
			continue
		}
		if _, err := fmt.Sscanf(frame, "<q %d>", &id); err == nil {
			d.publish(station.FeedbackEvent{Source: "dccex", Kind: "sensor", Address: id, Active: false})
			continue
		}
		if _, err := fmt.Sscanf(frame, "<S %d %d>", &id, &state); err == nil {
			d.publish(station.FeedbackEvent{Source: "dccex", Kind: "sensor", Address: id, Active: state != 0})
		}
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
