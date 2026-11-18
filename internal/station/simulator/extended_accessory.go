package simulator

import (
	"context"
	"time"

	"github.com/agm650/TrainPilot-server/internal/station"
)

const OpExtendedAccessory Operation = "extended_accessory"

type ExtendedAccessoryState struct {
	Aspect    *int                           `json:"aspect"`
	Quality   station.AccessoryReportQuality `json:"quality"`
	UpdatedAt time.Time                      `json:"updatedAt"`
}

type ExtendedAccessoryCommandRecord struct {
	Command station.ExtendedAccessoryCommand `json:"command"`
	At      time.Time                        `json:"at"`
}

var _ station.ExtendedAccessoryController = (*Simulator)(nil)
var _ station.ExtendedAccessoryStateEventProvider = (*Simulator)(nil)

func (s *Simulator) SetExtendedAccessory(ctx context.Context, command station.ExtendedAccessoryCommand) error {
	if err := command.Validate(); err != nil {
		return err
	}
	epoch, err := s.beforeOperation(ctx, OpExtendedAccessory, true, command.Address)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.ensureOperationReadyLocked(epoch, true); err != nil {
		return err
	}
	now := s.clock.Now()
	previous := s.state.ExtendedAccessories[command.Address]
	aspect := command.Aspect
	s.state.ExtendedAccessories[command.Address] = ExtendedAccessoryState{Aspect: &aspect, Quality: station.AccessoryReportAssumed, UpdatedAt: now}
	s.state.ExtendedCommands = append(s.state.ExtendedCommands, ExtendedAccessoryCommandRecord{Command: command, At: now})
	s.markActivityLocked(now)
	if previous.Aspect == nil || *previous.Aspect != aspect {
		reportedAspect := aspect
		event := station.ExtendedAccessoryStateEvent{Address: command.Address, Aspect: &reportedAspect, State: station.AccessoryReportKnown, Quality: station.AccessoryReportAssumed, ObservedAt: now}
		// Best-effort buffered notifications; snapshot state is authoritative.
		select {
		case s.extendedAccessoryStateEvents <- event:
		default:
		}
	}
	return nil
}

func (s *Simulator) ExtendedAccessoryStateEvents() <-chan station.ExtendedAccessoryStateEvent {
	return s.extendedAccessoryStateEvents
}

func (s *Simulator) ExtendedAccessoryCommands() []ExtendedAccessoryCommandRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]ExtendedAccessoryCommandRecord{}, s.state.ExtendedCommands...)
}

func cloneExtendedAccessoryState(value ExtendedAccessoryState) ExtendedAccessoryState {
	if value.Aspect != nil {
		aspect := *value.Aspect
		value.Aspect = &aspect
	}
	return value
}

func (s *Simulator) invalidateExtendedAccessoriesLocked() {
	for address, value := range s.state.ExtendedAccessories {
		value.Aspect = nil
		value.Quality = ""
		s.state.ExtendedAccessories[address] = value
	}
}
