package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/model/turnoutfixture"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/station/simulator"
	"github.com/agm650/TrainPilot-server/internal/store"
)

func TestRailwayServiceComposesTripleAndReportsInvalidPhysicalVector(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fixture := tripleTurnout()
	db, sim, service := newAccessoryRailwayService(t, ctx, fixture)
	defer db.Close()

	dispatcher := model.User{Role: model.RoleDispatcher}
	if err := service.SetTurnout(ctx, dispatcher, "triple", "left"); err != nil {
		t.Fatal(err)
	}
	waitForTurnoutPosition(t, ctx, db, "triple", "left", false)
	if got := sim.Accessory(fixture.Endpoints[0].LinearAddress); got.Reported != station.AccessoryPosition2 {
		t.Fatalf("endpoint A=%+v", got)
	}
	if got := sim.Accessory(fixture.Endpoints[1].LinearAddress); got.Reported != station.AccessoryPosition1 {
		t.Fatalf("endpoint B=%+v", got)
	}

	if err := sim.ReportAccessoryPosition(fixture.Endpoints[0].LinearAddress, station.AccessoryPosition2, station.AccessoryReportPhysical); err != nil {
		t.Fatal(err)
	}
	if err := sim.ReportAccessoryPosition(fixture.Endpoints[1].LinearAddress, station.AccessoryPosition2, station.AccessoryReportPhysical); err != nil {
		t.Fatal(err)
	}
	waitForTurnoutPosition(t, ctx, db, "triple", "", false)
	turnout, err := db.GetTurnout(ctx, "triple")
	if err != nil {
		t.Fatal(err)
	}
	if turnout.ReportedStatus != station.AccessoryReportInvalid {
		t.Fatalf("invalid physical vector status=%q", turnout.ReportedStatus)
	}
}

func TestRailwayServiceComposesEveryDoubleSlipPosition(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fixture := doubleSlipTurnout()
	db, _, service := newAccessoryRailwayService(t, ctx, fixture)
	defer db.Close()
	dispatcher := model.User{Role: model.RoleDispatcher}

	for _, position := range []string{"route_a", "route_b", "route_c", "route_d"} {
		if err := service.SetTurnout(ctx, dispatcher, fixture.ID, position); err != nil {
			t.Fatalf("set %s: %v", position, err)
		}
		waitForTurnoutPosition(t, ctx, db, fixture.ID, position, false)
	}
}

func TestRailwayServiceReproducesPartialAccessoryFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fixture := tripleTurnout()
	fixture.DesiredPosition = "left"
	fixture.ReportedPosition = "left"
	db, sim, service := newAccessoryRailwayService(t, ctx, fixture)
	defer db.Close()
	eventCh, unsubscribe := service.events.Subscribe(16)
	defer unsubscribe()
	errInjected := errors.New("endpoint B failed")
	if err := sim.SetOperationFault(simulator.OpAccessory, simulator.OperationFault{
		Address:   fixture.Endpoints[1].LinearAddress,
		Error:     errInjected,
		Remaining: 1,
	}); err != nil {
		t.Fatal(err)
	}

	err := service.SetTurnout(ctx, model.User{Role: model.RoleDispatcher}, "triple", "right")
	if !errors.Is(err, errInjected) {
		t.Fatalf("SetTurnout error=%v", err)
	}
	if got := sim.Accessory(fixture.Endpoints[0].LinearAddress); got.Desired != station.AccessoryPosition1 {
		t.Fatalf("endpoint A was not commanded: %+v", got)
	}
	if got := sim.Accessory(fixture.Endpoints[1].LinearAddress); got.Desired == station.AccessoryPosition2 || got.Reported != station.AccessoryPosition1 || got.Pending {
		t.Fatalf("failed endpoint B left its initial position: %+v", got)
	}
	turnout, err := db.GetTurnout(ctx, "triple")
	if err != nil {
		t.Fatal(err)
	}
	if turnout.DesiredPosition != "right" || turnout.Pending || turnout.CommandStatus != model.TurnoutCommandFailed {
		t.Fatalf("logical state after partial failure=%+v", turnout)
	}
	foundFailed := false
	for i := 0; i < 4; i++ {
		select {
		case event := <-eventCh:
			if event.Type == "turnout.command.failed" {
				payload, ok := event.Payload.(map[string]any)
				foundFailed = ok && payload["turnoutId"] == "triple" && payload["targetPosition"] == "right" && payload["reason"] == "driver_error"
			}
		case <-time.After(20 * time.Millisecond):
			i = 4
		}
	}
	if !foundFailed {
		t.Fatal("turnout.command.failed event not published with public payload")
	}
}

