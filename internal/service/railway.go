package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/store"
)

type RailwayService struct {
	store               *store.Store
	station             station.CommandStation
	events              *events.Bus
	confirmationTimeout time.Duration

	turnoutLocksMu sync.Mutex
	turnoutLocks   map[string]*turnoutCommandLock

	accessoryMu          sync.Mutex
	accessoryStates      map[string]map[string]model.AccessoryPosition
	accessoryReports     map[string]map[string]station.AccessoryReportState
	accessoryQualities   map[string]map[string]station.AccessoryReportQuality
	accessoryGenerations map[string]map[string]uint64
	turnoutRuntime       map[string]*turnoutCommandRuntime
}

const DefaultTurnoutConfirmationTimeout = 2 * time.Second

var (
	ErrTurnoutConfirmationTimeout = errors.New("turnout confirmation timeout")
	ErrUnsafeTurnoutTransition    = errors.New("no safe turnout transition")
)

type turnoutCommandLock struct {
	mu   sync.Mutex
	refs int
}

type turnoutCommandRuntime struct {
	generation   uint64
	confirmation uint64
	startedAt    time.Time
	updates      chan struct{}
}

func NewRailwayService(s *store.Store, st station.CommandStation, b *events.Bus, confirmationTimeout ...time.Duration) *RailwayService {
	timeout := DefaultTurnoutConfirmationTimeout
	if len(confirmationTimeout) > 0 && confirmationTimeout[0] > 0 {
		timeout = confirmationTimeout[0]
	}
	return &RailwayService{
		store:                s,
		station:              st,
		events:               b,
		confirmationTimeout:  timeout,
		turnoutLocks:         map[string]*turnoutCommandLock{},
		accessoryStates:      map[string]map[string]model.AccessoryPosition{},
		accessoryReports:     map[string]map[string]station.AccessoryReportState{},
		accessoryQualities:   map[string]map[string]station.AccessoryReportQuality{},
		accessoryGenerations: map[string]map[string]uint64{},
		turnoutRuntime:       map[string]*turnoutCommandRuntime{},
	}
}
func (r *RailwayService) Locomotives(ctx context.Context) ([]model.Locomotive, error) {
	return r.store.ListLocomotives(ctx)
}
func (r *RailwayService) Locomotive(ctx context.Context, id string) (model.Locomotive, error) {
	return r.store.GetLocomotive(ctx, id)
}

func (r *RailwayService) CreateLocomotive(ctx context.Context, user model.User, input model.LocomotiveInput) (model.Locomotive, error) {
	if !Allowed(user.Role, PermissionConfigure) {
		return model.Locomotive{}, ErrPermissionDenied
	}
	x, err := locomotiveFromInput(newID(), input)
	if err != nil {
		return model.Locomotive{}, err
	}
	if err := r.store.CreateLocomotive(ctx, x); err != nil {
		return model.Locomotive{}, err
	}
	r.events.Publish("locomotive.created", x)
	return x, nil
}

func (r *RailwayService) UpdateLocomotive(ctx context.Context, user model.User, id string, input model.LocomotiveInput) (model.Locomotive, error) {
	if !Allowed(user.Role, PermissionConfigure) {
		return model.Locomotive{}, ErrPermissionDenied
	}
	if _, err := r.store.GetLocomotive(ctx, id); err != nil {
		return model.Locomotive{}, err
	}
	if _, err := r.store.LiveLeaseForLoco(ctx, id); err == nil {
		return model.Locomotive{}, fmt.Errorf("locomotive has an active control lease: %w", store.ErrConflict)
	} else if !errors.Is(err, store.ErrNotFound) {
		return model.Locomotive{}, err
	}
	x, err := locomotiveFromInput(id, input)
	if err != nil {
		return model.Locomotive{}, err
	}
	if err := r.store.UpdateLocomotive(ctx, x); err != nil {
		return model.Locomotive{}, err
	}
	r.events.Publish("locomotive.updated", x)
	return x, nil
}

