package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/store"
)

func TestControlAcquireAndHeartbeatErrors(t *testing.T) {
	ctx := context.Background()
	control, db, _, clk, user, sess := newControlFixture(t)

	viewer := user
	viewer.Role = model.RoleViewer
	if _, err := control.Acquire(ctx, viewer, sess, "loco-bb26001"); err == nil {
		t.Fatal("viewer acquired a locomotive")
	}
	if _, err := control.Acquire(ctx, user, sess, "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing locomotive error=%v", err)
	}
	if _, err := control.Heartbeat(ctx, "missing", sess); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing heartbeat error=%v", err)
	}

	lease, err := control.Acquire(ctx, user, sess, "loco-bb26001")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Heartbeat(ctx, lease.ID, model.Session{ID: "other"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("other session heartbeat error=%v", err)
	}
	clk.Advance(time.Second)
	renewed, err := control.Heartbeat(ctx, lease.ID, sess)
	if err != nil {
		t.Fatal(err)
	}
	if renewed.HeartbeatMillis != 5000 || !renewed.RenewedAt.Equal(clk.Now()) || !renewed.ExpiresAt.Equal(clk.Now().Add(15*time.Second)) {
		t.Fatalf("renewed lease=%+v", renewed)
	}
	stored, err := db.GetLease(ctx, lease.ID)
	if err != nil || !stored.RenewedAt.Equal(clk.Now()) {
		t.Fatalf("stored lease=%+v err=%v", stored, err)
	}
}

func TestControlThrottleAndFunctionErrors(t *testing.T) {
	ctx := context.Background()
	control, db, sim, _, user, sess := newControlFixture(t)

	if err := control.Throttle(ctx, user, sess, "loco-bb26001", "missing", -0.1, station.Forward); err == nil {
		t.Fatal("negative speed accepted")
	}
	if err := control.Throttle(ctx, user, sess, "loco-bb26001", "missing", 1.1, station.Forward); err == nil {
		t.Fatal("speed above one accepted")
	}
	if err := control.Throttle(ctx, user, sess, "loco-bb26001", "missing", 0.5, station.Forward); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing lease error=%v", err)
	}
	if err := control.Function(ctx, sess, "loco-bb26001", "missing", 1, true); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing function lease error=%v", err)
	}

	lease, err := control.Acquire(ctx, user, sess, "loco-bb26001")
	if err != nil {
		t.Fatal(err)
	}
	if err := control.Throttle(ctx, user, sess, "other-loco", lease.ID, 0.5, station.Forward); err == nil {
		t.Fatal("lease used for another locomotive")
	}
	if err := control.Throttle(ctx, user, model.Session{ID: "other"}, "loco-bb26001", lease.ID, 0.5, station.Forward); err == nil {
		t.Fatal("lease used by another session")
	}
	if err := control.Function(ctx, model.Session{ID: "other"}, "loco-bb26001", lease.ID, 1, true); err == nil {
		t.Fatal("function used by another session")
	}

	if err := sim.Close(); err != nil {
		t.Fatal(err)
	}
	if err := control.Throttle(ctx, user, sess, "loco-bb26001", lease.ID, 0.5, station.Forward); err == nil {
		t.Fatal("throttle succeeded while station disconnected")
	}
	if err := control.Function(ctx, sess, "loco-bb26001", lease.ID, 1, true); err == nil {
		t.Fatal("function succeeded while station disconnected")
	}
	if err := sim.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if err := control.Function(ctx, sess, "loco-bb26001", lease.ID, 1, true); err != nil {
		t.Fatal(err)
	}
	if !sim.Loco(2601).Functions[1] {
		t.Fatal("function state was not recorded")
	}

	if err := db.ReleaseLease(ctx, lease.ID, sess.ID, "test"); err != nil {
		t.Fatal(err)
	}
	if err := control.Throttle(ctx, user, sess, "loco-bb26001", lease.ID, 0.5, station.Forward); err == nil {
		t.Fatal("released lease used for throttle")
	}
	if err := control.Function(ctx, sess, "loco-bb26001", lease.ID, 1, false); err == nil {
		t.Fatal("released lease used for function")
	}
}

func TestControlReleaseErrorsAndStopFailure(t *testing.T) {
	ctx := context.Background()
	control, db, sim, _, user, sess := newControlFixture(t)

	if err := control.Release(ctx, "missing", sess); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing release error=%v", err)
	}
	lease, err := control.Acquire(ctx, user, sess, "loco-bb26001")
	if err != nil {
		t.Fatal(err)
	}
	if err := control.Release(ctx, lease.ID, model.Session{ID: "other"}); err == nil {
		t.Fatal("other session released lease")
	}
	if err := sim.Close(); err != nil {
		t.Fatal(err)
	}
	err = control.Release(ctx, lease.ID, sess)
	if err == nil || !strings.Contains(err.Error(), "stop command failed") {
		t.Fatalf("stop failure error=%v", err)
	}
	stored, getErr := db.GetLease(ctx, lease.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if stored.State != model.LeaseStopping || stored.ReleaseReason != "client_release" || stored.ReleaseAfter == nil {
		t.Fatalf("lease after failed stop=%+v", stored)
	}
}

func TestControlStartAndCloseAreSafe(t *testing.T) {
	control, _, _, _, _, _ := newControlFixture(t)
	control.Start()
	control.Close()
	control.Close()
}
