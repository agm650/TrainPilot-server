package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/store"
)

func TestDrivingRequiresExplicitSafePowerState(t *testing.T) {
	ctx := context.Background()
	control, _, sim, _, user, sess := newControlFixture(t)
	lease, err := control.Acquire(ctx, user, sess, "loco-bb26001")
	if err != nil {
		t.Fatal(err)
	}

	if err := control.Throttle(ctx, user, sess, "loco-bb26001", lease.ID, 30, station.Forward); !errors.Is(err, ErrTrackPowerOff) {
		t.Fatalf("throttle with power off error=%v", err)
	}
	if err := control.SetTrackPower(ctx, user, true); err != nil {
		t.Fatal(err)
	}
	if err := control.Throttle(ctx, user, sess, "loco-bb26001", lease.ID, 30, station.Forward); err != nil {
		t.Fatal(err)
	}
	if err := control.EmergencyStop(ctx, user); err != nil {
		t.Fatal(err)
	}
	if err := control.Throttle(ctx, user, sess, "loco-bb26001", lease.ID, 30, station.Forward); !errors.Is(err, ErrEmergencyStopActive) {
		t.Fatalf("throttle during emergency stop error=%v", err)
	}
	if err := control.Function(ctx, sess, "loco-bb26001", lease.ID, 1, true); !errors.Is(err, ErrEmergencyStopActive) {
		t.Fatalf("function during emergency stop error=%v", err)
	}
	if err := control.Throttle(ctx, user, sess, "loco-bb26001", lease.ID, 0, station.Forward); err != nil {
		t.Fatalf("zero throttle during emergency stop: %v", err)
	}
	if err := sim.SetTrackPower(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := control.Throttle(ctx, user, sess, "loco-bb26001", lease.ID, 30, station.Forward); !errors.Is(err, ErrEmergencyStopActive) {
		t.Fatalf("external status change cleared server emergency stop: %v", err)
	}
	if err := control.SetTrackPower(ctx, user, true); err != nil {
		t.Fatal(err)
	}
	if err := control.Throttle(ctx, user, sess, "loco-bb26001", lease.ID, 30, station.Forward); err != nil {
		t.Fatalf("throttle after explicit power-on: %v", err)
	}
	if err := control.SetTrackPower(ctx, user, false); err != nil {
		t.Fatal(err)
	}
	if err := control.Throttle(ctx, user, sess, "loco-bb26001", lease.ID, 30, station.Forward); !errors.Is(err, ErrTrackPowerOff) {
		t.Fatalf("throttle after power-off error=%v", err)
	}
	if err := control.Function(ctx, sess, "loco-bb26001", lease.ID, 1, true); !errors.Is(err, ErrTrackPowerOff) {
		t.Fatalf("function after power-off error=%v", err)
	}
}

func TestSafetyCommandPreemptsQueuedOrdinaryCommands(t *testing.T) {
	for _, tc := range []struct {
		name        string
		safety      func(context.Context, *ControlService, model.User, model.Session, string) error
		wantCommand string
	}{
		{
			name: "emergency stop",
			safety: func(ctx context.Context, control *ControlService, user model.User, _ model.Session, _ string) error {
				return control.EmergencyStop(ctx, user)
			},
			wantCommand: "emergency-stop",
		},
		{
			name: "track power off",
			safety: func(ctx context.Context, control *ControlService, user model.User, _ model.Session, _ string) error {
				return control.SetTrackPower(ctx, user, false)
			},
			wantCommand: "power-off",
		},
		{
			name: "zero throttle",
			safety: func(ctx context.Context, control *ControlService, user model.User, sess model.Session, leaseID string) error {
				return control.Throttle(ctx, user, sess, "loco-bb26001", leaseID, 0, station.Forward)
			},
			wantCommand: "throttle:0",
		},
	} {
		for _, ordinary := range []struct {
			name string
			call func(context.Context, *ControlService, model.User, model.Session, string) error
		}{
			{
				name: "throttle",
				call: func(ctx context.Context, control *ControlService, user model.User, sess model.Session, leaseID string) error {
					return control.Throttle(ctx, user, sess, "loco-bb26001", leaseID, 40, station.Forward)
				},
			},
			{
				name: "function",
				call: func(ctx context.Context, control *ControlService, _ model.User, sess model.Session, leaseID string) error {
					return control.Function(ctx, sess, "loco-bb26001", leaseID, 1, true)
				},
			},
		} {
			t.Run(tc.name+" before "+ordinary.name, func(t *testing.T) {
				ctx := context.Background()
				commandStation := newBlockingCommandStation()
				control, _, _, user, sess := newControlFixtureWithStation(t, commandStation)
				lease, err := control.Acquire(ctx, user, sess, "loco-bb26001")
				if err != nil {
					t.Fatal(err)
				}

				firstThrottle := make(chan error, 1)
				go func() {
					firstThrottle <- control.Throttle(ctx, user, sess, "loco-bb26001", lease.ID, 30, station.Forward)
				}()
				select {
				case <-commandStation.throttleStarted:
				case <-time.After(time.Second):
					t.Fatal("first throttle did not reach the command station")
				}

				safetyResult := make(chan error, 1)
				go func() { safetyResult <- tc.safety(ctx, control, user, sess, lease.ID) }()
				waitForSafetyWaiter(t, control.commands)

				queuedOrdinary := make(chan error, 1)
				go func() {
					queuedOrdinary <- ordinary.call(ctx, control, user, sess, lease.ID)
				}()
				waitForOrdinaryWaiter(t, control.commands)

				close(commandStation.releaseThrottle)
				if err := <-firstThrottle; err != nil {
					t.Fatalf("first throttle: %v", err)
				}
				if err := <-safetyResult; err != nil {
					t.Fatalf("safety command: %v", err)
				}
				if err := <-queuedOrdinary; !errors.Is(err, ErrSafetyPreempted) {
					t.Fatalf("queued %s error=%v", ordinary.name, err)
				}

				want := []string{"throttle:30", tc.wantCommand}
				if got := commandStation.Commands(); !equalStrings(got, want) {
					t.Fatalf("commands=%v want %v", got, want)
				}
			})
		}
	}
}

func TestTakeoverPreemptsQueuedOldSessionCommand(t *testing.T) {
	ctx := context.Background()
	commandStation := newBlockingCommandStation()
	control, db, clk, user, session1 := newControlFixtureWithStation(t, commandStation)
	session2 := createAdditionalControlSession(t, db, clk.Now(), user, "session-2")
	lease, err := control.Acquire(ctx, user, session1, "loco-bb26001")
	if err != nil {
		t.Fatal(err)
	}

	firstThrottle := make(chan error, 1)
	go func() {
		firstThrottle <- control.Throttle(ctx, user, session1, lease.LocomotiveID, lease.ID, 30, station.Forward)
	}()
	select {
	case <-commandStation.throttleStarted:
	case <-time.After(time.Second):
		t.Fatal("first throttle did not reach the command station")
	}

	takeoverResult := make(chan struct {
		lease model.ControlLease
		err   error
	}, 1)
	go func() {
		transferred, takeoverErr := control.TakeoverLease(ctx, user, session2, lease.ID)
		takeoverResult <- struct {
			lease model.ControlLease
			err   error
		}{transferred, takeoverErr}
	}()
	waitForSafetyWaiter(t, control.commands)

	queuedOldThrottle := make(chan error, 1)
	go func() {
		queuedOldThrottle <- control.Throttle(ctx, user, session1, lease.LocomotiveID, lease.ID, 60, station.Forward)
	}()
	waitForOrdinaryWaiter(t, control.commands)

	close(commandStation.releaseThrottle)
	if err := <-firstThrottle; err != nil {
		t.Fatalf("first throttle: %v", err)
	}
	result := <-takeoverResult
	if result.err != nil || result.lease.SessionID != session2.ID {
		t.Fatalf("takeover lease=%+v err=%v", result.lease, result.err)
	}
	if err := <-queuedOldThrottle; !errors.Is(err, ErrSafetyPreempted) {
		t.Fatalf("queued old-session throttle error=%v", err)
	}
	if got, want := commandStation.Commands(), []string{"throttle:30", "throttle:0"}; !equalStrings(got, want) {
		t.Fatalf("commands=%v want=%v", got, want)
	}
	if err := control.Throttle(ctx, user, session1, lease.LocomotiveID, lease.ID, 70, station.Forward); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("old-session throttle after takeover error=%v", err)
	}
	if got, want := commandStation.Commands(), []string{"throttle:30", "throttle:0"}; !equalStrings(got, want) {
		t.Fatalf("commands after takeover=%v want=%v", got, want)
	}
}