func (r *RailwayService) DeleteLocomotive(ctx context.Context, user model.User, id string) error {
	if !Allowed(user.Role, PermissionConfigure) {
		return ErrPermissionDenied
	}
	if _, err := r.store.GetLocomotive(ctx, id); err != nil {
		return err
	}
	if _, err := r.store.LiveLeaseForLoco(ctx, id); err == nil {
		return fmt.Errorf("locomotive has an active control lease: %w", store.ErrConflict)
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if err := r.store.DeleteLocomotive(ctx, id); err != nil {
		return err
	}
	r.events.Publish("locomotive.deleted", map[string]any{"locomotiveId": id})
	return nil
}

func locomotiveFromInput(id string, input model.LocomotiveInput) (model.Locomotive, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.AddressKind = strings.ToLower(strings.TrimSpace(input.AddressKind))
	input.Manufacturer = strings.TrimSpace(input.Manufacturer)
	input.Model = strings.TrimSpace(input.Model)

	if input.Name == "" {
		return model.Locomotive{}, invalid("name is required")
	}
	if len(input.Name) > 128 {
		return model.Locomotive{}, invalid("name must be at most 128 characters")
	}
	if len(input.Manufacturer) > 128 {
		return model.Locomotive{}, invalid("manufacturer must be at most 128 characters")
	}
	if len(input.Model) > 128 {
		return model.Locomotive{}, invalid("model must be at most 128 characters")
	}
	if input.DCCAddress < 1 || input.DCCAddress > 10239 {
		return model.Locomotive{}, invalid("dccAddress must be in range 1..10239")
	}
	switch input.AddressKind {
	case "short":
		if input.DCCAddress > 127 {
			return model.Locomotive{}, invalid("short dccAddress must be in range 1..127")
		}
	case "long":
		if input.DCCAddress < 128 {
			return model.Locomotive{}, invalid("long dccAddress must be in range 128..10239")
		}
	default:
		return model.Locomotive{}, invalid("addressKind must be short or long")
	}
	switch input.SpeedSteps {
	case 14, 28, 128:
	default:
		return model.Locomotive{}, invalid("speedSteps must be one of 14, 28 or 128")
	}

	return model.Locomotive{
		ID:           id,
		Name:         input.Name,
		DCCAddress:   input.DCCAddress,
		AddressKind:  input.AddressKind,
		SpeedSteps:   input.SpeedSteps,
		Manufacturer: input.Manufacturer,
		Model:        input.Model,
	}, nil
}
func (r *RailwayService) Blocks(ctx context.Context) ([]model.Block, error) {
	return r.store.ListBlocks(ctx)
}
func (r *RailwayService) Turnouts(ctx context.Context) ([]model.Turnout, error) {
	return r.store.ListTurnouts(ctx)
}
func (r *RailwayService) SetTurnout(ctx context.Context, user model.User, id, position string) error {
	if !Allowed(user.Role, PermissionDispatch) {
		return ErrPermissionDenied
	}
	release := r.lockTurnout(id)
	defer release()
	if err := station.CheckCommandAllowed(r.station); err != nil {
		return err
	}
	t, err := r.store.GetTurnout(ctx, id)
	if err != nil {
		return err
	}
	_, exists := t.Position(position)
	if !exists {
		valid := make([]string, 0, len(t.Positions))
		for _, candidate := range t.Positions {
			valid = append(valid, candidate.ID)
		}
		return fmt.Errorf("%w: %q is not declared; valid positions: %s", ErrInvalidTurnoutPosition, position, strings.Join(valid, ", "))
	}
	r.seedAccessoryState(t)
	generation := r.beginTurnoutCommand(id)
	if err := r.store.SetTurnoutDesiredPosition(ctx, id, position, true); err != nil {
		return err
	}
	r.events.Publish("turnout.commanded", map[string]any{
		"turnoutId":      id,
		"targetPosition": position,
	})

	path, err := safeTurnoutPath(t, t.ReportedPosition, position)
	if err != nil {
		return r.failTurnoutCommand(ctx, t, position, "unsafe_transition", errors.Join(ErrTurnoutTransitionFailed, err))
	}
	if len(path) == 0 {
		if err := r.store.SetTurnoutCommandResult(ctx, id, false, model.TurnoutCommandSucceeded); err != nil {
			return err
		}
		r.publishTurnoutState(ctx, id)
		return nil
	}

	currentPosition := t.ReportedPosition
	for _, next := range path {
		changed := changedTurnoutEndpoints(t, currentPosition, next.ID)
		if len(changed) == 0 {
			currentPosition = next.ID
			continue
		}
		confirmation := r.beginTurnoutStep(id, generation)
		for _, endpoint := range t.Endpoints {
			if !changed[endpoint.ID] {
				continue
			}
			required, ok := next.Endpoints[endpoint.ID]
			if !ok {
				return r.failTurnoutCommand(ctx, t, position, "invalid_definition", fmt.Errorf("%w: invalid turnout definition", ErrTurnoutTransitionFailed))
			}
			command := station.AccessoryCommand{
				Address:  endpoint.LinearAddress,
				Position: model.PhysicalAccessoryPosition(endpoint, required),
			}
			if err := r.station.SetBasicAccessory(ctx, command); err != nil {
				return r.failTurnoutCommand(ctx, t, position, "driver_error", errors.Join(ErrTurnoutTransitionFailed, err))
			}
		}
		if err := r.waitForTurnoutPosition(ctx, t, generation, confirmation, next.ID, changed); err != nil {
			reason := "confirmation_timeout"
			status := model.TurnoutCommandTimeout
			if !errors.Is(err, ErrTurnoutConfirmationTimeout) {
				reason = "confirmation_interrupted"
				status = model.TurnoutCommandFailed
			}
			return r.failTurnoutCommandWithStatus(ctx, t, position, reason, status, err)
		}
		currentPosition = next.ID
	}
	if err := r.store.SetTurnoutCommandResult(ctx, id, false, model.TurnoutCommandSucceeded); err != nil {
		return err
	}
	r.publishTurnoutState(ctx, id)
	return nil
}
func (r *RailwayService) SetBlockFeedback(ctx context.Context, id string, occupied bool) error {
	if err := r.store.SetBlockOccupied(ctx, id, occupied); err != nil {
		return err
	}
	r.events.Publish("block.occupancy.changed", map[string]any{"blockId": id, "occupied": occupied})
	return nil
}

func (r *RailwayService) StartFeedback(ctx context.Context) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-r.station.Feedback():
				if !ok {
					return
				}
				blockID, err := r.store.BlockForFeedback(ctx, event.Source, event.Address)
				if err != nil {
					continue
				}
				_ = r.SetBlockFeedback(ctx, blockID, event.Active)
			}
		}
	}()
	provider, ok := r.station.(station.AccessoryStateEventProvider)
	if !ok {
		return
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case event, open := <-provider.AccessoryStateEvents():
				if !open {
					return
				}
				r.handleAccessoryStateEvent(ctx, event)
			}
		}
	}()
}

