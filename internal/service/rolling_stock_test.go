package service

import (
	"context"
	"errors"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station/simulator"
	"github.com/agm650/TrainPilot-server/internal/store"
)

func newRollingStockFixture(t *testing.T) (*RailwayService, *store.Store) {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	sim := simulator.New()
	if err := sim.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	return NewRailwayService(db, sim, events.New()), db
}

func TestLocomotiveCRUD(t *testing.T) {
	ctx := context.Background()
	railway, _ := newRollingStockFixture(t)
	admin := model.User{ID: "admin", Role: model.RoleAdministrator}

	created, err := railway.CreateLocomotive(ctx, admin, model.LocomotiveInput{
		Name:         " BB 63000 ",
		DCCAddress:   3,
		AddressKind:  "SHORT",
		SpeedSteps:   128,
		Manufacturer: " Roco ",
		Model:        " BB 63000 ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Name != "BB 63000" || created.AddressKind != "short" {
		t.Fatalf("unexpected created locomotive: %+v", created)
	}

	got, err := railway.Locomotive(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != created {
		t.Fatalf("get=%+v want=%+v", got, created)
	}

	updated, err := railway.UpdateLocomotive(ctx, admin, created.ID, model.LocomotiveInput{
		Name:         "BB 63001",
		DCCAddress:   4,
		AddressKind:  "short",
		SpeedSteps:   28,
		Manufacturer: "Roco",
		Model:        "BB 63000",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != created.ID || updated.Name != "BB 63001" || updated.DCCAddress != 4 || updated.SpeedSteps != 28 {
		t.Fatalf("unexpected updated locomotive: %+v", updated)
	}

	items, err := railway.Locomotives(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != created.ID {
		t.Fatalf("items=%+v", items)
	}

	if err := railway.DeleteLocomotive(ctx, admin, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := railway.Locomotive(ctx, created.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("get after delete error=%v want not found", err)
	}
}

func TestLocomotiveWriteRequiresAdministrator(t *testing.T) {
	ctx := context.Background()
	railway, _ := newRollingStockFixture(t)
	driver := model.User{ID: "driver", Role: model.RoleDriver}
	input := model.LocomotiveInput{Name: "Test", DCCAddress: 3, AddressKind: "short", SpeedSteps: 128}

	if _, err := railway.CreateLocomotive(ctx, driver, input); err == nil || err.Error() != "permission denied" {
		t.Fatalf("create error=%v", err)
	}
}

func TestLocomotiveValidation(t *testing.T) {
	tests := []struct {
		name  string
		input model.LocomotiveInput
	}{
		{"empty name", model.LocomotiveInput{DCCAddress: 3, AddressKind: "short", SpeedSteps: 128}},
		{"address zero", model.LocomotiveInput{Name: "L", DCCAddress: 0, AddressKind: "short", SpeedSteps: 128}},
		{"address too high", model.LocomotiveInput{Name: "L", DCCAddress: 10240, AddressKind: "long", SpeedSteps: 128}},
		{"short address too high", model.LocomotiveInput{Name: "L", DCCAddress: 128, AddressKind: "short", SpeedSteps: 128}},
		{"long address too low", model.LocomotiveInput{Name: "L", DCCAddress: 127, AddressKind: "long", SpeedSteps: 128}},
		{"invalid address kind", model.LocomotiveInput{Name: "L", DCCAddress: 3, AddressKind: "extended", SpeedSteps: 128}},
		{"invalid speed steps", model.LocomotiveInput{Name: "L", DCCAddress: 3, AddressKind: "short", SpeedSteps: 27}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := locomotiveFromInput("id", tc.input); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestLocomotiveCannotBeUpdatedOrDeletedWithLiveLease(t *testing.T) {
	ctx := context.Background()
	control, db, sim, _, driver, sess := newControlFixture(t)
	lease, err := control.Acquire(ctx, driver, sess, "loco-bb26001")
	if err != nil {
		t.Fatal(err)
	}
	if lease.ID == "" {
		t.Fatal("missing lease ID")
	}
	railway := NewRailwayService(db, sim, events.New())
	admin := model.User{ID: "admin", Role: model.RoleAdministrator}
	input := model.LocomotiveInput{Name: "BB 26001", DCCAddress: 3, AddressKind: "short", SpeedSteps: 128}

	if _, err := railway.UpdateLocomotive(ctx, admin, "loco-bb26001", input); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("update error=%v want conflict", err)
	}
	if err := railway.DeleteLocomotive(ctx, admin, "loco-bb26001"); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("delete error=%v want conflict", err)
	}
}

func TestLocomotiveWithControlHistoryCannotBeDeleted(t *testing.T) {
	ctx := context.Background()
	control, db, sim, _, driver, sess := newControlFixture(t)
	lease, err := control.Acquire(ctx, driver, sess, "loco-bb26001")
	if err != nil {
		t.Fatal(err)
	}
	if err := control.Release(ctx, lease.ID, sess); err != nil {
		t.Fatal(err)
	}
	// Force the lease to released directly; this test is about retained history.
	if err := db.ReleaseLease(ctx, lease.ID, "", "test"); err != nil {
		t.Fatal(err)
	}
	railway := NewRailwayService(db, sim, events.New())
	admin := model.User{ID: "admin", Role: model.RoleAdministrator}
	if err := railway.DeleteLocomotive(ctx, admin, "loco-bb26001"); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("delete error=%v want conflict", err)
	}
}
