package service

import (
	"context"
	"errors"
	"sync"
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
	eventCh, unsubscribe := service.events.Subscribe(16)
	defer unsubscribe()
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
	if len(commands) != 2 || commands[0].Address != 1 || commands[0].Position != station.AccessoryPosition1 || commands[1].Address != 2 || commands[1].Position != station.AccessoryPosition2 {
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
	if err := sim.SetAccessoryBehavior(2, simulator.AccessoryBehavior{Mode: simulator.AccessoryBehaviorNoConfirmation}); err != nil {
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
	if err := sim.ReportAccessoryPosition(1, station.AccessoryPosition1, station.AccessoryReportStation); err != nil {
		t.Fatal(err)
	}
	if err := sim.ReportAccessoryPosition(2, station.AccessoryPosition1, station.AccessoryReportAssumed); err != nil {
		t.Fatal(err)
	}
	waitForTurnoutPosition(t, ctx, db, turnout.ID, "straight", false)
	stored, err := db.GetTurnout(ctx, turnout.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Quality != station.AccessoryReportAssumed {
		t.Fatalf("aggregate quality=%q want assumed", stored.Quality)
	}
}

func newAccessoryRailwayService(t *testing.T, ctx context.Context, turnout model.Turnout) (*store.Store, *simulator.Simulator, *RailwayService) {
	return newAccessoryRailwayServiceWithTimeout(t, ctx, turnout, DefaultTurnoutConfirmationTimeout)
}

func newAccessoryRailwayServiceWithTimeout(t *testing.T, ctx context.Context, turnout model.Turnout, timeout time.Duration) (*store.Store, *simulator.Simulator, *RailwayService) {
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
	service := NewRailwayService(db, sim, events.New(), timeout)
	service.StartFeedback(ctx)
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