func (r *RailwayService) handleAccessoryStateEvent(ctx context.Context, event station.AccessoryStateEvent) {
	turnouts, err := r.store.ListTurnoutsByAccessoryAddress(ctx, event.Address)
	if err != nil {
		return
	}
	for _, turnout := range turnouts {
		for _, endpoint := range turnout.Endpoints {
			if endpoint.LinearAddress != event.Address {
				continue
			}
			r.accessoryMu.Lock()
			r.ensureAccessoryMapsLocked(turnout.ID)
			states := r.accessoryStates[turnout.ID]
			if event.HasKnownPosition() {
				states[endpoint.ID] = event.Position
				r.accessoryReports[turnout.ID][endpoint.ID] = station.AccessoryReportKnown
			} else if event.State.Valid() {
				states[endpoint.ID] = ""
				r.accessoryReports[turnout.ID][endpoint.ID] = event.State
			} else {
				r.accessoryMu.Unlock()
				break
			}
			if event.Quality.Valid() {
				r.accessoryQualities[turnout.ID][endpoint.ID] = event.Quality
			}
			if runtime := r.turnoutRuntime[turnout.ID]; runtime != nil &&
				(event.ObservedAt.IsZero() || !event.ObservedAt.Before(runtime.startedAt)) {
				r.accessoryGenerations[turnout.ID][endpoint.ID] = runtime.confirmation
			}
			position, reportState, quality := r.resolveTurnoutObservationLocked(turnout)
			r.accessoryMu.Unlock()
			_ = r.persistTurnoutObservation(ctx, turnout.ID, position, reportState, quality)
			r.accessoryMu.Lock()
			r.signalTurnoutUpdateLocked(turnout.ID)
			r.accessoryMu.Unlock()
			break
		}
	}
}

