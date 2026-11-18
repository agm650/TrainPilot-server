package service

import (
	"context"
	"errors"
	"time"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/store"
)

var (
	ErrSignalUnsafe               = errors.New("protected block must be known free")
	ErrSignalBusy                 = errors.New("signal command is already pending")
	ErrSignalMode                 = errors.New("signal must be in manual mode")
	ErrSignalAutomaticUnavailable = errors.New("automatic signalling ruleset is unavailable for this profile")
)

func (s *SignalService) blockFreeLocked(r *signalRuntime) bool {
	return s.occupancy != nil && s.occupancy.BlockState(r.definition.ProtectedBlockID).State == model.OccupancyFree
}

// CommandAspect validates the request before changing the desired state.
// A stop request cancels a pending opening; ordinary commands never cancel stops.
func (s *SignalService) CommandAspect(ctx context.Context, user model.User, id string, target model.SignalAspectID) (err error) {
	registered := false
	defer func() {
		err = errors.Join(err, s.auditSignal(ctx, user, "signal.aspect", id, string(target), "result", err))
		if registered {
			s.wg.Done()
		}
	}()
	if !Allowed(user.Role, PermissionDispatch) {
		return ErrPermissionDenied
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.occupancy != nil {
		if err := s.refreshSignalOccupancy(ctx); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.syncFeedbackEpochLocked()
	s.calculateLocked(s.occupancySnapshot())
	r := s.entries[id]
	if r == nil {
		s.mu.Unlock()
		return store.ErrNotFound
	}
	aspect, found := r.profile.Aspect(target)
	if !found {
		s.mu.Unlock()
		return invalid("aspect is not declared by the signal profile")
	}
	restrictive := aspect.Role == model.SignalRoleStop
	if r.definition.Mode != model.SignalModeManual {
		s.mu.Unlock()
		return ErrSignalMode
	}
	if !s.station.Capabilities().ExtendedAccessoryControl {
		s.mu.Unlock()
		return station.ErrUnsupported
	}
	if s.signalCommandErrorLocked(r, r.definition.FallbackAspect) != nil {
		s.mu.Unlock()
		return station.ErrUnsupported
	}
	if err := s.signalCommandErrorLocked(r, target); err != nil {
		s.mu.Unlock()
		return err
	}
	if s.ctx == nil || s.ctx.Err() != nil || !s.usableLocked() {
		s.mu.Unlock()
		return ErrSignalUnavailable
	}
	if !restrictive && !s.blockFreeLocked(r) {
		s.mu.Unlock()
		return ErrSignalUnsafe
	}
	if !restrictive && r.stopRequired {
		s.mu.Unlock()
		return ErrSignalUnavailable
	}
	if r.state.Pending && !restrictive {
		s.mu.Unlock()
		return ErrSignalBusy
	}
	if r.cancel != nil {
		r.cancel()
	}
	r.generation++
	generation := r.generation
	r.fence = time.Now()
	r.state.DesiredAspect = target
	r.state.Pending = true
	r.state.Error = nil
	r.state.ReportedAspect = nil
	r.state.ReportQuality = nil
	r.needsFallback = false
	commandCtx, cancel := context.WithTimeout(s.ctx, signalCommandTimeout)
	stop := context.AfterFunc(ctx, cancel)
	r.cancel = cancel
	s.wg.Add(1)
	registered = true
	s.publishStateLocked(r)
	s.mu.Unlock()
	defer cancel()
	defer stop()
	if err := s.auditSignal(ctx, user, "signal.aspect", id, string(target), "attempt", nil); err != nil {
		cancel()
		_ = s.dispatch(commandCtx, id, r, generation, target, restrictive)
		return err
	}
	return s.dispatch(commandCtx, id, r, generation, target, restrictive)
}

// A feedback stream can be renewed while public connectivity remains online.
// Its old observations and openings must still cross a new stop boundary.
func (s *SignalService) syncFeedbackEpochLocked() {
	provider, ok := s.station.(station.FeedbackStateProvider)
	if !ok {
		return
	}
	state := provider.FeedbackState()
	if !s.feedbackEpochKnown || state.Generation != s.feedbackEpoch.Generation || state.PowerGeneration != s.feedbackEpoch.PowerGeneration {
		for _, r := range s.entries {
			s.invalidateLocked(r, "station_resynchronizing")
		}
	}
	s.feedbackEpoch = state
	s.feedbackEpochKnown = true
}

func (s *SignalService) refreshSignalOccupancy(ctx context.Context) error {
	if provider, ok := s.station.(station.FeedbackStateProvider); ok {
		if err := s.occupancy.SyncStationFeedback(ctx, provider); err != nil {
			return err
		}
	}
	return s.occupancy.Recompute(ctx)
}

// Mode is configuration and survives export/restart. Runtime targets never do.
func (s *SignalService) SetMode(ctx context.Context, user model.User, id string, mode model.SignalMode) (err error) {
	defer func() {
		err = errors.Join(err, s.auditSignal(ctx, user, "signal.mode", id, string(mode), "result", err))
	}()
	if !Allowed(user.Role, PermissionDispatch) {
		return ErrPermissionDenied
	}
	if mode != model.SignalModeManual && mode != model.SignalModeAutomatic {
		return invalid("mode must be manual or automatic")
	}
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.entries[id]
	if r == nil {
		return store.ErrNotFound
	}
	if mode == model.SignalModeAutomatic && r.profile.RuleSetID != model.SignalRuleSetBAL3 {
		return ErrSignalAutomaticUnavailable
	}
	if r.definition.Mode == mode {
		return nil
	}
	if err := s.auditSignal(ctx, user, "signal.mode", id, string(mode), "attempt", nil); err != nil {
		return err
	}
	d, err := s.store.Signal(ctx, id)
	if err != nil {
		return err
	}
	d.Mode = mode
	if err := s.store.SetSignal(ctx, d); err != nil {
		return err
	}
	r.definition = d
	s.invalidateLocked(r, "")
	s.setReasonLocked(r)
	s.events.Publish("signal.mode.changed", model.SignalModeChanged{SignalID: id, Mode: mode})
	s.publishStateLocked(r)
	s.notify()
	return nil
}

func (s *SignalService) auditSignal(ctx context.Context, user model.User, action, id, value, phase string, result error) error {
	auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), signalCommandTimeout)
	defer cancel()
	code := "ok"
	switch {
	case errors.Is(result, ErrPermissionDenied):
		code = "denied"
	case errors.Is(result, ErrValidation), errors.Is(result, station.ErrInvalidExtendedAccessoryAddress), errors.Is(result, station.ErrInvalidExtendedAccessoryAspect):
		code = "invalid"
	case errors.Is(result, ErrSignalUnsafe):
		code = "unsafe"
	case errors.Is(result, ErrSignalBusy):
		code = "busy"
	case errors.Is(result, context.Canceled), errors.Is(result, context.DeadlineExceeded):
		code = "canceled"
	case result != nil:
		code = "failed"
	}
	return s.store.AddAudit(auditCtx, store.AuditEvent{ID: newID(), OccurredAt: time.Now().UTC(), ActorType: "user", ActorID: user.ID, Action: action, TargetType: "signal", TargetID: id, Source: "server", Details: map[string]any{"value": value, "phase": phase, "result": code}})
}
