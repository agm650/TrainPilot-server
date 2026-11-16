package service

import (
	"context"
	"errors"
	"sort"
	"sync"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/store"
)

type momentaryKey struct {
	LocomotiveID string
	Number       int
}

type heldMomentaryFunction struct {
	LeaseID, SessionID string
	Address            int
	PendingOff         bool
}

// ConnectClient tracks event streams per authenticated session. Only the last
// stream closing ends its held functions; an unrelated session cannot stop them.
func (c *ControlService) ConnectClient(sessionID string) func(context.Context) error {
	c.momentaryMu.Lock()
	c.clientConnections[sessionID]++
	c.momentaryMu.Unlock()
	var once sync.Once
	return func(ctx context.Context) (err error) {
		once.Do(func() {
			c.momentaryMu.Lock()
			c.clientConnections[sessionID]--
			last := c.clientConnections[sessionID] == 0
			if last {
				c.clientGenerations[sessionID]++
			}
			c.momentaryMu.Unlock()
			if last {
				err = c.StopSessionFunctions(ctx, sessionID)
			}
		})
		return err
	}
}

func (c *ControlService) StopSessionFunctions(ctx context.Context, sessionID string) error {
	release, err := c.commands.acquire(ctx, true)
	if err != nil {
		return err
	}
	defer release()
	return c.stopMomentaryLocked(ctx, func(_ momentaryKey, held heldMomentaryFunction) bool { return held.SessionID == sessionID })
}

func (c *ControlService) stopLeaseMomentaryFunctions(ctx context.Context, leaseID string) error {
	release, err := c.commands.acquire(ctx, true)
	if err != nil {
		return err
	}
	defer release()
	return c.stopMomentaryLocked(ctx, func(_ momentaryKey, held heldMomentaryFunction) bool { return held.LeaseID == leaseID })
}

// The command gate must be held throughout selection, OFF writes and removal.
// Failed OFF writes remain pending and are retried by Sweep, even after expiry.
func (c *ControlService) stopMomentaryLocked(ctx context.Context, matches func(momentaryKey, heldMomentaryFunction) bool) error {
	c.momentaryMu.Lock()
	keys := make([]momentaryKey, 0)
	for key, held := range c.momentary {
		if matches(key, held) {
			held.PendingOff = true
			c.momentary[key] = held
			keys = append(keys, key)
		}
	}
	c.momentaryMu.Unlock()
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].LocomotiveID != keys[j].LocomotiveID {
			return keys[i].LocomotiveID < keys[j].LocomotiveID
		}
		return keys[i].Number < keys[j].Number
	})
	var failures []error
	for _, key := range keys {
		c.momentaryMu.Lock()
		held := c.momentary[key]
		c.momentaryMu.Unlock()
		err := station.CheckCommandAllowed(c.station)
		if err == nil {
			err = c.observeStationCommand("function", func() error { return c.station.SetLocoFunction(ctx, held.Address, key.Number, false) })
		}
		if err != nil {
			failures = append(failures, err)
			continue
		}
		c.momentaryMu.Lock()
		delete(c.momentary, key)
		c.momentaryMu.Unlock()
		c.events.Publish("locomotive.function.changed", map[string]any{"locomotiveId": key.LocomotiveID, "function": key.Number, "enabled": false})
	}
	return errors.Join(failures...)
}

func (c *ControlService) momentarySessionActive(ctx context.Context, sessionID string) (bool, error) {
	session, err := c.store.SessionByID(ctx, sessionID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if session.RevokedAt != nil || !session.AccessExpiry.After(c.clock.Now()) {
		return false, nil
	}
	user, err := c.store.GetUserByID(ctx, session.UserID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	return err == nil && user.User.Enabled, err
}

func (c *ControlService) sweepMomentaryFunctions(ctx context.Context) {
	c.momentaryMu.Lock()
	if len(c.momentary) == 0 {
		c.momentaryMu.Unlock()
		return
	}
	c.momentaryMu.Unlock()
	release, err := c.commands.acquire(ctx, true)
	if err != nil {
		return
	}
	defer release()
	c.momentaryMu.Lock()
	heldFunctions := make(map[momentaryKey]heldMomentaryFunction, len(c.momentary))
	for key, held := range c.momentary {
		heldFunctions[key] = held
	}
	c.momentaryMu.Unlock()
	for key, held := range heldFunctions {
		pending := held.PendingOff
		if !pending {
			lease, err := c.store.GetLease(ctx, held.LeaseID)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				continue
			}
			pending = err != nil || lease.State != model.LeaseActive || lease.SessionID != held.SessionID || !lease.ExpiresAt.After(c.clock.Now())
			if !pending {
				active, err := c.momentarySessionActive(ctx, held.SessionID)
				if err != nil {
					continue
				}
				pending = !active
			}
			c.momentaryMu.Lock()
			connections, known := c.clientConnections[held.SessionID]
			c.momentaryMu.Unlock()
			pending = pending || (known && connections == 0)
		}
		if pending {
			_ = c.stopMomentaryLocked(ctx, func(candidateKey momentaryKey, _ heldMomentaryFunction) bool {
				return candidateKey == key
			})
		}
	}
}
