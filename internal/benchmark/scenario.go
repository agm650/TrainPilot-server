package benchmark

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/agm650/TrainPilot-server/internal/client"
	simscenario "github.com/agm650/TrainPilot-server/internal/station/simulator/scenario"
)

type SimulatorScenario struct {
	Name   string
	SHA256 string
	Data   json.RawMessage
	Steps  []SimulatorScenarioStep
}

type SimulatorScenarioStep struct {
	At     time.Duration
	Action string
}

type ScenarioSummary struct {
	Name      string                `json:"name"`
	SHA256    string                `json:"sha256"`
	StartedAt time.Time             `json:"startedAt"`
	EndedAt   time.Time             `json:"endedAt"`
	Status    string                `json:"status"`
	Steps     []ScenarioStepSummary `json:"steps"`
}

type ScenarioStepSummary struct {
	At        string    `json:"at"`
	Action    string    `json:"action"`
	AppliedAt time.Time `json:"appliedAt"`
}

type simulatorScenarioState struct {
	Name      string `json:"name"`
	State     string `json:"state"`
	Elapsed   string `json:"elapsed"`
	NextStep  int    `json:"nextStep"`
	StepCount int    `json:"stepCount"`
	Error     string `json:"error,omitempty"`
}

type scenarioRunResult struct {
	summary ScenarioSummary
	err     error
}

func LoadSimulatorScenario(path string) (*SimulatorScenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read simulator scenario: %w", err)
	}
	definition, err := simscenario.Parse(data)
	if err != nil {
		return nil, err
	}
	steps := make([]SimulatorScenarioStep, 0, len(definition.Steps))
	for _, step := range definition.Steps {
		steps = append(steps, SimulatorScenarioStep{At: step.At, Action: string(step.Action)})
	}
	hash := sha256.Sum256(data)
	return &SimulatorScenario{
		Name: definition.Name, SHA256: hex.EncodeToString(hash[:]),
		Data: append(json.RawMessage(nil), data...), Steps: steps,
	}, nil
}

func validateSimulatorScenario(profile Profile, scenario *SimulatorScenario) error {
	if scenario == nil {
		return nil
	}
	if scenario.Name == "" || len(scenario.Data) == 0 || len(scenario.SHA256) != 64 {
		return fmt.Errorf("simulator scenario is incomplete")
	}
	for index, step := range scenario.Steps {
		if step.At < 0 || step.At >= profile.Duration.Duration {
			return fmt.Errorf("simulator scenario step %d at %s must be within the measured duration", index, step.At)
		}
	}
	return nil
}

func (e *runEngine) runSimulatorScenario(ctx context.Context, startedAt time.Time) scenarioRunResult {
	scenario := e.options.SimulatorScenario
	summary := ScenarioSummary{
		Name: scenario.Name, SHA256: scenario.SHA256, StartedAt: startedAt,
		Status: "running", Steps: make([]ScenarioStepSummary, 0, len(scenario.Steps)),
	}
	finish := func(status string, err error) scenarioRunResult {
		summary.EndedAt = time.Now().UTC()
		summary.Status = status
		return scenarioRunResult{summary: summary, err: err}
	}
	do := func(method, path string, body any, result any) error {
		return e.sessions[0].withClient(ctx, func(api *client.Client) error {
			_, err := api.Do(ctx, method, path, body, result)
			return err
		})
	}
	var state simulatorScenarioState
	if err := do(http.MethodPost, "/test/v1/simulator/scenarios", scenario.Data, &state); err != nil {
		return finish("failed", fmt.Errorf("load simulator scenario: %w", err))
	}
	if err := do(http.MethodPost, "/test/v1/simulator/scenarios/start", nil, &state); err != nil {
		return finish("failed", fmt.Errorf("start simulator scenario: %w", err))
	}
	elapsed := time.Duration(0)
	for index := 0; index < len(scenario.Steps); {
		at := scenario.Steps[index].At
		if err := waitContext(ctx, at-elapsed); err != nil {
			return finish("stopped", err)
		}
		appliedAt := time.Now().UTC()
		if err := do(http.MethodPost, "/test/v1/simulator/scenarios/advance", map[string]string{"duration": (at - elapsed).String()}, &state); err != nil {
			return finish("failed", fmt.Errorf("advance simulator scenario to %s: %w", at, err))
		}
		for index < len(scenario.Steps) && scenario.Steps[index].At == at {
			step := scenario.Steps[index]
			summary.Steps = append(summary.Steps, ScenarioStepSummary{At: step.At.String(), Action: step.Action, AppliedAt: appliedAt})
			index++
		}
		elapsed = at
	}
	if state.State != "completed" || state.NextStep != state.StepCount || state.StepCount != len(scenario.Steps) {
		return finish("failed", fmt.Errorf("simulator scenario ended in state %q at step %d/%d", state.State, state.NextStep, state.StepCount))
	}
	return finish("completed", nil)
}

func waitForScenario(ctx context.Context, duration time.Duration, done <-chan scenarioRunResult) (scenarioRunResult, error) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	var result scenarioRunResult
	for {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case current := <-done:
			result = current
			done = nil
			if current.err != nil {
				return result, current.err
			}
		case <-timer.C:
			if done == nil {
				return result, nil
			}
			select {
			case current := <-done:
				if current.err != nil {
					return current, current.err
				}
				return current, nil
			default:
				return result, fmt.Errorf("simulator scenario did not complete within %s", duration)
			}
		}
	}
}
