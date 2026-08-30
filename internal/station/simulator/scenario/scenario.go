package scenario

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/station/simulator"
)

const CurrentVersion = 1

type Action string

const (
	ActionStationConnectivity Action = "station.connectivity"
	ActionStationTrackPower   Action = "station.track_power"
	ActionStationEmergency    Action = "station.emergency_stop"
	ActionStationElectrical   Action = "station.electrical"
	ActionFeedbackSet         Action = "feedback.set"
	ActionFeedbackEmit        Action = "feedback.emit"
	ActionAccessoryReport     Action = "accessory.report"
	ActionAccessoryBehavior   Action = "accessory.behavior"
	ActionFaultOperation      Action = "fault.operation"
	ActionFaultClear          Action = "fault.clear"
	ActionSimulatorReset      Action = "simulator.reset"
)

type Definition struct {
	Version int
	Name    string
	Initial Initial
	Steps   []Step
}

type Initial struct {
	Connectivity  *station.Connectivity
	TrackPower    *bool
	EmergencyStop *bool
	Electrical    *simulator.ElectricalState
}

type Step struct {
	At      time.Duration
	Action  Action
	payload any
}

type rawDefinition struct {
	Version *int               `json:"version"`
	Name    string             `json:"name"`
	Initial *json.RawMessage   `json:"initial"`
	Steps   *[]json.RawMessage `json:"steps"`
}

type rawInitial struct {
	Connectivity  *station.Connectivity      `json:"connectivity"`
	TrackPower    *bool                      `json:"trackPower"`
	EmergencyStop *bool                      `json:"emergencyStop"`
	Electrical    *simulator.ElectricalState `json:"electrical"`
}

type stepHeader struct {
	At     string `json:"at"`
	Action Action `json:"action"`
}

type connectivityStep struct {
	At           string                `json:"at"`
	Action       Action                `json:"action"`
	Connectivity *station.Connectivity `json:"connectivity"`
}

type trackPowerStep struct {
	At     string `json:"at"`
	Action Action `json:"action"`
	On     *bool  `json:"on"`
}

type emergencyStep struct {
	At     string `json:"at"`
	Action Action `json:"action"`
	Active *bool  `json:"active"`
}

type electricalStep struct {
	At         string                     `json:"at"`
	Action     Action                     `json:"action"`
	Electrical *simulator.ElectricalState `json:"electrical"`
}

type feedbackSetStep struct {
	At      string `json:"at"`
	Action  Action `json:"action"`
	Source  string `json:"source"`
	Kind    string `json:"kind"`
	Address *int   `json:"address"`
	Active  *bool  `json:"active"`
	Emit    *bool  `json:"emit"`
}

type feedbackEmitStep struct {
	At      string `json:"at"`
	Action  Action `json:"action"`
	Source  string `json:"source"`
	Kind    string `json:"kind"`
	Address *int   `json:"address"`
	Active  *bool  `json:"active"`
}

type accessoryReportStep struct {
	At      string `json:"at"`
	Action  Action `json:"action"`
	Address *int   `json:"address"`
	State   string `json:"state"`
}

type accessoryBehaviorStep struct {
	At            string                          `json:"at"`
	Action        Action                          `json:"action"`
	Address       *int                            `json:"address"`
	Mode          simulator.AccessoryBehaviorMode `json:"mode"`
	Delay         string                          `json:"delay,omitempty"`
	ReportedState string                          `json:"reportedState,omitempty"`
}

type faultOperationStep struct {
	At        string              `json:"at"`
	Action    Action              `json:"action"`
	Operation simulator.Operation `json:"operation"`
	Delay     string              `json:"delay,omitempty"`
	Error     string              `json:"error,omitempty"`
	Remaining *int                `json:"remaining,omitempty"`
}

type emptyStep struct {
	At     string `json:"at"`
	Action Action `json:"action"`
}

type connectivityPayload struct{ Connectivity station.Connectivity }
type boolPayload struct{ Value bool }
type electricalPayload struct{ State simulator.ElectricalState }
type feedbackPayload struct {
	Source  string
	Kind    string
	Address int
	Active  bool
	Emit    bool
}
type accessoryReportPayload struct {
	Address int
	State   string
}
type accessoryBehaviorPayload struct {
	Address  int
	Behavior simulator.AccessoryBehavior
}
type faultPayload struct {
	Operation simulator.Operation
	Fault     simulator.OperationFault
	Message   string
}

func Load(reader io.Reader) (*Definition, error) {
	if reader == nil {
		return nil, fmt.Errorf("scenario reader is nil")
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read scenario: %w", err)
	}
	return Parse(data)
}

func LoadFile(path string) (*Definition, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open scenario %q: %w", path, err)
	}
	defer file.Close()
	return Load(file)
}

