package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func customSignalProfile() SignalProfile {
	return SignalProfile{
		ID: "CUSTOM_3", Name: "Custom signalling", RuleSetID: SignalRuleSetBAL3, FallbackAspect: "HALT",
		Targets: []SignalTarget{{ID: "main"}},
		Lights: []SignalLightDefinition{
			{ID: "one", TargetID: "main", Color: "#123456"},
			{ID: "two", TargetID: "main", Color: "#ABCDEF"},
			{ID: "three", TargetID: "main", Color: "#987654"},
		},
		Aspects: []SignalAspect{
			{ID: "HALT", Name: "Stop", Role: SignalRoleStop, Indications: []SignalLightIndication{{LightID: "one", State: SignalLightOn}}},
			{ID: "WARN", Name: "Caution", Role: SignalRoleCaution, Indications: []SignalLightIndication{{LightID: "two", State: SignalLightOn}}},
			{ID: "GO", Name: "Proceed", Role: SignalRoleProceed, Indications: []SignalLightIndication{{LightID: "three", State: SignalLightOn}}},
		},
	}
}

func signalForProfile(p SignalProfile) SignalDefinition {
	codes := make(map[SignalAspectID]int, len(p.Aspects))
	for i, aspect := range p.Aspects {
		codes[aspect.ID] = i
	}
	return SignalDefinition{
		ID: "signal-a", Name: "Signal A", ProfileID: p.ID,
		Placement:        SignalPlacement{TrackSectionID: "section-a", EntryNodeID: "node-a"},
		ProtectedBlockID: "block-a",
		Downstream:       SignalDownstream{Kind: SignalDownstreamTerminal, Aspect: p.FallbackAspect},
		Endpoint:         SignalEndpoint{Kind: SignalEndpointDCCExtended, LinearAddress: 1, AspectCodes: codes},
	}
}

func TestSignalProfilesAreIndependentFromCountryAndDecoder(t *testing.T) {
	for _, profile := range []SignalProfile{SNCFBAL3Profile(), customSignalProfile()} {
		t.Run(profile.ID, func(t *testing.T) {
			if err := ValidateSignalProfile(profile); err != nil {
				t.Fatal(err)
			}
			signal := signalForProfile(profile)
			signal.Mode = SignalModeAutomatic
			for i, aspect := range profile.Aspects {
				signal.Endpoint.AspectCodes[aspect.ID] = []int{0, 7, 255}[i]
			}
			if err := ValidateSignalDefinition(signal, profile); err != nil {
				t.Fatal(err)
			}
			for _, role := range []SignalAspectRole{SignalRoleStop, SignalRoleCaution, SignalRoleProceed} {
				aspect, found := profile.AspectForRole(role)
				if !found || aspect.Role != role {
					t.Fatalf("role %q not resolved: %+v", role, aspect)
				}
			}
			encoded, err := json.Marshal(struct {
				Profile SignalProfile    `json:"profile"`
				Signal  SignalDefinition `json:"signal"`
			}{profile, signal})
			if err != nil {
				t.Fatal(err)
			}
			var decoded struct {
				Profile SignalProfile    `json:"profile"`
				Signal  SignalDefinition `json:"signal"`
			}
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(decoded.Profile, profile) || !reflect.DeepEqual(decoded.Signal, signal) {
				t.Fatalf("profile or mapping lost in JSON: %s", encoded)
			}
		})
	}
}

func TestSignalProfileValidation(t *testing.T) {
	cases := map[string]func(*SignalProfile){
		"missing id":         func(p *SignalProfile) { p.ID = "" },
		"blank name":         func(p *SignalProfile) { p.Name = " " },
		"missing targets":    func(p *SignalProfile) { p.Targets = nil },
		"duplicate target":   func(p *SignalProfile) { p.Targets = append(p.Targets, p.Targets[0]) },
		"empty target":       func(p *SignalProfile) { p.Targets = append(p.Targets, SignalTarget{ID: "empty"}) },
		"blank target":       func(p *SignalProfile) { p.Targets[0].ID = " " },
		"missing lights":     func(p *SignalProfile) { p.Lights = nil },
		"duplicate light":    func(p *SignalProfile) { p.Lights[1].ID = p.Lights[0].ID },
		"orphan light":       func(p *SignalProfile) { p.Lights[0].TargetID = "missing" },
		"invalid color":      func(p *SignalProfile) { p.Lights[0].Color = "#GGGGGG" },
		"missing aspects":    func(p *SignalProfile) { p.Aspects = nil },
		"duplicate aspect":   func(p *SignalProfile) { p.Aspects[1].ID = p.Aspects[0].ID },
		"blank aspect":       func(p *SignalProfile) { p.Aspects[0].ID = " " },
		"blank aspect name":  func(p *SignalProfile) { p.Aspects[0].Name = "" },
		"unknown role":       func(p *SignalProfile) { p.Aspects[0].Role = "unknown" },
		"orphan indication":  func(p *SignalProfile) { p.Aspects[0].Indications[0].LightID = "missing" },
		"invalid indication": func(p *SignalProfile) { p.Aspects[0].Indications[0].State = "unknown" },
		"duplicate indication": func(p *SignalProfile) {
			p.Aspects[0].Indications = append(p.Aspects[0].Indications, p.Aspects[0].Indications[0])
		},
		"missing fallback":   func(p *SignalProfile) { p.FallbackAspect = "" },
		"unknown fallback":   func(p *SignalProfile) { p.FallbackAspect = "missing" },
		"unsafe fallback":    func(p *SignalProfile) { p.FallbackAspect = "VL" },
		"unknown ruleset":    func(p *SignalProfile) { p.RuleSetID = "unknown" },
		"ambiguous BAL role": func(p *SignalProfile) { p.Aspects[1].Role = SignalRoleStop },
		"extra BAL aspect": func(p *SignalProfile) {
			p.Aspects = append(p.Aspects, SignalAspect{ID: "extra", Name: "Extra", Role: SignalRoleOther})
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			p := SNCFBAL3Profile()
			change(&p)
			if err := ValidateSignalProfile(p); !errors.Is(err, ErrInvalidSignalProfile) {
				t.Fatalf("invalid profile accepted: %v", err)
			}
		})
	}
}

