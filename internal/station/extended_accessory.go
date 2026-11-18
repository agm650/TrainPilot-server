package station

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Initial TrainPilot portable subset. These limits are separate from basic
// accessories; drivers can advertise a narrower subset explicitly.
const (
	MinExtendedAccessoryAddress = 1
	MaxExtendedAccessoryAddress = 2040
	MinExtendedAccessoryAspect  = 0
	MaxExtendedAccessoryAspect  = 255
)

var ErrInvalidExtendedAccessoryAddress = errors.New("invalid extended accessory address")
var ErrInvalidExtendedAccessoryAspect = errors.New("invalid extended accessory aspect")

type ExtendedAccessoryCommand struct {
	Address int `json:"address"`
	Aspect  int `json:"aspect"`
}

func (c ExtendedAccessoryCommand) Validate() error {
	if c.Address < MinExtendedAccessoryAddress || c.Address > MaxExtendedAccessoryAddress {
		return fmt.Errorf("%w: %d", ErrInvalidExtendedAccessoryAddress, c.Address)
	}
	if c.Aspect < MinExtendedAccessoryAspect || c.Aspect > MaxExtendedAccessoryAspect {
		return fmt.Errorf("%w: %d", ErrInvalidExtendedAccessoryAspect, c.Aspect)
	}
	return nil
}

type ExtendedAccessoryLimits struct {
	MinAddress int `json:"minAddress"`
	MaxAddress int `json:"maxAddress"`
	MinAspect  int `json:"minAspect"`
	MaxAspect  int `json:"maxAspect"`
}

func PortableExtendedAccessoryLimits() ExtendedAccessoryLimits {
	return ExtendedAccessoryLimits{MinExtendedAccessoryAddress, MaxExtendedAccessoryAddress, MinExtendedAccessoryAspect, MaxExtendedAccessoryAspect}
}

// Optional: existing CommandStation implementations need no new methods.
type ExtendedAccessoryController interface {
	SetExtendedAccessory(context.Context, ExtendedAccessoryCommand) error
}

type ExtendedAccessoryStateEvent struct {
	Address    int                    `json:"address"`
	Aspect     *int                   `json:"aspect"`
	State      AccessoryReportState   `json:"state"`
	Quality    AccessoryReportQuality `json:"quality"`
	ObservedAt time.Time              `json:"observedAt"`
}

func (e ExtendedAccessoryStateEvent) HasKnownAspect() bool {
	return e.State == AccessoryReportKnown && e.Aspect != nil && (ExtendedAccessoryCommand{Address: e.Address, Aspect: *e.Aspect}).Validate() == nil
}

type ExtendedAccessoryStateEventProvider interface {
	ExtendedAccessoryStateEvents() <-chan ExtendedAccessoryStateEvent
}

// SendExtendedAccessory validates before dispatch and preserves transport
// errors. Successful dispatch never manufactures an observation or confirmation.
func SendExtendedAccessory(ctx context.Context, s CommandStation, command ExtendedAccessoryCommand) error {
	if err := command.Validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	controller, supported := s.(ExtendedAccessoryController)
	caps := s.Capabilities()
	if !supported || !caps.ExtendedAccessoryControl {
		return ErrUnsupported
	}
	limits := PortableExtendedAccessoryLimits()
	if caps.ExtendedAccessoryLimits != nil {
		limits = *caps.ExtendedAccessoryLimits
	}
	if limits.MinAddress < MinExtendedAccessoryAddress || limits.MaxAddress > MaxExtendedAccessoryAddress || limits.MinAddress > limits.MaxAddress || limits.MinAspect < 0 || limits.MaxAspect > MaxExtendedAccessoryAspect || limits.MinAspect > limits.MaxAspect {
		return fmt.Errorf("%w: inconsistent extended accessory limits", ErrUnsupported)
	}
	if command.Address < limits.MinAddress || command.Address > limits.MaxAddress {
		return ErrInvalidExtendedAccessoryAddress
	}
	if command.Aspect < limits.MinAspect || command.Aspect > limits.MaxAspect {
		return ErrInvalidExtendedAccessoryAspect
	}
	if err := CheckCommandAllowed(s); err != nil {
		return err
	}
	return controller.SetExtendedAccessory(ctx, command)
}
