package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/agm650/TrainPilot-server/internal/station"
)

// These are initial portable signalling limits, independent from basic
// accessory addressing. SIG-003 also validates each driver's actual limits.
const (
	MinSignalLinearAddress = station.MinExtendedAccessoryAddress
	MaxSignalLinearAddress = station.MaxExtendedAccessoryAddress
	MinSignalAspectCode    = station.MinExtendedAccessoryAspect
	MaxSignalAspectCode    = station.MaxExtendedAccessoryAspect
)

type SignalMode string

const (
	SignalModeManual    SignalMode = "manual"
	SignalModeAutomatic SignalMode = "automatic"
)

type SignalEndpointKind string

const SignalEndpointDCCExtended SignalEndpointKind = "dcc-extended"

// Manufacturer and country are intentionally absent from the wire mapping.
type SignalEndpoint struct {
	Kind          SignalEndpointKind     `json:"kind"`
	LinearAddress int                    `json:"linearAddress"`
	AspectCodes   map[SignalAspectID]int `json:"aspectCodes"`
}

type SignalPlacement struct {
	TrackSectionID string `json:"trackSectionId"`
	EntryNodeID    string `json:"entryNodeId"`
}

type SignalDownstreamKind string

const (
	SignalDownstreamSignal   SignalDownstreamKind = "signal"
	SignalDownstreamTerminal SignalDownstreamKind = "terminal"
)

type SignalDownstream struct {
	Kind     SignalDownstreamKind `json:"kind"`
	SignalID string               `json:"signalId,omitempty"`
	Aspect   SignalAspectID       `json:"aspect,omitempty"`
}

// SignalDefinition contains configuration only. Cross-resource references
// and oriented continuity are validated by the layout layer in SIG-006.
type SignalDefinition struct {
	ID               string           `json:"id"`
	Name             string           `json:"name"`
	ProfileID        string           `json:"profileId"`
	Mode             SignalMode       `json:"mode"`
	Placement        SignalPlacement  `json:"placement"`
	ProtectedBlockID string           `json:"protectedBlockId"`
	Downstream       SignalDownstream `json:"downstream"`
	FallbackAspect   SignalAspectID   `json:"fallbackAspect"`
	Endpoint         SignalEndpoint   `json:"endpoint"`
}

var ErrInvalidSignal = errors.New("invalid signal definition")

func NormalizeSignalDefinition(s SignalDefinition, profile SignalProfile) (SignalDefinition, error) {
	if err := ValidateSignalProfile(profile); err != nil {
		return SignalDefinition{}, fmt.Errorf("%w: %w", ErrInvalidSignal, err)
	}
	if s.Mode == "" {
		s.Mode = SignalModeManual
	}
	if s.FallbackAspect == "" {
		s.FallbackAspect = profile.FallbackAspect
	}
	if err := validateSignalDefinition(s, profile); err != nil {
		return SignalDefinition{}, err
	}
	return s, nil
}

func ValidateSignalDefinition(s SignalDefinition, profile SignalProfile) error {
	_, err := NormalizeSignalDefinition(s, profile)
	return err
}

func validateSignalDefinition(s SignalDefinition, profile SignalProfile) error {
	invalid := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalidSignal, fmt.Sprintf(format, args...))
	}
	if !validSignalID(s.ID) || strings.TrimSpace(s.Name) == "" || s.ProfileID != profile.ID {
		return invalid("id/name are required and profile must match")
	}
	if s.Mode != SignalModeManual && s.Mode != SignalModeAutomatic {
		return invalid("unsupported mode %q", s.Mode)
	}
	if s.Mode == SignalModeAutomatic && profile.RuleSetID == "" {
		return invalid("automatic mode requires an explicit supported ruleset")
	}
	if !validSignalID(s.Placement.TrackSectionID) || !validSignalID(s.Placement.EntryNodeID) || !validSignalID(s.ProtectedBlockID) {
		return invalid("oriented placement and protected block are required")
	}
	fallback, found := profile.Aspect(s.FallbackAspect)
	if !found || fallback.Role != SignalRoleStop {
		return invalid("fallback must be a declared stop aspect")
	}
	switch s.Downstream.Kind {
	case SignalDownstreamSignal:
		if !validSignalID(s.Downstream.SignalID) || s.Downstream.Aspect != "" {
			return invalid("downstream signal requires only a signal id")
		}
	case SignalDownstreamTerminal:
		aspect, found := profile.Aspect(s.Downstream.Aspect)
		if s.Downstream.SignalID != "" || !found || aspect.Role != SignalRoleStop {
			return invalid("terminal requires only a declared stop aspect")
		}
	default:
		return invalid("explicit downstream signal or terminal is required")
	}
	e := s.Endpoint
	if e.Kind != SignalEndpointDCCExtended || e.LinearAddress < MinSignalLinearAddress || e.LinearAddress > MaxSignalLinearAddress {
		return invalid("unsupported endpoint kind or address")
	}
	if len(e.AspectCodes) != len(profile.Aspects) {
		return invalid("endpoint must map every profile aspect exactly once")
	}
	used := make(map[int]bool, len(e.AspectCodes))
	for id, code := range e.AspectCodes {
		if _, found := profile.Aspect(id); !found {
			return invalid("endpoint maps undeclared aspect %q", id)
		}
		if code < MinSignalAspectCode || code > MaxSignalAspectCode || used[code] {
			return invalid("invalid or duplicate hardware code %d", code)
		}
		used[code] = true
	}
	return nil
}

// SignalState is never embedded in SignalDefinition or persisted as valid
// configuration. Null reports convey an absence of observation/confirmation.
type SignalState struct {
	ID              string                          `json:"id"`
	ComputedAspect  SignalAspectID                  `json:"computedAspect"`
	DesiredAspect   SignalAspectID                  `json:"desiredAspect"`
	CommandedAspect *SignalAspectID                 `json:"commandedAspect"`
	ReportedAspect  *SignalAspectID                 `json:"reportedAspect"`
	ReportQuality   *station.AccessoryReportQuality `json:"reportQuality"`
	Pending         bool                            `json:"pending"`
	Synchronized    bool                            `json:"synchronized"`
	InhibitedReason string                          `json:"inhibitedReason,omitempty"`
	Error           *SignalError                    `json:"error"`
}

// SignalError is an operational diagnostic, never a physical confirmation.
type SignalError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Signal struct {
	Definition SignalDefinition `json:"definition"`
	State      SignalState      `json:"state"`
}

type SignalCommanded struct {
	SignalID string         `json:"signalId"`
	Aspect   SignalAspectID `json:"aspect"`
}
type SignalCommandFailed struct {
	SignalID string         `json:"signalId"`
	Aspect   SignalAspectID `json:"aspect"`
	Reason   string         `json:"reason"`
}
type SignalModeChanged struct {
	SignalID string     `json:"signalId"`
	Mode     SignalMode `json:"mode"`
}

func NewSignalState(s SignalDefinition, profile SignalProfile) (SignalState, error) {
	normalized, err := NormalizeSignalDefinition(s, profile)
	if err != nil {
		return SignalState{}, err
	}
	return SignalState{ID: normalized.ID, ComputedAspect: normalized.FallbackAspect, DesiredAspect: normalized.FallbackAspect}, nil
}
