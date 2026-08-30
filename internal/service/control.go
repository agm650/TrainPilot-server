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

var (
	ErrEmergencyStopActive = errors.New("emergency stop is active")
	ErrTrackPowerOff       = errors.New("track power is off")
	ErrTrackPowerUnknown   = errors.New("track power state is unknown")
	ErrSafetyPreempted     = errors.New("command was preempted by a safety command")
)

type ControlService struct {
	store                        *store.Store
	station                      station.CommandStation
	events                       *events.Bus
	clock                        clock.Clock
	leaseTTL, stopGrace, monitor time.Duration
	stop                         chan struct{}
	once                         sync.Once
	commands                     *priorityCommandGate
	safetyMu                     sync.RWMutex
	trackPowerKnown              bool
	trackPowerOn                 bool
	emergencyStopActive          bool
	safetyEpoch                  uint64
	statusMu                     sync.Mutex
	lastStationStatus            *station.Status
}

func NewControlService(s *store.Store, st station.CommandStation, b *events.Bus, c clock.Clock, leaseTTL, stopGrace, monitor time.Duration) *ControlService {
	return &ControlService{store: s, station: st, events: b, clock: c, leaseTTL: leaseTTL, stopGrace: stopGrace, monitor: monitor, stop: make(chan struct{}), commands: newPriorityCommandGate()}
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

	if provider, ok := c.station.(station.StatusEventProvider); ok {
		go func() {
			for {
				select {
				case status, ok := <-provider.StatusEvents():
					if !ok {
						return
					}
					c.publishStationStatusChanges(status)
				case <-c.stop:
					return
				}
			}
		}()
	}
}
func (c *ControlService) Close() { c.once.Do(func() { close(c.stop) }) }

func (c *ControlService) currentSafetyEpoch() uint64 {
	c.safetyMu.RLock()
	defer c.safetyMu.RUnlock()
	return c.safetyEpoch
}

func (c *ControlService) preemptOrdinaryCommands() {
	c.safetyMu.Lock()
	c.safetyEpoch++
	c.safetyMu.Unlock()
}

func (c *ControlService) observeSafetyStatus(status station.Status) {
	c.safetyMu.Lock()
	defer c.safetyMu.Unlock()

	if status.TrackPower == "on" || status.TrackPower == "off" {
		on := status.TrackPower == "on"
		if !on && (!c.trackPowerKnown || c.trackPowerOn) {
			c.safetyEpoch++
		}
		c.trackPowerKnown = true
		c.trackPowerOn = on
	}
	if status.EmergencyStop && !c.emergencyStopActive {
		c.safetyEpoch++
		c.emergencyStopActive = true
	}
}

func (c *ControlService) setTrackPowerState(enabled bool) {
	c.safetyMu.Lock()
	if !enabled && (!c.trackPowerKnown || c.trackPowerOn) {
		c.safetyEpoch++
	}
	c.trackPowerKnown = true
	c.trackPowerOn = enabled
	c.safetyMu.Unlock()
}

func (c *ControlService) setEmergencyStopState(active bool) {
	c.safetyMu.Lock()
	if active && !c.emergencyStopActive {
		c.safetyEpoch++
	}
	c.emergencyStopActive = active
	c.safetyMu.Unlock()
}

func (c *ControlService) safetySnapshot() (powerKnown, powerOn, emergencyStop bool) {
	c.safetyMu.RLock()
	defer c.safetyMu.RUnlock()
	return c.trackPowerKnown, c.trackPowerOn, c.emergencyStopActive
}

func (c *ControlService) ensureTrackPowerKnown(ctx context.Context) error {
	known, _, _ := c.safetySnapshot()
	if known {
		return nil
	}
	provider, ok := c.station.(station.StatusProvider)
	if !ok {
		return nil
	}
	status, err := provider.Status(ctx)
	if err != nil {
		return err
	}
	c.observeSafetyStatus(status)
	return nil
}

func (c *ControlService) checkDriveAllowed(allowStop bool) error {
	known, on, emergencyStop := c.safetySnapshot()
	if allowStop {
		return nil
	}
	if emergencyStop {
		return ErrEmergencyStopActive
	}
	if !known {
		return ErrTrackPowerUnknown
	}
	if !on {
		return ErrTrackPowerOff
	}
	return nil
}

func (c *ControlService) validateCommandLease(ctx context.Context, leaseID, locomotiveID, sessionID string) error {
	lease, err := c.store.GetLease(ctx, leaseID)
	if err != nil {
		return err
	}
	if lease.LocomotiveID != locomotiveID || lease.SessionID != sessionID || lease.State != model.LeaseActive || !lease.ExpiresAt.After(c.clock.Now()) {
		return store.ErrNotFound
	}
	return nil
}

