package simulator

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/station"
)

type LocoState struct {
	Speed     float64
	Direction station.Direction
	Functions map[int]bool
}

type Operation string

const (
	OpStatus        Operation = "status"
	OpTrackPower    Operation = "track_power"
	OpEmergencyStop Operation = "emergency_stop"
	OpThrottle      Operation = "throttle"
	OpFunction      Operation = "function"
	OpAccessory     Operation = "accessory"
)

var ErrOperationCanceled = errors.New("simulator operation canceled")

type OperationFault struct {
	Delay     time.Duration
	Error     error
	Remaining int
}

type AccessoryBehaviorMode string

const (
	AccessoryBehaviorImmediate      AccessoryBehaviorMode = "immediate"
	AccessoryBehaviorDelayed        AccessoryBehaviorMode = "delayed"
	AccessoryBehaviorNoConfirmation AccessoryBehaviorMode = "no_confirmation"
	AccessoryBehaviorInconsistent   AccessoryBehaviorMode = "inconsistent"
)

type AccessoryBehavior struct {
	Mode          AccessoryBehaviorMode
	Delay         time.Duration
	ReportedState string
}

type AccessoryState struct {
	Desired       string
	Reported      string
	Pending       bool
	LastCommandAt *time.Time
	LastReportAt  *time.Time
}

type ElectricalState struct {
	MainCurrentMilliAmps         int16
	ProgrammingCurrentMilliAmps  int16
	FilteredMainCurrentMilliAmps int16
	TemperatureCelsius           int16
	SupplyVoltageMilliVolts      uint16
	TrackVoltageMilliVolts       uint16

	ProgrammingMode      bool
	HighTemperature      bool
	PowerLost            bool
	ExternalShortCircuit bool
	InternalShortCircuit bool
}

type scheduledAccessoryReport struct {
	State      string
	DueAt      time.Time
	Generation uint64
}

type State struct {
	Connected     bool
	Connectivity  station.Connectivity
	LastSeen      *time.Time
	TrackPower    bool
	EmergencyStop bool
	Locomotives   map[int]LocoState
	Accessories   map[int]AccessoryState
	Electrical    ElectricalState

	accessoryBehaviors  map[int]AccessoryBehavior
	accessoryReports    map[int]scheduledAccessoryReport
	accessoryGeneration map[int]uint64
	operationFaults     map[Operation]OperationFault
	operationEpoch      uint64
}

type Snapshot struct {
	Connected     bool
	Connectivity  station.Connectivity
	LastSeen      *time.Time
	TrackPower    bool
	EmergencyStop bool
	Locomotives   map[int]LocoState
	Accessories   map[int]AccessoryState
	Electrical    ElectricalState
}

type Simulator struct {
	mu       sync.RWMutex
	clock    clock.Clock
	state    State
	feedback chan station.FeedbackEvent
}

type deadlineWaiter interface {
	WaitUntil(context.Context, time.Time) error
}

func New() *Simulator {
	return NewWithClock(clock.Real{})
}

func NewWithClock(clk clock.Clock) *Simulator {
	if clk == nil {
		clk = clock.Real{}
	}
	return &Simulator{
		clock:    clk,
		state:    newState(false),
		feedback: make(chan station.FeedbackEvent, 64),
	}
}

func newState(connected bool) State {
	return State{
		Connected:           connected,
		Connectivity:        station.Offline,
		Locomotives:         map[int]LocoState{},
		Accessories:         map[int]AccessoryState{},
		Electrical:          nominalElectricalState(),
		accessoryBehaviors:  map[int]AccessoryBehavior{},
		accessoryReports:    map[int]scheduledAccessoryReport{},
		accessoryGeneration: map[int]uint64{},
		operationFaults:     map[Operation]OperationFault{},
	}
}

func (s *Simulator) Connect(context.Context) error {
	s.mu.Lock()
	s.state.Connected = true
	s.state.Connectivity = station.Online
	s.state.LastSeen = timePointer(s.clock.Now())
	s.mu.Unlock()
	return nil
}

