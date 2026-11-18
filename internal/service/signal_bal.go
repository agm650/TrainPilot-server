package service

import "github.com/agm650/TrainPilot-server/internal/model"

// BALSignalInput belongs to one coherent calculation snapshot. Available
// includes station, power, configuration and endpoint validity. State must be
// from the current synchronization generation, never restored from persistence.
type BALSignalInput struct {
	Definition model.SignalDefinition
	Profile    model.SignalProfile
	Occupancy  model.OccupancyState
	Available  bool
	State      model.SignalState
}

type BALDecision struct {
	Aspect       model.SignalAspectID
	Reason       string
	Synchronized bool
}

// CalculateBAL has no clock, transport, store or mutation. Two bounded passes
// suffice: a downstream stop depends on its own protected block, not recursive
// graph traversal. Opening further requires a usable downstream command already
// sent in this generation. Later successful writes trigger another calculation.
func CalculateBAL(inputs map[string]BALSignalInput) map[string]BALDecision {
	base := make(map[string]BALDecision, len(inputs))
	normalized := make(map[string]BALSignalInput, len(inputs))
	for id, input := range inputs {
		decision := BALDecision{Aspect: input.Profile.FallbackAspect, Reason: "invalid_configuration"}
		definition, err := model.NormalizeSignalDefinition(input.Definition, input.Profile)
		if err != nil || definition.ID != id {
			base[id] = decision
			continue
		}
		input.Definition = definition
		normalized[id] = input
		decision.Aspect = definition.FallbackAspect
		switch {
		case definition.Downstream.Kind == model.SignalDownstreamSignal && inputs[definition.Downstream.SignalID].Definition.ID == "":
		case !input.Available:
			decision.Reason = "station_or_endpoint_unavailable"
		case input.State.Error != nil:
			decision.Reason = "command_failed"
		case input.Occupancy == model.OccupancyOccupied:
			decision.Reason = "protected_block_occupied"
			decision.Synchronized = true
		case input.Occupancy != model.OccupancyFree:
			decision.Reason = "protected_block_unknown"
		case input.Profile.RuleSetID != model.SignalRuleSetBAL3:
			decision.Reason = "unsupported_ruleset"
		default:
			caution, _ := input.Profile.AspectForRole(model.SignalRoleCaution)
			decision = BALDecision{Aspect: caution.ID, Reason: "downstream_restrictive_or_unknown", Synchronized: true}
		}
		base[id] = decision
	}
	result := make(map[string]BALDecision, len(base))
	for id, decision := range base {
		input, valid := normalized[id]
		aspect, _ := input.Profile.Aspect(decision.Aspect)
		if !valid || aspect.Role != model.SignalRoleCaution {
			result[id] = decision
			continue
		}
		if input.Definition.Downstream.Kind == model.SignalDownstreamTerminal {
			decision.Reason = "terminal_stop"
			result[id] = decision
			continue
		}
		nextID := input.Definition.Downstream.SignalID
		next, nextValid := normalized[nextID]
		nextBase := base[nextID]
		nextAspect, _ := next.Profile.Aspect(nextBase.Aspect)
		// A new stop invalidates reliance on an old downstream opening at once.
		if nextValid && next.Available && next.State.Error == nil && next.Occupancy == model.OccupancyFree &&
			(next.Definition.Mode == model.SignalModeManual || nextAspect.Role != model.SignalRoleStop) && usableBALDownstream(next) {
			proceed, _ := input.Profile.AspectForRole(model.SignalRoleProceed)
			decision.Aspect = proceed.ID
			decision.Reason = "downstream_available"
		}
		result[id] = decision
	}
	return result
}

func usableBALDownstream(input BALSignalInput) bool {
	state := input.State
	if state.Pending || !state.Synchronized || state.Error != nil || state.CommandedAspect == nil || *state.CommandedAspect != state.DesiredAspect {
		return false
	}
	if state.ReportedAspect != nil && *state.ReportedAspect != state.DesiredAspect {
		return false
	}
	aspect, found := input.Profile.Aspect(state.DesiredAspect)
	return found && (aspect.Role == model.SignalRoleCaution || aspect.Role == model.SignalRoleProceed)
}