func Parse(data []byte) (*Definition, error) {
	var raw rawDefinition
	if err := decodeStrict(data, &raw); err != nil {
		return nil, fmt.Errorf("decode scenario: %w", err)
	}
	if raw.Version == nil {
		return nil, fmt.Errorf("scenario.version is required")
	}
	if *raw.Version != CurrentVersion {
		return nil, fmt.Errorf("unsupported scenario.version %d (want %d)", *raw.Version, CurrentVersion)
	}
	if strings.TrimSpace(raw.Name) == "" {
		return nil, fmt.Errorf("scenario.name is required")
	}
	if raw.Initial == nil || bytes.Equal(bytes.TrimSpace(*raw.Initial), []byte("null")) {
		return nil, fmt.Errorf("scenario.initial is required")
	}
	if raw.Steps == nil {
		return nil, fmt.Errorf("scenario.steps is required")
	}

	var initialRaw rawInitial
	if err := decodeStrict(*raw.Initial, &initialRaw); err != nil {
		return nil, fmt.Errorf("decode scenario.initial: %w", err)
	}
	if initialRaw.Connectivity != nil && !validConnectivity(*initialRaw.Connectivity) {
		return nil, fmt.Errorf("scenario.initial.connectivity has unsupported value %q", *initialRaw.Connectivity)
	}

	definition := &Definition{
		Version: *raw.Version,
		Name:    raw.Name,
		Initial: Initial{
			Connectivity:  initialRaw.Connectivity,
			TrackPower:    initialRaw.TrackPower,
			EmergencyStop: initialRaw.EmergencyStop,
			Electrical:    initialRaw.Electrical,
		},
		Steps: make([]Step, 0, len(*raw.Steps)),
	}
	var previous time.Duration
	for index, rawStep := range *raw.Steps {
		step, err := parseStep(rawStep)
		if err != nil {
			return nil, fmt.Errorf("scenario.steps[%d]: %w", index, err)
		}
		if index > 0 && step.At < previous {
			return nil, fmt.Errorf("scenario.steps[%d].at %s is before previous step at %s", index, step.At, previous)
		}
		previous = step.At
		definition.Steps = append(definition.Steps, step)
	}
	return definition, nil
}

func parseStep(data []byte) (Step, error) {
	var header stepHeader
	if err := json.Unmarshal(data, &header); err != nil {
		return Step{}, fmt.Errorf("decode step header: %w", err)
	}
	if header.At == "" {
		return Step{}, fmt.Errorf("at is required")
	}
	at, err := time.ParseDuration(header.At)
	if err != nil {
		return Step{}, fmt.Errorf("invalid at duration %q: %w", header.At, err)
	}
	if at < 0 {
		return Step{}, fmt.Errorf("at must not be negative")
	}
	if header.Action == "" {
		return Step{}, fmt.Errorf("action is required")
	}

	step := Step{At: at, Action: header.Action}
	switch header.Action {
	case ActionStationConnectivity:
		var raw connectivityStep
		if err := decodeStrict(data, &raw); err != nil {
			return Step{}, err
		}
		if raw.Connectivity == nil {
			return Step{}, fmt.Errorf("connectivity is required")
		}
		if !validConnectivity(*raw.Connectivity) {
			return Step{}, fmt.Errorf("unsupported connectivity %q", *raw.Connectivity)
		}
		step.payload = connectivityPayload{Connectivity: *raw.Connectivity}
	case ActionStationTrackPower:
		var raw trackPowerStep
		if err := decodeStrict(data, &raw); err != nil {
			return Step{}, err
		}
		if raw.On == nil {
			return Step{}, fmt.Errorf("on is required")
		}
		step.payload = boolPayload{Value: *raw.On}
	case ActionStationEmergency:
		var raw emergencyStep
		if err := decodeStrict(data, &raw); err != nil {
			return Step{}, err
		}
		if raw.Active == nil {
			return Step{}, fmt.Errorf("active is required")
		}
		step.payload = boolPayload{Value: *raw.Active}
	case ActionStationElectrical:
		var raw electricalStep
		if err := decodeStrict(data, &raw); err != nil {
			return Step{}, err
		}
		if raw.Electrical == nil {
			return Step{}, fmt.Errorf("electrical is required")
		}
		step.payload = electricalPayload{State: *raw.Electrical}
	case ActionFeedbackSet:
		var raw feedbackSetStep
		if err := decodeStrict(data, &raw); err != nil {
			return Step{}, err
		}
		payload, err := parseFeedback(raw.Source, raw.Kind, raw.Address, raw.Active)
		if err != nil {
			return Step{}, err
		}
		if raw.Emit == nil {
			return Step{}, fmt.Errorf("emit is required")
		}
		payload.Emit = *raw.Emit
		step.payload = payload
	case ActionFeedbackEmit:
		var raw feedbackEmitStep
		if err := decodeStrict(data, &raw); err != nil {
			return Step{}, err
		}
		payload, err := parseFeedback(raw.Source, raw.Kind, raw.Address, raw.Active)
		if err != nil {
			return Step{}, err
		}
		payload.Emit = true
		step.payload = payload
	case ActionAccessoryReport:
		var raw accessoryReportStep
		if err := decodeStrict(data, &raw); err != nil {
			return Step{}, err
		}
		if err := validateAddress(raw.Address); err != nil {
			return Step{}, err
		}
		if raw.State == "" {
			return Step{}, fmt.Errorf("state is required")
		}
		if raw.State != "straight" && raw.State != "diverging" && raw.State != "unknown" {
			return Step{}, fmt.Errorf("unsupported accessory report state %q", raw.State)
		}
		step.payload = accessoryReportPayload{Address: *raw.Address, State: raw.State}
	case ActionAccessoryBehavior:
		var raw accessoryBehaviorStep
		if err := decodeStrict(data, &raw); err != nil {
			return Step{}, err
		}
		if err := validateAddress(raw.Address); err != nil {
			return Step{}, err
		}
		delay, err := optionalDuration("delay", raw.Delay)
		if err != nil {
			return Step{}, err
		}
		behavior := simulator.AccessoryBehavior{Mode: raw.Mode, Delay: delay, ReportedState: raw.ReportedState}
		if err := validateAccessoryBehavior(behavior); err != nil {
			return Step{}, err
		}
		step.payload = accessoryBehaviorPayload{Address: *raw.Address, Behavior: behavior}
	case ActionFaultOperation:
		var raw faultOperationStep
		if err := decodeStrict(data, &raw); err != nil {
			return Step{}, err
		}
		if !raw.Operation.Valid() {
			return Step{}, fmt.Errorf("unsupported operation %q", raw.Operation)
		}
		delay, err := optionalDuration("delay", raw.Delay)
		if err != nil {
			return Step{}, err
		}
		remaining := 0
		if raw.Remaining != nil {
			remaining = *raw.Remaining
		}
		if remaining < 0 {
			return Step{}, fmt.Errorf("remaining must not be negative")
		}
		if delay == 0 && raw.Error == "" {
			return Step{}, fmt.Errorf("fault.operation requires a positive delay or an error")
		}
		step.payload = faultPayload{
			Operation: raw.Operation,
			Fault:     simulator.OperationFault{Delay: delay, Remaining: remaining},
			Message:   raw.Error,
		}
	case ActionFaultClear, ActionSimulatorReset:
		var raw emptyStep
		if err := decodeStrict(data, &raw); err != nil {
			return Step{}, err
		}
		step.payload = struct{}{}
	default:
		return Step{}, fmt.Errorf("unsupported action %q", header.Action)
	}
	return step, nil
}

