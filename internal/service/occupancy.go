package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/observability"
	"github.com/agm650/TrainPilot-server/internal/store"
)

var (
	ErrOccupancySequenceStale    = errors.New("occupancy sequence is stale")
	ErrOccupancySequenceConflict = errors.New("occupancy sequence conflicts with accepted observation")
)

type occupancySourceKey struct {
	providerID string
	sensorID   string
}

type occupancyDiagnostics struct {
	stateConflict    bool
	identityConflict bool
}

type OccupancyService struct {
	store   *store.Store
	events  *events.Bus
	clock   clock.Clock
	metrics *observability.Metrics

	mu           sync.RWMutex
	observations map[occupancySourceKey]model.OccupancyObservation
	states       map[string]model.BlockOccupancy
	availability map[string]bool
	startedAt    time.Time
	diagnostics  map[string]occupancyDiagnostics
}

func NewOccupancyService(s *store.Store, bus *events.Bus, c clock.Clock) *OccupancyService {
	if c == nil {
		c = clock.Real{}
	}
	now := c.Now().UTC()
	service := &OccupancyService{
		store:        s,
		events:       bus,
		clock:        c,
		observations: make(map[occupancySourceKey]model.OccupancyObservation),
		states:       make(map[string]model.BlockOccupancy),
		availability: make(map[string]bool),
		startedAt:    now,
		diagnostics:  make(map[string]occupancyDiagnostics),
	}
	if blocks, err := s.ListBlocks(context.Background()); err == nil {
		for _, block := range blocks {
			service.states[block.ID] = model.NewUnknownBlockOccupancy(block.ID, now)
		}
	}
	return service
}

func (s *OccupancyService) SetMetrics(metrics *observability.Metrics) {
	configs, _ := s.resolvedMappings(context.Background())
	s.mu.Lock()
	s.metrics = metrics
	s.updateMetricsLocked(configs, s.clock.Now().UTC())
	s.mu.Unlock()
}

// Run performs central freshness sweeps until ctx is canceled.
func (s *OccupancyService) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		return errors.New("occupancy sweep interval must be positive")
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := s.Recompute(ctx); err != nil && !errors.Is(err, context.Canceled) {
				return err
			}
		}
	}
}

func (s *OccupancyService) Observe(ctx context.Context, observation model.OccupancyObservation) error {
	return s.ObserveBatch(ctx, []model.OccupancyObservation{observation})
}

// ObserveBatch validates the complete batch before changing runtime state.
func (s *OccupancyService) ObserveBatch(ctx context.Context, observations []model.OccupancyObservation) error {
	if len(observations) == 0 {
		s.reject("invalid")
		return fmt.Errorf("%w: at least one observation is required", model.ErrInvalidOccupancy)
	}
	configs, err := s.resolvedMappings(ctx)
	if err != nil {
		s.reject("mapping")
		return err
	}
	type preparedObservation struct {
		key         occupancySourceKey
		mapping     model.ResolvedOccupancySensorMapping
		observation model.OccupancyObservation
	}
	now := s.clock.Now().UTC()
	prepared := make([]preparedObservation, 0, len(observations))
	seen := make(map[occupancySourceKey]struct{}, len(observations))
	for _, observation := range observations {
		if err := model.ValidateOccupancyObservation(observation, true); err != nil {
			s.reject("invalid")
			return err
		}
		key := occupancySourceKey{providerID: observation.ProviderID, sensorID: observation.SensorID}
		if _, duplicate := seen[key]; duplicate {
			s.reject("invalid")
			return fmt.Errorf("%w: duplicate sensor in observation batch", model.ErrInvalidOccupancy)
		}
		seen[key] = struct{}{}
		mapping, found := mappingForSource(configs, key)
		if !found {
			s.reject("mapping")
			return store.ErrNotFound
		}
		observation.ReceivedAt = now
		prepared = append(prepared, preparedObservation{key: key, mapping: mapping, observation: observation})
	}

	s.mu.Lock()
	for _, item := range prepared {
		previous, exists := s.observations[item.key]
		if !exists {
			continue
		}
		switch {
		case item.observation.Sequence < previous.Sequence:
			s.mu.Unlock()
			s.reject("stale")
			return ErrOccupancySequenceStale
		case item.observation.Sequence == previous.Sequence && sameObservationPayload(previous, item.observation):
			continue
		case item.observation.Sequence == previous.Sequence:
			s.mu.Unlock()
			s.reject("conflict")
			return ErrOccupancySequenceConflict
		}
	}
	affectedBlocks := make(map[string]struct{})
	for _, item := range prepared {
		if previous, exists := s.observations[item.key]; exists && item.observation.Sequence == previous.Sequence {
			continue
		}
		s.observations[item.key] = item.observation
		affectedBlocks[item.mapping.BlockID] = struct{}{}
	}
	changes := make([]model.BlockOccupancy, 0, len(affectedBlocks))
	for blockID := range affectedBlocks {
		if changed := s.recomputeBlockLocked(blockID, configs, now); changed != nil {
			changes = append(changes, *changed)
		}
	}
	s.updateMetricsLocked(configs, now)
	s.mu.Unlock()
	for range prepared {
		s.accept()
	}
	for i := range changes {
		s.publish(&changes[i])
	}
	return nil
}

