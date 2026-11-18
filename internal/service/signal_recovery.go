package service

import (
	"context"
	"errors"
	"time"

	"github.com/agm650/TrainPilot-server/internal/station"
)

func (s *SignalService) resetLayoutOccupancy(ctx context.Context) error {
	if s.occupancy == nil {
		return nil
	}
	err := s.occupancy.ResetObservations(ctx)
	if p, ok := s.station.(station.FeedbackPowerResynchronizer); ok {
		p.BeginFeedbackPowerSync()
		err = errors.Join(err, p.RefreshFeedback(ctx))
	}
	return err
}

// SetRefreshInterval configures an optional decoder-watchdog heartbeat before
// Start. Zero disables repeated writes. Every refresh crosses normal safety
// and generation checks; it cannot replay an obsolete opening.
func (s *SignalService) SetRefreshInterval(interval time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return errors.New("configure signal refresh before Start")
	}
	if interval < 0 {
		return errors.New("signal refresh interval must be non-negative")
	}
	s.refreshInterval = interval
	return nil
}

func (s *SignalService) scheduleRetryLocked(r *signalRuntime) {
	if r.retryDelay == 0 {
		r.retryDelay = 250 * time.Millisecond
	} else {
		r.retryDelay *= 2
	}
	if r.retryDelay > 2*time.Second {
		r.retryDelay = 2 * time.Second
	}
	r.retryAt = time.Now().Add(r.retryDelay)
}

// Safety requests latch opening inhibition before waiting for the transport
// gate. Stop corrections remain possible while DCC power is available.
func (s *SignalService) SetEmergencyStop(active bool) {
	s.mu.Lock()
	if active != s.emergencyStop {
		s.emergencyStop = active
		for _, r := range s.entries {
			s.invalidateLocked(r, "emergency_stop")
		}
		s.publishAllLocked()
	}
	s.mu.Unlock()
	s.notify()
}

// A current unknown or contradictory observation may indicate a decoder reset.
// It is not a physical stop confirmation. Retry the configured stop first.
func (s *SignalService) resetReportedSignalLocked(r *signalRuntime) {
	s.invalidateLocked(r, "decoder_resynchronizing")
	s.scheduleRetryLocked(r)
	s.notify()
}
