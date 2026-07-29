package station

import (
	"context"
	"errors"
)

var ErrUnsupported = errors.New("command not supported by station driver")

type Direction string

const (
	Forward Direction = "forward"
	Reverse Direction = "reverse"
)

type Capabilities struct {
	Driver            string `json:"driver"`
	TrackPower        bool   `json:"trackPower"`
	LocomotiveControl bool   `json:"locomotiveControl"`
	Functions         int    `json:"functions"`
	AccessoryControl  bool   `json:"accessoryControl"`
	Feedback          bool   `json:"feedback"`
}

type FeedbackEvent struct {
	Source  string
	Kind    string
	Address int
	Active  bool
}

type CommandStation interface {
	Connect(context.Context) error
	Close() error
	Capabilities() Capabilities
	SetTrackPower(context.Context, bool) error
	EmergencyStop(context.Context) error
	SetLocoSpeed(context.Context, int, float64, Direction) error
	SetLocoFunction(context.Context, int, int, bool) error
	SetAccessory(context.Context, int, string) error
	Feedback() <-chan FeedbackEvent
}
