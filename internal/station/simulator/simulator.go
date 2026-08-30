package simulator

import (
	"context"
	"fmt"
	"sync"

	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/station"
)

type LocoState struct {
	Speed     float64
	Direction station.Direction
	Functions map[int]bool
}

type AccessoryState struct {
	State string
}

type State struct {
	Connected     bool
	TrackPower    bool
	EmergencyStop bool
	Locomotives   map[int]LocoState
	Accessories   map[int]AccessoryState
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
		Connected:   connected,
		Locomotives: map[int]LocoState{},
		Accessories: map[int]AccessoryState{},
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
	s.state.Accessories[address] = AccessoryState{State: state}
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
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.Accessories[address]
}

func (s *Simulator) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

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
		snapshot.Accessories[address] = accessory
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
