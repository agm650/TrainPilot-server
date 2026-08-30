package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/station/simulator"
	simscenario "github.com/agm650/TrainPilot-server/internal/station/simulator/scenario"
)

type simulatorTestController struct {
	mu sync.Mutex

	simulator *simulator.Simulator
	runner    *simscenario.Runner
}

type simulatorFeedbackState struct {
	Source  string `json:"source"`
	Kind    string `json:"kind"`
	Address int    `json:"address"`
	Active  bool   `json:"active"`
}

type simulatorFaultState struct {
	Delay     string `json:"delay"`
	Remaining int    `json:"remaining"`
	Error     string `json:"error,omitempty"`
}

type simulatorAccessoryBehavior struct {
	Mode          simulator.AccessoryBehaviorMode `json:"mode"`
	Delay         string                          `json:"delay"`
	ReportedState string                          `json:"reportedState,omitempty"`
}

type simulatorScenarioState struct {
	Name      string            `json:"name"`
	State     simscenario.State `json:"state"`
	Elapsed   string            `json:"elapsed"`
	NextStep  int               `json:"nextStep"`
	StepCount int               `json:"stepCount"`
	Error     string            `json:"error,omitempty"`
}

type simulatorStateResponse struct {
	Connected          bool                               `json:"connected"`
	Connectivity       station.Connectivity               `json:"connectivity"`
	LastSeen           *time.Time                         `json:"lastSeen,omitempty"`
	TrackPower         bool                               `json:"trackPower"`
	EmergencyStop      bool                               `json:"emergencyStop"`
	Locomotives        map[int]simulator.LocoState        `json:"locomotives"`
	Accessories        map[int]simulator.AccessoryState   `json:"accessories"`
	AccessoryBehaviors map[int]simulatorAccessoryBehavior `json:"accessoryBehaviors"`
	Electrical         simulator.ElectricalState          `json:"electrical"`
	FeedbackStates     []simulatorFeedbackState           `json:"feedbackStates"`
	Faults             map[string]simulatorFaultState     `json:"faults"`
	Scenario           *simulatorScenarioState            `json:"scenario,omitempty"`
}

func newSimulatorTestController(sim *simulator.Simulator) *simulatorTestController {
	return &simulatorTestController{simulator: sim}
}

func (c *simulatorTestController) scenarioSnapshot() *simulatorScenarioState {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.runner == nil {
		return nil
	}
	snapshot := c.runner.Snapshot()
	response := simulatorScenarioResponse(snapshot)
	return &response
}

func (c *simulatorTestController) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.runner != nil {
		c.runner.Stop()
	}
	c.runner = nil
	c.simulator.Reset()
}

func (c *simulatorTestController) load(definition *simscenario.Definition) (simscenario.ControlSnapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.runner != nil {
		c.runner.Stop()
	}
	logicalClock := clock.NewFake(time.Now().UTC())
	runner, err := simscenario.New(definition, c.simulator, logicalClock)
	if err != nil {
		return simscenario.ControlSnapshot{}, err
	}
	c.runner = runner
	return runner.Snapshot(), nil
}

func (c *simulatorTestController) start(ctx context.Context) (simscenario.ControlSnapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.runner == nil {
		return simscenario.ControlSnapshot{}, fmt.Errorf("no simulator scenario loaded: %w", simscenario.ErrNotRunning)
	}
	if err := c.runner.Start(ctx); err != nil {
		return c.runner.Snapshot(), err
	}
	return c.runner.Snapshot(), nil
}

func (c *simulatorTestController) advance(ctx context.Context, duration time.Duration) (simscenario.ControlSnapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.runner == nil {
		return simscenario.ControlSnapshot{}, fmt.Errorf("no simulator scenario loaded: %w", simscenario.ErrNotRunning)
	}
	if err := c.runner.Advance(ctx, duration); err != nil {
		return c.runner.Snapshot(), err
	}
	return c.runner.Snapshot(), nil
}

func (c *simulatorTestController) stop() (simscenario.ControlSnapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.runner == nil {
		return simscenario.ControlSnapshot{}, fmt.Errorf("no simulator scenario loaded: %w", simscenario.ErrNotRunning)
	}
	c.runner.Stop()
	return c.runner.Snapshot(), nil
}

