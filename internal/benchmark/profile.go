package benchmark

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

const ProfileSchemaVersion = 1

type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	value, err := time.ParseDuration(node.Value)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", node.Value, err)
	}
	d.Duration = value
	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf("%q", d.String())), nil
}

func (d *Duration) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	value, err := time.ParseDuration(text)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", text, err)
	}
	d.Duration = value
	return nil
}

type Profile struct {
	SchemaVersion    int                 `yaml:"schema_version" json:"schemaVersion"`
	Name             string              `yaml:"name" json:"name"`
	Warmup           Duration            `yaml:"warmup" json:"warmup"`
	Duration         Duration            `yaml:"duration" json:"duration"`
	Seed             int64               `yaml:"seed" json:"seed"`
	Fixture          string              `yaml:"fixture,omitempty" json:"fixture,omitempty"`
	OperationTimeout Duration            `yaml:"operation_timeout,omitempty" json:"operationTimeout"`
	Clients          ClientProfile       `yaml:"clients" json:"clients"`
	Rates            RateProfile         `yaml:"rates" json:"rates"`
	Behavior         BehaviorProfile     `yaml:"behavior" json:"behavior"`
	Bursts           []BurstProfile      `yaml:"bursts,omitempty" json:"bursts,omitempty"`
	ExpectedErrors   []ExpectedErrorRule `yaml:"expected_errors,omitempty" json:"expectedErrors,omitempty"`
}

type ClientProfile struct {
	Users             int `yaml:"users" json:"users"`
	WebSockets        int `yaml:"websockets" json:"websockets"`
	ActiveLocomotives int `yaml:"active_locomotives" json:"activeLocomotives"`
	Workers           int `yaml:"workers,omitempty" json:"workers"`
}

type RateProfile struct {
	LoginPerSecond           float64 `yaml:"login_per_second" json:"loginPerSecond"`
	RefreshPerSecond         float64 `yaml:"refresh_per_second" json:"refreshPerSecond"`
	LeaseAcquirePerSecond    float64 `yaml:"lease_acquire_per_second" json:"leaseAcquirePerSecond"`
	LeaseHeartbeatPerSecond  float64 `yaml:"lease_heartbeat_per_second" json:"leaseHeartbeatPerSecond"`
	LeaseReleasePerSecond    float64 `yaml:"lease_release_per_second" json:"leaseReleasePerSecond"`
	ThrottlePerSecond        float64 `yaml:"throttle_per_second" json:"throttlePerSecond"`
	FunctionsPerSecond       float64 `yaml:"functions_per_second" json:"functionsPerSecond"`
	FeedbackPerSecond        float64 `yaml:"feedback_per_second" json:"feedbackPerSecond"`
	AccessoriesPerSecond     float64 `yaml:"accessories_per_second" json:"accessoriesPerSecond"`
	RouteOperationsPerSecond float64 `yaml:"route_operations_per_second" json:"routeOperationsPerSecond"`
	ReadsPerSecond           float64 `yaml:"reads_per_second" json:"readsPerSecond"`
}

type BehaviorProfile struct {
	ReconnectProbability float64 `yaml:"reconnect_probability" json:"reconnectProbability"`
	SnapshotProbability  float64 `yaml:"snapshot_probability" json:"snapshotProbability"`
	DropEventProbability float64 `yaml:"drop_event_probability" json:"dropEventProbability"`
}

type BurstProfile struct {
	At        Duration `yaml:"at" json:"at"`
	Operation string   `yaml:"operation" json:"operation"`
	Count     int      `yaml:"count" json:"count"`
}

type ExpectedErrorRule struct {
	Operation    string   `yaml:"operation" json:"operation"`
	HTTPStatuses []int    `yaml:"http_statuses,omitempty" json:"httpStatuses,omitempty"`
	ProblemCodes []string `yaml:"problem_codes,omitempty" json:"problemCodes,omitempty"`
	Kinds        []string `yaml:"kinds,omitempty" json:"kinds,omitempty"`
}

func LoadProfile(path string) (Profile, error) {
	f, err := os.Open(path)
	if err != nil {
		return Profile{}, fmt.Errorf("open profile: %w", err)
	}
	defer f.Close()
	profile, err := DecodeProfile(f)
	if err != nil {
		return Profile{}, fmt.Errorf("decode profile: %w", err)
	}
	return profile, nil
}