func TestSignalCustomProfileSupportsMultipleTargetsAndNineLights(t *testing.T) {
	p := customSignalProfile()
	p.RuleSetID = ""
	p.Targets = append(p.Targets, SignalTarget{ID: "second"})
	for i := len(p.Lights); i < MaxSignalLights; i++ {
		p.Lights = append(p.Lights, SignalLightDefinition{ID: fmt.Sprintf("l%d", i), TargetID: "second", Color: "#FFFFFF"})
	}
	p.Aspects = append(p.Aspects,
		SignalAspect{ID: "DARK", Name: "All lights off", Role: SignalRoleOther},
		SignalAspect{ID: "COMBINED", Name: "Two targets", Role: SignalRoleOther, Indications: []SignalLightIndication{
			{LightID: "one", State: SignalLightOn}, {LightID: "l3", State: SignalLightFlashing}, {LightID: "l4", State: SignalLightOff},
		}},
		SignalAspect{ID: "OTHER_STOP", Name: "Second stop", Role: SignalRoleStop},
	)
	p = NormalizeSignalProfile(p)
	if err := ValidateSignalProfile(p); err != nil {
		t.Fatal(err)
	}
	if _, found := p.AspectForRole(SignalRoleStop); found {
		t.Fatal("ambiguous stop role resolved implicitly")
	}
	s := signalForProfile(p)
	if err := ValidateSignalDefinition(s, p); err != nil {
		t.Fatal(err)
	}
	s.Mode = SignalModeAutomatic
	if err := ValidateSignalDefinition(s, p); !errors.Is(err, ErrInvalidSignal) {
		t.Fatal("custom manual profile inherited an automatic ruleset")
	}
	encoded, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var decoded SignalProfile
	if err := json.Unmarshal(encoded, &decoded); err != nil || !reflect.DeepEqual(decoded, p) {
		t.Fatalf("combined/dark profile round trip failed: %v", err)
	}
	if dark, _ := p.Aspect("DARK"); dark.Indications == nil || len(dark.Indications) != 0 {
		t.Fatal("dark aspect must serialize an empty indication list")
	}
	p.Lights = append(p.Lights, SignalLightDefinition{ID: "too-many", TargetID: "second", Color: "#FFFFFF"})
	if err := ValidateSignalProfile(p); !errors.Is(err, ErrInvalidSignalProfile) {
		t.Fatal("more than nine lights accepted")
	}
}

func TestSignalProfileNormalizationAndPresetOwnTheirSlices(t *testing.T) {
	original := SNCFBAL3Profile()
	copy := NormalizeSignalProfile(original)
	copy.Targets[0].ID = "changed"
	copy.Lights[0].Color = "#FFFFFF"
	copy.Aspects[0].Indications[0].LightID = "changed"
	if !reflect.DeepEqual(original, SNCFBAL3Profile()) {
		t.Fatal("normalization or preset shares mutable configuration")
	}
	empty := NormalizeSignalProfile(SignalProfile{})
	if empty.Targets == nil || empty.Lights == nil || empty.Aspects == nil {
		t.Fatal("empty profile lists must be non-nil")
	}
}

