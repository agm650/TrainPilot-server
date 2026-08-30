package simulator

import (
	"context"
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

type scheduledAccessoryReport struct {
	State      string
	DueAt      time.Time
	Generation uint64
}

type State struct {
	Connected     bool
	TrackPower    bool
	EmergencyStop bool
	Locomotives   map[int]LocoState
	Accessories   map[int]AccessoryState

	accessoryBehaviors  map[int]AccessoryBehavior
	accessoryReports    map[int]scheduledAccessoryReport
	accessoryGeneration map[int]uint64
}

type Snapshot struct {
	Connected     bool
	TrackPower    bool
	EmergencyStop bool
	Locomotives   map[int]LocoState
	Accessories   map[int]AccessoryState
}

type Simulator struct {
	mu       sync.RWMutex
	clock    clock.Clock
	state    State
	feedback chan station.FeedbackEvent
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
		Locomotives:         map[int]LocoState{},
		Accessories:         map[int]AccessoryState{},
		accessoryBehaviors:  map[int]AccessoryBehavior{},
		accessoryReports:    map[int]scheduledAccessoryReport{},
		accessoryGeneration: map[int]uint64{},
	}
}

func (s *Simulator) Connect(context.Context) error {
	s.mu.Lock()
	s.state.Connected = true
	s.mu.Unlock()
	return nil
}

func (s *Simulator) Close() error {
	s.mu.Lock()
	s.state.Connected = false
	s.mu.Unlock()
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

func (s *Simulator) SetTrackPower(_ context.Context, on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensure(); err != nil {
		return err
	}
	s.state.TrackPower = on
	if on {
		s.state.EmergencyStop = false
	}
	return nil
}

func (s *Simulator) EmergencyStop(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensure(); err != nil {
		return err
	}
	for address, l := range s.state.Locomotives {
		l.Speed = 0
		s.state.Locomotives[address] = l
	}
	s.state.EmergencyStop = true
	return nil
}

func (s *Simulator) Status(context.Context) (station.Status, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.ensure(); err != nil {
		return station.Status{}, err
	}
	power := "off"
	if s.state.TrackPower {
		power = "on"
	}
	health := s.healthLocked()
	return station.Status{Connectivity: health.Connectivity, LastSeen: health.LastSeen, TrackPower: power, EmergencyStop: s.state.EmergencyStop}, nil
}

func (s *Simulator) Health() station.Health {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.healthLocked()
}

func (s *Simulator) healthLocked() station.Health {
	if !s.state.Connected {
		return station.Health{Connectivity: station.Offline}
	}
	now := s.clock.Now()
	return station.Health{Connectivity: station.Online, LastSeen: &now}
}

func (s *Simulator) SetLocoSpeed(_ context.Context, address int, speed float64, direction station.Direction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensure(); err != nil {
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
	return nil
}

func (s *Simulator) SetLocoFunction(_ context.Context, address, fn int, on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensure(); err != nil {
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
	return nil
}

func (s *Simulator) SetAccessory(_ context.Context, address int, state string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensure(); err != nil {
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
		TrackPower:    s.state.TrackPower,
		EmergencyStop: s.state.EmergencyStop,
		Locomotives:   make(map[int]LocoState, len(s.state.Locomotives)),
		Accessories:   make(map[int]AccessoryState, len(s.state.Accessories)),
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

	s.state = newState(s.state.Connected)
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
