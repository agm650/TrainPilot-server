package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/station/simulator"
	"github.com/agm650/TrainPilot-server/internal/store"
)

func configureMomentaryLocomotive(t *testing.T, db *store.Store) {
	t.Helper()
	loco, err := db.GetLocomotive(context.Background(), "loco-bb26001")
	if err != nil {
		t.Fatal(err)
	}
	loco.Functions = []model.LocomotiveFunction{{Number: 2, Momentary: true}, {Number: 4, Momentary: true}}
	if err := db.UpdateLocomotive(context.Background(), loco); err != nil {
		t.Fatal(err)
	}
}

func TestMomentaryFunctionsStopWhenOwnerLosesControl(t *testing.T) {
	for _, reason := range []string{"disconnect", "release", "lease_expiry", "takeover", "logout", "session_expiry", "user_disabled"} {
		t.Run(reason, func(t *testing.T) {
			ctx := context.Background()
			c, db, sim, clk, user, session := newControlFixture(t)
			configureMomentaryLocomotive(t, db)
			if reason == "session_expiry" {
				c.leaseTTL = 2 * time.Hour
			}
			disconnect := c.ConnectClient(session.ID)
			if err := c.SetTrackPower(ctx, user, true); err != nil {
				t.Fatal(err)
			}
			lease, err := c.Acquire(ctx, user, session, "loco-bb26001")
			if err != nil {
				t.Fatal(err)
			}
			for _, fn := range []int{2, 3, 4} {
				if err := c.Function(ctx, session, lease.LocomotiveID, lease.ID, fn, true); err != nil {
					t.Fatal(err)
				}
			}
			published, unsubscribe := c.events.Subscribe(16)
			defer unsubscribe()
			switch reason {
			case "disconnect":
				err = disconnect(ctx)
			case "release":
				err = c.Release(ctx, lease.ID, session)
			case "lease_expiry":
				clk.Advance(16 * time.Second)
				c.Sweep(ctx)
			case "takeover":
				other := createAdditionalControlSession(t, db, clk.Now(), user, "other-session")
				_, err = c.TakeoverLease(ctx, user, other, lease.ID)
			case "logout":
				err = db.RevokeSession(ctx, session.ID, clk.Now())
				c.Sweep(ctx)
			case "session_expiry":
				clk.Advance(time.Hour + time.Second)
				c.Sweep(ctx)
			case "user_disabled":
				_, err = db.DB.ExecContext(ctx, `UPDATE users SET enabled=0 WHERE id=?`, user.ID)
				c.Sweep(ctx)
			}
			if err != nil {
				t.Fatal(err)
			}
			state := sim.Loco(2601)
			if state.Functions[2] || state.Functions[4] || !state.Functions[3] {
				t.Fatalf("momentary functions must stop and normal functions remain on: %+v", state.Functions)
			}
			offEvents := 0
			for len(published) > 0 {
				if event := <-published; event.Type == "locomotive.function.changed" {
					payload := event.Payload.(map[string]any)
					if payload["enabled"] != false {
						t.Fatalf("cleanup event=%+v", event)
					}
					offEvents++
				}
			}
			if offEvents != 2 {
				t.Fatalf("OFF events=%d want 2", offEvents)
			}
		})
	}
}

func TestMomentaryDisconnectOnlyStopsLastStreamOfOwningSession(t *testing.T) {
	ctx := context.Background()
	c, db, sim, _, user, session := newControlFixture(t)
	configureMomentaryLocomotive(t, db)
	first := c.ConnectClient(session.ID)
	last := c.ConnectClient(session.ID)
	unrelated := c.ConnectClient("unrelated-session")
	if err := c.SetTrackPower(ctx, user, true); err != nil {
		t.Fatal(err)
	}
	lease, err := c.Acquire(ctx, user, session, "loco-bb26001")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Function(ctx, session, lease.LocomotiveID, lease.ID, 2, true); err != nil {
		t.Fatal(err)
	}
	if err := unrelated(ctx); err != nil {
		t.Fatal(err)
	}
	if err := first(ctx); err != nil {
		t.Fatal(err)
	}
	if !sim.Loco(2601).Functions[2] {
		t.Fatal("closing another stream stopped a held function")
	}
	if err := last(ctx); err != nil {
		t.Fatal(err)
	}
	if sim.Loco(2601).Functions[2] {
		t.Fatal("last disconnect did not stop held function")
	}
	if err := c.Function(ctx, session, lease.LocomotiveID, lease.ID, 2, true); err == nil {
		t.Fatal("disconnected session re-enabled momentary function")
	}
	disconnect := c.ConnectClient(session.ID)
	defer disconnect(ctx)
	if err := c.Function(ctx, session, lease.LocomotiveID, lease.ID, 2, true); err != nil {
		t.Fatalf("reconnected client: %v", err)
	}
}

