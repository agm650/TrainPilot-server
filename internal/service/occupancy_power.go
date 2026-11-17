package service

import (
	"context"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station"
)

func (s *OccupancyService) TrackPowerAvailable() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.trackPowerAvailable
}

func (s *OccupancyService) sourceAvailableLocked(config model.ResolvedOccupancySensorMapping) bool {
	return s.providerAvailableLocked(config.ProviderID) && (!config.TrackPowerDependent || s.trackPowerAvailable)
}

// Both edges clear dependent observations. OFF reports are never reusable
// after ON, even if a transport stayed connected throughout the interruption.
func (s *OccupancyService) SetTrackPowerAvailable(ctx context.Context, available bool) error {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	configs, err := s.resolvedMappings(ctx)
	if err != nil {
		return err
	}
	now := s.clock.Now().UTC()
	s.mu.Lock()
	if s.trackPowerAvailable == available {
		s.mu.Unlock()
		return nil
	}
	s.trackPowerAvailable = available
	blocks := make(map[string]bool)
	for _, config := range configs {
		if config.TrackPowerDependent {
			delete(s.observations, occupancySourceKey{providerID: config.ProviderID, sensorID: config.SensorID})
			blocks[config.BlockID] = true
		}
	}
	var changes []model.BlockOccupancy
	for id := range blocks {
		if changed := s.recomputeBlockLocked(id, configs, now); changed != nil {
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

func (s *OccupancyService) stationDependsOnTrackPower(ctx context.Context, source string) bool {
	configs, err := s.resolvedMappings(ctx)
	if err != nil {
		return true
	}
	for _, config := range configs {
		if (config.ProviderID == source || config.ProviderID == "*") && config.TrackPowerDependent {
			return true
		}
	}
	return false
}

func (c *ControlService) updateOccupancyTrackPower(ctx context.Context, status station.Status) {
	if c.occupancy == nil {
		return
	}
	c.occupancyPowerMu.Lock()
	defer c.occupancyPowerMu.Unlock()
	usable := status.TrackPower == "on" && !status.ShortCircuit && !status.InternalShortCircuit && !status.PowerLost
	wasAvailable := c.occupancy.TrackPowerAvailable()
	refresh := usable && !wasAvailable && c.occupancy.stationDependsOnTrackPower(ctx, c.occupancyProviderID)
	provider, supported := c.station.(station.FeedbackPowerResynchronizer)
	if refresh && supported {
		provider.BeginFeedbackPowerSync()
	}
	if err := c.occupancy.SetTrackPowerAvailable(ctx, usable); err != nil {
		return
	}
	if refresh && supported {
		if err := provider.RefreshFeedback(ctx); err != nil {
			_ = c.occupancy.SetTrackPowerAvailable(ctx, false)
		}
	}
}