func (r *RailwayService) persistTurnoutObservation(ctx context.Context, turnoutID, position string, reportState station.AccessoryReportState, quality station.AccessoryReportQuality) error {
	if err := r.store.SetTurnoutObservation(ctx, turnoutID, position, reportState, quality); err != nil {
		return err
	}
	r.publishTurnoutState(ctx, turnoutID)
	return nil
}

func (r *RailwayService) resolveTurnoutObservationLocked(turnout model.Turnout) (string, station.AccessoryReportState, station.AccessoryReportQuality) {
	states := r.accessoryStates[turnout.ID]
	reports := r.accessoryReports[turnout.ID]
	qualities := r.accessoryQualities[turnout.ID]
	quality := station.AccessoryReportPhysical
	for _, endpoint := range turnout.Endpoints {
		report, exists := reports[endpoint.ID]
		if !exists || report == station.AccessoryReportUnknown {
			return "", station.AccessoryReportUnknown, aggregateAccessoryQuality(quality, qualities[endpoint.ID])
		}
		if report == station.AccessoryReportInvalid {
			return "", station.AccessoryReportInvalid, aggregateAccessoryQuality(quality, qualities[endpoint.ID])
		}
		if !states[endpoint.ID].Valid() {
			return "", station.AccessoryReportUnknown, aggregateAccessoryQuality(quality, qualities[endpoint.ID])
		}
		quality = aggregateAccessoryQuality(quality, qualities[endpoint.ID])
	}
	position, ok := model.ResolveTurnoutPosition(turnout, states)
	if !ok {
		return "", station.AccessoryReportInvalid, quality
	}
	return position, station.AccessoryReportKnown, quality
}

func aggregateAccessoryQuality(current, next station.AccessoryReportQuality) station.AccessoryReportQuality {
	if !next.Valid() {
		return station.AccessoryReportAssumed
	}
	rank := func(quality station.AccessoryReportQuality) int {
		switch quality {
		case station.AccessoryReportPhysical:
			return 3
		case station.AccessoryReportStation:
			return 2
		default:
			return 1
		}
	}
	if rank(next) < rank(current) {
		return next
	}
	return current
}

func (r *RailwayService) seedAccessoryState(turnout model.Turnout) {
	position, ok := turnout.Position(turnout.ReportedPosition)
	if !ok {
		return
	}
	r.accessoryMu.Lock()
	defer r.accessoryMu.Unlock()
	r.ensureAccessoryMapsLocked(turnout.ID)
	for _, endpoint := range turnout.Endpoints {
		if _, exists := r.accessoryReports[turnout.ID][endpoint.ID]; exists {
			continue
		}
		r.accessoryStates[turnout.ID][endpoint.ID] = model.PhysicalAccessoryPosition(endpoint, position.Endpoints[endpoint.ID])
		r.accessoryReports[turnout.ID][endpoint.ID] = station.AccessoryReportKnown
		quality := turnout.Quality
		if !quality.Valid() {
			quality = station.AccessoryReportAssumed
		}
		r.accessoryQualities[turnout.ID][endpoint.ID] = quality
	}
}

