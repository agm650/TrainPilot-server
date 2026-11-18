package station_test

import (
	"context"
	"errors"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/station/contracttest"
)

type legacyExtendedTestStation struct {
	station.CommandStation
	caps station.Capabilities
}

func (s *legacyExtendedTestStation) Capabilities() station.Capabilities { return s.caps }

type extendedTestStation struct {
	legacyExtendedTestStation
	commands []station.ExtendedAccessoryCommand
	err      error
	offline  bool
}

func (s *extendedTestStation) SetExtendedAccessory(_ context.Context, c station.ExtendedAccessoryCommand) error {
	s.commands = append(s.commands, c)
	return s.err
}
func (s *extendedTestStation) Health() station.Health {
	if s.offline {
		return station.Health{Connectivity: station.Offline}
	}
	return station.Health{Connectivity: station.Online}
}
func newExtendedTestStation() *extendedTestStation {
	s := &extendedTestStation{}
	s.caps.ExtendedAccessoryControl = true
	return s
}

func TestExtendedAccessoryContract(t *testing.T) {
	contracttest.ExtendedAccessoryContract(t, func(t *testing.T) contracttest.ExtendedAccessoryHarness {
		s := newExtendedTestStation()
		return contracttest.ExtendedAccessoryHarness{Station: s, Commands: func() []station.ExtendedAccessoryCommand { return s.commands }}
	})
}
func TestExtendedAccessoryDispatchCapabilitiesAndTransport(t *testing.T) {
	c := station.ExtendedAccessoryCommand{Address: 1, Aspect: 5}
	legacy := &legacyExtendedTestStation{}
	if err := station.SendExtendedAccessory(context.Background(), legacy, c); !errors.Is(err, station.ErrUnsupported) {
		t.Fatal(err)
	}
	legacy.caps.ExtendedAccessoryControl = true
	if err := station.SendExtendedAccessory(context.Background(), legacy, c); !errors.Is(err, station.ErrUnsupported) {
		t.Fatal("capability alone was trusted")
	}
	s := newExtendedTestStation()
	s.caps.ExtendedAccessoryControl = false
	if err := station.SendExtendedAccessory(context.Background(), s, c); !errors.Is(err, station.ErrUnsupported) {
		t.Fatal(err)
	}
	s.caps.ExtendedAccessoryControl = true
	limits := station.PortableExtendedAccessoryLimits()
	limits.MaxAspect = 3
	s.caps.ExtendedAccessoryLimits = &limits
	if err := station.SendExtendedAccessory(context.Background(), s, c); !errors.Is(err, station.ErrInvalidExtendedAccessoryAspect) {
		t.Fatal(err)
	}
	limits.MaxAspect = 255
	s.offline = true
	if err := station.SendExtendedAccessory(context.Background(), s, c); !errors.Is(err, station.ErrOffline) {
		t.Fatal(err)
	}
	if len(s.commands) != 0 {
		t.Fatal("rejected commands reached the transport")
	}
	s.offline = false
	s.err = errors.New("transport failed")
	if err := station.SendExtendedAccessory(context.Background(), s, c); !errors.Is(err, s.err) {
		t.Fatal("transport error lost")
	}
	e := station.ExtendedAccessoryStateEvent{Address: 1, Aspect: &c.Aspect, State: station.AccessoryReportUnknown}
	if e.HasKnownAspect() {
		t.Fatal("unknown report became confirmed")
	}
	e.State = station.AccessoryReportKnown
	if !e.HasKnownAspect() || e.Quality == station.AccessoryReportPhysical {
		t.Fatal("incorrect report semantics")
	}
}
