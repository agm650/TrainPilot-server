package z21

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net"
	"sync"
	"time"

	"github.com/agm650/TrainPilot-server/internal/station"
)

const (
	DefaultAccessoryPulse = 100 * time.Millisecond

	accessoryDeactivationTimeout = time.Second
	accessoryInfoTimeout         = 2 * time.Second
	lanSetBroadcastFlags         = 0x0050
	lanXHeader                   = 0x0040
	lanXSetTurnout               = 0x53
	lanXGetTurnoutInfo           = 0x43
	lanRMBusDataChanged          = 0x0080
	lanRMBusGetData              = 0x0081
	rbusGroupCount               = 2
	rbusInputsPerGroup           = 80

	// The z21 broadcast mask keeps the existing R-BUS feedback stream and adds
	// driving/switching broadcasts, including LAN_X_TURNOUT_INFO.
	broadcastFlagDrivingSwitching uint32 = 0x00000001
	broadcastFlagRBus             uint32 = 0x00000002
	broadcastFlags                       = broadcastFlagDrivingSwitching | broadcastFlagRBus
)

type Driver struct {
	address            string
	mu                 sync.Mutex
	connectMu          sync.Mutex
	conn               *net.UDPConn
	retiredConnection  *net.UDPConn
	started            bool
	closed             bool
	feedbackGeneration uint64
	feedback           chan station.FeedbackEvent
	accessoryEvents    chan station.AccessoryStateEvent
	accessoryPulse     time.Duration
	accessoryMu        sync.Mutex
	accessoryWaiters   map[uint16][]chan station.AccessoryStateEvent
	statusEvents       chan station.Status
	statusMu           sync.Mutex
	rbusReceived       [rbusGroupCount]bool
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

var _ station.CommandStation = (*Driver)(nil)
var _ station.AccessoryStateEventProvider = (*Driver)(nil)

func New(address string, offlineAfter, accessoryPulse time.Duration) *Driver {
	if accessoryPulse <= 0 {
		accessoryPulse = DefaultAccessoryPulse
	}
	return &Driver{
		address:            address,
		feedbackGeneration: 1,
		// Connect runs before the service consumes feedback. Keep a complete
		// initial R-BUS snapshot, including the last inputs of both groups.
		feedback:         make(chan station.FeedbackEvent, 64),
		accessoryEvents:  make(chan station.AccessoryStateEvent, 64),
		accessoryPulse:   accessoryPulse,
		accessoryWaiters: make(map[uint16][]chan station.AccessoryStateEvent),
		statusEvents:     make(chan station.Status, 16),
		health:           station.NewHealthTracker(offlineAfter),
		done:             make(chan struct{}),
	}
}
func (d *Driver) Connect(ctx context.Context) error {
	d.mu.Lock()
	if d.started || d.closed {
		d.mu.Unlock()
		return errors.New("z21 driver is already started or closed")
	}
	d.started = true
	d.mu.Unlock()
	d.health.Connected()
	if err := d.ensureConnection(ctx); err != nil {
		_ = d.Close()
		return err
	}
	go d.statusLoop()
	return nil
}

// Recovery creates a new UDP endpoint. Delayed replies addressed to the old
// endpoint cannot be mistaken for replies to the new generation's requests.
func (d *Driver) ensureConnection(ctx context.Context) error {
	d.connectMu.Lock()
	defer d.connectMu.Unlock()
	d.mu.Lock()
	if d.closed || !d.started {
		d.mu.Unlock()
		return station.ErrOffline
	}
	if d.conn != nil {
		d.mu.Unlock()
		return nil
	}
	d.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	remote, err := net.ResolveUDPAddr("udp", d.address)
	if err != nil {
		return err
	}
	c, err := net.DialUDP("udp", nil, remote)
	if err != nil {
		return err
	}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		_ = c.Close()
		return station.ErrOffline
	}
	d.conn = c
	retired := d.retiredConnection
	d.retiredConnection = nil
	d.mu.Unlock()
	if retired != nil {
		_ = retired.Close()
	}
	go d.readLoop(c)
	if err := d.initializeFeedback(ctx); err != nil {
		d.loseConnection(c)
		return err
	}
	return nil
}

