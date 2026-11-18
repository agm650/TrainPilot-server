package service

import (
	"context"

	"github.com/agm650/TrainPilot-server/internal/model"
)

// ResetObservations establishes an import boundary even if sensor identifiers
// are reused. Cached observations never describe a newly imported layout.
func (s *OccupancyService) ResetObservations(ctx context.Context) error {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	configs, err := s.resolvedMappings(ctx)
	if err != nil {
		return err
	}
	now := s.clock.Now().UTC()
	s.mu.Lock()
	s.observations = make(map[occupancySourceKey]model.OccupancyObservation)
	s.revision++
	blocks := make(map[string]bool)
	for _, c := range configs {
		blocks[c.BlockID] = true
	}
	var changes []model.BlockOccupancy
	for id := range blocks {
		if v := s.recomputeBlockLocked(id, configs, now); v != nil {
			changes = append(changes, *v)
		}
	}
	s.updateMetricsLocked(configs, now)
	s.mu.Unlock()
	for i := range changes {
		s.publish(&changes[i])
	}
	return nil
}
