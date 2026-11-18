package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/station/simulator"
	"github.com/agm650/TrainPilot-server/internal/store"
)

var signalDispatcher = model.User{ID: "dispatcher", Role: model.RoleDispatcher}

func signalOccupancyFixture(t *testing.T, db *store.Store, bus *events.Bus) *OccupancyService {
	t.Helper()
	ctx := context.Background()
	independent := false
	if err := db.SetOccupancyProvider(ctx, model.OccupancyProvider{ID: "external", Type: "external", Required: true, TrackPowerDependent: &independent}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetOccupancySensorMapping(ctx, model.OccupancySensorMapping{ProviderID: "external", SensorID: "one", BlockID: "block"}); err != nil {
		t.Fatal(err)
	}
	return NewOccupancyService(db, bus, nil)
}

func signalObserveBlock(t *testing.T, o *OccupancyService, state model.OccupancyState, sequence uint64) {
	t.Helper()
	if err := o.Observe(context.Background(), model.OccupancyObservation{ProviderID: "external", SensorID: "one", State: state, Sequence: sequence, ObservedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

func TestSignalManualPermissionsSafetyRecoveryAndAudit(t *testing.T) {
	db, sim, bus := signalServiceFixture(t)
	occupancy := signalOccupancyFixture(t, db, bus)
	_ = sim.SetTrackPower(context.Background(), true)
	s := NewSignalService(db, sim, bus, occupancy)
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	awaitSignal(t, s, func(v model.SignalState) bool { return v.CommandedAspect != nil && !v.Pending })
	ctx := context.Background()
	for _, role := range []model.Role{model.RoleViewer, model.RoleDriver, model.RoleSensor, "unknown"} {
		if err := s.CommandAspect(ctx, model.User{ID: string(role), Role: role}, "s1", "S"); !errors.Is(err, ErrPermissionDenied) {
			t.Fatal(role, err)
		}
	}
	if err := s.CommandAspect(ctx, signalDispatcher, "s1", "INVALID"); !errors.Is(err, ErrValidation) {
		t.Fatal(err)
	}
	if err := s.CommandAspect(ctx, signalDispatcher, "missing", "S"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.CommandAspect(ctx, signalDispatcher, "s1", "VL"); !errors.Is(err, ErrSignalUnsafe) {
		t.Fatal(err)
	}
	if len(sim.ExtendedAccessoryCommands()) != 1 {
		t.Fatal("rejected command reached transport")
	}
	signalObserveBlock(t, occupancy, model.OccupancyFree, 1)
	if err := s.CommandAspect(ctx, signalDispatcher, "s1", "VL"); err != nil {
		t.Fatal(err)
	}
	state := awaitSignal(t, s, func(v model.SignalState) bool { return v.CommandedAspect != nil && *v.CommandedAspect == "VL" })
	if state.DesiredAspect != "VL" || state.ComputedAspect != "S" {
		t.Fatal(state)
	}
	signalObserveBlock(t, occupancy, model.OccupancyOccupied, 2)
	awaitSignal(t, s, func(v model.SignalState) bool { return v.CommandedAspect != nil && *v.CommandedAspect == "S" })
	signalObserveBlock(t, occupancy, model.OccupancyFree, 3)
	// Becoming free cannot replay the manual opening.
	state, _ = s.State("s1")
	if state.DesiredAspect != "S" {
		t.Fatal(state)
	}
	s.ObserveStatus(station.Status{Connectivity: station.Online, TrackPower: "off"})
	if err := s.CommandAspect(ctx, signalDispatcher, "s1", "VL"); !errors.Is(err, ErrSignalUnavailable) {
		t.Fatal(err)
	}
	s.ObserveStatus(station.Status{Connectivity: station.Online, TrackPower: "on"})
	awaitSignal(t, s, func(v model.SignalState) bool { return v.CommandedAspect != nil && *v.CommandedAspect == "S" })
	if err := s.CommandAspect(ctx, model.User{ID: "admin", Role: model.RoleAdministrator}, "s1", "S"); err != nil {
		t.Fatal(err)
	}
	var denied, unsafe, attempts int
	if err := db.DB.QueryRowContext(ctx, `SELECT count(*) FROM audit_events WHERE json_extract(details_json,'$.result')='denied'`).Scan(&denied); err != nil {
		t.Fatal(err)
	}
	if err := db.DB.QueryRowContext(ctx, `SELECT count(*) FROM audit_events WHERE json_extract(details_json,'$.result')='unsafe'`).Scan(&unsafe); err != nil {
		t.Fatal(err)
	}
	if err := db.DB.QueryRowContext(ctx, `SELECT count(*) FROM audit_events WHERE actor_id='dispatcher' AND action='signal.aspect' AND json_extract(details_json,'$.phase')='attempt'`).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if denied != 4 || unsafe != 1 || attempts != 1 {
		t.Fatal(denied, unsafe, attempts)
	}
}

func TestSignalModePersistenceAndUnavailableAutomaticEngine(t *testing.T) {
	db, sim, bus := signalServiceFixture(t)
	_ = sim.SetTrackPower(context.Background(), true)
	d, _ := db.Signal(context.Background(), "s1")
	d.Mode = model.SignalModeAutomatic
	if err := db.SetSignal(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	s := NewSignalService(db, sim, bus, nil)
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	awaitSignal(t, s, func(v model.SignalState) bool { return v.CommandedAspect != nil })
	if err := s.CommandAspect(context.Background(), signalDispatcher, "s1", "S"); !errors.Is(err, ErrSignalMode) {
		t.Fatal(err)
	}
	if err := s.SetMode(context.Background(), signalDispatcher, "s1", model.SignalModeManual); err != nil {
		t.Fatal(err)
	}
	d, _ = db.Signal(context.Background(), "s1")
	if d.Mode != model.SignalModeManual {
		t.Fatal(d)
	}
	if err := s.SetMode(context.Background(), signalDispatcher, "s1", model.SignalModeAutomatic); !errors.Is(err, ErrSignalAutomaticUnavailable) {
		t.Fatal(err)
	}
	if err := s.SetMode(context.Background(), signalDispatcher, "s1", "wrong"); !errors.Is(err, ErrValidation) {
		t.Fatal(err)
	}
	if err := s.SetMode(context.Background(), model.User{Role: model.RoleDriver}, "s1", model.SignalModeManual); !errors.Is(err, ErrPermissionDenied) {
		t.Fatal(err)
	}
	d, _ = db.Signal(context.Background(), "s1")
	if d.Mode != model.SignalModeManual {
		t.Fatal("failed command changed persisted mode")
	}
}

type signalOpeningDriver struct {
	*simulator.Simulator
	entered chan struct{}
	fail    bool
}

func (d *signalOpeningDriver) SetExtendedAccessory(ctx context.Context, c station.ExtendedAccessoryCommand) error {
	if c.Aspect == 16 {
		if d.fail {
			return errors.New("secret transport detail")
		}
		select {
		case d.entered <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return ctx.Err()
	}
	return d.Simulator.SetExtendedAccessory(ctx, c)
}

func TestSignalRestrictiveCommandPreemptsOpeningAndAuditsFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "preemption", true: "transport failure"}[fail], func(t *testing.T) {
			db, sim, bus := signalServiceFixture(t)
			occupancy := signalOccupancyFixture(t, db, bus)
			signalObserveBlock(t, occupancy, model.OccupancyFree, 1)
			_ = sim.SetTrackPower(context.Background(), true)
			driver := &signalOpeningDriver{Simulator: sim, entered: make(chan struct{}, 1), fail: fail}
			s := NewSignalService(db, driver, bus, occupancy)
			if err := s.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			awaitSignal(t, s, func(v model.SignalState) bool { return v.CommandedAspect != nil && !v.Pending })
			result := make(chan error, 1)
			go func() { result <- s.CommandAspect(context.Background(), signalDispatcher, "s1", "VL") }()
			if !fail {
				select {
				case <-driver.entered:
				case <-time.After(3 * time.Second):
					t.Fatal("opening not started")
				}
				if err := s.CommandAspect(context.Background(), signalDispatcher, "s1", "A"); !errors.Is(err, ErrSignalBusy) {
					t.Fatal(err)
				}
				if err := s.CommandAspect(context.Background(), signalDispatcher, "s1", "S"); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-result:
				if err == nil {
					t.Fatal("failed/preempted opening reported success")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("opening not canceled")
			}
			awaitSignal(t, s, func(v model.SignalState) bool {
				return v.CommandedAspect != nil && *v.CommandedAspect == "S" && !v.Pending
			})
			rows, err := db.DB.QueryContext(context.Background(), `SELECT details_json FROM audit_events`)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			for rows.Next() {
				var details string
				if err := rows.Scan(&details); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(details, "secret") {
					t.Fatal("transport secret leaked into audit")
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
