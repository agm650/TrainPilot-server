package service

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/store"
)

var ErrSignalUnavailable = errors.New("signal is unavailable")

const signalCommandTimeout = 2 * time.Second

type signalRuntime struct {
	definition    model.SignalDefinition
	profile       model.SignalProfile
	state         model.SignalState
	generation    uint64
	fence         time.Time
	cancel        context.CancelFunc
	needsFallback bool
	published     *model.SignalState
	stopRequired  bool
	retryAt       time.Time
	retryDelay    time.Duration
	lastSent      time.Time
}

// SignalService owns runtime state. No aspect or observation is persisted.
// Status is fed by ControlService; it never competes for its status channel.
type SignalService struct {
	store                 *store.Store
	station               station.CommandStation
	events                *events.Bus
	occupancy             *OccupancyService
	mu                    sync.RWMutex
	entries               map[string]*signalRuntime
	status                station.Status
	ctx                   context.Context
	cancel                context.CancelFunc
	wake                  chan struct{}
	wg                    sync.WaitGroup
	gate                  *priorityCommandGate
	lifecycle             sync.Mutex
	reloadMu              sync.Mutex
	configurationValid    bool
	lastLayoutSequence    uint64
	feedbackEpoch         station.FeedbackState
	feedbackEpochKnown    bool
	calculationRevision   uint64
	lastCalculationInputs map[string]BALSignalInput
	refreshInterval       time.Duration
	queue                 *signalCommandQueue
	emergencyStop         bool
}

func NewSignalService(db *store.Store, st station.CommandStation, bus *events.Bus, occupancy *OccupancyService) *SignalService {
	return &SignalService{store: db, station: st, events: bus, occupancy: occupancy, entries: map[string]*signalRuntime{}, wake: make(chan struct{}, 1), gate: newPriorityCommandGate()}
}

func (s *SignalService) Start(ctx context.Context) error {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		return errors.New("signal service already started")
	}
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.queue = newSignalCommandQueue()
	s.mu.Unlock()
	if err := s.Reload(ctx); err != nil {
		s.mu.Lock()
		s.cancel()
		s.cancel = nil
		s.mu.Unlock()
		return err
	}
	ch, overflow, unsubscribe := s.events.SubscribeTypesWithOverflow(64, "layout.imported", "block.occupancy.changed")
	s.wg.Add(1)
	go s.runCommands(s.ctx, s.queue)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer unsubscribe()
		var reports <-chan station.ExtendedAccessoryStateEvent
		if p, ok := s.station.(station.ExtendedAccessoryStateEventProvider); ok {
			reports = p.ExtendedAccessoryStateEvents()
		}
		if p, ok := s.station.(station.StatusProvider); ok {
			probe, cancel := context.WithTimeout(s.ctx, signalCommandTimeout)
			status, err := p.Status(probe)
			cancel()
			if err == nil {
				s.ObserveStatus(status)
			}
		}
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case report, ok := <-reports:
				if !ok {
					reports = nil
				} else {
					s.observeReport(report)
				}
			case <-overflow:
				_ = s.Reload(s.ctx) // A lost boundary invalidates all cached observations.
			case e, ok := <-ch:
				if !ok {
					return
				}
				if e.Type == "layout.imported" {
					_ = s.reloadForEvent(s.ctx, e.Sequence)
				}
			case <-s.wake:
			case <-ticker.C:
			}
			s.reconcile()
		}
	}()
	return nil
}

func (s *SignalService) Close() {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	s.mu.Lock()
	if s.cancel == nil {
		s.mu.Unlock()
		return
	}
	s.cancel()
	for _, r := range s.entries {
		s.invalidateLocked(r, "stopped")
	}
	s.publishAllLocked()
	s.mu.Unlock()
	s.wg.Wait()
	s.mu.Lock()
	s.cancel = nil
	s.mu.Unlock()
}

func (s *SignalService) notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *SignalService) Reload(ctx context.Context) error {
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	return s.reloadLocked(ctx)
}

func (s *SignalService) reloadForEvent(ctx context.Context, sequence uint64) error {
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	if sequence <= s.lastLayoutSequence {
		return nil
	}
	s.Invalidate("configuration_unavailable")
	return errors.Join(s.resetLayoutOccupancy(ctx), s.reloadLocked(ctx))
}

