package model

import "time"

type Role string

const (
	RoleViewer        Role = "viewer"
	RoleDriver        Role = "driver"
	RoleDispatcher    Role = "dispatcher"
	RoleAdministrator Role = "administrator"
)

func (r Role) Valid() bool {
	switch r {
	case RoleViewer, RoleDriver, RoleDispatcher, RoleAdministrator:
		return true
	default:
		return false
	}
}

type User struct {
	ID                 string     `json:"id"`
	Username           string     `json:"username"`
	DisplayName        string     `json:"displayName,omitempty"`
	Role               Role       `json:"role"`
	Enabled            bool       `json:"enabled"`
	MustChangePassword bool       `json:"mustChangePassword"`
	CreatedAt          time.Time  `json:"createdAt"`
	UpdatedAt          time.Time  `json:"updatedAt"`
	LastLoginAt        *time.Time `json:"lastLoginAt,omitempty"`
}

type Session struct {
	ID            string
	UserID        string
	ClientID      string
	ClientName    string
	Platform      string
	AccessHash    string
	RefreshHash   string
	AccessExpiry  time.Time
	RefreshExpiry time.Time
	CreatedAt     time.Time
	LastSeenAt    time.Time
	RevokedAt     *time.Time
}

type Locomotive struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	DCCAddress   int    `json:"dccAddress"`
	AddressKind  string `json:"addressKind"`
	SpeedSteps   int    `json:"speedSteps"`
	Manufacturer string `json:"manufacturer,omitempty"`
	Model        string `json:"model,omitempty"`
}

type LeaseState string

const (
	LeaseActive   LeaseState = "active"
	LeaseStopping LeaseState = "stopping"
	LeaseReleased LeaseState = "released"
)

type ControlLease struct {
	ID              string     `json:"id"`
	LocomotiveID    string     `json:"locomotiveId"`
	UserID          string     `json:"userId"`
	SessionID       string     `json:"sessionId"`
	State           LeaseState `json:"state"`
	AcquiredAt      time.Time  `json:"acquiredAt"`
	RenewedAt       time.Time  `json:"renewedAt"`
	ExpiresAt       time.Time  `json:"expiresAt"`
	ReleaseAfter    *time.Time `json:"releaseAfter,omitempty"`
	ReleaseReason   string     `json:"releaseReason,omitempty"`
	HeartbeatMillis int64      `json:"heartbeatMillis"`
}

type Block struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Occupied bool   `json:"occupied"`
}

type Turnout struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	DCCAddress    int    `json:"dccAddress"`
	DesiredState  string `json:"desiredState"`
	ReportedState string `json:"reportedState"`
}

type Route struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	State             string `json:"state"`
	ReservedBySession string `json:"reservedBySession,omitempty"`
}

type FeedbackMapping struct {
	Provider string `json:"provider"`
	Address  int    `json:"address"`
	BlockID  string `json:"blockId"`
}

type RouteDefinition struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	BlockIDs         []string          `json:"blockIds"`
	TurnoutStates    map[string]string `json:"turnoutStates"`
	ConflictRouteIDs []string          `json:"conflictRouteIds,omitempty"`
}

type LayoutDefinition struct {
	Blocks           []Block           `json:"blocks"`
	Turnouts         []Turnout         `json:"turnouts"`
	Routes           []RouteDefinition `json:"routes"`
	FeedbackMappings []FeedbackMapping `json:"feedbackMappings"`
}
