package station

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var ErrUnsupported = errors.New("command not supported by station driver")
var ErrOffline = errors.New("command station is offline")
var ErrInvalidAccessoryAddress = errors.New("invalid basic accessory address")
var ErrInvalidAccessoryPosition = errors.New("invalid basic accessory position")

const (
	// MinBasicAccessoryAddress and MaxBasicAccessoryAddress delimit the
	// portable TrainPilot range for DCC basic accessory linear addresses.
	// Addresses 2041 through 2044 are excluded because they map to the DCC
	// accessory broadcast decoder in systems such as DCC-EX.
	MinBasicAccessoryAddress = 1
	MaxBasicAccessoryAddress = 2040
)

type Connectivity string

const (
	Online   Connectivity = "online"
	Degraded Connectivity = "degraded"
	Offline  Connectivity = "offline"
)

type Health struct {
	Connectivity Connectivity `json:"connectivity"`
	LastSeen     *time.Time   `json:"lastSeen,omitempty"`
}

type Direction string

const (
	Forward Direction = "forward"
	Reverse Direction = "reverse"
)

func (d Direction) Valid() bool {
	return d == Forward || d == Reverse
}

// AccessoryPosition identifies one of the two outputs of a DCC basic
// accessory. It deliberately carries no turnout geometry.
type AccessoryPosition string

const (
	AccessoryPosition1 AccessoryPosition = "position1"
	AccessoryPosition2 AccessoryPosition = "position2"
)

func (p AccessoryPosition) Valid() bool {
	return p == AccessoryPosition1 || p == AccessoryPosition2
}

func (p AccessoryPosition) Inverted() AccessoryPosition {
	switch p {
	case AccessoryPosition1:
		return AccessoryPosition2
	case AccessoryPosition2:
		return AccessoryPosition1
	default:
		return p
	}
}

// AccessoryCommand addresses one DCC basic accessory output using the
// canonical TrainPilot linear address.
type AccessoryCommand struct {
	Address  int               `json:"address"`
	Position AccessoryPosition `json:"position"`
}

func (c AccessoryCommand) Validate() error {
	if c.Address < MinBasicAccessoryAddress || c.Address > MaxBasicAccessoryAddress {
		return fmt.Errorf("%w: got %d, want %d..%d", ErrInvalidAccessoryAddress, c.Address, MinBasicAccessoryAddress, MaxBasicAccessoryAddress)
	}
	if !c.Position.Valid() {
		return fmt.Errorf("%w: %q", ErrInvalidAccessoryPosition, c.Position)
	}
	return nil
}

// AccessoryReportQuality describes how authoritative an accessory observation
// is. It never promotes a command-station echo to physical confirmation.
type AccessoryReportQuality string

const (
	// AccessoryReportStation means the command station reported its function
	// state. It is not proof that the physical blades moved.
	AccessoryReportStation AccessoryReportQuality = "station"
	// AccessoryReportAssumed means the state is inferred from a successful
	// command or protocol echo, without authoritative station feedback.
	AccessoryReportAssumed AccessoryReportQuality = "assumed"
	// AccessoryReportPhysical is reserved for a real position sensor.
	AccessoryReportPhysical AccessoryReportQuality = "physical"
)

func (q AccessoryReportQuality) Valid() bool {
	switch q {
	case AccessoryReportStation, AccessoryReportAssumed, AccessoryReportPhysical:
		return true
	default:
		return false
	}
}

// AccessoryStateEvent reports the last observed binary state of one basic
// accessory endpoint.
type AccessoryStateEvent struct {
	Address    int                    `json:"address"`
	Position   AccessoryPosition      `json:"position"`
	Quality    AccessoryReportQuality `json:"quality"`
	ObservedAt time.Time              `json:"observedAt"`
}

type Capabilities struct {
	Driver            string `json:"driver"`
	TrackPower        bool   `json:"trackPower"`
	LocomotiveControl bool   `json:"locomotiveControl"`
	Functions         int    `json:"functions"`
	MaxFunctionNumber int    `json:"maxFunctionNumber"`
	AccessoryControl  bool   `json:"accessoryControl"`
	Feedback          bool   `json:"feedback"`
}

type FeedbackEvent struct {
	Source  string
	Kind    string
	Address int
	Active  bool
}

type Status struct {
	Connectivity                 Connectivity `json:"connectivity"`
	LastSeen                     *time.Time   `json:"lastSeen,omitempty"`
	TrackPower                   string       `json:"trackPower"`
	EmergencyStop                bool         `json:"emergencyStop"`
	ShortCircuit                 bool         `json:"shortCircuit"`
	ProgrammingMode              bool         `json:"programmingMode"`
	MainCurrentMilliAmps         int16        `json:"mainCurrentMilliAmps"`
	ProgrammingCurrentMilliAmps  int16        `json:"programmingCurrentMilliAmps"`
	FilteredMainCurrentMilliAmps int16        `json:"filteredMainCurrentMilliAmps"`
	TemperatureCelsius           int16        `json:"temperatureCelsius"`
	SupplyVoltageMilliVolts      uint16       `json:"supplyVoltageMilliVolts"`
	TrackVoltageMilliVolts       uint16       `json:"trackVoltageMilliVolts"`
	HighTemperature              bool         `json:"highTemperature"`
	PowerLost                    bool         `json:"powerLost"`
	ExternalShortCircuit         bool         `json:"externalShortCircuit"`
	InternalShortCircuit         bool         `json:"internalShortCircuit"`
}

type StatusProvider interface {
	Status(context.Context) (Status, error)
}

// StatusEventProvider is implemented by station drivers that expose
// asynchronous station status updates to the service layer.
type StatusEventProvider interface {
	StatusEvents() <-chan Status
}

// AccessoryStateEventProvider is implemented by drivers that can publish
// basic accessory state observations. Drivers must document their buffering
// and backpressure policy.
type AccessoryStateEventProvider interface {
	AccessoryStateEvents() <-chan AccessoryStateEvent
}

type HealthProvider interface {
	Health() Health
}

func CheckCommandAllowed(s CommandStation) error {
	if provider, ok := s.(HealthProvider); ok && provider.Health().Connectivity == Offline {
		return ErrOffline
	}
	return nil
}

type CommandStation interface {
	Connect(context.Context) error
	Close() error
	Capabilities() Capabilities
	SetTrackPower(context.Context, bool) error
	EmergencyStop(context.Context) error
	SetLocoSpeed(context.Context, int, float64, Direction) error
	SetLocoFunction(context.Context, int, int, bool) error
	SetBasicAccessory(context.Context, AccessoryCommand) error
	Feedback() <-chan FeedbackEvent
}
