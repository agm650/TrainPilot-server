package simulator

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/agm650/TrainPilot-server/internal/station"
)

type LocoState struct {
	Speed     float64
	Direction station.Direction
	Functions map[int]bool
}

type Simulator struct {
	mu          sync.RWMutex
	connected   bool
	power       bool
	emergency   bool
	locos       map[int]*LocoState
	accessories map[int]string
	feedback    chan station.FeedbackEvent
}

func New() *Simulator {
	return &Simulator{locos: map[int]*LocoState{}, accessories: map[int]string{}, feedback: make(chan station.FeedbackEvent, 64)}
}
func (s *Simulator) Connect(context.Context) error {
	s.mu.Lock()
	s.connected = true
	s.mu.Unlock()
	return nil
}
func (s *Simulator) Close() error { s.mu.Lock(); s.connected = false; s.mu.Unlock(); return nil }
func (s *Simulator) Capabilities() station.Capabilities {
	return station.Capabilities{Driver: "simulator", TrackPower: true, LocomotiveControl: true, Functions: 69, MaxFunctionNumber: 68, AccessoryControl: true, Feedback: true}
}
func (s *Simulator) ensure() error {
	if !s.connected {
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
	s.power = on
	if on {
		s.emergency = false
	}
	return nil
}
func (s *Simulator) EmergencyStop(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensure(); err != nil {
		return err
	}
	for _, l := range s.locos {
		l.Speed = 0
	}
	s.emergency = true
	return nil
}
func (s *Simulator) Status(context.Context) (station.Status, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.ensure(); err != nil {
		return station.Status{}, err
	}
	power := "off"
	if s.power {
		power = "on"
	}
	health := s.healthLocked()
	return station.Status{Connectivity: health.Connectivity, LastSeen: health.LastSeen, TrackPower: power, EmergencyStop: s.emergency}, nil
}
func (s *Simulator) Health() station.Health {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.healthLocked()
}
func (s *Simulator) healthLocked() station.Health {
	if !s.connected {
		return station.Health{Connectivity: station.Offline}
	}
	now := time.Now()
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
	l := s.locos[address]
	if l == nil {
		l = &LocoState{Functions: map[int]bool{}}
		s.locos[address] = l
	}
	l.Speed = speed
	l.Direction = direction
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
	l := s.locos[address]
	if l == nil {
		l = &LocoState{Functions: map[int]bool{}}
		s.locos[address] = l
	}
	l.Functions[fn] = on
	return nil
}
func (s *Simulator) SetAccessory(_ context.Context, address int, state string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensure(); err != nil {
		return err
	}
	s.accessories[address] = state
	return nil
}
func (s *Simulator) Feedback() <-chan station.FeedbackEvent { return s.feedback }
func (s *Simulator) InjectFeedback(e station.FeedbackEvent) {
	select {
	case s.feedback <- e:
	default:
	}
}
func (s *Simulator) Loco(address int) LocoState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	l := s.locos[address]
	if l == nil {
		return LocoState{Functions: map[int]bool{}}
	}
	copyFunctions := map[int]bool{}
	for k, v := range l.Functions {
		copyFunctions[k] = v
	}
	return LocoState{Speed: l.Speed, Direction: l.Direction, Functions: copyFunctions}
}
