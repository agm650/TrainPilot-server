package model

import (
	"errors"
	"fmt"
	"strings"
)

const MaxSignalLights = 9

type SignalAspectID string
type SignalAspectRole string

const (
	SignalRoleStop    SignalAspectRole = "stop"
	SignalRoleCaution SignalAspectRole = "caution"
	SignalRoleProceed SignalAspectRole = "proceed"
	SignalRoleOther   SignalAspectRole = "other"
)

func (r SignalAspectRole) Valid() bool {
	return r == SignalRoleStop || r == SignalRoleCaution || r == SignalRoleProceed || r == SignalRoleOther
}

type SignalLightState string

const (
	SignalLightOff      SignalLightState = "off"
	SignalLightOn       SignalLightState = "on"
	SignalLightFlashing SignalLightState = "flashing"
)

func (s SignalLightState) Valid() bool {
	return s == SignalLightOff || s == SignalLightOn || s == SignalLightFlashing
}

// The BAL ruleset uses roles, never country-specific aspect identifiers.
const SignalRuleSetBAL3 = "bal-3"

type SignalTarget struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

type SignalLightDefinition struct {
	ID       string `json:"id"`
	TargetID string `json:"targetId"`
	Color    string `json:"color"`
}

type SignalLightIndication struct {
	LightID string           `json:"lightId"`
	State   SignalLightState `json:"state"`
}

// Indications are sparse: an omitted light is off; an empty list is dark.
// Flashing is representable here, independently from driver/rendering support.
type SignalAspect struct {
	ID          SignalAspectID          `json:"id"`
	Name        string                  `json:"name"`
	Role        SignalAspectRole        `json:"role"`
	Indications []SignalLightIndication `json:"indications"`
}

type SignalProfile struct {
	ID             string                  `json:"id"`
	Name           string                  `json:"name"`
	RuleSetID      string                  `json:"ruleSetId,omitempty"`
	FallbackAspect SignalAspectID          `json:"fallbackAspect"`
	Targets        []SignalTarget          `json:"targets"`
	Lights         []SignalLightDefinition `json:"lights"`
	Aspects        []SignalAspect          `json:"aspects"`
}

func (p SignalProfile) Aspect(id SignalAspectID) (SignalAspect, bool) {
	for _, aspect := range p.Aspects {
		if aspect.ID == id {
			return aspect, true
		}
	}
	return SignalAspect{}, false
}

// AspectForRole only resolves an unambiguous role. Manual profiles may define
// several aspects with the same role; a ruleset must handle that explicitly.
func (p SignalProfile) AspectForRole(role SignalAspectRole) (SignalAspect, bool) {
	var result SignalAspect
	found := false
	for _, aspect := range p.Aspects {
		if aspect.Role == role {
			if found {
				return SignalAspect{}, false
			}
			result, found = aspect, true
		}
	}
	return result, found
}

// NormalizeSignalProfile owns its slices and supplies JSON [] for empty lists.
func NormalizeSignalProfile(p SignalProfile) SignalProfile {
	p.Targets = append([]SignalTarget{}, p.Targets...)
	p.Lights = append([]SignalLightDefinition{}, p.Lights...)
	aspects := make([]SignalAspect, 0, len(p.Aspects))
	for _, aspect := range p.Aspects {
		aspect.Indications = append([]SignalLightIndication{}, aspect.Indications...)
		aspects = append(aspects, aspect)
	}
	p.Aspects = aspects
	return p
}

var ErrInvalidSignalProfile = errors.New("invalid signal profile")

func ValidateSignalProfile(p SignalProfile) error {
	invalid := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalidSignalProfile, fmt.Sprintf(format, args...))
	}
	if !validSignalID(p.ID) || strings.TrimSpace(p.Name) == "" {
		return invalid("id and name are required")
	}
	if len(p.Targets) == 0 || len(p.Lights) == 0 || len(p.Lights) > MaxSignalLights {
		return invalid("a profile requires targets and 1..%d lights", MaxSignalLights)
	}
	targets := make(map[string]bool, len(p.Targets))
	for _, target := range p.Targets {
		if !validSignalID(target.ID) || targets[target.ID] {
			return invalid("invalid or duplicate target %q", target.ID)
		}
		targets[target.ID] = true
	}
	lights := make(map[string]bool, len(p.Lights))
	usedTargets := make(map[string]bool)
	for _, light := range p.Lights {
		if !validSignalID(light.ID) || lights[light.ID] || !targets[light.TargetID] || !validLayoutColor(light.Color) {
			return invalid("invalid light %q or target/color reference", light.ID)
		}
		lights[light.ID] = true
		usedTargets[light.TargetID] = true
	}
	if len(usedTargets) != len(targets) {
		return invalid("each target must contain a light")
	}
	aspects := make(map[SignalAspectID]bool, len(p.Aspects))
	for _, aspect := range p.Aspects {
		if !validSignalID(string(aspect.ID)) || strings.TrimSpace(aspect.Name) == "" || aspects[aspect.ID] || !aspect.Role.Valid() {
			return invalid("invalid or duplicate aspect %q", aspect.ID)
		}
		aspects[aspect.ID] = true
		seen := make(map[string]bool, len(aspect.Indications))
		for _, indication := range aspect.Indications {
			if !lights[indication.LightID] || seen[indication.LightID] || !indication.State.Valid() {
				return invalid("invalid indication %q in aspect %q", indication.LightID, aspect.ID)
			}
			seen[indication.LightID] = true
		}
	}
	fallback, found := p.Aspect(p.FallbackAspect)
	if !found || fallback.Role != SignalRoleStop {
		return invalid("fallback must be a declared stop aspect")
	}
	switch p.RuleSetID {
	case "": // Custom manual profiles do not inherit an automatic ruleset.
	case SignalRuleSetBAL3:
		if len(p.Aspects) != 3 {
			return invalid("bal-3 requires exactly three aspects")
		}
		for _, role := range []SignalAspectRole{SignalRoleStop, SignalRoleCaution, SignalRoleProceed} {
			if _, found := p.AspectForRole(role); !found {
				return invalid("bal-3 requires one unambiguous %q aspect", role)
			}
		}
	default:
		return invalid("unsupported ruleset %q", p.RuleSetID)
	}
	return nil
}

func validSignalID(value string) bool {
	return value != "" && strings.TrimSpace(value) == value
}