func TestMomentaryReleaseAllowedDuringSafetyInterlock(t *testing.T) {
	for _, interlock := range []string{"power_off", "power_unknown", "emergency_stop"} {
		t.Run(interlock, func(t *testing.T) {
			ctx := context.Background()
			c, db, sim, _, user, session := newControlFixture(t)
			configureMomentaryLocomotive(t, db)
			if err := c.SetTrackPower(ctx, user, true); err != nil {
				t.Fatal(err)
			}
			lease, err := c.Acquire(ctx, user, session, "loco-bb26001")
			if err != nil {
				t.Fatal(err)
			}
			if err := c.Function(ctx, session, lease.LocomotiveID, lease.ID, 2, true); err != nil {
				t.Fatal(err)
			}
			switch interlock {
			case "power_off":
				err = c.SetTrackPower(ctx, user, false)
			case "power_unknown":
				c.safetyMu.Lock()
				c.trackPowerKnown = false
				c.safetyMu.Unlock()
			case "emergency_stop":
				err = c.EmergencyStop(ctx, user)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := c.Function(ctx, session, lease.LocomotiveID, lease.ID, 2, false); err != nil {
				t.Fatal(err)
			}
			if sim.Loco(2601).Functions[2] {
				t.Fatal("released momentary function remains active")
			}
		})
	}
}

type failingMomentaryStation struct {
	*simulator.Simulator
	failOff          atomic.Bool
	failOnAfterWrite atomic.Bool
}

func (s *failingMomentaryStation) SetLocoFunction(ctx context.Context, address, fn int, on bool) error {
	if fn == 2 && !on && s.failOff.Load() {
		return errors.New("OFF write failed")
	}
	if err := s.Simulator.SetLocoFunction(ctx, address, fn, on); err != nil {
		return err
	}
	if fn == 2 && on && s.failOnAfterWrite.Load() {
		return errors.New("ON write outcome unknown")
	}
	return nil
}

func TestMomentaryFailedOffRetriesAndBlocksLeaseRelease(t *testing.T) {
	ctx := context.Background()
	sim := simulator.New()
	if err := sim.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	st := &failingMomentaryStation{Simulator: sim}
	c, db, clk, user, session := newControlFixtureWithStation(t, st)
	configureMomentaryLocomotive(t, db)
	if err := c.SetTrackPower(ctx, user, true); err != nil {
		t.Fatal(err)
	}
	lease, err := c.Acquire(ctx, user, session, "loco-bb26001")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Function(ctx, session, lease.LocomotiveID, lease.ID, 2, true); err != nil {
		t.Fatal(err)
	}
	st.failOff.Store(true)
	if err := c.Release(ctx, lease.ID, session); err == nil {
		t.Fatal("failed OFF was not reported")
	}
	clk.Advance(3 * time.Second)
	c.Sweep(ctx)
	stored, err := db.GetLease(ctx, lease.ID)
	if err != nil || stored.State != model.LeaseStopping {
		t.Fatalf("lease released before OFF succeeded: %+v err=%v", stored, err)
	}
	st.failOff.Store(false)
	c.Sweep(ctx)
	stored, err = db.GetLease(ctx, lease.ID)
	if err != nil || stored.State != model.LeaseReleased || sim.Loco(2601).Functions[2] {
		t.Fatalf("retry did not stop function and release lease: %+v err=%v", stored, err)
	}
}

func TestMomentaryUncertainOnIsTurnedOffBySweep(t *testing.T) {
	ctx := context.Background()
	sim := simulator.New()
	if err := sim.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	st := &failingMomentaryStation{Simulator: sim}
	c, db, _, user, session := newControlFixtureWithStation(t, st)
	configureMomentaryLocomotive(t, db)
	if err := c.SetTrackPower(ctx, user, true); err != nil {
		t.Fatal(err)
	}
	lease, err := c.Acquire(ctx, user, session, "loco-bb26001")
	if err != nil {
		t.Fatal(err)
	}
	st.failOnAfterWrite.Store(true)
	if err := c.Function(ctx, session, lease.LocomotiveID, lease.ID, 2, true); err == nil {
		t.Fatal("uncertain ON write was not reported")
	}
	if err := c.Function(ctx, session, lease.LocomotiveID, lease.ID, 4, true); err != nil {
		t.Fatal(err)
	}
	c.Sweep(ctx)
	if state := sim.Loco(2601); state.Functions[2] || !state.Functions[4] {
		t.Fatalf("retry must stop only the uncertain function: %+v", state.Functions)
	}
}

func TestMomentaryManualOffRetriesAfterStationRecovers(t *testing.T) {
	ctx := context.Background()
	c, db, sim, _, user, session := newControlFixture(t)
	configureMomentaryLocomotive(t, db)
	if err := c.SetTrackPower(ctx, user, true); err != nil {
		t.Fatal(err)
	}
	lease, err := c.Acquire(ctx, user, session, "loco-bb26001")
	if err != nil {
		t.Fatal(err)
	}
	for _, fn := range []int{2, 4} {
		if err := c.Function(ctx, session, lease.LocomotiveID, lease.ID, fn, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := sim.SetConnectivity(station.Offline); err != nil {
		t.Fatal(err)
	}
	if err := c.Function(ctx, session, lease.LocomotiveID, lease.ID, 2, false); !errors.Is(err, station.ErrOffline) {
		t.Fatalf("offline release error=%v", err)
	}
	c.Sweep(ctx)
	if err := sim.SetConnectivity(station.Online); err != nil {
		t.Fatal(err)
	}
	c.Sweep(ctx)
	if state := sim.Loco(2601); state.Functions[2] || !state.Functions[4] {
		t.Fatalf("retry must preserve the other held function: %+v", state.Functions)
	}
}

func TestMomentaryQueuedOnCannotSurviveDisconnectAndReconnect(t *testing.T) {
	ctx := context.Background()
	c, db, sim, _, user, session := newControlFixture(t)
	configureMomentaryLocomotive(t, db)
	disconnect := c.ConnectClient(session.ID)
	if err := c.SetTrackPower(ctx, user, true); err != nil {
		t.Fatal(err)
	}
	lease, err := c.Acquire(ctx, user, session, "loco-bb26001")
	if err != nil {
		t.Fatal(err)
	}
	releaseGate, err := c.commands.acquire(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	onResult := make(chan error, 1)
	go func() { onResult <- c.Function(ctx, session, lease.LocomotiveID, lease.ID, 2, true) }()
	waitCommandQueue(t, c, false)
	disconnected := make(chan error, 1)
	go func() { disconnected <- disconnect(ctx) }()
	waitCommandQueue(t, c, true)
	newDisconnect := c.ConnectClient(session.ID)
	defer newDisconnect(ctx)
	releaseGate()
	if err := <-disconnected; err != nil {
		t.Fatal(err)
	}
	if err := <-onResult; err == nil {
		t.Fatal("queued ON survived disconnect and reconnect")
	}
	if sim.Loco(2601).Functions[2] {
		t.Fatal("stale command enabled momentary function")
	}
}

func waitCommandQueue(t *testing.T, c *ControlService, safety bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		c.commands.mu.Lock()
		queued := c.commands.ordinaryWaiters
		if safety {
			queued = c.commands.safetyWaiters
		}
		c.commands.mu.Unlock()
		if queued > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("command did not enter queue")
}