func TestRailwayServiceFailsBeforeFirstCompoundEndpoint(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fixture := tripleTurnout()
	fixture.DesiredPosition = "left"
	fixture.ReportedPosition = "left"
	db, sim, railway := newAccessoryRailwayService(t, ctx, fixture)
	defer db.Close()
	errInjected := errors.New("endpoint A failed")
	if err := sim.SetOperationFault(simulator.OpAccessory, simulator.OperationFault{
		Address: fixture.Endpoints[0].LinearAddress, Error: errInjected, Remaining: 1,
	}); err != nil {
		t.Fatal(err)
	}

	err := railway.SetTurnout(ctx, model.User{Role: model.RoleDispatcher}, fixture.ID, "right")
	if !errors.Is(err, errInjected) {
		t.Fatalf("SetTurnout error=%v", err)
	}
	if got := sim.Accessory(fixture.Endpoints[0].LinearAddress); got.Reported != station.AccessoryPosition2 || got.Desired == station.AccessoryPosition1 {
		t.Fatalf("failed first endpoint changed=%+v", got)
	}
	stored, err := db.GetTurnout(ctx, fixture.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ReportedPosition != "left" || stored.Pending || stored.CommandStatus != model.TurnoutCommandFailed {
		t.Fatalf("state after first endpoint failure=%+v", stored)
	}
}

func TestRailwayServiceSimpleNoConfirmationTimesOut(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	turnout := model.NewSimpleTurnout("simple", "Simple", 12, "straight", "straight")
	db, sim, service := newAccessoryRailwayServiceWithTimeout(t, ctx, turnout, 20*time.Millisecond)
	defer db.Close()
	if err := sim.SetAccessoryBehavior(12, simulator.AccessoryBehavior{Mode: simulator.AccessoryBehaviorNoConfirmation}); err != nil {
		t.Fatal(err)
	}

	err := service.SetTurnout(ctx, model.User{Role: model.RoleDispatcher}, turnout.ID, "diverging")
	if !errors.Is(err, ErrTurnoutConfirmationTimeout) {
		t.Fatalf("SetTurnout error=%v", err)
	}
	stored, err := db.GetTurnout(ctx, turnout.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Pending || stored.DesiredPosition != "diverging" || stored.ReportedPosition != "straight" || stored.CommandStatus != model.TurnoutCommandTimeout {
		t.Fatalf("turnout after timeout=%+v", stored)
	}
}

func TestRailwayServiceSimpleInconsistentReportDoesNotConfirmTarget(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	turnout := model.NewSimpleTurnout("simple", "Simple", 12, "straight", "straight")
	db, sim, service := newAccessoryRailwayServiceWithTimeout(t, ctx, turnout, 20*time.Millisecond)
	defer db.Close()
	if err := sim.SetAccessoryBehavior(12, simulator.AccessoryBehavior{
		Mode:             simulator.AccessoryBehaviorInconsistent,
		ReportedPosition: station.AccessoryPosition1,
	}); err != nil {
		t.Fatal(err)
	}

	err := service.SetTurnout(ctx, model.User{Role: model.RoleDispatcher}, turnout.ID, "diverging")
	if !errors.Is(err, ErrTurnoutConfirmationTimeout) {
		t.Fatalf("SetTurnout error=%v", err)
	}
	stored, err := db.GetTurnout(ctx, turnout.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ReportedPosition != "straight" || stored.Pending || stored.CommandStatus != model.TurnoutCommandTimeout {
		t.Fatalf("turnout after inconsistent report=%+v", stored)
	}
}

func TestRailwayServiceCompoundWrongConfirmationNeverSucceeds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fixture := tripleTurnout()
	db, sim, railway := newAccessoryRailwayServiceWithTimeout(t, ctx, fixture, 20*time.Millisecond)
	defer db.Close()
	if err := sim.SetAccessoryBehavior(fixture.Endpoints[1].LinearAddress, simulator.AccessoryBehavior{
		Mode: simulator.AccessoryBehaviorInconsistent, ReportedPosition: station.AccessoryPosition1,
	}); err != nil {
		t.Fatal(err)
	}

	err := railway.SetTurnout(ctx, model.User{Role: model.RoleDispatcher}, fixture.ID, "right")
	if !errors.Is(err, ErrTurnoutConfirmationTimeout) {
		t.Fatalf("SetTurnout error=%v", err)
	}
	stored, err := db.GetTurnout(ctx, fixture.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ReportedPosition != "straight" || stored.Pending || stored.CommandStatus != model.TurnoutCommandTimeout {
		t.Fatalf("state after wrong compound confirmation=%+v", stored)
	}
}

func TestRailwayServiceCommandInterruptedByContextNeverSucceeds(t *testing.T) {
	serviceCtx, stopService := context.WithCancel(context.Background())
	defer stopService()
	turnout := model.NewSimpleTurnout("simple", "Simple", 12, "straight", "straight")
	db, sim, railway := newAccessoryRailwayServiceWithTimeoutAtPath(t, serviceCtx, turnout, time.Second, filepath.Join(t.TempDir(), "cancellation.db"))
	defer db.Close()
	if err := sim.SetAccessoryBehavior(12, simulator.AccessoryBehavior{Mode: simulator.AccessoryBehaviorNoConfirmation}); err != nil {
		t.Fatal(err)
	}

	commandCtx, cancelCommand := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- railway.SetTurnout(commandCtx, model.User{Role: model.RoleDispatcher}, turnout.ID, "diverging")
	}()
	waitForSimulatorAccessoryPending(t, sim, 12)
	cancelCommand()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("SetTurnout error=%v, want context.Canceled", err)
	}
	stored, err := db.GetTurnout(serviceCtx, turnout.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Pending || stored.CommandStatus != model.TurnoutCommandFailed || stored.ReportedPosition != "straight" {
		t.Fatalf("state after cancellation=%+v", stored)
	}
}

func TestRailwayServiceStationGoingOfflineDuringConfirmationNeverSucceeds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	turnout := model.NewSimpleTurnout("simple", "Simple", 12, "straight", "straight")
	db, sim, railway := newAccessoryRailwayServiceWithTimeout(t, ctx, turnout, 20*time.Millisecond)
	defer db.Close()
	if err := sim.SetAccessoryBehavior(12, simulator.AccessoryBehavior{Mode: simulator.AccessoryBehaviorNoConfirmation}); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		done <- railway.SetTurnout(ctx, model.User{Role: model.RoleDispatcher}, turnout.ID, "diverging")
	}()
	waitForSimulatorAccessoryPending(t, sim, 12)
	if err := sim.SetConnectivity(station.Offline); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrTurnoutConfirmationTimeout) {
		t.Fatalf("SetTurnout error=%v, want confirmation timeout", err)
	}
	stored, err := db.GetTurnout(ctx, turnout.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Pending || stored.CommandStatus != model.TurnoutCommandTimeout || stored.ReportedPosition != "straight" {
		t.Fatalf("state after station outage=%+v", stored)
	}
}

