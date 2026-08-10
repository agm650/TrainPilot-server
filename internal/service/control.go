package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/store"
)

type ControlService struct {
	store                        *store.Store
	station                      station.CommandStation
	events                       *events.Bus
	clock                        clock.Clock
	leaseTTL, stopGrace, monitor time.Duration
	stop                         chan struct{}
	once                         sync.Once
	powerMu                      sync.RWMutex
	trackPower                   *bool
}

type TrackPowerStatus struct {
	State string `json:"state"`
}

func NewControlService(s *store.Store, st station.CommandStation, b *events.Bus, c clock.Clock, leaseTTL, stopGrace, monitor time.Duration) *ControlService {
	return &ControlService{store: s, station: st, events: b, clock: c, leaseTTL: leaseTTL, stopGrace: stopGrace, monitor: monitor, stop: make(chan struct{})}
}
func (c *ControlService) Start() {
	go func() {
		ticker := time.NewTicker(c.monitor)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				c.Sweep(context.Background())
			case <-c.stop:
				return
			}
		}
	}()
}
func (c *ControlService) Close() { c.once.Do(func() { close(c.stop) }) }

func (c *ControlService) SetTrackPower(ctx context.Context, user model.User, enabled bool) error {
	if !Allowed(user.Role, PermissionDrive) {
		return errors.New("permission denied")
	}
	if !c.station.Capabilities().TrackPower {
		return station.ErrUnsupported
	}
	if err := c.station.SetTrackPower(ctx, enabled); err != nil {
		return err
	}
	c.powerMu.Lock()
	c.trackPower = new(bool)
	*c.trackPower = enabled
	c.powerMu.Unlock()
	c.events.Publish("track.power.changed", map[string]any{"enabled": enabled, "userId": user.ID})
	return nil
}

func (c *ControlService) TrackPowerStatus() TrackPowerStatus {
	c.powerMu.RLock()
	defer c.powerMu.RUnlock()
	if c.trackPower == nil {
		return TrackPowerStatus{State: "unknown"}
	}
	if *c.trackPower {
		return TrackPowerStatus{State: "on"}
	}
	return TrackPowerStatus{State: "off"}
}

func (c *ControlService) EmergencyStop(ctx context.Context, user model.User) error {
	if !Allowed(user.Role, PermissionDrive) {
		return errors.New("permission denied")
	}
	if err := c.station.EmergencyStop(ctx); err != nil {
		return err
	}
	c.events.Publish("track.emergency_stop", map[string]any{"userId": user.ID})
	return nil
}
func (c *ControlService) Acquire(ctx context.Context, user model.User, sess model.Session, locoID string) (model.ControlLease, error) {
	if !Allowed(user.Role, PermissionDrive) {
		return model.ControlLease{}, errors.New("permission denied")
	}
	if _, err := c.store.GetLocomotive(ctx, locoID); err != nil {
		return model.ControlLease{}, err
	}
	now := c.clock.Now()
	lease := model.ControlLease{ID: newID(), LocomotiveID: locoID, UserID: user.ID, SessionID: sess.ID, State: model.LeaseActive, AcquiredAt: now, RenewedAt: now, ExpiresAt: now.Add(c.leaseTTL), HeartbeatMillis: c.leaseTTL.Milliseconds() / 3}
	if err := c.store.CreateLease(ctx, lease); err != nil {
		return model.ControlLease{}, err
	}
	c.events.Publish("locomotive.control.acquired", lease)
	return lease, nil
}
func (c *ControlService) Heartbeat(ctx context.Context, id string, sess model.Session) (model.ControlLease, error) {
	now := c.clock.Now()
	if err := c.store.HeartbeatLease(ctx, id, sess.ID, now, now.Add(c.leaseTTL)); err != nil {
		return model.ControlLease{}, err
	}
	lease, err := c.store.GetLease(ctx, id)
	if err == nil {
		lease.HeartbeatMillis = c.leaseTTL.Milliseconds() / 3
	}
	return lease, err
}
func (c *ControlService) Release(ctx context.Context, id string, sess model.Session) error {
	lease, err := c.store.GetLease(ctx, id)
	if err != nil {
		return err
	}
	if lease.SessionID != sess.ID {
		return errors.New("lease is owned by another session")
	}
	return c.stopAndScheduleRelease(ctx, lease, "client_release")
}
func (c *ControlService) Throttle(ctx context.Context, user model.User, sess model.Session, locoID, leaseID string, speed int, direction station.Direction) error {
	if speed < 0 || speed > 100 {
		return errors.New("speed must be between 0 and 100")
	}
	now := c.clock.Now()
	if err := c.store.RenewActiveLeaseForCommand(ctx, leaseID, locoID, sess.ID, now, now.Add(c.leaseTTL)); err != nil {
		return err
	}
	loco, err := c.store.GetLocomotive(ctx, locoID)
	if err != nil {
		return err
	}
	if err := c.station.SetLocoSpeed(ctx, loco.DCCAddress, float64(speed)/100, direction); err != nil {
		return err
	}
	c.events.Publish("locomotive.speed.changed", map[string]any{"locomotiveId": locoID, "speed": speed, "direction": direction, "userId": user.ID})
	return nil
}
func (c *ControlService) Function(ctx context.Context, sess model.Session, locoID, leaseID string, fn int, on bool) error {
	now := c.clock.Now()
	if err := c.store.RenewActiveLeaseForCommand(ctx, leaseID, locoID, sess.ID, now, now.Add(c.leaseTTL)); err != nil {
		return err
	}
	loco, err := c.store.GetLocomotive(ctx, locoID)
	if err != nil {
		return err
	}
	if err := c.station.SetLocoFunction(ctx, loco.DCCAddress, fn, on); err != nil {
		return err
	}
	c.events.Publish("locomotive.function.changed", map[string]any{"locomotiveId": locoID, "function": fn, "enabled": on})
	return nil
}
func (c *ControlService) Sweep(ctx context.Context) {
	now := c.clock.Now()
	expired, _ := c.store.ExpiredActiveLeases(ctx, now)
	for _, l := range expired {
		_ = c.stopAndScheduleRelease(ctx, l, "heartbeat_timeout")
	}
	ready, _ := c.store.StoppingLeasesReady(ctx, now)
	for _, l := range ready {
		if err := c.store.ReleaseLease(ctx, l.ID, "", l.ReleaseReason); err == nil {
			c.events.Publish("locomotive.control.released", l)
		}
	}
}
func (c *ControlService) stopAndScheduleRelease(ctx context.Context, l model.ControlLease, reason string) error {
	loco, err := c.store.GetLocomotive(ctx, l.LocomotiveID)
	if err != nil {
		return err
	}
	releaseAt := c.clock.Now().Add(c.stopGrace)
	if err := c.store.MarkLeaseStopping(ctx, l.ID, reason, releaseAt); err != nil {
		return err
	}
	if err := c.station.SetLocoSpeed(ctx, loco.DCCAddress, 0, station.Forward); err != nil {
		return fmt.Errorf("stop command failed: %w", err)
	}
	c.events.Publish("locomotive.control.expired", map[string]any{"leaseId": l.ID, "locomotiveId": l.LocomotiveID, "reason": reason, "releaseAfter": releaseAt})
	return nil
}