func (d *Driver) loseConnection(c *net.UDPConn) {
	d.mu.Lock()
	if d.closed || c == nil || d.conn != c {
		d.mu.Unlock()
		return
	}
	d.health.CommunicationError()
	d.conn = nil
	d.retiredConnection = c
	d.feedbackGeneration++
	generation := d.feedbackGeneration
	d.rbusReceived = [rbusGroupCount]bool{}
	d.mu.Unlock()
	// Keep the old local port reserved until the replacement is bound.
	// Interrupt its reader now; it cannot submit data to the new generation.
	_ = c.SetReadDeadline(time.Now())
	d.publish(station.FeedbackEvent{Source: "z21-rbus", Kind: "synchronization", Generation: generation})
}

func (d *Driver) FeedbackState() station.FeedbackState {
	d.mu.Lock()
	defer d.mu.Unlock()
	return station.FeedbackState{Source: "z21-rbus", Generation: d.feedbackGeneration,
		Available: !d.closed && d.conn != nil && d.health.Health().Connectivity == station.Online}
}
func (d *Driver) Close() error {
	d.closeOnce.Do(func() { close(d.done) })
	d.mu.Lock()
	d.closed = true
	c := d.conn
	retired := d.retiredConnection
	d.conn = nil
	d.retiredConnection = nil
	d.mu.Unlock()
	if retired != nil {
		_ = retired.Close()
	}
	if c != nil {
		return c.Close()
	}
	return nil
}
func (d *Driver) Health() station.Health { return d.health.Health() }
func (d *Driver) Capabilities() station.Capabilities {
	return station.Capabilities{Driver: "z21", TrackPower: true, LocomotiveControl: true, Functions: 29, MaxFunctionNumber: 28, AccessoryControl: true, Feedback: true}
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

func linearAddressToFAddress(address int) (uint16, error) {
	if address < station.MinBasicAccessoryAddress || address > station.MaxBasicAccessoryAddress {
		return 0, fmt.Errorf("%w: got %d, want %d..%d", station.ErrInvalidAccessoryAddress, address, station.MinBasicAccessoryAddress, station.MaxBasicAccessoryAddress)
	}
	return uint16(address - 1), nil
}

func fAddressToLinearAddress(fAddress uint16) (int, error) {
	address := int(fAddress) + 1
	if address < station.MinBasicAccessoryAddress || address > station.MaxBasicAccessoryAddress {
		return 0, fmt.Errorf("%w: z21 FAdr %d", station.ErrInvalidAccessoryAddress, fAddress)
	}
	return address, nil
}

func turnoutCommandPacket(fAddress uint16, position station.AccessoryPosition, activate bool) []byte {
	db2 := byte(0x80 | 0x20) // 1 0 Q 0 A 0 0 P, with Q=1.
	if activate {
		db2 |= 0x08
	}
	if position == station.AccessoryPosition2 {
		db2 |= 0x01
	}
	return xbus(lanXSetTurnout, byte(fAddress>>8), byte(fAddress), db2)
}

func turnoutInfoRequestPacket(fAddress uint16) []byte {
	return xbus(lanXGetTurnoutInfo, byte(fAddress>>8), byte(fAddress))
}

func waitForPulse(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func lan(header uint16, payload ...byte) []byte {
	record := make([]byte, 4+len(payload))
	binary.LittleEndian.PutUint16(record[0:2], uint16(len(record)))
	binary.LittleEndian.PutUint16(record[2:4], header)
	copy(record[4:], payload)
	return record
}

func broadcastFlagsPacket() []byte {
	payload := make([]byte, 4)
	binary.LittleEndian.PutUint32(payload, broadcastFlags)
	return lan(lanSetBroadcastFlags, payload...)
}

func (d *Driver) initializeFeedback(ctx context.Context) error {
	if err := d.send(ctx, broadcastFlagsPacket()); err != nil {
		return fmt.Errorf("configure z21 broadcast flags: %w", err)
	}
	return d.requestMissingRBusData(ctx)
}

func (d *Driver) requestMissingRBusData(ctx context.Context) error {
	d.mu.Lock()
	received := d.rbusReceived
	d.mu.Unlock()
	for group, complete := range received {
		if !complete {
			if err := d.send(ctx, lan(lanRMBusGetData, byte(group))); err != nil {
				return fmt.Errorf("request z21 R-BUS group %d: %w", group, err)
			}
		}
	}
	return nil
}

func (d *Driver) send(ctx context.Context, b []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	if d.conn == nil {
		d.mu.Unlock()
		return station.ErrOffline
	}
	c := d.conn
	if deadline, ok := ctx.Deadline(); ok {
		_ = d.conn.SetWriteDeadline(deadline)
	} else {
		_ = d.conn.SetWriteDeadline(time.Time{})
	}
	_, err := c.Write(b)
	d.mu.Unlock()
	if err != nil {
		d.loseConnection(c)
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
	if err := d.ensureConnection(ctx); err != nil {
		d.health.CommunicationError()
		return d.currentStatus(), err
	}
	d.mu.Lock()
	connection := d.conn
	d.mu.Unlock()
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
		case <-d.done:
			return d.currentStatus(), station.ErrOffline
		case <-timer.C:
			d.loseConnection(connection)
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
	if !direction.Valid() {
		return station.ErrUnsupported
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
	if fn < 0 || fn > d.Capabilities().MaxFunctionNumber {
		return station.ErrUnsupported
	}
	mode := byte(0)
	if on {
		mode = 0x40
	}
	value := mode | byte(fn&0x3f)
	return d.send(ctx, xbus(0xE4, 0xF8, byte((address>>8)&0x3f), byte(address), value))
}
func (d *Driver) SetBasicAccessory(ctx context.Context, command station.AccessoryCommand) error {
	if err := command.Validate(); err != nil {
		return err
	}
	if err := station.CheckCommandAllowed(d); err != nil {
		return err
	}
	fAddress, err := linearAddressToFAddress(command.Address)
	if err != nil {
		return err
	}
	if err := d.send(ctx, turnoutCommandPacket(fAddress, command.Position, true)); err != nil {
		return err
	}

	waitErr := waitForPulse(ctx, d.accessoryPulse)
	deactivateCtx, cancel := context.WithTimeout(context.Background(), accessoryDeactivationTimeout)
	deactivateErr := d.send(deactivateCtx, turnoutCommandPacket(fAddress, command.Position, false))
	cancel()
	if waitErr != nil {
		return errors.Join(waitErr, deactivateErr)
	}
	if deactivateErr != nil {
		return deactivateErr
	}
	// Ask the command station for the resulting function state. The correlated
	// response is also published through AccessoryStateEvents.
	queryCtx, queryCancel := context.WithTimeout(ctx, accessoryInfoTimeout)
	defer queryCancel()
	_, err = d.getAccessoryState(queryCtx, command.Address)
	return err
}
func (d *Driver) Feedback() <-chan station.FeedbackEvent { return d.feedback }
func (d *Driver) StatusEvents() <-chan station.Status    { return d.statusEvents }
func (d *Driver) AccessoryStateEvents() <-chan station.AccessoryStateEvent {
	return d.accessoryEvents
}
func (d *Driver) readLoop(c *net.UDPConn) {
	d.mu.Lock()
	generation := d.feedbackGeneration
	d.mu.Unlock()
	buf := make([]byte, 2048)
	for {
		n, err := c.Read(buf)
		if err != nil {
			d.loseConnection(c)
			return
		}
		d.parseConnection(c, generation, buf[:n])
	}
}
func (d *Driver) parse(data []byte) {
	d.mu.Lock()
	c, generation := d.conn, d.feedbackGeneration
	d.mu.Unlock()
	d.parseConnection(c, generation, data)
}

func (d *Driver) parseConnection(c *net.UDPConn, generation uint64, data []byte) {
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
		d.mu.Lock()
		if d.closed || d.conn != c || d.feedbackGeneration != generation {
			d.mu.Unlock()
			return
		}
		d.health.ValidResponse()
		d.mu.Unlock()
		switch {
		case header == 0x0040 && len(payload) == 4 && payload[0] == 0x62 && payload[1] == 0x22 && xor(payload[:3]) == payload[3]:
			d.publishXStatus(payload[2])
		case header == lanXHeader && len(payload) == 5 && payload[0] == lanXGetTurnoutInfo:
			d.parseTurnoutInfo(payload)
		case header == 0x0084 && len(payload) == 16:
			d.publishSystemState(parseSystemState(payload))
		case header == lanRMBusDataChanged && len(payload) == 11 && payload[0] < rbusGroupCount:
			group := int(payload[0])
			d.mu.Lock()
			if d.feedbackGeneration != generation {
				d.mu.Unlock()
				return
			}
			d.rbusReceived[group] = true
			d.mu.Unlock()
			inputs := make([]station.FeedbackInput, 0, rbusInputsPerGroup)
			for i := 1; i < len(payload); i++ {
				bits := payload[i]
				for bit := 0; bit < 8; bit++ {
					addr := group*rbusInputsPerGroup + (i-1)*8 + bit + 1
					inputs = append(inputs, station.FeedbackInput{Address: addr, Active: bits&(1<<bit) != 0})
				}
			}
			d.publish(station.FeedbackEvent{Source: "z21-rbus", Kind: "occupancy", Generation: generation, Inputs: inputs})
		}
		data = data[length:]
	}
}

func (d *Driver) parseTurnoutInfo(payload []byte) {
	fAddress := uint16(payload[1])<<8 | uint16(payload[2])
	address, err := fAddressToLinearAddress(fAddress)
	if err != nil {
		return
	}
	event := station.AccessoryStateEvent{
		Address:    address,
		Quality:    station.AccessoryReportStation,
		ObservedAt: time.Now(),
	}
	switch payload[3] & 0x03 {
	case 0x00:
		event.State = station.AccessoryReportUnknown
	case 0x01:
		event.State = station.AccessoryReportKnown
		event.Position = station.AccessoryPosition1
	case 0x02:
		event.State = station.AccessoryReportKnown
		event.Position = station.AccessoryPosition2
	case 0x03:
		event.State = station.AccessoryReportInvalid
	}
	d.publishAccessoryState(fAddress, event)
}

func (d *Driver) getAccessoryState(ctx context.Context, address int) (station.AccessoryStateEvent, error) {
	fAddress, err := linearAddressToFAddress(address)
	if err != nil {
		return station.AccessoryStateEvent{}, err
	}
	reply := make(chan station.AccessoryStateEvent, 1)
	d.accessoryMu.Lock()
	d.accessoryWaiters[fAddress] = append(d.accessoryWaiters[fAddress], reply)
	d.accessoryMu.Unlock()
	defer d.removeAccessoryWaiter(fAddress, reply)
	if err := d.send(ctx, turnoutInfoRequestPacket(fAddress)); err != nil {
		return station.AccessoryStateEvent{}, err
	}
	select {
	case event := <-reply:
		return event, nil
	case <-ctx.Done():
		return station.AccessoryStateEvent{}, ctx.Err()
	}
}

func (d *Driver) publishAccessoryState(fAddress uint16, event station.AccessoryStateEvent) {
	d.accessoryMu.Lock()
	waiters := d.accessoryWaiters[fAddress]
	delete(d.accessoryWaiters, fAddress)
	d.accessoryMu.Unlock()
	for _, waiter := range waiters {
		waiter <- event
	}
	select {
	case d.accessoryEvents <- event:
	default:
	}
}

func (d *Driver) removeAccessoryWaiter(fAddress uint16, target chan station.AccessoryStateEvent) {
	d.accessoryMu.Lock()
	defer d.accessoryMu.Unlock()
	waiters := d.accessoryWaiters[fAddress]
	for i, waiter := range waiters {
		if waiter != target {
			continue
		}
		waiters = append(waiters[:i], waiters[i+1:]...)
		if len(waiters) == 0 {
			delete(d.accessoryWaiters, fAddress)
		} else {
			d.accessoryWaiters[fAddress] = waiters
		}
		return
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
			// UDP requests or replies can be lost. Retry only groups for which
			// no complete observation has arrived since startup.
			_ = d.initializeFeedback(context.Background())
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
	case <-d.done:
	}
}