func TestRailwayServiceTripleUsesOnlySafeIntermediateVectors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	turnout := tripleTurnout()
	turnout.DesiredPosition = "left"
	turnout.ReportedPosition = "left"
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.ImportLayout(ctx, model.LayoutDefinition{Turnouts: []model.Turnout{turnout}}, false); err != nil {
		t.Fatal(err)
	}
	sim := simulator.New()
	if err := sim.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	recorder := &recordingAccessoryStation{Simulator: sim}
	service := NewRailwayService(db, recorder, events.New())
	service.StartFeedback(ctx)

	if err := service.SetTurnout(ctx, model.User{Role: model.RoleDispatcher}, turnout.ID, "right"); err != nil {
		t.Fatal(err)
	}
	commands := recorder.Commands()
	if len(commands) != 2 || commands[0].Address != turnout.Endpoints[0].LinearAddress || commands[0].Position != station.AccessoryPosition1 || commands[1].Address != turnout.Endpoints[1].LinearAddress || commands[1].Position != station.AccessoryPosition2 {
		t.Fatalf("unsafe or non-deterministic command sequence=%+v", commands)
	}
	vectors := []map[string]model.AccessoryPosition{
		{"A": model.AccessoryPosition2, "B": model.AccessoryPosition1},
		{"A": model.AccessoryPosition1, "B": model.AccessoryPosition1},
		{"A": model.AccessoryPosition1, "B": model.AccessoryPosition2},
	}
	for _, vector := range vectors {
		if _, ok := model.ResolveTurnoutPosition(turnout, vector); !ok {
			t.Fatalf("transition traversed invalid vector=%+v", vector)
		}
	}
}

