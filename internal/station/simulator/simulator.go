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
	Speed     float64           `json:"speed"`
	Direction station.Direction `json:"direction"`
	Functions map[int]bool      `json:"functions"`
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

var (
	ErrOperationCanceled  = errors.New("simulator operation canceled")
	ErrFeedbackBufferFull = errors.New("simulator feedback buffer is full")
)

type OperationFault struct {
	Delay     time.Duration
	Error     error
	Remaining int
	Address   int
}

type FeedbackKey struct {
	Source  string `json:"source"`
	Kind    string `json:"kind"`
	Address int    `json:"address"`
}

type FeedbackTransition struct {
	Delay  time.Duration
	Active bool
}

type AccessoryBehaviorMode string

const (
	AccessoryBehaviorImmediate      AccessoryBehaviorMode = "immediate"
	AccessoryBehaviorDelayed        AccessoryBehaviorMode = "delayed"
	AccessoryBehaviorNoConfirmation AccessoryBehaviorMode = "no_confirmation"
	AccessoryBehaviorInconsistent   AccessoryBehaviorMode = "inconsistent"
)

type AccessoryBehavior struct {
	Mode             AccessoryBehaviorMode     `json:"mode"`
	Delay            time.Duration             `json:"delay"`
	ReportedPosition station.AccessoryPosition `json:"reportedPosition,omitempty"`
}

type AccessoryState struct {
	Desired       station.AccessoryPosition `json:"desired"`
	Reported      station.AccessoryPosition `json:"reported"`
	Pending       bool                      `json:"pending"`
	LastCommandAt *time.Time                `json:"lastCommandAt,omitempty"`
	LastReportAt  *time.Time                `json:"lastReportAt,omitempty"`
}

type ElectricalState struct {
	MainCurrentMilliAmps         int16  `json:"mainCurrentMilliAmps"`
	ProgrammingCurrentMilliAmps  int16  `json:"programmingCurrentMilliAmps"`
	FilteredMainCurrentMilliAmps int16  `json:"filteredMainCurrentMilliAmps"`
	TemperatureCelsius           int16  `json:"temperatureCelsius"`
	SupplyVoltageMilliVolts      uint16 `json:"supplyVoltageMilliVolts"`
	TrackVoltageMilliVolts       uint16 `json:"trackVoltageMilliVolts"`

	ProgrammingMode      bool `json:"programmingMode"`
	HighTemperature      bool `json:"highTemperature"`
	PowerLost            bool `json:"powerLost"`
	ExternalShortCircuit bool `json:"externalShortCircuit"`
	InternalShortCircuit bool `json:"internalShortCircuit"`
}

type OperationFaultState struct {
	Delay     time.Duration `json:"delay"`
	Error     string        `json:"error,omitempty"`
	Remaining int           `json:"remaining"`
	Address   int           `json:"address,omitempty"`
}

type scheduledAccessoryReport struct {
	Position   station.AccessoryPosition
	DueAt      time.Time
	Generation uint64
}

type State struct {
	Connected      bool
	Connectivity   station.Connectivity
	LastSeen       *time.Time
	TrackPower     bool
	EmergencyStop  bool
	Locomotives    map[int]LocoState
	Accessories    map[int]AccessoryState
	Electrical     ElectricalState
	FeedbackStates map[FeedbackKey]bool

	accessoryBehaviors  map[int]AccessoryBehavior
	accessoryReports    map[int]scheduledAccessoryReport
	accessoryGeneration map[int]uint64
	operationFaults     map[Operation]OperationFault
	operationEpoch      uint64
	feedbackEpoch       uint64
}

type Snapshot struct {
	Connected          bool                              `json:"connected"`
	Connectivity       station.Connectivity              `json:"connectivity"`
	LastSeen           *time.Time                        `json:"lastSeen,omitempty"`
	TrackPower         bool                              `json:"trackPower"`
	EmergencyStop      bool                              `json:"emergencyStop"`
	Locomotives        map[int]LocoState                 `json:"locomotives"`
	Accessories        map[int]AccessoryState            `json:"accessories"`
	AccessoryBehaviors map[int]AccessoryBehavior         `json:"accessoryBehaviors"`
	Electrical         ElectricalState                   `json:"electrical"`
	FeedbackStates     map[FeedbackKey]bool              `json:"-"`
	OperationFaults    map[Operation]OperationFaultState `json:"operationFaults"`
}

