package z21

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"sync"
	"time"

	"github.com/agm650/TrainPilot-server/internal/station"
)

type Driver struct {
	address            string
	mu                 sync.Mutex
	conn               *net.UDPConn
	feedback           chan station.FeedbackEvent
	statusEvents       chan station.Status
	statusMu           sync.Mutex
	xStatusWaiters     []chan byte
	systemStateWaiters []chan systemState
	health             station.HealthTracker
	lastStatus         *station.Status
	done               chan struct{}
	closeOnce          sync.Once
}

type systemState struct {
	mainCurrent, programmingCurrent, filteredMainCurrent, temperature int16
	supplyVoltage, trackVoltage                                       uint16
	centralState, centralStateEx, capabilities                        byte
}

func New(address string) *Driver {
	return &Driver{
		address:      address,
		feedback:     make(chan station.FeedbackEvent, 64),
		statusEvents: make(chan station.Status, 16),
		done:         make(chan struct{}),
	}
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
	d.health.Connected()
	go d.readLoop(c)
	go d.statusLoop()
	return nil
}
func (d *Driver) Close() error {
	d.closeOnce.Do(func() { close(d.done) })
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn != nil {
		return d.conn.Close()
	}
	return nil
}
func (d *Driver) Health() station.Health { return d.health.Health() }
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
func lan(header uint16) []byte {
	record := make([]byte, 4)
	binary.LittleEndian.PutUint16(record[0:2], uint16(len(record)))
	binary.LittleEndian.PutUint16(record[2:4], header)
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
	if err != nil {
		d.health.CommunicationError()
	}
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
func (d *Driver) Status(ctx context.Context) (station.Status, error) {
	xReply := make(chan byte, 1)
	systemReply := make(chan systemState, 1)
	d.statusMu.Lock()
	d.xStatusWaiters = append(d.xStatusWaiters, xReply)
	d.systemStateWaiters = append(d.systemStateWaiters, systemReply)
	d.statusMu.Unlock()
	defer d.removeStatusWaiters(xReply, systemReply)
	if err := d.send(ctx, xbus(0x21, 0x24)); err != nil {
		d.health.CommunicationError()
		return d.currentStatus(), nil
	}
	if err := d.send(ctx, lan(0x0085)); err != nil {
		d.health.CommunicationError()
		return d.currentStatus(), nil
	}
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for xReply != nil || systemReply != nil {
		select {
		case <-xReply:
			xReply = nil
		case <-systemReply:
			systemReply = nil
		case <-ctx.Done():
			return d.currentStatus(), ctx.Err()
		case <-timer.C:
			d.health.CommunicationError()
			return d.currentStatus(), nil
		}
	}
	// The system-state reply is richer and authoritative. LAN_X_GET_STATUS is
	// still queried to support and verify both protocol paths.
	return d.currentStatus(), nil
}
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
func (d *Driver) StatusEvents() <-chan station.Status             { return d.statusEvents }
func (d *Driver) readLoop(c *net.UDPConn) {
	buf := make([]byte, 2048)
	for {
		n, err := c.Read(buf)
		if err != nil {
			d.health.CommunicationError()
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
		valid := header != 0x0040 || (len(payload) > 0 && xor(payload) == 0)
		if !valid {
			data = data[length:]
			continue
		}
		d.health.ValidResponse()
		switch {
		case header == 0x0040 && len(payload) == 4 && payload[0] == 0x62 && payload[1] == 0x22 && xor(payload[:3]) == payload[3]:
			d.publishXStatus(payload[2])
		case header == 0x0084 && len(payload) == 16:
			d.publishSystemState(parseSystemState(payload))
		case header == 0x0080 && len(payload) >= 11:
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

func parseSystemState(payload []byte) systemState {
	return systemState{
		mainCurrent:         int16(binary.LittleEndian.Uint16(payload[0:2])),
		programmingCurrent:  int16(binary.LittleEndian.Uint16(payload[2:4])),
		filteredMainCurrent: int16(binary.LittleEndian.Uint16(payload[4:6])),
		temperature:         int16(binary.LittleEndian.Uint16(payload[6:8])),
		supplyVoltage:       binary.LittleEndian.Uint16(payload[8:10]),
		trackVoltage:        binary.LittleEndian.Uint16(payload[10:12]),
		centralState:        payload[12], centralStateEx: payload[13], capabilities: payload[15],
	}
}

func statusFromSystemState(s systemState) station.Status {
	power := "on"
	if s.centralState&0x02 != 0 {
		power = "off"
	}
	return station.Status{
		TrackPower: power, EmergencyStop: s.centralState&0x01 != 0,
		ShortCircuit: s.centralState&0x04 != 0, ProgrammingMode: s.centralState&0x20 != 0,
		MainCurrentMilliAmps: s.mainCurrent, ProgrammingCurrentMilliAmps: s.programmingCurrent,
		FilteredMainCurrentMilliAmps: s.filteredMainCurrent, TemperatureCelsius: s.temperature,
		SupplyVoltageMilliVolts: s.supplyVoltage, TrackVoltageMilliVolts: s.trackVoltage,
		HighTemperature: s.centralStateEx&0x01 != 0, PowerLost: s.centralStateEx&0x02 != 0,
		ExternalShortCircuit: s.centralStateEx&0x04 != 0, InternalShortCircuit: s.centralStateEx&0x08 != 0,
	}
}

func (d *Driver) publishXStatus(status byte) {
	d.statusMu.Lock()
	waiters := d.xStatusWaiters
	d.xStatusWaiters = nil
	d.statusMu.Unlock()
	for _, ch := range waiters {
		ch <- status
	}
}
func (d *Driver) publishSystemState(status systemState) {
	d.statusMu.Lock()
	parsed := statusFromSystemState(status)
	d.lastStatus = &parsed
	waiters := d.systemStateWaiters
	d.systemStateWaiters = nil
	d.statusMu.Unlock()
	for _, ch := range waiters {
		ch <- status
	}
}

func (d *Driver) currentStatus() station.Status {
	d.statusMu.Lock()
	var status station.Status
	if d.lastStatus != nil {
		status = *d.lastStatus
	} else {
		status.TrackPower = "unknown"
	}
	d.statusMu.Unlock()
	health := d.health.Health()
	status.Connectivity = health.Connectivity
	status.LastSeen = health.LastSeen
	return status
}
func (d *Driver) publishStatusEvent(status station.Status) {
	select {
	case d.statusEvents <- status:
	default:
	}
}
func (d *Driver) statusLoop() {
	status, _ := d.Status(context.Background())
	d.publishStatusEvent(status)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			status, _ := d.Status(context.Background())
			d.publishStatusEvent(status)
		case <-d.done:
			return
		}
	}
}
func (d *Driver) removeStatusWaiters(x chan byte, system chan systemState) {
	d.statusMu.Lock()
	defer d.statusMu.Unlock()
	for i, ch := range d.xStatusWaiters {
		if ch == x {
			d.xStatusWaiters = append(d.xStatusWaiters[:i], d.xStatusWaiters[i+1:]...)
			break
		}
	}
	for i, ch := range d.systemStateWaiters {
		if ch == system {
			d.systemStateWaiters = append(d.systemStateWaiters[:i], d.systemStateWaiters[i+1:]...)
			break
		}
	}
}
func (d *Driver) publish(e station.FeedbackEvent) {
	select {
	case d.feedback <- e:
	default:
	}
}