func DecodeProfile(r io.Reader) (Profile, error) {
	decoder := yaml.NewDecoder(r)
	decoder.KnownFields(true)
	var profile Profile
	if err := decoder.Decode(&profile); err != nil {
		return Profile{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Profile{}, errors.New("profile must contain one YAML document")
		}
		return Profile{}, err
	}
	profile.setDefaults()
	if err := profile.Validate(); err != nil {
		return Profile{}, err
	}
	return profile, nil
}

func (p *Profile) setDefaults() {
	if p.OperationTimeout.Duration == 0 {
		p.OperationTimeout.Duration = 5 * time.Second
	}
	if p.Clients.Workers == 0 {
		p.Clients.Workers = 16
	}
}

func (p Profile) Validate() error {
	if p.SchemaVersion != ProfileSchemaVersion {
		return fmt.Errorf("unsupported schema_version %d (want %d)", p.SchemaVersion, ProfileSchemaVersion)
	}
	if p.Name == "" {
		return errors.New("name is required")
	}
	if p.Warmup.Duration < 0 {
		return errors.New("warmup must not be negative")
	}
	if p.Duration.Duration <= 0 {
		return errors.New("duration must be greater than zero")
	}
	if p.OperationTimeout.Duration <= 0 {
		return errors.New("operation_timeout must be greater than zero")
	}
	if p.Clients.Users <= 0 {
		return errors.New("clients.users must be greater than zero")
	}
	if p.Clients.WebSockets < 0 || p.Clients.ActiveLocomotives < 0 {
		return errors.New("client counts must not be negative")
	}
	if p.Clients.Workers <= 0 || p.Clients.Workers > 1024 {
		return errors.New("clients.workers must be between 1 and 1024")
	}
	rates := map[string]float64{
		"login_per_second":            p.Rates.LoginPerSecond,
		"refresh_per_second":          p.Rates.RefreshPerSecond,
		"lease_acquire_per_second":    p.Rates.LeaseAcquirePerSecond,
		"lease_heartbeat_per_second":  p.Rates.LeaseHeartbeatPerSecond,
		"lease_release_per_second":    p.Rates.LeaseReleasePerSecond,
		"throttle_per_second":         p.Rates.ThrottlePerSecond,
		"functions_per_second":        p.Rates.FunctionsPerSecond,
		"feedback_per_second":         p.Rates.FeedbackPerSecond,
		"accessories_per_second":      p.Rates.AccessoriesPerSecond,
		"route_operations_per_second": p.Rates.RouteOperationsPerSecond,
		"reads_per_second":            p.Rates.ReadsPerSecond,
	}
	for name, rate := range rates {
		if math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 {
			return fmt.Errorf("rates.%s must be a finite non-negative number", name)
		}
	}
	if p.Behavior.ReconnectProbability < 0 || p.Behavior.ReconnectProbability > 1 {
		return errors.New("behavior.reconnect_probability must be between 0 and 1")
	}
	if p.Behavior.SnapshotProbability < 0 || p.Behavior.SnapshotProbability > 1 {
		return errors.New("behavior.snapshot_probability must be between 0 and 1")
	}
	if p.Behavior.DropEventProbability < 0 || p.Behavior.DropEventProbability > 1 {
		return errors.New("behavior.drop_event_probability must be between 0 and 1")
	}
	allowedBurstOperations := map[string]bool{
		"login": true, "refresh": true, "lease_acquire": true, "lease_heartbeat": true,
		"lease_release": true, "throttle": true, "function": true, "feedback": true,
		"accessory": true, "route": true, "read": true,
		"lease_contention": true, "route_contention": true,
	}
	for index, burst := range p.Bursts {
		if burst.At.Duration < 0 || burst.At.Duration >= p.Warmup.Duration+p.Duration.Duration {
			return fmt.Errorf("bursts[%d].at must be within the warm-up and measured run", index)
		}
		if !allowedBurstOperations[burst.Operation] {
			return fmt.Errorf("bursts[%d].operation %q is unsupported", index, burst.Operation)
		}
		if burst.Count <= 0 || burst.Count > 100_000 {
			return fmt.Errorf("bursts[%d].count must be between 1 and 100000", index)
		}
	}
	for index, rule := range p.ExpectedErrors {
		if !allowedBurstOperations[rule.Operation] {
			return fmt.Errorf("expected_errors[%d].operation %q is unsupported", index, rule.Operation)
		}
		if !p.HasOperation(rule.Operation) {
			return fmt.Errorf("expected_errors[%d].operation %q is not scheduled", index, rule.Operation)
		}
		if len(rule.HTTPStatuses) == 0 && len(rule.ProblemCodes) == 0 && len(rule.Kinds) == 0 {
			return fmt.Errorf("expected_errors[%d] must define http_statuses, problem_codes, or kinds", index)
		}
		for _, status := range rule.HTTPStatuses {
			if status < 400 || status > 599 {
				return fmt.Errorf("expected_errors[%d].http_statuses must contain HTTP error statuses", index)
			}
		}
		for _, code := range rule.ProblemCodes {
			if code == "" {
				return fmt.Errorf("expected_errors[%d].problem_codes must not contain empty values", index)
			}
		}
		for _, kind := range rule.Kinds {
			if kind != "timeout" {
				return fmt.Errorf("expected_errors[%d].kinds contains unsupported value %q", index, kind)
			}
		}
	}
	leaseConsumerRate := p.Rates.LeaseHeartbeatPerSecond + p.Rates.LeaseReleasePerSecond + p.Rates.ThrottlePerSecond + p.Rates.FunctionsPerSecond
	leaseProducer := p.Rates.LeaseAcquirePerSecond > 0 || p.Clients.ActiveLocomotives > 0
	leaseConsumerBurst := false
	for _, burst := range p.Bursts {
		switch burst.Operation {
		case "lease_acquire", "lease_contention":
			leaseProducer = true
		case "lease_heartbeat", "lease_release", "throttle", "function":
			leaseConsumerBurst = true
		}
	}
	if (leaseConsumerRate > 0 || leaseConsumerBurst) && !leaseProducer {
		return errors.New("lease-consuming operations require active_locomotives or lease acquisition")
	}
	if p.Clients.WebSockets == 0 && (p.Behavior.ReconnectProbability > 0 || p.Behavior.SnapshotProbability > 0 || p.Behavior.DropEventProbability > 0) {
		return errors.New("WebSocket behavior probabilities require clients.websockets")
	}
	return nil
}