func TestRailwayServicePartialTimeoutPreservesIntermediateReport(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	turnout := tripleTurnout()
	turnout.DesiredPosition = "left"
	turnout.ReportedPosition = "left"
	db, sim, service := newAccessoryRailwayServiceWithTimeout(t, ctx, turnout, 20*time.Millisecond)
	defer db.Close()
	if err := sim.SetAccessoryBehavior(turnout.Endpoints[1].LinearAddress, simulator.AccessoryBehavior{Mode: simulator.AccessoryBehaviorNoConfirmation}); err != nil {
		t.Fatal(err)
	}

	err := service.SetTurnout(ctx, model.User{Role: model.RoleDispatcher}, turnout.ID, "right")
	if !errors.Is(err, ErrTurnoutConfirmationTimeout) {
		t.Fatalf("SetTurnout error=%v", err)
	}
	stored, err := db.GetTurnout(ctx, turnout.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ReportedPosition != "straight" || stored.DesiredPosition != "right" || stored.Pending || stored.CommandStatus != model.TurnoutCommandTimeout {
		t.Fatalf("partial timeout state=%+v", stored)
	}
}

func TestRailwayServiceIgnoresStaleConfirmationForNewCommand(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	turnout := model.NewSimpleTurnout("simple", "Simple", 12, "straight", "straight")
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.ImportLayout(ctx, model.LayoutDefinition{Turnouts: []model.Turnout{turnout}}, false); err != nil {
		t.Fatal(err)
	}
	manual := newManualAccessoryStation()
	service := NewRailwayService(db, manual, events.New(), 30*time.Millisecond)
	service.StartFeedback(ctx)
	dispatcher := model.User{Role: model.RoleDispatcher}
	oldObservedAt := time.Now().Add(-time.Second)
	if err := service.SetTurnout(ctx, dispatcher, turnout.ID, "diverging"); !errors.Is(err, ErrTurnoutConfirmationTimeout) {
		t.Fatalf("first command error=%v", err)
	}
	<-manual.commands

	done := make(chan error, 1)
	go func() { done <- service.SetTurnout(ctx, dispatcher, turnout.ID, "diverging") }()
	<-manual.commands
	manual.events <- station.AccessoryStateEvent{Address: 12, Position: station.AccessoryPosition2, State: station.AccessoryReportKnown, Quality: station.AccessoryReportStation, ObservedAt: oldObservedAt}
	select {
	case err := <-done:
		t.Fatalf("stale confirmation completed new command: %v", err)
	case <-time.After(5 * time.Millisecond):
	}
	manual.events <- station.AccessoryStateEvent{Address: 12, Position: station.AccessoryPosition2, State: station.AccessoryReportKnown, Quality: station.AccessoryReportStation, ObservedAt: time.Now()}
	if err := <-done; err != nil {
		t.Fatalf("fresh confirmation error=%v", err)
	}
}

func TestRailwayServiceSerializesSameTurnoutCommands(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	turnout := tripleTurnout()
	turnout.DesiredPosition = "straight"
	turnout.ReportedPosition = "straight"
	db, _, service := newAccessoryRailwayService(t, ctx, turnout)
	defer db.Close()
	dispatcher := model.User{Role: model.RoleDispatcher}
	positions := []string{"left", "straight", "right"}
	var wg sync.WaitGroup
	errs := make(chan error, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		position := positions[i%len(positions)]
		go func() {
			defer wg.Done()
			errs <- service.SetTurnout(ctx, dispatcher, turnout.ID, position)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent command error=%v", err)
		}
	}
	stored, err := db.GetTurnout(ctx, turnout.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Pending || stored.ReportedPosition != stored.DesiredPosition || stored.ReportedStatus != station.AccessoryReportKnown {
		t.Fatalf("incoherent final turnout=%+v", stored)
	}
}

func TestRailwayServiceAllowsDifferentTurnoutsInParallel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := model.NewSimpleTurnout("first", "First", 31, "straight", "straight")
	second := model.NewSimpleTurnout("second", "Second", 32, "straight", "straight")
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.ImportLayout(ctx, model.LayoutDefinition{Turnouts: []model.Turnout{first, second}}, false); err != nil {
		t.Fatal(err)
	}
	manual := newManualAccessoryStation()
	service := NewRailwayService(db, manual, events.New(), time.Second)
	service.StartFeedback(ctx)
	dispatcher := model.User{Role: model.RoleDispatcher}
	done := make(chan error, 2)
	go func() { done <- service.SetTurnout(ctx, dispatcher, first.ID, "diverging") }()
	go func() { done <- service.SetTurnout(ctx, dispatcher, second.ID, "diverging") }()

	commands := []station.AccessoryCommand{<-manual.commands, <-manual.commands}
	if commands[0].Address == commands[1].Address {
		t.Fatalf("commands did not enter in parallel: %+v", commands)
	}
	for _, command := range commands {
		manual.events <- station.AccessoryStateEvent{Address: command.Address, Position: command.Position, State: station.AccessoryReportKnown, Quality: station.AccessoryReportStation, ObservedAt: time.Now()}
	}
	for range commands {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func TestRailwayServiceHandlesTwentyTurnoutsAndExternalReportsConcurrently(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	definitions := make([]model.Turnout, 20)
	for index := range definitions {
		definitions[index] = model.NewSimpleTurnout(
			fmt.Sprintf("turnout-%02d", index),
			fmt.Sprintf("Turnout %02d", index),
			100+index,
			"",
			"",
		)
	}
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.ImportLayout(ctx, model.LayoutDefinition{Turnouts: definitions}, false); err != nil {
		t.Fatal(err)
	}
	sim := simulator.New()
	if err := sim.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	railway := NewRailwayService(db, sim, events.New(), time.Second)
	railway.StartFeedback(ctx)
	dispatcher := model.User{Role: model.RoleDispatcher}

	errCh := make(chan error, 100)
	var commands sync.WaitGroup
	for index := 0; index < 100; index++ {
		definition := definitions[index%len(definitions)]
		position := "straight"
		if index%2 == 0 {
			position = "diverging"
		}
		commands.Add(1)
		go func() {
			defer commands.Done()
			errCh <- railway.SetTurnout(ctx, dispatcher, definition.ID, position)
		}()
	}
	commands.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent command: %v", err)
		}
	}

	desiredBeforeReports := make(map[string]string, len(definitions))
	for _, definition := range definitions {
		stored, err := db.GetTurnout(ctx, definition.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.Pending || stored.ReportedPosition != stored.DesiredPosition || stored.CommandStatus != model.TurnoutCommandSucceeded {
			t.Fatalf("state after concurrent commands=%+v", stored)
		}
		desiredBeforeReports[definition.ID] = stored.DesiredPosition
	}

	var reports sync.WaitGroup
	for _, definition := range definitions {
		definition := definition
		reports.Add(1)
		go func() {
			defer reports.Done()
			if err := sim.ReportAccessoryPosition(definition.Endpoints[0].LinearAddress, station.AccessoryPosition1, station.AccessoryReportPhysical); err != nil {
				t.Errorf("external report: %v", err)
			}
		}()
	}
	reports.Wait()
	for _, definition := range definitions {
		waitForTurnoutPosition(t, ctx, db, definition.ID, "straight", false)
		stored, err := db.GetTurnout(ctx, definition.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.DesiredPosition != desiredBeforeReports[definition.ID] || stored.Quality != station.AccessoryReportPhysical {
			t.Fatalf("external report changed desired state or quality=%+v", stored)
		}
	}
}

func TestRailwayServiceExternalChangeUpdatesReportedOnly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	turnout := model.NewSimpleTurnout("simple", "Simple", 12, "straight", "straight")
	db, sim, _ := newAccessoryRailwayService(t, ctx, turnout)
	defer db.Close()
	if err := sim.ReportAccessoryPosition(12, station.AccessoryPosition2, station.AccessoryReportPhysical); err != nil {
		t.Fatal(err)
	}
	waitForTurnoutPosition(t, ctx, db, turnout.ID, "diverging", false)
	stored, err := db.GetTurnout(ctx, turnout.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.DesiredPosition != "straight" || stored.Quality != station.AccessoryReportPhysical {
		t.Fatalf("external change rewrote desired or quality=%+v", stored)
	}
}

func TestRailwayServiceAggregatesLowestAccessoryQuality(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	turnout := tripleTurnout()
	db, sim, _ := newAccessoryRailwayService(t, ctx, turnout)
	defer db.Close()
	if err := sim.ReportAccessoryPosition(turnout.Endpoints[0].LinearAddress, station.AccessoryPosition1, station.AccessoryReportStation); err != nil {
		t.Fatal(err)
	}
	if err := sim.ReportAccessoryPosition(turnout.Endpoints[1].LinearAddress, station.AccessoryPosition1, station.AccessoryReportAssumed); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	var stored model.Turnout
	var err error
	for {
		stored, err = db.GetTurnout(ctx, turnout.ID)
		if err == nil && stored.ReportedPosition == "straight" && !stored.Pending && stored.Quality == station.AccessoryReportAssumed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("aggregate quality was not observed: turnout=%+v err=%v", stored, err)
		}
		time.Sleep(time.Millisecond)
	}
	if stored.Quality != station.AccessoryReportAssumed {
		t.Fatalf("aggregate quality=%q want assumed", stored.Quality)
	}
}

func newAccessoryRailwayService(t *testing.T, ctx context.Context, turnout model.Turnout) (*store.Store, *simulator.Simulator, *RailwayService) {
	return newAccessoryRailwayServiceWithTimeout(t, ctx, turnout, DefaultTurnoutConfirmationTimeout)
}

func newAccessoryRailwayServiceWithTimeout(t *testing.T, ctx context.Context, turnout model.Turnout, timeout time.Duration) (*store.Store, *simulator.Simulator, *RailwayService) {
	return newAccessoryRailwayServiceWithTimeoutAtPath(t, ctx, turnout, timeout, ":memory:")
}

func newAccessoryRailwayServiceWithTimeoutAtPath(t *testing.T, ctx context.Context, turnout model.Turnout, timeout time.Duration, path string) (*store.Store, *simulator.Simulator, *RailwayService) {
	t.Helper()
	db, err := store.Open(path)
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
	service := NewRailwayService(db, sim, events.New(), timeout)
	service.StartFeedback(ctx)
	if position, ok := turnout.Position(turnout.ReportedPosition); ok {
		for _, endpoint := range turnout.Endpoints {
			if err := sim.ReportAccessoryPosition(
				endpoint.LinearAddress,
				model.PhysicalAccessoryPosition(endpoint, position.Endpoints[endpoint.ID]),
				station.AccessoryReportPhysical,
			); err != nil {
				db.Close()
				t.Fatal(err)
			}
		}
		deadline := time.Now().Add(time.Second)
		for {
			stored, err := db.GetTurnout(ctx, turnout.ID)
			if err == nil && stored.ReportedPosition == turnout.ReportedPosition && stored.Quality == station.AccessoryReportPhysical {
				break
			}
			if time.Now().After(deadline) {
				db.Close()
				t.Fatalf("initial accessory reports not consumed: turnout=%+v err=%v", stored, err)
			}
			time.Sleep(time.Millisecond)
		}
	}
	return db, sim, service
}

type recordingAccessoryStation struct {
	*simulator.Simulator
	mu       sync.Mutex
	commands []station.AccessoryCommand
}

func (s *recordingAccessoryStation) SetBasicAccessory(ctx context.Context, command station.AccessoryCommand) error {
	s.mu.Lock()
	s.commands = append(s.commands, command)
	s.mu.Unlock()
	return s.Simulator.SetBasicAccessory(ctx, command)
}

func (s *recordingAccessoryStation) Commands() []station.AccessoryCommand {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]station.AccessoryCommand(nil), s.commands...)
}