func (s *Server) testSimulatorState(w http.ResponseWriter, _ *http.Request) {
	snapshot := s.simulator.Snapshot()
	feedback := make([]simulatorFeedbackState, 0, len(snapshot.FeedbackStates))
	for key, active := range snapshot.FeedbackStates {
		feedback = append(feedback, simulatorFeedbackState{Source: key.Source, Kind: key.Kind, Address: key.Address, Active: active})
	}
	sort.Slice(feedback, func(i, j int) bool {
		if feedback[i].Source != feedback[j].Source {
			return feedback[i].Source < feedback[j].Source
		}
		if feedback[i].Kind != feedback[j].Kind {
			return feedback[i].Kind < feedback[j].Kind
		}
		return feedback[i].Address < feedback[j].Address
	})
	faults := make(map[string]simulatorFaultState, len(snapshot.OperationFaults))
	for operation, fault := range snapshot.OperationFaults {
		faults[string(operation)] = simulatorFaultState{Delay: fault.Delay.String(), Remaining: fault.Remaining, Error: fault.Error}
	}
	behaviors := make(map[int]simulatorAccessoryBehavior, len(snapshot.AccessoryBehaviors))
	for address, behavior := range snapshot.AccessoryBehaviors {
		behaviors[address] = simulatorAccessoryBehavior{Mode: behavior.Mode, Delay: behavior.Delay.String(), ReportedState: behavior.ReportedState}
	}
	writeJSON(w, http.StatusOK, simulatorStateResponse{
		Connected:          snapshot.Connected,
		Connectivity:       snapshot.Connectivity,
		LastSeen:           snapshot.LastSeen,
		TrackPower:         snapshot.TrackPower,
		EmergencyStop:      snapshot.EmergencyStop,
		Locomotives:        snapshot.Locomotives,
		Accessories:        snapshot.Accessories,
		AccessoryBehaviors: behaviors,
		Electrical:         snapshot.Electrical,
		FeedbackStates:     feedback,
		Faults:             faults,
		Scenario:           s.simulatorTest.scenarioSnapshot(),
	})
}