type Simulator struct {
	mu                   sync.RWMutex
	clock                clock.Clock
	state                State
	feedback             chan station.FeedbackEvent
	statusEvents         chan station.Status
	accessoryStateEvents chan station.AccessoryStateEvent
	lifecycle            chan struct{}
}

var _ station.CommandStation = (*Simulator)(nil)
var _ station.StatusEventProvider = (*Simulator)(nil)
var _ station.AccessoryStateEventProvider = (*Simulator)(nil)

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
		clock:                clk,
		state:                newState(false),
		feedback:             make(chan station.FeedbackEvent, 64),
		statusEvents:         make(chan station.Status, 64),
		accessoryStateEvents: make(chan station.AccessoryStateEvent, 64),
		lifecycle:            make(chan struct{}),
	}
}

func newState(connected bool) State {
	return State{
		Connected:           connected,
		Connectivity:        station.Offline,
		Locomotives:         map[int]LocoState{},
		Accessories:         map[int]AccessoryState{},
		Electrical:          nominalElectricalState(),
		FeedbackStates:      map[FeedbackKey]bool{},
		accessoryBehaviors:  map[int]AccessoryBehavior{},
		accessoryReports:    map[int]scheduledAccessoryReport{},
		accessoryGeneration: map[int]uint64{},
		operationFaults:     map[Operation]OperationFault{},
	}
}

func (s *Simulator) Connect(context.Context) error {
	s.mu.Lock()
	select {
	case <-s.lifecycle:
		s.lifecycle = make(chan struct{})
	default:
	}
	s.state.Connected = true
	s.state.Connectivity = station.Online
	s.state.LastSeen = timePointer(s.clock.Now())
	s.mu.Unlock()
	return nil
}

func (s *Simulator) Close() error {
	s.mu.Lock()
	s.closeLifecycleLocked()
	s.state.Connected = false
	s.state.Connectivity = station.Offline
	s.state.operationEpoch++
	s.state.feedbackEpoch++
	s.publishStatusLocked()
	s.mu.Unlock()
	return nil
}

// LifecycleDone is closed when Close or Reset invalidates work associated with
// the current simulator lifecycle. A new channel is installed by Reset and by
// a subsequent Connect after Close.
func (s *Simulator) LifecycleDone() <-chan struct{} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lifecycle
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
	changed := s.state.Connectivity != connectivity
	if changed {
		s.state.operationEpoch++
	}
	s.state.Connectivity = connectivity
	if connectivity == station.Online {
		s.state.LastSeen = timePointer(s.clock.Now())
	}
	if changed {
		s.publishStatusLocked()
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
	return s.statusLocked(), nil
}

func (s *Simulator) SetElectricalState(state ElectricalState) {
	s.mu.Lock()
	s.state.Electrical = state
	s.publishStatusLocked()
	s.mu.Unlock()
}

// SetTrackPowerState injects the station's externally observed power state.
// Unlike SetTrackPower it is not a command and therefore bypasses operation
// faults and offline command rejection.
func (s *Simulator) SetTrackPowerState(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := s.state.TrackPower != on || (on && s.state.EmergencyStop)
	s.state.TrackPower = on
	if on {
		s.state.EmergencyStop = false
		s.state.Electrical.TrackVoltageMilliVolts = s.state.Electrical.SupplyVoltageMilliVolts
	} else {
		s.state.Electrical.TrackVoltageMilliVolts = 0
	}
	s.markActivityLocked(s.clock.Now())
	if changed {
		s.publishStatusLocked()
	}
}

// SetEmergencyStopState injects the station's externally observed emergency
// state. Activating it immediately sets every remembered locomotive to zero.
func (s *Simulator) SetEmergencyStopState(active bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := s.state.EmergencyStop != active
	if active {
		for address, loco := range s.state.Locomotives {
			loco.Speed = 0
			s.state.Locomotives[address] = loco
		}
	}
	s.state.EmergencyStop = active
	s.markActivityLocked(s.clock.Now())
	if changed {
		s.publishStatusLocked()
	}
}