func (p Profile) HasActiveOperations() bool {
	if p.Clients.ActiveLocomotives > 0 ||
		p.Rates.LeaseAcquirePerSecond > 0 || p.Rates.LeaseHeartbeatPerSecond > 0 ||
		p.Rates.LeaseReleasePerSecond > 0 || p.Rates.ThrottlePerSecond > 0 ||
		p.Rates.FunctionsPerSecond > 0 || p.Rates.AccessoriesPerSecond > 0 ||
		p.Rates.RouteOperationsPerSecond > 0 {
		return true
	}
	for _, burst := range p.Bursts {
		switch burst.Operation {
		case "lease_acquire", "lease_heartbeat", "lease_release", "throttle", "function", "accessory", "route", "lease_contention", "route_contention":
			return true
		}
	}
	return false
}

func (p Profile) HasSimulatorOperations() bool {
	if p.Rates.FeedbackPerSecond > 0 {
		return true
	}
	for _, burst := range p.Bursts {
		if burst.Operation == "feedback" {
			return true
		}
	}
	return false
}

func (p Profile) HasOperation(name string) bool {
	rates := map[string]float64{
		"login": p.Rates.LoginPerSecond, "refresh": p.Rates.RefreshPerSecond,
		"lease_acquire": p.Rates.LeaseAcquirePerSecond, "lease_heartbeat": p.Rates.LeaseHeartbeatPerSecond,
		"lease_release": p.Rates.LeaseReleasePerSecond, "throttle": p.Rates.ThrottlePerSecond,
		"function": p.Rates.FunctionsPerSecond, "feedback": p.Rates.FeedbackPerSecond,
		"accessory": p.Rates.AccessoriesPerSecond, "route": p.Rates.RouteOperationsPerSecond,
		"read": p.Rates.ReadsPerSecond,
	}
	if rates[name] > 0 {
		return true
	}
	for _, burst := range p.Bursts {
		if burst.Operation == name {
			return true
		}
	}
	return false
}