func (s *Server) testSimulatorReset(w http.ResponseWriter, _ *http.Request) {
	s.simulatorTest.reset()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) testSimulatorConnectivity(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Connectivity *station.Connectivity `json:"connectivity"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.Connectivity == nil || (*request.Connectivity != station.Online && *request.Connectivity != station.Degraded && *request.Connectivity != station.Offline) {
		writeProblem(w, http.StatusBadRequest, "invalid_connectivity", "connectivity must be online, degraded or offline")
		return
	}
	if err := s.simulator.SetConnectivity(*request.Connectivity); err != nil {
		writeProblem(w, http.StatusConflict, "simulator_state_conflict", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) testSimulatorElectrical(w http.ResponseWriter, r *http.Request) {
	var state simulator.ElectricalState
	if !decodeJSON(w, r, &state) {
		return
	}
	s.simulator.SetElectricalState(state)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) testSimulatorFeedback(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Source  string `json:"source"`
		Kind    string `json:"kind"`
		Address *int   `json:"address"`
		Active  *bool  `json:"active"`
		Emit    *bool  `json:"emit"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if strings.TrimSpace(request.Source) == "" || strings.TrimSpace(request.Kind) == "" || request.Address == nil || *request.Address < 0 || request.Active == nil || request.Emit == nil {
		writeProblem(w, http.StatusBadRequest, "invalid_feedback", "source, kind, non-negative address, active and emit are required")
		return
	}
	event := station.FeedbackEvent{Source: request.Source, Kind: request.Kind, Address: *request.Address, Active: *request.Active}
	if *request.Emit {
		if err := s.simulator.SetFeedbackAtomic(r.Context(), event); err != nil {
			writeProblem(w, http.StatusConflict, "feedback_delivery_failed", err.Error())
			return
		}
	} else {
		s.simulator.SetFeedbackState(event)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) testSimulatorAccessoryReportedState(w http.ResponseWriter, r *http.Request) {
	address, ok := testSimulatorAddress(w, r.PathValue("address"))
	if !ok {
		return
	}
	var request struct {
		State string `json:"state"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if err := s.simulator.ReportAccessoryState(address, request.State); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_accessory_state", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) testSimulatorAccessoryBehavior(w http.ResponseWriter, r *http.Request) {
	address, ok := testSimulatorAddress(w, r.PathValue("address"))
	if !ok {
		return
	}
	var request struct {
		Mode          simulator.AccessoryBehaviorMode `json:"mode"`
		Delay         string                          `json:"delay,omitempty"`
		ReportedState string                          `json:"reportedState,omitempty"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	delay, ok := testSimulatorDuration(w, request.Delay, true)
	if !ok {
		return
	}
	behavior := simulator.AccessoryBehavior{Mode: request.Mode, Delay: delay, ReportedState: request.ReportedState}
	if err := s.simulator.SetAccessoryBehavior(address, behavior); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_accessory_behavior", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) testSimulatorFault(w http.ResponseWriter, r *http.Request) {
	operation := simulator.Operation(r.PathValue("operation"))
	if !operation.Valid() {
		writeProblem(w, http.StatusBadRequest, "invalid_simulator_operation", "unknown simulator operation")
		return
	}
	var request struct {
		Delay     string `json:"delay,omitempty"`
		Remaining *int   `json:"remaining,omitempty"`
		Error     string `json:"error,omitempty"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	delay, ok := testSimulatorDuration(w, request.Delay, true)
	if !ok {
		return
	}
	remaining := 0
	if request.Remaining != nil {
		remaining = *request.Remaining
	}
	if remaining < 0 || (delay == 0 && strings.TrimSpace(request.Error) == "") {
		writeProblem(w, http.StatusBadRequest, "invalid_simulator_fault", "remaining must be non-negative and a positive delay or error is required")
		return
	}
	fault := simulator.OperationFault{Delay: delay, Remaining: remaining}
	if request.Error != "" {
		fault.Error = errors.New(request.Error)
	}
	if err := s.simulator.SetOperationFault(operation, fault); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_simulator_fault", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) testSimulatorClearFaults(w http.ResponseWriter, _ *http.Request) {
	s.simulator.ClearFaults()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) testSimulatorLoadScenario(w http.ResponseWriter, r *http.Request) {
	definition, err := simscenario.Load(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_simulator_scenario", err.Error())
		return
	}
	snapshot, err := s.simulatorTest.load(definition)
	if err != nil {
		writeSimulatorScenarioProblem(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, simulatorScenarioResponse(snapshot))
}

func (s *Server) testSimulatorStartScenario(w http.ResponseWriter, r *http.Request) {
	snapshot, err := s.simulatorTest.start(r.Context())
	if err != nil {
		writeSimulatorScenarioProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, simulatorScenarioResponse(snapshot))
}

func (s *Server) testSimulatorAdvanceScenario(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Duration string `json:"duration"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	duration, ok := testSimulatorDuration(w, request.Duration, false)
	if !ok {
		return
	}
	snapshot, err := s.simulatorTest.advance(r.Context(), duration)
	if err != nil {
		writeSimulatorScenarioProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, simulatorScenarioResponse(snapshot))
}

func (s *Server) testSimulatorStopScenario(w http.ResponseWriter, _ *http.Request) {
	snapshot, err := s.simulatorTest.stop()
	if err != nil {
		writeSimulatorScenarioProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, simulatorScenarioResponse(snapshot))
}

func testSimulatorAddress(w http.ResponseWriter, value string) (int, bool) {
	address, err := strconv.Atoi(value)
	if err != nil || address < 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_simulator_address", "address must be a non-negative integer")
		return 0, false
	}
	return address, true
}

func testSimulatorDuration(w http.ResponseWriter, value string, optional bool) (time.Duration, bool) {
	if value == "" && optional {
		return 0, true
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration < 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_duration", "duration must use Go duration syntax and must not be negative")
		return 0, false
	}
	return duration, true
}

func writeSimulatorScenarioProblem(w http.ResponseWriter, err error) {
	status := http.StatusConflict
	code := "simulator_scenario_conflict"
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		code = "simulator_scenario_stopped"
	}
	writeProblem(w, status, code, err.Error())
}

func simulatorScenarioResponse(snapshot simscenario.ControlSnapshot) simulatorScenarioState {
	return simulatorScenarioState{
		Name:      snapshot.Name,
		State:     snapshot.State,
		Elapsed:   snapshot.Elapsed.String(),
		NextStep:  snapshot.NextStep,
		StepCount: snapshot.StepCount,
		Error:     snapshot.Error,
	}
}