func (s *Simulator) Close() error {
	s.mu.Lock()
	s.state.Connected = false
	s.state.Connectivity = station.Offline
	s.state.operationEpoch++
	s.mu.Unlock()
	return nil
}

func (s *Simulator) SetConnectivity(connectivity station.Connectivity) error {
	if connectivity != station.Online && connectivity != station.Degraded && connectivity != station.Offline {
		return fmt.Errorf("unsupported simulator connectivity %q", connectivity)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensure(); err != nil {
		return err
	}
	if s.state.Connectivity != connectivity {
		s.state.operationEpoch++
	}
	s.state.Connectivity = connectivity
	if connectivity == station.Online {
		s.state.LastSeen = timePointer(s.clock.Now())
	}
	return nil
}

func (s *Simulator) Capabilities() station.Capabilities {
	return station.Capabilities{Driver: "simulator", TrackPower: true, LocomotiveControl: true, Functions: 69, MaxFunctionNumber: 68, AccessoryControl: true, Feedback: true}
}

func (s *Simulator) ensure() error {
	if !s.state.Connected {
		return fmt.Errorf("simulator is disconnected")
	}
	return nil
}

func (s *Simulator) SetTrackPower(ctx context.Context, on bool) error {
	epoch, err := s.beforeOperation(ctx, OpTrackPower, true)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureOperationReadyLocked(epoch, true); err != nil {
		return err
	}
	s.state.TrackPower = on
	if on {
		s.state.EmergencyStop = false
		s.state.Electrical.TrackVoltageMilliVolts = s.state.Electrical.SupplyVoltageMilliVolts
	} else {
		s.state.Electrical.TrackVoltageMilliVolts = 0
	}
	s.markActivityLocked(s.clock.Now())
	return nil
}

func (s *Simulator) EmergencyStop(ctx context.Context) error {
	epoch, err := s.beforeOperation(ctx, OpEmergencyStop, true)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureOperationReadyLocked(epoch, true); err != nil {
		return err
	}
	for address, l := range s.state.Locomotives {
		l.Speed = 0
		s.state.Locomotives[address] = l
	}
	s.state.EmergencyStop = true
	s.markActivityLocked(s.clock.Now())
	return nil
}

func (s *Simulator) Status(ctx context.Context) (station.Status, error) {
	epoch, err := s.beforeOperation(ctx, OpStatus, false)
	if err != nil {
		return station.Status{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureOperationReadyLocked(epoch, false); err != nil {
		return station.Status{}, err
	}
	if s.state.Connectivity == station.Online {
		s.markActivityLocked(s.clock.Now())
	}
	power := "off"
	if s.state.TrackPower {
		power = "on"
	}
	health := s.healthLocked()
	electrical := s.state.Electrical
	return station.Status{
		Connectivity:                 health.Connectivity,
		LastSeen:                     health.LastSeen,
		TrackPower:                   power,
		EmergencyStop:                s.state.EmergencyStop,
		ShortCircuit:                 electrical.ExternalShortCircuit || electrical.InternalShortCircuit,
		ProgrammingMode:              electrical.ProgrammingMode,
		MainCurrentMilliAmps:         electrical.MainCurrentMilliAmps,
		ProgrammingCurrentMilliAmps:  electrical.ProgrammingCurrentMilliAmps,
		FilteredMainCurrentMilliAmps: electrical.FilteredMainCurrentMilliAmps,
		TemperatureCelsius:           electrical.TemperatureCelsius,
		SupplyVoltageMilliVolts:      electrical.SupplyVoltageMilliVolts,
		TrackVoltageMilliVolts:       electrical.TrackVoltageMilliVolts,
		HighTemperature:              electrical.HighTemperature,
		PowerLost:                    electrical.PowerLost,
		ExternalShortCircuit:         electrical.ExternalShortCircuit,
		InternalShortCircuit:         electrical.InternalShortCircuit,
	}, nil
}

func (s *Simulator) SetElectricalState(state ElectricalState) {
	s.mu.Lock()
	s.state.Electrical = state
	s.mu.Unlock()
}

// SetOperationFault configures a deterministic fault. Remaining == 0 means
// that the rule remains active until ClearFaults or Reset is called.
func (s *Simulator) SetOperationFault(operation Operation, fault OperationFault) error {
	if !validOperation(operation) {
		return fmt.Errorf("unsupported simulator operation %q", operation)
	}
	if fault.Delay < 0 {
		return fmt.Errorf("operation fault delay must not be negative")
	}
	if fault.Remaining < 0 {
		return fmt.Errorf("operation fault remaining count must not be negative")
	}
	s.mu.Lock()
	s.state.operationFaults[operation] = fault
	s.mu.Unlock()
	return nil
}

func (s *Simulator) ClearFaults() {
	s.mu.Lock()
	s.state.operationFaults = map[Operation]OperationFault{}
	s.state.operationEpoch++
	s.mu.Unlock()
}

func (s *Simulator) Health() station.Health {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.healthLocked()
}

func (s *Simulator) healthLocked() station.Health {
	if !s.state.Connected {
		return station.Health{Connectivity: station.Offline, LastSeen: cloneTimePointer(s.state.LastSeen)}
	}
	return station.Health{Connectivity: s.state.Connectivity, LastSeen: cloneTimePointer(s.state.LastSeen)}
}

func (s *Simulator) SetLocoSpeed(ctx context.Context, address int, speed float64, direction station.Direction) error {
	epoch, err := s.beforeOperation(ctx, OpThrottle, true)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureOperationReadyLocked(epoch, true); err != nil {
		return err
	}
	if speed < 0 || speed > 1 {
		return fmt.Errorf("speed out of range")
	}
	if !direction.Valid() {
		return station.ErrUnsupported
	}
	l, ok := s.state.Locomotives[address]
	if !ok {
		l = LocoState{Functions: map[int]bool{}}
	}
	l.Speed = speed
	l.Direction = direction
	s.state.Locomotives[address] = l
	s.markActivityLocked(s.clock.Now())
	return nil
}

func (s *Simulator) SetLocoFunction(ctx context.Context, address, fn int, on bool) error {
	epoch, err := s.beforeOperation(ctx, OpFunction, true)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureOperationReadyLocked(epoch, true); err != nil {
		return err
	}
	if fn < 0 || fn > s.Capabilities().MaxFunctionNumber {
		return station.ErrUnsupported
	}
	l, ok := s.state.Locomotives[address]
	if !ok {
		l = LocoState{Functions: map[int]bool{}}
	}
	l.Functions[fn] = on
	s.state.Locomotives[address] = l
	s.markActivityLocked(s.clock.Now())
	return nil
}

func (s *Simulator) SetAccessory(ctx context.Context, address int, state string) error {
	epoch, err := s.beforeOperation(ctx, OpAccessory, true)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureOperationReadyLocked(epoch, true); err != nil {
		return err
	}
	if !validAccessoryDesiredState(state) {
		return fmt.Errorf("unsupported accessory state %q", state)
	}
	now := s.clock.Now()
	s.applyDueAccessoryReportsLocked(now)

	accessory := s.state.Accessories[address]
	s.state.accessoryGeneration[address]++
	generation := s.state.accessoryGeneration[address]
	accessory.Desired = state
	accessory.LastCommandAt = timePointer(now)
	delete(s.state.accessoryReports, address)

	behavior := s.state.accessoryBehaviors[address]
	if behavior.Mode == "" {
		behavior.Mode = AccessoryBehaviorImmediate
	}
	switch behavior.Mode {
	case AccessoryBehaviorImmediate:
		accessory.Reported = state
		accessory.Pending = false
		accessory.LastReportAt = timePointer(now)
	case AccessoryBehaviorDelayed:
		accessory.Pending = true
		s.state.accessoryReports[address] = scheduledAccessoryReport{
			State:      state,
			DueAt:      now.Add(behavior.Delay),
			Generation: generation,
		}
	case AccessoryBehaviorNoConfirmation:
		accessory.Pending = true
	case AccessoryBehaviorInconsistent:
		accessory.Reported = behavior.ReportedState
		accessory.Pending = behavior.ReportedState != state
		accessory.LastReportAt = timePointer(now)
	}
	s.state.Accessories[address] = accessory
	s.markActivityLocked(now)
	return nil
}

func (s *Simulator) SetAccessoryBehavior(address int, behavior AccessoryBehavior) error {
	if behavior.Mode == "" {
		behavior.Mode = AccessoryBehaviorImmediate
	}
	switch behavior.Mode {
	case AccessoryBehaviorImmediate, AccessoryBehaviorNoConfirmation:
		if behavior.Delay < 0 {
			return fmt.Errorf("accessory behavior delay must not be negative")
		}
	case AccessoryBehaviorDelayed:
		if behavior.Delay <= 0 {
			return fmt.Errorf("delayed accessory behavior requires a positive delay")
		}
	case AccessoryBehaviorInconsistent:
		if !validAccessoryReportedState(behavior.ReportedState) {
			return fmt.Errorf("unsupported reported accessory state %q", behavior.ReportedState)
		}
		if behavior.Delay < 0 {
			return fmt.Errorf("accessory behavior delay must not be negative")
		}
	default:
		return fmt.Errorf("unsupported accessory behavior mode %q", behavior.Mode)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.applyDueAccessoryReportsLocked(s.clock.Now())
	s.state.accessoryBehaviors[address] = behavior
	return nil
}

func (s *Simulator) ReportAccessoryState(address int, state string) error {
	if !validAccessoryReportedState(state) {
		return fmt.Errorf("unsupported reported accessory state %q", state)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	s.applyDueAccessoryReportsLocked(now)

	s.state.accessoryGeneration[address]++
	delete(s.state.accessoryReports, address)
	accessory := s.state.Accessories[address]
	accessory.Reported = state
	accessory.Pending = accessory.Desired != "" && accessory.Desired != state
	accessory.LastReportAt = timePointer(now)
	s.state.Accessories[address] = accessory
	return nil
}

func (s *Simulator) Feedback() <-chan station.FeedbackEvent { return s.feedback }

func (s *Simulator) InjectFeedback(e station.FeedbackEvent) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	select {
	case s.feedback <- e:
	default:
	}
}

func (s *Simulator) Loco(address int) LocoState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	l, ok := s.state.Locomotives[address]
	if !ok {
		return LocoState{Functions: map[int]bool{}}
	}
	return cloneLocoState(l)
}

func (s *Simulator) Accessory(address int) AccessoryState {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applyDueAccessoryReportsLocked(s.clock.Now())
	return cloneAccessoryState(s.state.Accessories[address])
}

func (s *Simulator) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applyDueAccessoryReportsLocked(s.clock.Now())

	snapshot := Snapshot{
		Connected:     s.state.Connected,
		Connectivity:  s.state.Connectivity,
		LastSeen:      cloneTimePointer(s.state.LastSeen),
		TrackPower:    s.state.TrackPower,
		EmergencyStop: s.state.EmergencyStop,
		Locomotives:   make(map[int]LocoState, len(s.state.Locomotives)),
		Accessories:   make(map[int]AccessoryState, len(s.state.Accessories)),
		Electrical:    s.state.Electrical,
	}
	for address, loco := range s.state.Locomotives {
		snapshot.Locomotives[address] = cloneLocoState(loco)
	}
	for address, accessory := range s.state.Accessories {
		snapshot.Accessories[address] = cloneAccessoryState(accessory)
	}
	return snapshot
}

// Reset clears the simulated layout state and buffered feedback while preserving
// whether the simulator is currently connected.
func (s *Simulator) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	connected := s.state.Connected
	epoch := s.state.operationEpoch + 1
	s.state = newState(connected)
	s.state.operationEpoch = epoch
	if connected {
		s.state.Connectivity = station.Online
		s.state.LastSeen = timePointer(s.clock.Now())
	}
	for {
		select {
		case <-s.feedback:
		default:
			return
		}
	}
}

func cloneLocoState(loco LocoState) LocoState {
	functions := make(map[int]bool, len(loco.Functions))
	for number, enabled := range loco.Functions {
		functions[number] = enabled
	}
	return LocoState{
		Speed:     loco.Speed,
		Direction: loco.Direction,
		Functions: functions,
	}
}

func validOperation(operation Operation) bool {
	switch operation {
	case OpStatus, OpTrackPower, OpEmergencyStop, OpThrottle, OpFunction, OpAccessory:
		return true
	default:
		return false
	}
}

func (s *Simulator) beforeOperation(ctx context.Context, operation Operation, active bool) (uint64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if err := s.ensure(); err != nil {
		s.mu.Unlock()
		return 0, err
	}
	if active && s.state.Connectivity == station.Offline {
		s.mu.Unlock()
		return 0, station.ErrOffline
	}
	epoch := s.state.operationEpoch
	fault, hasFault := s.state.operationFaults[operation]
	if hasFault && fault.Remaining > 0 {
		remaining := fault.Remaining - 1
		if remaining == 0 {
			delete(s.state.operationFaults, operation)
		} else {
			fault.Remaining = remaining
			s.state.operationFaults[operation] = fault
		}
	}
	s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if hasFault && fault.Delay > 0 {
		if err := s.waitForDelay(ctx, fault.Delay); err != nil {
			return 0, err
		}
	}

	s.mu.RLock()
	err := s.ensureOperationReadyLocked(epoch, active)
	s.mu.RUnlock()
	if err != nil {
		return 0, err
	}
	if hasFault && fault.Error != nil {
		return 0, fault.Error
	}
	return epoch, nil
}

func (s *Simulator) ensureOperationReadyLocked(epoch uint64, active bool) error {
	if epoch != s.state.operationEpoch {
		return ErrOperationCanceled
	}
	if err := s.ensure(); err != nil {
		return err
	}
	if active && s.state.Connectivity == station.Offline {
		return station.ErrOffline
	}
	return nil
}

func (s *Simulator) waitForDelay(ctx context.Context, delay time.Duration) error {
	deadline := s.clock.Now().Add(delay)
	if waiter, ok := s.clock.(deadlineWaiter); ok {
		return waiter.WaitUntil(ctx, deadline)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *Simulator) markActivityLocked(at time.Time) {
	if s.state.Connected && s.state.Connectivity != station.Offline {
		s.state.LastSeen = timePointer(at)
	}
}

func nominalElectricalState() ElectricalState {
	return ElectricalState{
		TemperatureCelsius:      25,
		SupplyVoltageMilliVolts: 18000,
	}
}

func cloneAccessoryState(accessory AccessoryState) AccessoryState {
	accessory.LastCommandAt = cloneTimePointer(accessory.LastCommandAt)
	accessory.LastReportAt = cloneTimePointer(accessory.LastReportAt)
	return accessory
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	return timePointer(*value)
}

func timePointer(value time.Time) *time.Time {
	copy := value
	return &copy
}

func validAccessoryDesiredState(state string) bool {
	return state == "straight" || state == "diverging"
}

func validAccessoryReportedState(state string) bool {
	return validAccessoryDesiredState(state) || state == "unknown"
}

func (s *Simulator) applyDueAccessoryReportsLocked(now time.Time) {
	for address, report := range s.state.accessoryReports {
		if now.Before(report.DueAt) {
			continue
		}
		if s.state.accessoryGeneration[address] == report.Generation {
			accessory := s.state.Accessories[address]
			if accessory.Desired == report.State {
				accessory.Reported = report.State
				accessory.Pending = false
				accessory.LastReportAt = timePointer(report.DueAt)
				s.state.Accessories[address] = accessory
			}
		}
		delete(s.state.accessoryReports, address)
	}
}
