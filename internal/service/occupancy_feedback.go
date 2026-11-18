package service

import (
	"context"
	"errors"
	"time"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station"
)

var ErrFeedbackGenerationStale = errors.New("station feedback belongs to an unavailable or obsolete generation")

// Station observations are guarded at mutation time, after mapping resolution.
// A queued observation cannot restore data after a resynchronization boundary.
func (s *OccupancyService) ObserveStationBatch(ctx context.Context, observations []model.OccupancyObservation, provider station.FeedbackStateProvider, generation, powerGeneration uint64) error {
	return s.observeBatch(ctx, observations, provider, generation, powerGeneration)
}

func (s *OccupancyService) SyncStationFeedback(ctx context.Context, provider station.FeedbackStateProvider) error {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	configs, err := s.resolvedMappings(ctx)
	if err != nil {
		return err
	}
	now := s.clock.Now().UTC()
	s.mu.Lock()
	changes := s.applyFeedbackStateLocked(configs, provider.FeedbackState(), now)
	s.updateMetricsLocked(configs, now)
	s.mu.Unlock()
	for i := range changes {
		s.publish(&changes[i])
	}
	return nil
}

func (s *OccupancyService) applyFeedbackStateLocked(configs []model.ResolvedOccupancySensorMapping, state station.FeedbackState, now time.Time) []model.BlockOccupancy {
	previous, known := s.feedbackStates[state.Source]
	if known && state.Generation < previous.Generation {
		return nil
	}
	if known && previous == state {
		return nil
	}
	s.feedbackStates[state.Source] = state
	s.revision++
	reset := !state.Available || !known || previous.Generation != state.Generation
	blocks := make(map[string]bool)
	for _, config := range configs {
		if config.ProviderID != state.Source && config.ProviderID != "*" {
			continue
		}
		s.availability[config.ProviderID] = state.Available
		blocks[config.BlockID] = true
		if reset || (known && previous.PowerGeneration != state.PowerGeneration && config.TrackPowerDependent) {
			delete(s.observations, occupancySourceKey{providerID: config.ProviderID, sensorID: config.SensorID})
		}
	}
	var changes []model.BlockOccupancy
	for blockID := range blocks {
		if changed := s.recomputeBlockLocked(blockID, configs, now); changed != nil {
			changes = append(changes, *changed)
		}
	}
	return changes
}