func (c *ControlService) publishStationStatusChanges(status station.Status) {
	c.observeSafetyStatus(status)
	_, _, status.EmergencyStop = c.safetySnapshot()

	c.statusMu.Lock()
	defer c.statusMu.Unlock()

	previous := c.lastStationStatus
	current := status
	c.lastStationStatus = &current

	if previous == nil || previous.Connectivity != status.Connectivity {
		c.events.Publish("station.status.changed", map[string]any{
			"connectivity": status.Connectivity,
			"lastSeen":     status.LastSeen,
		})
	}

	if (status.TrackPower == "on" || status.TrackPower == "off") &&
		(previous == nil || previous.TrackPower != status.TrackPower) {
		c.events.Publish("track.power.changed", map[string]any{
			"enabled": status.TrackPower == "on",
		})
	}

	if previous == nil || previous.EmergencyStop != status.EmergencyStop {
		c.events.Publish("track.emergency_stop", map[string]any{
			"active": status.EmergencyStop,
		})
	}
}

func (c *ControlService) rememberTrackPower(enabled bool) {
	c.setTrackPowerState(enabled)

	c.statusMu.Lock()
	defer c.statusMu.Unlock()

	if c.lastStationStatus == nil {
		c.lastStationStatus = &station.Status{
			Connectivity: station.Degraded,
			TrackPower:   "unknown",
		}
	}
	if enabled {
		c.lastStationStatus.TrackPower = "on"
	} else {
		c.lastStationStatus.TrackPower = "off"
	}
}

func (c *ControlService) rememberEmergencyStop(active bool) {
	c.setEmergencyStopState(active)

	c.statusMu.Lock()
	defer c.statusMu.Unlock()

	if c.lastStationStatus == nil {
		c.lastStationStatus = &station.Status{
			Connectivity: station.Degraded,
			TrackPower:   "unknown",
		}
	}
	c.lastStationStatus.EmergencyStop = active
}

func (c *ControlService) SetTrackPower(ctx context.Context, user model.User, enabled bool) error {
	if !Allowed(user.Role, PermissionDrive) {
		return ErrPermissionDenied
	}
	if !c.station.Capabilities().TrackPower {
		return station.ErrUnsupported
	}
	epoch := c.currentSafetyEpoch()
	release, err := c.commands.acquire(ctx, !enabled)
	if err != nil {
		return err
	}
	defer release()
	if !enabled {
		c.preemptOrdinaryCommands()
	} else if c.currentSafetyEpoch() != epoch {
		return ErrSafetyPreempted
	}
	if err := station.CheckCommandAllowed(c.station); err != nil {
		return err
	}
	if err := c.station.SetTrackPower(ctx, enabled); err != nil {
		return err
	}

	// Keep the status-event deduplication cache aligned with commands issued
	// by TrainPilot. If the station later reports a different real state, the
	// corrective status event will still be emitted.
	c.rememberTrackPower(enabled)
	_, _, wasEmergencyStop := c.safetySnapshot()
	if enabled {
		c.rememberEmergencyStop(false)
	}

	c.events.Publish("track.power.changed", map[string]any{"enabled": enabled, "userId": user.ID})
	if enabled && wasEmergencyStop {
		c.events.Publish("track.emergency_stop", map[string]any{"active": false, "userId": user.ID})
	}
	return nil
}

func (c *ControlService) StationStatus(ctx context.Context) (station.Status, error) {
	if provider, ok := c.station.(station.StatusProvider); ok {
		status, err := provider.Status(ctx)
		if err == nil {
			c.observeSafetyStatus(status)
			_, _, status.EmergencyStop = c.safetySnapshot()
		}
		return status, err
	}
	known, on, emergencyStop := c.safetySnapshot()
	trackPower := "unknown"
	if known && on {
		trackPower = "on"
	} else if known {
		trackPower = "off"
	}
	connectivity := station.Degraded
	var lastSeen *time.Time
	if provider, ok := c.station.(station.HealthProvider); ok {
		health := provider.Health()
		connectivity = health.Connectivity
		lastSeen = health.LastSeen
	}
	return station.Status{Connectivity: connectivity, LastSeen: lastSeen, TrackPower: trackPower, EmergencyStop: emergencyStop}, nil
}