func (r *RailwayService) ensureAccessoryMapsLocked(turnoutID string) {
	if r.accessoryStates[turnoutID] == nil {
		r.accessoryStates[turnoutID] = map[string]model.AccessoryPosition{}
	}
	if r.accessoryReports[turnoutID] == nil {
		r.accessoryReports[turnoutID] = map[string]station.AccessoryReportState{}
	}
	if r.accessoryQualities[turnoutID] == nil {
		r.accessoryQualities[turnoutID] = map[string]station.AccessoryReportQuality{}
	}
	if r.accessoryGenerations[turnoutID] == nil {
		r.accessoryGenerations[turnoutID] = map[string]uint64{}
	}
}

func (r *RailwayService) beginTurnoutCommand(turnoutID string) uint64 {
	r.accessoryMu.Lock()
	defer r.accessoryMu.Unlock()
	runtime := r.turnoutRuntime[turnoutID]
	if runtime == nil {
		runtime = &turnoutCommandRuntime{updates: make(chan struct{})}
		r.turnoutRuntime[turnoutID] = runtime
	}
	runtime.generation++
	r.signalTurnoutUpdateLocked(turnoutID)
	return runtime.generation
}

func (r *RailwayService) beginTurnoutStep(turnoutID string, generation uint64) uint64 {
	r.accessoryMu.Lock()
	defer r.accessoryMu.Unlock()
	runtime := r.turnoutRuntime[turnoutID]
	if runtime == nil || runtime.generation != generation {
		return 0
	}
	runtime.confirmation++
	runtime.startedAt = time.Now()
	r.signalTurnoutUpdateLocked(turnoutID)
	return runtime.confirmation
}

func (r *RailwayService) signalTurnoutUpdateLocked(turnoutID string) {
	runtime := r.turnoutRuntime[turnoutID]
	if runtime == nil {
		return
	}
	close(runtime.updates)
	runtime.updates = make(chan struct{})
}

func (r *RailwayService) waitForTurnoutPosition(ctx context.Context, turnout model.Turnout, generation, confirmation uint64, target string, changed map[string]bool) error {
	timer := time.NewTimer(r.confirmationTimeout)
	defer timer.Stop()
	for {
		r.accessoryMu.Lock()
		runtime := r.turnoutRuntime[turnout.ID]
		if runtime == nil || runtime.generation != generation {
			r.accessoryMu.Unlock()
			return errors.New("turnout command superseded")
		}
		confirmed := true
		for endpointID := range changed {
			if r.accessoryGenerations[turnout.ID][endpointID] != confirmation {
				confirmed = false
				break
			}
		}
		updates := runtime.updates
		r.accessoryMu.Unlock()

		current, err := r.store.GetTurnout(ctx, turnout.ID)
		if err != nil {
			return err
		}
		if current.ReportedStatus == station.AccessoryReportKnown && current.ReportedPosition == target && confirmed {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return ErrTurnoutConfirmationTimeout
		case <-updates:
		}
	}
}

func (r *RailwayService) failTurnoutCommand(ctx context.Context, turnout model.Turnout, target, reason string, cause error) error {
	return r.failTurnoutCommandWithStatus(ctx, turnout, target, reason, model.TurnoutCommandFailed, cause)
}