func (s *OccupancyService) BlockState(blockID string) model.BlockOccupancy {
	s.mu.RLock()
	state, ok := s.states[blockID]
	s.mu.RUnlock()
	if ok {
		return cloneBlockOccupancy(state)
	}
	return model.NewUnknownBlockOccupancy(blockID, s.startedAt)
}

func (s *OccupancyService) ProviderStates(ctx context.Context, blockID string) ([]model.SensorOccupancyState, error) {
	configs, err := s.resolvedMappings(ctx)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now().UTC()
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []model.SensorOccupancyState
	for _, config := range configs {
		if config.BlockID != blockID {
			continue
		}
		key := occupancySourceKey{providerID: config.ProviderID, sensorID: config.SensorID}
		observation, exists := s.observations[key]
		available := s.providerAvailableLocked(config.ProviderID)
		state := model.SensorOccupancyState{
			ProviderID: config.ProviderID,
			SensorID:   config.SensorID,
			BlockID:    config.BlockID,
			State:      model.OccupancyUnknown,
			Required:   config.Required,
			Priority:   config.Priority,
			Available:  available,
		}
		if exists {
			state.State = observation.State
			state.Sequence = observation.Sequence
			state.ObservedAt = observation.ObservedAt
			state.ReceivedAt = observation.ReceivedAt
			state.Occupant = cloneOccupant(observation.Occupant)
			state.Fresh = sourceFresh(config, observation, available, now)
		}
		result = append(result, state)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ProviderID == result[j].ProviderID {
			return result[i].SensorID < result[j].SensorID
		}
		return result[i].ProviderID < result[j].ProviderID
	})
	return result, nil
}

func (s *OccupancyService) Recompute(ctx context.Context) error {
	configs, err := s.resolvedMappings(ctx)
	if err != nil {
		return err
	}
	persistedBlocks, err := s.store.ListBlocks(ctx)
	if err != nil {
		return err
	}
	now := s.clock.Now().UTC()
	blocks := make(map[string]struct{})
	for _, block := range persistedBlocks {
		blocks[block.ID] = struct{}{}
	}
	s.mu.Lock()
	for blockID := range s.states {
		if _, exists := blocks[blockID]; !exists {
			delete(s.states, blockID)
			delete(s.diagnostics, blockID)
		}
	}
	changes := make([]model.BlockOccupancy, 0, len(blocks))
	for blockID := range blocks {
		if changed := s.recomputeBlockLocked(blockID, configs, now); changed != nil {
			changes = append(changes, *changed)
		}
	}
	s.updateMetricsLocked(configs, now)
	s.mu.Unlock()
	for i := range changes {
		s.publish(&changes[i])
	}
	return nil
}