func (s *SignalService) reloadLocked(ctx context.Context) error {
	profiles, definitions, err := s.store.SignalConfiguration(ctx)
	if err != nil {
		s.Invalidate("configuration_unavailable")
		return err
	}
	byProfile := map[string]model.SignalProfile{}
	for _, p := range profiles {
		byProfile[p.ID] = p
	}
	next := map[string]*signalRuntime{}
	for _, d := range definitions {
		p := byProfile[d.ProfileID]
		state, err := model.NewSignalState(d, p)
		if err != nil {
			s.Invalidate("invalid_configuration")
			return err
		}
		d, _ = model.NormalizeSignalDefinition(d, p)
		next[d.ID] = &signalRuntime{definition: d, profile: p, state: state, fence: time.Now(), needsFallback: true, stopRequired: true}
	}
	s.mu.Lock()
	for _, r := range s.entries {
		if r.cancel != nil {
			r.cancel()
		}
	}
	s.entries = next
	s.lastCalculationInputs = nil
	s.configurationValid = true
	s.lastLayoutSequence = s.events.CurrentSequence()
	for _, r := range next {
		s.setReasonLocked(r)
	}
	s.publishAllLocked()
	s.mu.Unlock()
	s.notify()
	return nil
}

// ApplyLayout cancels the old configuration before its transaction can commit.
// Even a failed import reloads the retained definitions at their stop fallback.
func (s *SignalService) ApplyLayout(ctx context.Context, apply func() error) error {
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	s.Invalidate("configuration_unavailable")
	release, err := s.gate.acquire(ctx, true)
	if err == nil {
		defer release()
		err = apply()
	}
	reloadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), signalCommandTimeout)
	defer cancel()
	err = errors.Join(err, s.resetLayoutOccupancy(reloadCtx))
	return errors.Join(err, s.reloadLocked(reloadCtx))
}

func (s *SignalService) Signals() []model.Signal {
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]model.Signal, 0, len(s.entries))
	for _, r := range s.entries {
		d := r.definition
		d.Endpoint.AspectCodes = make(map[model.SignalAspectID]int, len(r.definition.Endpoint.AspectCodes))
		for id, code := range r.definition.Endpoint.AspectCodes {
			d.Endpoint.AspectCodes[id] = code
		}
		result = append(result, model.Signal{Definition: d, State: cloneSignalState(r.state)})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Definition.ID < result[j].Definition.ID })
	return result
}

