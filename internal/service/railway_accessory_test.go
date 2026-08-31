package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/station/simulator"
	"github.com/agm650/TrainPilot-server/internal/store"
)

func TestRailwayServiceComposesTripleAndReportsInvalidPhysicalVector(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, sim, service := newAccessoryRailwayService(t, ctx, tripleTurnout())
	defer db.Close()

	dispatcher := model.User{Role: model.RoleDispatcher}
	if err := service.SetTurnout(ctx, dispatcher, "triple", "left"); err != nil {
		t.Fatal(err)
	}
	waitForTurnoutPosition(t, ctx, db, "triple", "left", false)
	if got := sim.Accessory(1); got.Reported != station.AccessoryPosition2 {
		t.Fatalf("endpoint A=%+v", got)
	}
	if got := sim.Accessory(2); got.Reported != station.AccessoryPosition1 {
		t.Fatalf("endpoint B=%+v", got)
	}

	if err := sim.ReportAccessoryPosition(1, station.AccessoryPosition2, station.AccessoryReportPhysical); err != nil {
		t.Fatal(err)
	}
	if err := sim.ReportAccessoryPosition(2, station.AccessoryPosition2, station.AccessoryReportPhysical); err != nil {
		t.Fatal(err)
	}
	waitForTurnoutPosition(t, ctx, db, "triple", "", true)
}

func TestRailwayServiceComposesEveryDoubleSlipPosition(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, _, service := newAccessoryRailwayService(t, ctx, doubleSlipTurnout())
	defer db.Close()
	dispatcher := model.User{Role: model.RoleDispatcher}

	for _, position := range []string{"route_a", "route_b", "route_c", "route_d"} {
		if err := service.SetTurnout(ctx, dispatcher, "tjd", position); err != nil {
			t.Fatalf("set %s: %v", position, err)
		}
		waitForTurnoutPosition(t, ctx, db, "tjd", position, false)
	}
}

func TestRailwayServiceReproducesPartialAccessoryFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, sim, service := newAccessoryRailwayService(t, ctx, tripleTurnout())
	defer db.Close()
	errInjected := errors.New("endpoint B failed")
	if err := sim.SetOperationFault(simulator.OpAccessory, simulator.OperationFault{
		Address:   2,
		Error:     errInjected,
		Remaining: 1,
	}); err != nil {
		t.Fatal(err)
	}

	err := service.SetTurnout(ctx, model.User{Role: model.RoleDispatcher}, "triple", "right")
	if !errors.Is(err, errInjected) {
		t.Fatalf("SetTurnout error=%v", err)
	}
	if got := sim.Accessory(1); got.Desired != station.AccessoryPosition1 {
		t.Fatalf("endpoint A was not commanded: %+v", got)
	}
	if got := sim.Accessory(2); got != (simulator.AccessoryState{}) {
		t.Fatalf("failed endpoint B changed: %+v", got)
	}
	turnout, err := db.GetTurnout(ctx, "triple")
	if err != nil {
		t.Fatal(err)
	}
	if turnout.DesiredPosition != "right" || !turnout.Pending {
		t.Fatalf("logical state after partial failure=%+v", turnout)
	}
}

func newAccessoryRailwayService(t *testing.T, ctx context.Context, turnout model.Turnout) (*store.Store, *simulator.Simulator, *RailwayService) {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ImportLayout(ctx, model.LayoutDefinition{Turnouts: []model.Turnout{turnout}}, false); err != nil {
		db.Close()
		t.Fatal(err)
	}
	sim := simulator.New()
	if err := sim.Connect(ctx); err != nil {
		db.Close()
		t.Fatal(err)
	}
	service := NewRailwayService(db, sim, events.New())
	service.StartFeedback(ctx)
	return db, sim, service
}

func waitForTurnoutPosition(t *testing.T, ctx context.Context, db *store.Store, id, reported string, pending bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		turnout, err := db.GetTurnout(ctx, id)
		if err == nil && turnout.ReportedPosition == reported && turnout.Pending == pending {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("turnout %s did not reach reported=%q pending=%t: state=%+v err=%v", id, reported, pending, turnout, err)
		}
		time.Sleep(time.Millisecond)
	}
}

func tripleTurnout() model.Turnout {
	return model.Turnout{
		ID:   "triple",
		Name: "Triple",
		Kind: model.TurnoutKindThreeWay,
		Endpoints: []model.AccessoryEndpoint{
			{ID: "A", LinearAddress: 1},
			{ID: "B", LinearAddress: 2},
		},
		Positions: []model.TurnoutPositionDefinition{
			{ID: "left", Endpoints: map[string]model.AccessoryPosition{"A": model.AccessoryPosition2, "B": model.AccessoryPosition1}},
			{ID: "straight", Endpoints: map[string]model.AccessoryPosition{"A": model.AccessoryPosition1, "B": model.AccessoryPosition1}},
			{ID: "right", Endpoints: map[string]model.AccessoryPosition{"A": model.AccessoryPosition1, "B": model.AccessoryPosition2}},
		},
	}
}

func doubleSlipTurnout() model.Turnout {
	return model.Turnout{
		ID:   "tjd",
		Name: "TJD",
		Kind: model.TurnoutKindDoubleSlip,
		Endpoints: []model.AccessoryEndpoint{
			{ID: "A", LinearAddress: 3},
			{ID: "B", LinearAddress: 4},
		},
		Positions: []model.TurnoutPositionDefinition{
			{ID: "route_a", Endpoints: map[string]model.AccessoryPosition{"A": model.AccessoryPosition1, "B": model.AccessoryPosition1}},
			{ID: "route_b", Endpoints: map[string]model.AccessoryPosition{"A": model.AccessoryPosition1, "B": model.AccessoryPosition2}},
			{ID: "route_c", Endpoints: map[string]model.AccessoryPosition{"A": model.AccessoryPosition2, "B": model.AccessoryPosition1}},
			{ID: "route_d", Endpoints: map[string]model.AccessoryPosition{"A": model.AccessoryPosition2, "B": model.AccessoryPosition2}},
		},
	}
}