func TestSignalDefinitionValidation(t *testing.T) {
	cases := map[string]func(*SignalDefinition){
		"missing id":           func(s *SignalDefinition) { s.ID = "" },
		"blank name":           func(s *SignalDefinition) { s.Name = " " },
		"wrong profile":        func(s *SignalDefinition) { s.ProfileID = "other" },
		"unknown mode":         func(s *SignalDefinition) { s.Mode = "unknown" },
		"missing section":      func(s *SignalDefinition) { s.Placement.TrackSectionID = "" },
		"missing direction":    func(s *SignalDefinition) { s.Placement.EntryNodeID = "" },
		"missing block":        func(s *SignalDefinition) { s.ProtectedBlockID = "" },
		"unknown fallback":     func(s *SignalDefinition) { s.FallbackAspect = "unknown" },
		"unsafe fallback":      func(s *SignalDefinition) { s.FallbackAspect = "VL" },
		"missing downstream":   func(s *SignalDefinition) { s.Downstream = SignalDownstream{} },
		"orphan signal id":     func(s *SignalDefinition) { s.Downstream = SignalDownstream{Kind: SignalDownstreamSignal} },
		"mixed downstream":     func(s *SignalDefinition) { s.Downstream.SignalID = "signal-b" },
		"unsafe terminal":      func(s *SignalDefinition) { s.Downstream.Aspect = "VL" },
		"unknown terminal":     func(s *SignalDefinition) { s.Downstream.Aspect = "unknown" },
		"unsupported endpoint": func(s *SignalDefinition) { s.Endpoint.Kind = "vendor-specific" },
		"zero address":         func(s *SignalDefinition) { s.Endpoint.LinearAddress = 0 },
		"negative address":     func(s *SignalDefinition) { s.Endpoint.LinearAddress = -1 },
		"reserved address":     func(s *SignalDefinition) { s.Endpoint.LinearAddress = MaxSignalLinearAddress + 1 },
		"missing mapping":      func(s *SignalDefinition) { delete(s.Endpoint.AspectCodes, "A") },
		"unknown aspect": func(s *SignalDefinition) {
			delete(s.Endpoint.AspectCodes, "A")
			s.Endpoint.AspectCodes["UNKNOWN"] = 1
		},
		"duplicate code": func(s *SignalDefinition) { s.Endpoint.AspectCodes["A"] = 0 },
		"negative code":  func(s *SignalDefinition) { s.Endpoint.AspectCodes["A"] = -1 },
		"oversize code":  func(s *SignalDefinition) { s.Endpoint.AspectCodes["A"] = MaxSignalAspectCode + 1 },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			profile := SNCFBAL3Profile()
			s := signalForProfile(profile)
			change(&s)
			if err := ValidateSignalDefinition(s, profile); !errors.Is(err, ErrInvalidSignal) {
				t.Fatalf("invalid signal accepted: %v", err)
			}
		})
	}
}

func TestSignalDefinitionDefaultsLimitsAndExplicitDownstream(t *testing.T) {
	p := customSignalProfile()
	for _, address := range []int{MinSignalLinearAddress, MaxSignalLinearAddress} {
		s := signalForProfile(p)
		s.Endpoint.LinearAddress = address
		s.Endpoint.AspectCodes["GO"] = MaxSignalAspectCode
		s.Downstream = SignalDownstream{Kind: SignalDownstreamSignal, SignalID: "signal-b"}
		normalized, err := NormalizeSignalDefinition(s, p)
		if err != nil {
			t.Fatal(err)
		}
		if normalized.Mode != SignalModeManual || normalized.FallbackAspect != "HALT" || s.Mode != "" || s.FallbackAspect != "" {
			t.Fatalf("wrong defaults or mutation: normalized=%+v original=%+v", normalized, s)
		}
	}
	p.Aspects = nil
	if err := ValidateSignalDefinition(signalForProfile(p), p); !errors.Is(err, ErrInvalidSignalProfile) || !errors.Is(err, ErrInvalidSignal) {
		t.Fatalf("invalid profile must also reject its signals: %v", err)
	}
}

func TestSignalRuntimeStartsRestrictiveWithoutRestoringConfirmation(t *testing.T) {
	for _, profile := range []SignalProfile{SNCFBAL3Profile(), customSignalProfile()} {
		t.Run(profile.ID, func(t *testing.T) {
			s := signalForProfile(profile)
			encoded, err := json.Marshal(s)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := json.Unmarshal(encoded, &fields); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"computedAspect", "desiredAspect", "commandedAspect", "reportedAspect", "reportQuality", "pending", "synchronized"} {
				if _, found := fields[key]; found {
					t.Fatalf("configuration exports runtime field %q", key)
				}
			}
			// A document containing old runtime values cannot restore an observed
			// aspect when it is decoded as configuration and initialized again.
			fields["reportedAspect"], fields["reportQuality"], fields["pending"] = profile.Aspects[2].ID, "physical", true
			encoded, err = json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			var loaded SignalDefinition
			if err := json.Unmarshal(encoded, &loaded); err != nil {
				t.Fatal(err)
			}
			state, err := NewSignalState(loaded, profile)
			if err != nil {
				t.Fatal(err)
			}
			if state.ComputedAspect != profile.FallbackAspect || state.DesiredAspect != profile.FallbackAspect || state.Pending || state.Synchronized {
				t.Fatalf("runtime did not start at the profile's fallback: %+v", state)
			}
			encoded, err = json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			var runtime map[string]any
			if err := json.Unmarshal(encoded, &runtime); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"commandedAspect", "reportedAspect", "reportQuality"} {
				value, present := runtime[key]
				if !present || value != nil {
					t.Fatalf("initial runtime must report %s=null: %s", key, encoded)
				}
			}
		})
	}
}