func parseFeedback(source, kind string, address *int, active *bool) (feedbackPayload, error) {
	if strings.TrimSpace(source) == "" {
		return feedbackPayload{}, fmt.Errorf("source is required")
	}
	if strings.TrimSpace(kind) == "" {
		return feedbackPayload{}, fmt.Errorf("kind is required")
	}
	if err := validateAddress(address); err != nil {
		return feedbackPayload{}, err
	}
	if active == nil {
		return feedbackPayload{}, fmt.Errorf("active is required")
	}
	return feedbackPayload{Source: source, Kind: kind, Address: *address, Active: *active}, nil
}

func validateAddress(address *int) error {
	if address == nil {
		return fmt.Errorf("address is required")
	}
	if *address < 0 {
		return fmt.Errorf("address must not be negative")
	}
	return nil
}

func optionalDuration(field, value string) (time.Duration, error) {
	if value == "" {
		return 0, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s duration %q: %w", field, value, err)
	}
	if duration < 0 {
		return 0, fmt.Errorf("%s must not be negative", field)
	}
	return duration, nil
}

func validateAccessoryBehavior(behavior simulator.AccessoryBehavior) error {
	switch behavior.Mode {
	case simulator.AccessoryBehaviorImmediate, simulator.AccessoryBehaviorNoConfirmation:
		if behavior.ReportedState != "" {
			return fmt.Errorf("reportedState is only valid for inconsistent behavior")
		}
	case simulator.AccessoryBehaviorDelayed:
		if behavior.Delay <= 0 {
			return fmt.Errorf("delayed accessory behavior requires a positive delay")
		}
		if behavior.ReportedState != "" {
			return fmt.Errorf("reportedState is only valid for inconsistent behavior")
		}
	case simulator.AccessoryBehaviorInconsistent:
		if behavior.ReportedState != "straight" && behavior.ReportedState != "diverging" && behavior.ReportedState != "unknown" {
			return fmt.Errorf("unsupported reportedState %q", behavior.ReportedState)
		}
	default:
		return fmt.Errorf("unsupported accessory behavior mode %q", behavior.Mode)
	}
	return nil
}

func validConnectivity(connectivity station.Connectivity) bool {
	return connectivity == station.Online || connectivity == station.Degraded || connectivity == station.Offline
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}