func (c *ControlService) EmergencyStop(ctx context.Context, user model.User) error {
	if !Allowed(user.Role, PermissionDrive) {
		return ErrPermissionDenied
	}
	release, err := c.commands.acquire(ctx, true)
	if err != nil {
		return err
	}
	defer release()
	c.preemptOrdinaryCommands()
	if err := station.CheckCommandAllowed(c.station); err != nil {
		return err
	}
	if err := c.station.EmergencyStop(ctx); err != nil {
		return err
	}

	c.rememberEmergencyStop(true)

	c.events.Publish("track.emergency_stop", map[string]any{"active": true, "userId": user.ID})
	return nil
}
func (c *ControlService) Acquire(ctx context.Context, user model.User, sess model.Session, locoID string) (model.ControlLease, error) {
	if !Allowed(user.Role, PermissionDrive) {
		return model.ControlLease{}, ErrPermissionDenied
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

func (c *ControlService) LeasesForSession(ctx context.Context, sess model.Session) ([]model.ControlLease, error) {
	leases, err := c.store.LiveLeasesForSession(ctx, sess.ID)
	if err != nil {
		return nil, err
	}
	for i := range leases {
		leases[i].HeartbeatMillis = c.leaseTTL.Milliseconds() / 3
	}
	return leases, nil
}
func (c *ControlService) Release(ctx context.Context, id string, sess model.Session) error {
	lease, err := c.store.GetLease(ctx, id)
	if err != nil {
		return err
	}
	if lease.SessionID != sess.ID {
		return ErrLeaseNotOwned
	}
	return c.stopAndScheduleRelease(ctx, lease, "client_release")
}
func (c *ControlService) Throttle(ctx context.Context, user model.User, sess model.Session, locoID, leaseID string, speed int, direction station.Direction) error {
	if speed < 0 || speed > 100 {
		return invalid("speed must be between 0 and 100")
	}
	if !direction.Valid() {
		return invalid("direction must be forward or reverse")
	}
	loco, err := c.store.GetLocomotive(ctx, locoID)
	if err != nil {
		return err
	}
	if err := c.validateCommandLease(ctx, leaseID, locoID, sess.ID); err != nil {
		return err
	}
	safety := speed == 0
	epoch := c.currentSafetyEpoch()
	release, err := c.commands.acquire(ctx, safety)
	if err != nil {
		return err
	}
	defer release()
	if safety {
		c.preemptOrdinaryCommands()
	} else if c.currentSafetyEpoch() != epoch {
		return ErrSafetyPreempted
	}
	if err := station.CheckCommandAllowed(c.station); err != nil {
		return err
	}
	if !safety {
		if err := c.ensureTrackPowerKnown(ctx); err != nil {
			return err
		}
	}
	if err := c.checkDriveAllowed(safety); err != nil {
		return err
	}
	now := c.clock.Now()
	if err := c.store.RenewActiveLeaseForCommand(ctx, leaseID, locoID, sess.ID, now, now.Add(c.leaseTTL)); err != nil {
		return err
	}
	if err := c.station.SetLocoSpeed(ctx, loco.DCCAddress, float64(speed)/100, direction); err != nil {
		return err
	}
	c.events.Publish("locomotive.speed.changed", map[string]any{"locomotiveId": locoID, "speed": speed, "direction": direction, "userId": user.ID})
	return nil
}
func (c *ControlService) Function(ctx context.Context, sess model.Session, locoID, leaseID string, fn int, on bool) error {
	caps := c.station.Capabilities()
	if caps.Functions <= 0 {
		return invalid("station does not support locomotive functions")
	}
	if fn < 0 || fn > caps.MaxFunctionNumber {
		return invalid(fmt.Sprintf("function must be between 0 and %d", caps.MaxFunctionNumber))
	}
	loco, err := c.store.GetLocomotive(ctx, locoID)
	if err != nil {
		return err
	}
	if err := c.validateCommandLease(ctx, leaseID, locoID, sess.ID); err != nil {
		return err
	}
	epoch := c.currentSafetyEpoch()
	release, err := c.commands.acquire(ctx, false)
	if err != nil {
		return err
	}
	defer release()
	if c.currentSafetyEpoch() != epoch {
		return ErrSafetyPreempted
	}
	if err := station.CheckCommandAllowed(c.station); err != nil {
		return err
	}
	if err := c.ensureTrackPowerKnown(ctx); err != nil {
		return err
	}
	if err := c.checkDriveAllowed(false); err != nil {
		return err
	}
	now := c.clock.Now()
	if err := c.store.RenewActiveLeaseForCommand(ctx, leaseID, locoID, sess.ID, now, now.Add(c.leaseTTL)); err != nil {
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
	release, err := c.commands.acquire(ctx, true)
	if err != nil {
		return fmt.Errorf("stop command failed: %w", err)
	}
	defer release()
	c.preemptOrdinaryCommands()
	if err := station.CheckCommandAllowed(c.station); err != nil {
		return fmt.Errorf("stop command failed: %w", err)
	}
	if err := c.station.SetLocoSpeed(ctx, loco.DCCAddress, 0, station.Forward); err != nil {
		return fmt.Errorf("stop command failed: %w", err)
	}
	c.events.Publish("locomotive.control.expired", map[string]any{"leaseId": l.ID, "locomotiveId": l.LocomotiveID, "reason": reason, "releaseAfter": releaseAt})
	return nil
}
