package z21

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"sync"

	"github.com/agm650/TrainPilot-server/internal/station"
)

type Driver struct {
	address  string
	mu       sync.Mutex
	conn     *net.UDPConn
	feedback chan station.FeedbackEvent
}

func New(address string) *Driver {
	return &Driver{address: address, feedback: make(chan station.FeedbackEvent, 64)}
}
func (d *Driver) Connect(ctx context.Context) error {
	remote, err := net.ResolveUDPAddr("udp", d.address)
	if err != nil {
		return err
	}
	c, err := net.DialUDP("udp", nil, remote)
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
	if d.conn != nil {
		return d.conn.Close()
	}
	return nil
}
func (d *Driver) Capabilities() station.Capabilities {
	return station.Capabilities{Driver: "z21", TrackPower: true, LocomotiveControl: true, Functions: 29, AccessoryControl: false, Feedback: true}
}
func xor(data []byte) byte {
	var out byte
	for _, b := range data {
		out ^= b
	}
	return out
}
func xbus(payload ...byte) []byte {
	record := make([]byte, 4+len(payload)+1)
	binary.LittleEndian.PutUint16(record[0:2], uint16(len(record)))
	binary.LittleEndian.PutUint16(record[2:4], 0x0040)
	copy(record[4:], payload)
	record[len(record)-1] = xor(payload)
	return record
}
func (d *Driver) send(ctx context.Context, b []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn == nil {
		return fmt.Errorf("Z21 is not connected")
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = d.conn.SetWriteDeadline(deadline)
	}
	_, err := d.conn.Write(b)
	return err
}
func (d *Driver) SetTrackPower(ctx context.Context, on bool) error {
	value := byte(0x80)
	if on {
		value = 0x81
	}
	return d.send(ctx, xbus(0x21, value))
}
func (d *Driver) EmergencyStop(ctx context.Context) error { return d.send(ctx, xbus(0x80)) }
func (d *Driver) SetLocoSpeed(ctx context.Context, address int, speed float64, direction station.Direction) error {
	if speed < 0 || speed > 1 {
		return fmt.Errorf("speed out of range")
	}
	raw := byte(0)
	if speed > 0 {
		raw = byte(math.Round(speed*125)) + 1
	}
	if direction == station.Forward {
		raw |= 0x80
	}
	msb := byte((address >> 8) & 0x3f)
	lsb := byte(address)
	return d.send(ctx, xbus(0xE4, 0x13, msb, lsb, raw))
}
func (d *Driver) SetLocoFunction(ctx context.Context, address, fn int, on bool) error {
	if fn < 0 || fn > 28 {
		return station.ErrUnsupported
	}
	mode := byte(0)
	if on {
		mode = 0x40
	}
	value := mode | byte(fn&0x3f)
	return d.send(ctx, xbus(0xE4, 0xF8, byte((address>>8)&0x3f), byte(address), value))
}
func (d *Driver) SetAccessory(context.Context, int, string) error { return station.ErrUnsupported }
func (d *Driver) Feedback() <-chan station.FeedbackEvent          { return d.feedback }
func (d *Driver) readLoop(c *net.UDPConn) {
	buf := make([]byte, 2048)
	for {
		n, err := c.Read(buf)
		if err != nil {
			return
		}
		d.parse(buf[:n])
	}
}
func (d *Driver) parse(data []byte) {
	for len(data) >= 4 {
		length := int(binary.LittleEndian.Uint16(data[:2]))
		if length < 4 || length > len(data) {
			return
		}
		header := binary.LittleEndian.Uint16(data[2:4])
		payload := data[4:length]
		if header == 0x0080 && len(payload) >= 11 {
			group := int(payload[0])
			for i := 1; i < len(payload); i++ {
				bits := payload[i]
				for bit := 0; bit < 8; bit++ {
					addr := group*80 + (i-1)*8 + bit + 1
					d.publish(station.FeedbackEvent{Source: "z21-rbus", Kind: "occupancy", Address: addr, Active: bits&(1<<bit) != 0})
				}
			}
		}
		data = data[length:]
	}
}
func (d *Driver) publish(e station.FeedbackEvent) {
	select {
	case d.feedback <- e:
	default:
	}
}
