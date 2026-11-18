package service

import (
	"reflect"
	"time"

	"github.com/agm650/TrainPilot-server/internal/model"
)

func (s *SignalService) occupancySnapshot() OccupancySnapshot {
	if s.occupancy == nil {
		return OccupancySnapshot{Blocks: map[string]model.BlockOccupancy{}}
	}
	return s.occupancy.Snapshot()
}

func knownSignalOccupancy(snapshot OccupancySnapshot, blockID string) bool {
	value := snapshot.Blocks[blockID].State
	return value == model.OccupancyFree || value == model.OccupancyOccupied
}

// Caller holds mu. No database or transport access belongs in this calculation.
func (s *SignalService) calculateLocked(snapshot OccupancySnapshot) {
	inputs := make(map[string]BALSignalInput, len(s.entries))
	for id, r := range s.entries {
		available := s.usableLocked() && s.signalCommandErrorLocked(r, r.definition.FallbackAspect) == nil && !r.stopRequired
		r.state.Synchronized = available && r.state.Error == nil && knownSignalOccupancy(snapshot, r.definition.ProtectedBlockID)
		state := cloneSignalState(r.state)
		state.ComputedAspect = ""
		state.InhibitedReason = ""
		state.CalculationRevision = 0
		state.OccupancyRevision = 0
		inputs[id] = BALSignalInput{Definition: r.definition, Profile: r.profile, Occupancy: snapshot.Blocks[r.definition.ProtectedBlockID].State, Available: available, State: state}
	}
	if reflect.DeepEqual(inputs, s.lastCalculationInputs) {
		return
	}
	s.lastCalculationInputs = inputs
	s.calculationRevision++
	decisions := CalculateBAL(inputs)
	for id, r := range s.entries {
		decision := decisions[id]
		if r.state.CalculationRevision == 0 || r.state.ComputedAspect != decision.Aspect || r.state.OccupancyRevision != snapshot.Revision {
			r.state.CalculationRevision = s.calculationRevision
			r.state.OccupancyRevision = snapshot.Revision
		}
		r.state.ComputedAspect = decision.Aspect
		if r.definition.Mode != model.SignalModeAutomatic {
			continue
		}
		target := decision.Aspect
		if r.stopRequired || s.signalCommandErrorLocked(r, target) != nil {
			target = r.definition.FallbackAspect
		}
		if target != r.state.DesiredAspect {
			if r.cancel != nil {
				r.cancel()
				r.cancel = nil
			}
			r.generation++
			r.state.Pending = false
			r.fence = time.Now()
			r.state.DesiredAspect = target
			r.state.ReportedAspect = nil
			r.state.ReportQuality = nil
			s.notify()
		}
		r.state.InhibitedReason = decision.Reason
		if r.stopRequired {
			r.state.InhibitedReason = "synchronizing"
		}
	}
}
