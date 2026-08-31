package scenario

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/station/simulator"
)

type State string

const (
	StateLoaded    State = "loaded"
	StateRunning   State = "running"
	StateCompleted State = "completed"
	StateStopped   State = "stopped"
	StateFailed    State = "failed"
)

var (
	ErrNotRunning              = errors.New("scenario is not running")
	ErrManualClockRequired     = errors.New("manual scenario execution requires an advancing clock")
	ErrSimulatorLifecycleEnded = errors.New("simulator lifecycle ended")
)

type ControlSnapshot struct {
	Name      string        `json:"name"`
	State     State         `json:"state"`
	Elapsed   time.Duration `json:"elapsed"`
	NextStep  int           `json:"nextStep"`
	StepCount int           `json:"stepCount"`
	Error     string        `json:"error,omitempty"`
}

type advancingClock interface {
	clock.Clock
	Advance(time.Duration)
}

type Runner struct {
	mu sync.Mutex

	definition *Definition
	simulator  *simulator.Simulator
	clock      clock.Clock

	state     State
	elapsed   time.Duration
	nextStep  int
	err       error
	lifecycle <-chan struct{}

	realtime      bool
	realtimeStart time.Time
	cancel        context.CancelFunc
	runtimeDone   chan struct{}
	done          chan struct{}
	doneOnce      sync.Once
}

func New(definition *Definition, sim *simulator.Simulator, clk clock.Clock) (*Runner, error) {
	if definition == nil {
		return nil, fmt.Errorf("scenario definition is nil")
	}
	if sim == nil {
		return nil, fmt.Errorf("scenario simulator is nil")
	}
	if clk == nil {
		return nil, fmt.Errorf("scenario clock is nil")
	}
	return &Runner{
		definition: definition,
		simulator:  sim,
		clock:      clk,
		state:      StateLoaded,
		done:       make(chan struct{}),
	}, nil
}

func (r *Runner) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.clock.(advancingClock); !ok {
		return ErrManualClockRequired
	}
	return r.startLocked(ctx, false)
}

func (r *Runner) StartRealtime(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	runtimeContext, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	if err := r.startLocked(runtimeContext, true); err != nil {
		r.mu.Unlock()
		cancel()
		return err
	}
	if r.state != StateRunning {
		r.mu.Unlock()
		cancel()
		return nil
	}
	r.cancel = cancel
	r.realtimeStart = time.Now()
	r.runtimeDone = make(chan struct{})
	r.mu.Unlock()
	go r.runRealtime(runtimeContext)
	return nil
}