func (s *SignalService) Snapshot() []model.SignalState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]model.SignalState, 0, len(s.entries))
	for _, r := range s.entries {
		result = append(result, cloneSignalState(r.state))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (s *SignalService) State(id string) (model.SignalState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r := s.entries[id]
	if r == nil {
		return model.SignalState{}, store.ErrNotFound
	}
	return cloneSignalState(r.state), nil
}

func cloneSignalState(v model.SignalState) model.SignalState {
	if v.CommandedAspect != nil {
		x := *v.CommandedAspect
		v.CommandedAspect = &x
	}
	if v.ReportedAspect != nil {
		x := *v.ReportedAspect
		v.ReportedAspect = &x
	}
	if v.ReportQuality != nil {
		x := *v.ReportQuality
		v.ReportQuality = &x
	}
	if v.Error != nil {
		x := *v.Error
		v.Error = &x
	}
	return v
}

func signalPowerUsable(v station.Status) bool {
	return v.Connectivity == station.Online && v.TrackPower == "on" && !v.ShortCircuit && !v.InternalShortCircuit && !v.ExternalShortCircuit && !v.PowerLost && !v.ProgrammingMode
}

func (s *SignalService) usableLocked() bool {
	if !s.configurationValid {
		return false
	}
	if !signalPowerUsable(s.status) {
		return false
	}
	if p, ok := s.station.(station.HealthProvider); ok && p.Health().Connectivity != station.Online {
		return false
	}
	return true
}

func (s *SignalService) ObserveStatus(v station.Status) {
	s.mu.Lock()
	was := signalPowerUsable(s.status)
	s.status = v
	if v.EmergencyStop && !s.emergencyStop {
		s.emergencyStop = true
		for _, r := range s.entries {
			s.invalidateLocked(r, "emergency_stop")
		}
	}
	if !signalPowerUsable(v) || !was {
		for _, r := range s.entries {
			s.invalidateLocked(r, "station_or_power_unavailable")
			s.setReasonLocked(r)
		}
	}
	s.publishAllLocked()
	s.mu.Unlock()
	s.notify()
}

func (s *SignalService) Invalidate(reason string) {
	s.mu.Lock()
	if reason == "configuration_unavailable" || reason == "invalid_configuration" {
		s.configurationValid = false
	}
	for _, r := range s.entries {
		s.invalidateLocked(r, reason)
	}
	s.publishAllLocked()
	s.mu.Unlock()
	s.notify()
}

func (s *SignalService) invalidateLocked(r *signalRuntime, reason string) {
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	r.generation++
	r.fence = time.Now()
	r.state = model.SignalState{ID: r.definition.ID, ComputedAspect: r.definition.FallbackAspect, DesiredAspect: r.definition.FallbackAspect, InhibitedReason: reason}
	r.needsFallback = true
	r.stopRequired = true
	r.retryAt = time.Time{}
	r.retryDelay = 0
}

func (s *SignalService) setReasonLocked(r *signalRuntime) {
	switch {
	case !s.station.Capabilities().ExtendedAccessoryControl:
		r.state.InhibitedReason = "unsupported"
	case s.signalCommandErrorLocked(r, r.definition.FallbackAspect) != nil:
		r.state.InhibitedReason = "unsupported_endpoint"
	case !s.usableLocked():
		r.state.InhibitedReason = "station_or_power_unavailable"
	default:
		r.state.InhibitedReason = ""
	}
}

func (s *SignalService) reconcile() {
	if s.occupancy != nil {
		if err := s.refreshSignalOccupancy(s.ctx); err != nil {
			s.Invalidate("occupancy_unavailable")
			return
		}
	}
	snapshot := s.occupancySnapshot()
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.publishAllLocked()
	if s.ctx.Err() != nil {
		return
	}
	s.syncFeedbackEpochLocked()
	s.calculateLocked(snapshot)
	for id, r := range s.entries {
		if !s.usableLocked() {
			if r.state.CommandedAspect != nil || r.state.Pending {
				s.invalidateLocked(r, "station_or_power_unavailable")
			}
			s.setReasonLocked(r)
			continue
		}
		if s.signalCommandErrorLocked(r, r.definition.FallbackAspect) != nil {
			if r.state.CommandedAspect != nil || r.state.Pending {
				s.invalidateLocked(r, "unsupported_endpoint")
			}
			r.state.Synchronized = false
			s.setReasonLocked(r)
			continue
		}
		if r.definition.Mode == model.SignalModeManual {
			s.setReasonLocked(r)
			desired, _ := r.profile.Aspect(r.state.DesiredAspect)
			if snapshot.Blocks[r.definition.ProtectedBlockID].State != model.OccupancyFree && desired.Role != model.SignalRoleStop {
				s.invalidateLocked(r, "protected_block_not_free")
			}
			r.state.Synchronized = r.state.Error == nil && !r.stopRequired && knownSignalOccupancy(snapshot, r.definition.ProtectedBlockID)
		}
		target := r.state.DesiredAspect
		if r.stopRequired {
			target = r.definition.FallbackAspect
		}
		if !r.needsFallback && r.state.CommandedAspect != nil && *r.state.CommandedAspect == target {
			if s.refreshInterval == 0 || time.Since(r.lastSent) < s.refreshInterval {
				continue
			}
		}
		if time.Now().Before(r.retryAt) {
			continue
		}
		if r.state.Pending || !s.station.Capabilities().ExtendedAccessoryControl {
			continue
		}
		if r.state.Error != nil && !r.needsFallback {
			continue
		}
		r.needsFallback = false
		r.state.Pending = true
		r.generation++
		generation := r.generation
		r.fence = time.Now()
		ctx, cancel := context.WithTimeout(s.ctx, signalCommandTimeout)
		r.cancel = cancel
		r.state.DesiredAspect = target
		aspect, _ := r.profile.Aspect(target)
		restrictive := aspect.Role == model.SignalRoleStop
		if _, err := s.enqueueSignalLocked(ctx, cancel, id, r, generation, target, restrictive); err != nil {
			cancel()
			r.cancel = nil
			r.state.Pending = false
			r.needsFallback = true
		}
	}
}

func (s *SignalService) signalCommandErrorLocked(r *signalRuntime, target model.SignalAspectID) error {
	return station.ValidateExtendedAccessoryCommand(s.station, station.ExtendedAccessoryCommand{Address: r.definition.Endpoint.LinearAddress, Aspect: r.definition.Endpoint.AspectCodes[target]})
}

func (s *SignalService) dispatch(ctx context.Context, id string, r *signalRuntime, generation uint64, target model.SignalAspectID, restrictive bool) error {
	priority := commandOrdinary
	if restrictive {
		priority = commandSignalStop
	}
	s.mu.RLock()
	var activeCancel context.CancelFunc
	if s.entries[id] == r && r.generation == generation {
		activeCancel = r.cancel
	}
	s.mu.RUnlock()
	err := ctx.Err()
	if err == nil && !restrictive && s.occupancy != nil {
		// Queries run outside the write gate. A superseded target must not
		// cancel shared SQLite reads; generation is checked after acquisition.
		err = s.refreshSignalOccupancy(s.ctx)
	}
	var release func()
	if err == nil {
		release, err = s.gate.acquirePriority(ctx, priority, activeCancel)
	}
	if err == nil {
		snapshot := s.occupancySnapshot()
		s.mu.Lock()
		if r.definition.Mode == model.SignalModeAutomatic {
			s.calculateLocked(snapshot)
		}
		valid := s.entries[id] == r && r.generation == generation && s.usableLocked() && (restrictive || (!s.emergencyStop && s.blockFreeLocked(r)))
		command := station.ExtendedAccessoryCommand{Address: r.definition.Endpoint.LinearAddress, Aspect: r.definition.Endpoint.AspectCodes[target]}
		s.mu.Unlock()
		if !valid {
			err = ErrSignalUnavailable
		} else if err == nil {
			err = station.SendExtendedAccessory(ctx, s.station, command)
		}
		release()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.publishAllLocked()
	if s.entries[id] != r || r.generation != generation {
		s.events.Publish("signal.command.failed", model.SignalCommandFailed{SignalID: id, Aspect: target, Reason: "preempted"})
		return ErrSafetyPreempted
	}
	if err == nil && (!s.usableLocked() || (!restrictive && !s.blockFreeLocked(r))) {
		s.invalidateLocked(r, "safety_changed")
		s.notify()
		s.events.Publish("signal.command.failed", model.SignalCommandFailed{SignalID: id, Aspect: target, Reason: "safety_changed"})
		return ErrSafetyPreempted
	}
	r.cancel = nil
	r.state.Pending = false
	if err != nil {
		r.state.Error = &model.SignalError{Code: "command_failed", Message: "extended accessory command failed"}
		r.state.CommandedAspect = nil
		r.state.ReportedAspect = nil
		r.state.ReportQuality = nil
		r.state.ComputedAspect = r.definition.FallbackAspect
		r.state.DesiredAspect = r.definition.FallbackAspect
		r.state.Synchronized = false
		r.stopRequired = true
		r.needsFallback = true
		s.scheduleRetryLocked(r)
		s.events.Publish("signal.command.failed", model.SignalCommandFailed{SignalID: id, Aspect: target, Reason: "command_failed"})
		return err
	}
	value := target
	r.state.CommandedAspect = &value
	r.state.Error = nil
	r.retryAt = time.Time{}
	r.retryDelay = 0
	r.lastSent = time.Now()
	if target == r.definition.FallbackAspect {
		r.stopRequired = false
	}
	s.events.Publish("signal.commanded", model.SignalCommanded{SignalID: id, Aspect: target})
	s.notify()
	return nil
}

func (s *SignalService) observeReport(e station.ExtendedAccessoryStateEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.publishAllLocked()
	if !s.usableLocked() {
		return
	}
	for _, r := range s.entries {
		if r.definition.Endpoint.LinearAddress != e.Address || e.ObservedAt.IsZero() || e.ObservedAt.Before(r.fence) {
			continue
		}
		if r.state.CommandedAspect == nil && !r.state.Pending {
			continue
		}
		r.state.ReportedAspect = nil
		r.state.ReportQuality = nil
		if !e.HasKnownAspect() || !e.Quality.Valid() {
			if !r.state.Pending {
				s.resetReportedSignalLocked(r)
			}
			continue
		}
		for id, code := range r.definition.Endpoint.AspectCodes {
			if code == *e.Aspect {
				value, quality := id, e.Quality
				r.state.ReportedAspect = &value
				r.state.ReportQuality = &quality
				break
			}
		}
		if !r.state.Pending && (r.state.ReportedAspect == nil || *r.state.ReportedAspect != *r.state.CommandedAspect) {
			s.resetReportedSignalLocked(r)
		}
	}
	s.notify()
}

func (s *SignalService) publishAllLocked() {
	for _, r := range s.entries {
		s.publishStateLocked(r)
	}
}

func (s *SignalService) publishStateLocked(r *signalRuntime) {
	if r.published != nil && reflect.DeepEqual(*r.published, r.state) {
		return
	}
	state := cloneSignalState(r.state)
	saved := cloneSignalState(state)
	r.published = &saved
	s.events.Publish("signal.aspect.changed", state)
}