func (r *RailwayService) failTurnoutCommandWithStatus(ctx context.Context, turnout model.Turnout, target, reason string, status model.TurnoutCommandStatus, cause error) error {
	// Persist the terminal state even when the caller canceled while waiting for
	// a physical confirmation. Otherwise a canceled command could remain
	// indefinitely pending in snapshots and events.
	cleanupCtx := context.WithoutCancel(ctx)
	if err := r.store.SetTurnoutCommandResult(cleanupCtx, turnout.ID, false, status); err != nil {
		return errors.Join(cause, err)
	}
	r.events.Publish("turnout.command.failed", map[string]any{
		"turnoutId":      turnout.ID,
		"targetPosition": target,
		"reason":         reason,
	})
	r.publishTurnoutState(cleanupCtx, turnout.ID)
	return cause
}

func (r *RailwayService) publishTurnoutState(ctx context.Context, turnoutID string) {
	turnout, err := r.store.GetTurnout(ctx, turnoutID)
	if err != nil {
		return
	}
	payload := map[string]any{
		"turnoutId":        turnout.ID,
		"desiredPosition":  turnout.DesiredPosition,
		"reportedPosition": turnout.ReportedPosition,
		"reportedStatus":   turnout.ReportedStatus,
		"pending":          turnout.Pending,
		"commandStatus":    turnout.CommandStatus,
	}
	if turnout.Quality.Valid() {
		payload["reportQuality"] = turnout.Quality
	}
	r.events.Publish("turnout.state.changed", payload)
}

func (r *RailwayService) lockTurnout(turnoutID string) func() {
	r.turnoutLocksMu.Lock()
	lock := r.turnoutLocks[turnoutID]
	if lock == nil {
		lock = &turnoutCommandLock{}
		r.turnoutLocks[turnoutID] = lock
	}
	lock.refs++
	r.turnoutLocksMu.Unlock()
	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		r.turnoutLocksMu.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(r.turnoutLocks, turnoutID)
		}
		r.turnoutLocksMu.Unlock()
	}
}

func safeTurnoutPath(turnout model.Turnout, from, target string) ([]model.TurnoutPositionDefinition, error) {
	targetPosition, ok := turnout.Position(target)
	if !ok {
		return nil, invalid("turnout position is not declared")
	}
	if from == "" {
		return []model.TurnoutPositionDefinition{targetPosition}, nil
	}
	if from == target {
		return nil, nil
	}
	if _, ok := turnout.Position(from); !ok {
		return nil, fmt.Errorf("%w: current position %q is not declared", ErrUnsafeTurnoutTransition, from)
	}
	type pathItem struct {
		id   string
		path []string
	}
	queue := []pathItem{{id: from}}
	visited := map[string]bool{from: true}
	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		current, _ := turnout.Position(item.id)
		for _, candidate := range turnout.Positions {
			if visited[candidate.ID] || endpointVectorDistance(turnout, current, candidate) != 1 {
				continue
			}
			path := append(append([]string(nil), item.path...), candidate.ID)
			if candidate.ID == target {
				result := make([]model.TurnoutPositionDefinition, 0, len(path))
				for _, id := range path {
					position, _ := turnout.Position(id)
					result = append(result, position)
				}
				return result, nil
			}
			visited[candidate.ID] = true
			queue = append(queue, pathItem{id: candidate.ID, path: path})
		}
	}
	return nil, fmt.Errorf("%w: %s -> %s", ErrUnsafeTurnoutTransition, from, target)
}

func endpointVectorDistance(turnout model.Turnout, left, right model.TurnoutPositionDefinition) int {
	distance := 0
	for _, endpoint := range turnout.Endpoints {
		if left.Endpoints[endpoint.ID] != right.Endpoints[endpoint.ID] {
			distance++
		}
	}
	return distance
}

func changedTurnoutEndpoints(turnout model.Turnout, from, target string) map[string]bool {
	changed := make(map[string]bool, len(turnout.Endpoints))
	targetPosition, _ := turnout.Position(target)
	fromPosition, hasFrom := turnout.Position(from)
	for _, endpoint := range turnout.Endpoints {
		if !hasFrom || fromPosition.Endpoints[endpoint.ID] != targetPosition.Endpoints[endpoint.ID] {
			changed[endpoint.ID] = true
		}
	}
	return changed
}