func (r *Runner) startLocked(ctx context.Context, realtime bool) error {
	if r.state != StateLoaded {
		return fmt.Errorf("start scenario in state %q: %w", r.state, ErrNotRunning)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.state = StateRunning
	r.realtime = realtime
	r.lifecycle = r.simulator.LifecycleDone()
	if err := r.applyInitialLocked(); err != nil {
		return r.failLocked(fmt.Errorf("apply initial state: %w", err))
	}
	if err := r.applyDueRealtimeLocked(ctx, 0); err != nil {
		return err
	}
	if r.nextStep == len(r.definition.Steps) {
		r.completeLocked()
	}
	return nil
}

func (r *Runner) Advance(ctx context.Context, duration time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if duration < 0 {
		return fmt.Errorf("scenario advance must not be negative")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != StateRunning || r.realtime {
		return ErrNotRunning
	}
	manualClock, ok := r.clock.(advancingClock)
	if !ok {
		return ErrManualClockRequired
	}
	if duration > time.Duration(1<<63-1)-r.elapsed {
		return fmt.Errorf("scenario logical time overflow")
	}
	if err := r.ensureRunnableLocked(ctx); err != nil {
		return err
	}
	target := r.elapsed + duration
	for r.nextStep < len(r.definition.Steps) && r.definition.Steps[r.nextStep].At <= target {
		step := r.definition.Steps[r.nextStep]
		manualClock.Advance(step.At - r.elapsed)
		r.elapsed = step.At
		if err := r.ensureRunnableLocked(ctx); err != nil {
			return err
		}
		if err := r.applyStepLocked(ctx, step); err != nil {
			return r.failLocked(fmt.Errorf("step %d (%s at %s): %w", r.nextStep, step.Action, step.At, err))
		}
		r.nextStep++
	}
	manualClock.Advance(target - r.elapsed)
	r.elapsed = target
	if r.nextStep == len(r.definition.Steps) {
		r.completeLocked()
	}
	return nil
}

func (r *Runner) Stop() {
	r.mu.Lock()
	runtimeDone := r.runtimeDone
	if r.state == StateLoaded || r.state == StateRunning {
		r.state = StateStopped
		if r.cancel != nil {
			r.cancel()
		}
		r.finishLocked()
	}
	r.mu.Unlock()
	if runtimeDone != nil {
		<-runtimeDone
	}
}

func (r *Runner) Close() error {
	r.Stop()
	return nil
}

func (r *Runner) Done() <-chan struct{} {
	return r.done
}

func (r *Runner) Snapshot() ControlSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	elapsed := r.elapsed
	if r.state == StateRunning && r.realtime {
		wallElapsed := time.Since(r.realtimeStart)
		if wallElapsed > elapsed {
			elapsed = wallElapsed
		}
		if count := len(r.definition.Steps); count > 0 && elapsed > r.definition.Steps[count-1].At {
			elapsed = r.definition.Steps[count-1].At
		}
	}
	snapshot := ControlSnapshot{
		Name:      r.definition.Name,
		State:     r.state,
		Elapsed:   elapsed,
		NextStep:  r.nextStep,
		StepCount: len(r.definition.Steps),
	}
	if r.err != nil {
		snapshot.Error = r.err.Error()
	}
	return snapshot
}

func (r *Runner) runRealtime(ctx context.Context) {
	defer close(r.runtimeDone)
	for {
		r.mu.Lock()
		if r.state != StateRunning {
			r.mu.Unlock()
			return
		}
		if r.nextStep == len(r.definition.Steps) {
			r.completeLocked()
			r.mu.Unlock()
			return
		}
		stepAt := r.definition.Steps[r.nextStep].At
		lifecycle := r.lifecycle
		deadline := r.realtimeStart.Add(stepAt)
		r.mu.Unlock()

		delay := time.Until(deadline)
		if delay < 0 {
			delay = 0
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			stopTimer(timer)
			r.stopFromRuntime()
			return
		case <-lifecycle:
			stopTimer(timer)
			r.stopFromRuntime()
			return
		case <-timer.C:
		}

		r.mu.Lock()
		if err := r.ensureRunnableLocked(ctx); err != nil {
			r.mu.Unlock()
			return
		}
		if err := r.applyDueRealtimeLocked(ctx, stepAt); err != nil {
			r.mu.Unlock()
			return
		}
		if r.nextStep == len(r.definition.Steps) {
			r.completeLocked()
			r.mu.Unlock()
			return
		}
		r.mu.Unlock()
	}
}

func (r *Runner) applyDueRealtimeLocked(ctx context.Context, target time.Duration) error {
	for r.nextStep < len(r.definition.Steps) && r.definition.Steps[r.nextStep].At <= target {
		step := r.definition.Steps[r.nextStep]
		r.elapsed = step.At
		if err := r.ensureRunnableLocked(ctx); err != nil {
			return err
		}
		if err := r.applyStepLocked(ctx, step); err != nil {
			return r.failLocked(fmt.Errorf("step %d (%s at %s): %w", r.nextStep, step.Action, step.At, err))
		}
		r.nextStep++
	}
	return nil
}

func (r *Runner) applyInitialLocked() error {
	initial := r.definition.Initial
	if initial.Connectivity != nil {
		if err := r.simulator.SetConnectivity(*initial.Connectivity); err != nil {
			return err
		}
	}
	if initial.TrackPower != nil {
		r.simulator.SetTrackPowerState(*initial.TrackPower)
	}
	if initial.EmergencyStop != nil {
		r.simulator.SetEmergencyStopState(*initial.EmergencyStop)
	}
	if initial.Electrical != nil {
		r.simulator.SetElectricalState(*initial.Electrical)
	}
	return nil
}

func (r *Runner) applyStepLocked(ctx context.Context, step Step) error {
	switch step.Action {
	case ActionStationConnectivity:
		payload := step.payload.(connectivityPayload)
		return r.simulator.SetConnectivity(payload.Connectivity)
	case ActionStationTrackPower:
		r.simulator.SetTrackPowerState(step.payload.(boolPayload).Value)
	case ActionStationEmergency:
		r.simulator.SetEmergencyStopState(step.payload.(boolPayload).Value)
	case ActionStationElectrical:
		r.simulator.SetElectricalState(step.payload.(electricalPayload).State)
	case ActionFeedbackSet, ActionFeedbackEmit:
		payload := step.payload.(feedbackPayload)
		event := station.FeedbackEvent{Source: payload.Source, Kind: payload.Kind, Address: payload.Address, Active: payload.Active}
		if payload.Emit {
			return r.simulator.SetFeedbackAtomic(ctx, event)
		}
		r.simulator.SetFeedbackState(event)
	case ActionAccessoryReport:
		payload := step.payload.(accessoryReportPayload)
		return r.simulator.ReportAccessoryPosition(payload.Address, payload.Position, payload.Quality)
	case ActionAccessoryBehavior:
		payload := step.payload.(accessoryBehaviorPayload)
		return r.simulator.SetAccessoryBehavior(payload.Address, payload.Behavior)
	case ActionFaultOperation:
		payload := step.payload.(faultPayload)
		fault := payload.Fault
		if payload.Message != "" {
			fault.Error = errors.New(payload.Message)
		}
		return r.simulator.SetOperationFault(payload.Operation, fault)
	case ActionFaultClear:
		r.simulator.ClearFaults()
	case ActionSimulatorReset:
		r.simulator.Reset()
		r.lifecycle = r.simulator.LifecycleDone()
	default:
		return fmt.Errorf("unsupported action %q", step.Action)
	}
	return nil
}

func (r *Runner) ensureRunnableLocked(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		r.state = StateStopped
		r.finishLocked()
		return err
	}
	select {
	case <-r.lifecycle:
		r.state = StateStopped
		r.finishLocked()
		return ErrSimulatorLifecycleEnded
	default:
		return nil
	}
}

func (r *Runner) failLocked(err error) error {
	r.err = err
	r.state = StateFailed
	if r.cancel != nil {
		r.cancel()
	}
	r.finishLocked()
	return err
}

func (r *Runner) completeLocked() {
	r.state = StateCompleted
	r.finishLocked()
}

func (r *Runner) stopFromRuntime() {
	r.mu.Lock()
	if r.state == StateRunning {
		r.state = StateStopped
		r.finishLocked()
	}
	r.mu.Unlock()
}

func (r *Runner) finishLocked() {
	r.doneOnce.Do(func() { close(r.done) })
}

func stopTimer(timer *time.Timer) {
	if timer.Stop() {
		return
	}
	select {
	case <-timer.C:
	default:
	}
}