type manualAccessoryStation struct {
	commands chan station.AccessoryCommand
	events   chan station.AccessoryStateEvent
	feedback chan station.FeedbackEvent
}

func newManualAccessoryStation() *manualAccessoryStation {
	return &manualAccessoryStation{
		commands: make(chan station.AccessoryCommand, 256),
		events:   make(chan station.AccessoryStateEvent, 256),
		feedback: make(chan station.FeedbackEvent),
	}
}

func (s *manualAccessoryStation) Connect(context.Context) error { return nil }
func (s *manualAccessoryStation) Close() error                  { return nil }
func (s *manualAccessoryStation) Capabilities() station.Capabilities {
	return station.Capabilities{AccessoryControl: true}
}
func (s *manualAccessoryStation) SetTrackPower(context.Context, bool) error { return nil }
func (s *manualAccessoryStation) EmergencyStop(context.Context) error       { return nil }
func (s *manualAccessoryStation) SetLocoSpeed(context.Context, int, float64, station.Direction) error {
	return nil
}
func (s *manualAccessoryStation) SetLocoFunction(context.Context, int, int, bool) error {
	return nil
}
func (s *manualAccessoryStation) SetBasicAccessory(_ context.Context, command station.AccessoryCommand) error {
	if err := command.Validate(); err != nil {
		return err
	}
	s.commands <- command
	return nil
}
func (s *manualAccessoryStation) Feedback() <-chan station.FeedbackEvent { return s.feedback }
func (s *manualAccessoryStation) AccessoryStateEvents() <-chan station.AccessoryStateEvent {
	return s.events
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

func waitForSimulatorAccessoryPending(t *testing.T, sim *simulator.Simulator, address int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !sim.Accessory(address).Pending {
		if time.Now().After(deadline) {
			t.Fatalf("accessory %d did not become pending: %+v", address, sim.Accessory(address))
		}
		time.Sleep(time.Millisecond)
	}
}

func tripleTurnout() model.Turnout {
	return turnoutfixture.ThreeWay()
}

func doubleSlipTurnout() model.Turnout {
	return turnoutfixture.DoubleSlip()
}