func (s *OccupancyService) SetProviderAvailable(ctx context.Context, providerID string, available bool) error {
	configs, err := s.resolvedMappings(ctx)
	if err != nil {
		return err
	}
	blocks := make(map[string]struct{})
	for _, config := range configs {
		if config.ProviderID == providerID {
			blocks[config.BlockID] = struct{}{}
		}
	}
	now := s.clock.Now().UTC()
	s.mu.Lock()
	s.availability[providerID] = available
	if !available {
		for key := range s.observations {
			if key.providerID == providerID {
				delete(s.observations, key)
			}
		}
	}
	var changes []model.BlockOccupancy
	for blockID := range blocks {
		if changed := s.recomputeBlockLocked(blockID, configs, now); changed != nil {
			changes = append(changes, *changed)
		}
	}
	s.updateMetricsLocked(configs, now)
	s.mu.Unlock()
	for i := range changes {
		s.publish(&changes[i])
	}
	return nil
}

func (s *OccupancyService) recomputeBlockLocked(blockID string, configs []model.ResolvedOccupancySensorMapping, now time.Time) *model.BlockOccupancy {
	next, diagnostics := aggregateBlock(blockID, configs, s.observations, s.availability, now)
	s.diagnostics[blockID] = diagnostics
	previous, exists := s.states[blockID]
	if !exists {
		previous = model.NewUnknownBlockOccupancy(blockID, now)
	}
	stateChanged := previous.State != next.State
	if stateChanged {
		next.UpdatedAt = now
	} else {
		next.UpdatedAt = previous.UpdatedAt
	}
	s.states[blockID] = next
	if !stateChanged {
		return nil
	}
	changed := cloneBlockOccupancy(next)
	return &changed
}

func aggregateBlock(blockID string, configs []model.ResolvedOccupancySensorMapping, observations map[occupancySourceKey]model.OccupancyObservation, availability map[string]bool, now time.Time) (model.BlockOccupancy, occupancyDiagnostics) {
	result := model.NewUnknownBlockOccupancy(blockID, now)
	diagnostics := occupancyDiagnostics{}
	hasRequired := false
	requiredMissing := false
	hasFreshFree := false
	bestPriority := -1
	var bestOccupant *model.OccupantRef
	identityConflict := false

	for _, config := range configs {
		if config.BlockID != blockID {
			continue
		}
		if config.Required {
			hasRequired = true
		}
		key := occupancySourceKey{providerID: config.ProviderID, sensorID: config.SensorID}
		observation, exists := observations[key]
		available, set := availability[config.ProviderID]
		if !set {
			available = true
		}
		fresh := exists && sourceFresh(config, observation, available, now)
		if !fresh || observation.State == model.OccupancyUnknown {
			if config.Required {
				requiredMissing = true
			}
			continue
		}
		if observation.State == model.OccupancyFree {
			hasFreshFree = true
			continue
		}
		if observation.State != model.OccupancyOccupied {
			continue
		}
		result.State = model.OccupancyOccupied
		if observation.Occupant == nil {
			continue
		}
		switch {
		case config.Priority > bestPriority:
			bestPriority = config.Priority
			bestOccupant = cloneOccupant(observation.Occupant)
			identityConflict = false
		case config.Priority == bestPriority && bestOccupant != nil && !reflect.DeepEqual(bestOccupant, observation.Occupant):
			identityConflict = true
		}
	}
	if result.State == model.OccupancyOccupied {
		diagnostics.stateConflict = hasFreshFree
		diagnostics.identityConflict = identityConflict
		if !identityConflict {
			result.Occupant = bestOccupant
		}
		return result, diagnostics
	}
	if requiredMissing {
		return result, diagnostics
	}
	if hasRequired || hasFreshFree {
		result.State = model.OccupancyFree
	}
	return result, diagnostics
}