func (s *Simulator) StatusEvents() <-chan station.Status { return s.statusEvents }

func (s *Simulator) statusLocked() station.Status {
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
	}
}

func (s *Simulator) publishStatusLocked() {
	select {
	case s.statusEvents <- s.statusLocked():
	default:
	}
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
	if fault.Address != 0 {
		if operation != OpAccessory {
			return fmt.Errorf("operation fault address is only valid for accessory operations")
		}
		if fault.Address < station.MinBasicAccessoryAddress || fault.Address > station.MaxBasicAccessoryAddress {
			return fmt.Errorf("operation fault accessory address must be between %d and %d", station.MinBasicAccessoryAddress, station.MaxBasicAccessoryAddress)
		}
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

func (s *Simulator) SetBasicAccessory(ctx context.Context, command station.AccessoryCommand) error {
	if err := command.Validate(); err != nil {
		return err
	}
	epoch, err := s.beforeOperation(ctx, OpAccessory, true, command.Address)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureOperationReadyLocked(epoch, true); err != nil {
		return err
	}
	now := s.clock.Now()
	s.applyDueAccessoryReportsLocked(now)

	address := command.Address
	accessory := s.state.Accessories[command.Address]
	s.state.accessoryGeneration[command.Address]++
	generation := s.state.accessoryGeneration[command.Address]
	accessory.Desired = command.Position
	accessory.LastCommandAt = timePointer(now)
	delete(s.state.accessoryReports, address)

	behavior := s.state.accessoryBehaviors[address]
	if behavior.Mode == "" {
		behavior.Mode = AccessoryBehaviorImmediate
	}
	switch behavior.Mode {
	case AccessoryBehaviorImmediate:
		accessory.Reported = command.Position
		accessory.Pending = false
		accessory.LastReportAt = timePointer(now)
		s.publishAccessoryStateLocked(command.Position, command.Address, station.AccessoryReportPhysical, now)
	case AccessoryBehaviorDelayed:
		accessory.Pending = true
		s.state.accessoryReports[address] = scheduledAccessoryReport{
			Position:   command.Position,
			DueAt:      now.Add(behavior.Delay),
			Generation: generation,
		}
	case AccessoryBehaviorNoConfirmation:
		accessory.Pending = true
	case AccessoryBehaviorInconsistent:
		accessory.Reported = behavior.ReportedPosition
		accessory.Pending = behavior.ReportedPosition != command.Position
		accessory.LastReportAt = timePointer(now)
		s.publishAccessoryStateLocked(behavior.ReportedPosition, command.Address, station.AccessoryReportPhysical, now)
	}
	s.state.Accessories[address] = accessory
	s.markActivityLocked(now)
	return nil
}

func (s *Simulator) SetAccessoryBehavior(address int, behavior AccessoryBehavior) error {
	if address < station.MinBasicAccessoryAddress || address > station.MaxBasicAccessoryAddress {
		return fmt.Errorf("accessory address must be between %d and %d", station.MinBasicAccessoryAddress, station.MaxBasicAccessoryAddress)
	}
	if behavior.Mode == "" {
		behavior.Mode = AccessoryBehaviorImmediate
	}
	switch behavior.Mode {
	case AccessoryBehaviorImmediate, AccessoryBehaviorNoConfirmation:
		if behavior.ReportedPosition != "" {
			return fmt.Errorf("reported accessory position is only valid for inconsistent behavior")
		}
		if behavior.Delay < 0 {
			return fmt.Errorf("accessory behavior delay must not be negative")
		}
	case AccessoryBehaviorDelayed:
		if behavior.ReportedPosition != "" {
			return fmt.Errorf("reported accessory position is only valid for inconsistent behavior")
		}
		if behavior.Delay <= 0 {
			return fmt.Errorf("delayed accessory behavior requires a positive delay")
		}
	case AccessoryBehaviorInconsistent:
		if !behavior.ReportedPosition.Valid() {
			return fmt.Errorf("unsupported reported accessory position %q", behavior.ReportedPosition)
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

func (s *Simulator) ReportAccessoryPosition(address int, position station.AccessoryPosition, quality station.AccessoryReportQuality) error {
	if address < station.MinBasicAccessoryAddress || address > station.MaxBasicAccessoryAddress {
		return fmt.Errorf("accessory address must be between %d and %d", station.MinBasicAccessoryAddress, station.MaxBasicAccessoryAddress)
	}
	if !position.Valid() {
		return fmt.Errorf("unsupported reported accessory position %q", position)
	}
	if !quality.Valid() {
		return fmt.Errorf("unsupported accessory report quality %q", quality)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	s.applyDueAccessoryReportsLocked(now)

	s.state.accessoryGeneration[address]++
	delete(s.state.accessoryReports, address)
	accessory := s.state.Accessories[address]
	accessory.Reported = position
	accessory.Pending = accessory.Desired != "" && accessory.Desired != position
	accessory.LastReportAt = timePointer(now)
	s.state.Accessories[address] = accessory
	s.publishAccessoryStateLocked(position, address, quality, now)
	return nil
}

func (s *Simulator) Feedback() <-chan station.FeedbackEvent { return s.feedback }

// AccessoryStateEvents returns a best-effort buffered stream. Producers never
// block a station command; the newest event is dropped when the buffer is full.
// Snapshot remains the authoritative simulator state for resynchronization.
func (s *Simulator) AccessoryStateEvents() <-chan station.AccessoryStateEvent {
	return s.accessoryStateEvents
}

// InjectFeedback preserves the historical best-effort API. It always updates
// the physical sensor state but intentionally ignores a full feedback buffer.
func (s *Simulator) InjectFeedback(e station.FeedbackEvent) {
	_ = s.SetFeedback(context.Background(), e)
}

// SetFeedback updates the physical sensor state and emits the event without
// blocking. Repeated values are emitted; a full buffer is reported explicitly.
func (s *Simulator) SetFeedback(ctx context.Context, event station.FeedbackEvent) error {
	return s.setFeedback(ctx, event, true, nil)
}

// SetFeedbackAtomic updates the physical sensor state only if the matching
// event can also be delivered. It is intended for scenario steps, whose
// application must be all-or-nothing.
func (s *Simulator) SetFeedbackAtomic(ctx context.Context, event station.FeedbackEvent) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case s.feedback <- event:
		key := FeedbackKey{Source: event.Source, Kind: event.Kind, Address: event.Address}
		s.state.FeedbackStates[key] = event.Active
		return nil
	default:
		return ErrFeedbackBufferFull
	}
}

// SetFeedbackState changes the physical state without emitting an event. It is
// the explicit way to simulate a lost feedback message.
func (s *Simulator) SetFeedbackState(event station.FeedbackEvent) {
	_ = s.setFeedback(context.Background(), event, false, nil)
}

func (s *Simulator) EmitFeedbackSequence(ctx context.Context, event station.FeedbackEvent, transitions []FeedbackTransition) error {
	for _, transition := range transitions {
		if transition.Delay < 0 {
			return fmt.Errorf("feedback transition delay must not be negative")
		}
	}
	s.mu.RLock()
	epoch := s.state.feedbackEpoch
	s.mu.RUnlock()
	for _, transition := range transitions {
		if transition.Delay > 0 {
			if err := s.waitForDelay(ctx, transition.Delay); err != nil {
				return err
			}
		}
		event.Active = transition.Active
		if err := s.setFeedback(ctx, event, true, &epoch); err != nil {
			return err
		}
	}
	return nil
}

func (s *Simulator) BounceFeedback(ctx context.Context, event station.FeedbackEvent, interval time.Duration) error {
	if interval < 0 {
		return fmt.Errorf("feedback bounce interval must not be negative")
	}
	return s.EmitFeedbackSequence(ctx, event, []FeedbackTransition{
		{Active: true},
		{Delay: interval, Active: false},
		{Delay: interval, Active: true},
	})
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
		Connected:          s.state.Connected,
		Connectivity:       s.state.Connectivity,
		LastSeen:           cloneTimePointer(s.state.LastSeen),
		TrackPower:         s.state.TrackPower,
		EmergencyStop:      s.state.EmergencyStop,
		Locomotives:        make(map[int]LocoState, len(s.state.Locomotives)),
		Accessories:        make(map[int]AccessoryState, len(s.state.Accessories)),
		AccessoryBehaviors: make(map[int]AccessoryBehavior, len(s.state.accessoryBehaviors)),
		Electrical:         s.state.Electrical,
		FeedbackStates:     make(map[FeedbackKey]bool, len(s.state.FeedbackStates)),
		OperationFaults:    make(map[Operation]OperationFaultState, len(s.state.operationFaults)),
	}
	for address, loco := range s.state.Locomotives {
		snapshot.Locomotives[address] = cloneLocoState(loco)
	}
	for address, accessory := range s.state.Accessories {
		snapshot.Accessories[address] = cloneAccessoryState(accessory)
	}
	for address, behavior := range s.state.accessoryBehaviors {
		snapshot.AccessoryBehaviors[address] = behavior
	}
	for key, active := range s.state.FeedbackStates {
		snapshot.FeedbackStates[key] = active
	}
	for operation, fault := range s.state.operationFaults {
		faultState := OperationFaultState{Delay: fault.Delay, Remaining: fault.Remaining, Address: fault.Address}
		if fault.Error != nil {
			faultState.Error = fault.Error.Error()
		}
		snapshot.OperationFaults[operation] = faultState
	}
	return snapshot
}

// Reset clears the simulated layout state and buffered feedback while preserving
// whether the simulator is currently connected.
func (s *Simulator) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.closeLifecycleLocked()
	connected := s.state.Connected
	epoch := s.state.operationEpoch + 1
	feedbackEpoch := s.state.feedbackEpoch + 1
	s.state = newState(connected)
	s.lifecycle = make(chan struct{})
	s.state.operationEpoch = epoch
	s.state.feedbackEpoch = feedbackEpoch
	if connected {
		s.state.Connectivity = station.Online
		s.state.LastSeen = timePointer(s.clock.Now())
	}
	s.publishStatusLocked()
	for {
		select {
		case <-s.feedback:
		default:
			goto feedbackDrained
		}
	}

feedbackDrained:
	for {
		select {
		case <-s.accessoryStateEvents:
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

func (operation Operation) Valid() bool {
	return validOperation(operation)
}

func (s *Simulator) closeLifecycleLocked() {
	select {
	case <-s.lifecycle:
	default:
		close(s.lifecycle)
	}
}

func (s *Simulator) beforeOperation(ctx context.Context, operation Operation, active bool, addresses ...int) (uint64, error) {
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
	if hasFault && fault.Address != 0 {
		hasFault = len(addresses) == 1 && addresses[0] == fault.Address
	}
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
	if ctx == nil {
		ctx = context.Background()
	}
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

func (s *Simulator) setFeedback(ctx context.Context, event station.FeedbackEvent, emit bool, expectedEpoch *uint64) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if expectedEpoch != nil && s.state.feedbackEpoch != *expectedEpoch {
		return ErrOperationCanceled
	}
	key := FeedbackKey{Source: event.Source, Kind: event.Kind, Address: event.Address}
	s.state.FeedbackStates[key] = event.Active
	if !emit {
		return nil
	}
	select {
	case s.feedback <- event:
		return nil
	default:
		return ErrFeedbackBufferFull
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

func (s *Simulator) publishAccessoryStateLocked(position station.AccessoryPosition, address int, quality station.AccessoryReportQuality, observedAt time.Time) {
	event := station.AccessoryStateEvent{
		Address:    address,
		Position:   position,
		State:      station.AccessoryReportKnown,
		Quality:    quality,
		ObservedAt: observedAt,
	}
	select {
	case s.accessoryStateEvents <- event:
	default:
	}
}

func (s *Simulator) applyDueAccessoryReportsLocked(now time.Time) {
	for address, report := range s.state.accessoryReports {
		if now.Before(report.DueAt) {
			continue
		}
		if s.state.accessoryGeneration[address] == report.Generation {
			accessory := s.state.Accessories[address]
			if accessory.Desired == report.Position {
				accessory.Reported = report.Position
				accessory.Pending = false
				accessory.LastReportAt = timePointer(report.DueAt)
				s.state.Accessories[address] = accessory
				s.publishAccessoryStateLocked(report.Position, address, station.AccessoryReportPhysical, report.DueAt)
			}
		}
		delete(s.state.accessoryReports, address)
	}
}