func waitForOrdinaryWaiter(t *testing.T, gate *priorityCommandGate) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		gate.mu.Lock()
		waiting := gate.ordinaryWaiters > 0
		gate.mu.Unlock()
		if waiting {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("ordinary command did not enter the priority queue")
}

func waitForSafetyWaiter(t *testing.T, gate *priorityCommandGate) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		gate.mu.Lock()
		waiting := gate.safetyWaiters > 0
		gate.mu.Unlock()
		if waiting {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("safety command did not enter the priority queue")
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

type blockingCommandStation struct {
	mu              sync.Mutex
	commands        []string
	blockThrottle   bool
	throttleStarted chan struct{}
	releaseThrottle chan struct{}
	feedback        chan station.FeedbackEvent
}

func newBlockingCommandStation() *blockingCommandStation {
	return &blockingCommandStation{
		blockThrottle:   true,
		throttleStarted: make(chan struct{}),
		releaseThrottle: make(chan struct{}),
		feedback:        make(chan station.FeedbackEvent),
	}
}

func (s *blockingCommandStation) Connect(context.Context) error { return nil }
func (s *blockingCommandStation) Close() error                  { return nil }
func (s *blockingCommandStation) Capabilities() station.Capabilities {
	return station.Capabilities{TrackPower: true, LocomotiveControl: true, Functions: 69, MaxFunctionNumber: 68}
}
func (s *blockingCommandStation) SetTrackPower(_ context.Context, enabled bool) error {
	command := "power-off"
	if enabled {
		command = "power-on"
	}
	s.record(command)
	return nil
}
func (s *blockingCommandStation) EmergencyStop(context.Context) error {
	s.record("emergency-stop")
	return nil
}
func (s *blockingCommandStation) SetLocoSpeed(ctx context.Context, _ int, speed float64, _ station.Direction) error {
	s.mu.Lock()
	block := speed > 0 && s.blockThrottle
	if block {
		s.blockThrottle = false
		close(s.throttleStarted)
	}
	s.mu.Unlock()
	if block {
		select {
		case <-s.releaseThrottle:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.record(fmt.Sprintf("throttle:%d", int(speed*100)))
	return nil
}
func (s *blockingCommandStation) SetLocoFunction(context.Context, int, int, bool) error {
	s.record("function")
	return nil
}
func (s *blockingCommandStation) SetAccessory(context.Context, int, string) error { return nil }
func (s *blockingCommandStation) Feedback() <-chan station.FeedbackEvent          { return s.feedback }
func (s *blockingCommandStation) Health() station.Health {
	return station.Health{Connectivity: station.Online}
}
func (s *blockingCommandStation) Status(context.Context) (station.Status, error) {
	return station.Status{Connectivity: station.Online, TrackPower: "on"}, nil
}
func (s *blockingCommandStation) Commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.commands...)
}
func (s *blockingCommandStation) record(command string) {
	s.mu.Lock()
	s.commands = append(s.commands, command)
	s.mu.Unlock()
}