func (s *OccupancyService) resolvedMappings(ctx context.Context) ([]model.ResolvedOccupancySensorMapping, error) {
	mappings, err := s.store.ListOccupancySensorMappings(ctx)
	if err != nil {
		return nil, err
	}
	providers := make(map[string]model.OccupancyProvider)
	resolved := make([]model.ResolvedOccupancySensorMapping, 0, len(mappings))
	for _, mapping := range mappings {
		provider, ok := providers[mapping.ProviderID]
		if !ok {
			provider, err = s.store.OccupancyProvider(ctx, mapping.ProviderID)
			if err != nil {
				return nil, err
			}
			providers[mapping.ProviderID] = provider
		}
		item, err := mapping.Resolve(provider)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, item)
	}
	return resolved, nil
}

func (s *OccupancyService) updateMetricsLocked(configs []model.ResolvedOccupancySensorMapping, now time.Time) {
	if s.metrics == nil {
		return
	}
	var unknown, free, occupied int
	for _, state := range s.states {
		switch state.State {
		case model.OccupancyUnknown:
			unknown++
		case model.OccupancyFree:
			free++
		case model.OccupancyOccupied:
			occupied++
		}
	}
	var staleRequired, staleOptional int
	for _, config := range configs {
		key := occupancySourceKey{providerID: config.ProviderID, sensorID: config.SensorID}
		observation, exists := s.observations[key]
		if exists && sourceFresh(config, observation, s.providerAvailableLocked(config.ProviderID), now) {
			continue
		}
		if config.Required {
			staleRequired++
		} else {
			staleOptional++
		}
	}
	s.metrics.SetOccupancyBlockStateCounts(unknown, free, occupied)
	s.metrics.SetOccupancyStaleSourceCounts(staleRequired, staleOptional)
	var stateConflicts, identityConflicts int
	for _, diagnostics := range s.diagnostics {
		if diagnostics.stateConflict {
			stateConflicts++
		}
		if diagnostics.identityConflict {
			identityConflicts++
		}
	}
	s.metrics.SetOccupancyConflictCounts(stateConflicts, identityConflicts)
}

func (s *OccupancyService) providerAvailableLocked(providerID string) bool {
	available, set := s.availability[providerID]
	return !set || available
}

func (s *OccupancyService) publish(changed *model.BlockOccupancy) {
	if changed != nil && s.events != nil {
		s.events.Publish("block.occupancy.changed", changed.ChangedEvent())
	}
}

func (s *OccupancyService) accept() {
	s.mu.RLock()
	metrics := s.metrics
	s.mu.RUnlock()
	metrics.OccupancyObservationAccepted()
}

func (s *OccupancyService) reject(reason string) {
	s.mu.RLock()
	metrics := s.metrics
	s.mu.RUnlock()
	metrics.OccupancyObservationRejected(reason)
}

func mappingForSource(configs []model.ResolvedOccupancySensorMapping, key occupancySourceKey) (model.ResolvedOccupancySensorMapping, bool) {
	for _, config := range configs {
		if config.ProviderID == key.providerID && config.SensorID == key.sensorID {
			return config, true
		}
	}
	return model.ResolvedOccupancySensorMapping{}, false
}

func sourceFresh(config model.ResolvedOccupancySensorMapping, observation model.OccupancyObservation, available bool, now time.Time) bool {
	if !available {
		return false
	}
	return config.StaleAfter <= 0 || now.Sub(observation.ReceivedAt) <= config.StaleAfter
}

func sameObservationPayload(left, right model.OccupancyObservation) bool {
	return left.ProviderID == right.ProviderID &&
		left.SensorID == right.SensorID &&
		left.State == right.State &&
		left.Sequence == right.Sequence &&
		left.ObservedAt.Equal(right.ObservedAt) &&
		reflect.DeepEqual(left.Occupant, right.Occupant)
}

func cloneBlockOccupancy(value model.BlockOccupancy) model.BlockOccupancy {
	value.Occupant = cloneOccupant(value.Occupant)
	return value
}

func cloneOccupant(value *model.OccupantRef) *model.OccupantRef {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
