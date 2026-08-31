package station

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAccessoryPositionValidation(t *testing.T) {
	tests := []struct {
		position AccessoryPosition
		valid    bool
	}{
		{AccessoryPosition1, true},
		{AccessoryPosition2, true},
		{AccessoryPosition("invalid"), false},
	}
	for _, test := range tests {
		if got := test.position.Valid(); got != test.valid {
			t.Fatalf("AccessoryPosition(%q).Valid()=%t want %t", test.position, got, test.valid)
		}
	}
}

func TestAccessoryCommandValidation(t *testing.T) {
	tests := []struct {
		name    string
		command AccessoryCommand
		wantErr error
	}{
		{"minimum address", AccessoryCommand{Address: MinBasicAccessoryAddress, Position: AccessoryPosition1}, nil},
		{"maximum address", AccessoryCommand{Address: MaxBasicAccessoryAddress, Position: AccessoryPosition2}, nil},
		{"zero address", AccessoryCommand{Address: 0, Position: AccessoryPosition1}, ErrInvalidAccessoryAddress},
		{"negative address", AccessoryCommand{Address: -1, Position: AccessoryPosition1}, ErrInvalidAccessoryAddress},
		{"reserved address", AccessoryCommand{Address: MaxBasicAccessoryAddress + 1, Position: AccessoryPosition1}, ErrInvalidAccessoryAddress},
		{"invalid position", AccessoryCommand{Address: 1, Position: AccessoryPosition("invalid")}, ErrInvalidAccessoryPosition},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.command.Validate()
			if test.wantErr == nil && err != nil {
				t.Fatalf("Validate() error=%v", err)
			}
			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Fatalf("Validate() error=%v want %v", err, test.wantErr)
			}
		})
	}
}

func TestAccessoryReportQualityValidation(t *testing.T) {
	for _, quality := range []AccessoryReportQuality{
		AccessoryReportStation,
		AccessoryReportAssumed,
		AccessoryReportPhysical,
	} {
		if !quality.Valid() {
			t.Fatalf("quality %q is invalid", quality)
		}
	}
	if AccessoryReportQuality("invalid").Valid() {
		t.Fatal("invalid accessory report quality accepted")
	}
}

func TestAccessoryStateEventProviderPreservesEvent(t *testing.T) {
	now := time.Date(2026, time.August, 31, 10, 0, 0, 0, time.UTC)
	want := AccessoryStateEvent{
		Address:    12,
		Position:   AccessoryPosition2,
		Quality:    AccessoryReportStation,
		ObservedAt: now,
	}
	fake := newAccessoryEventStation()
	var provider AccessoryStateEventProvider = fake
	fake.events <- want

	select {
	case got := <-provider.AccessoryStateEvents():
		if got != want {
			t.Fatalf("event=%+v want %+v", got, want)
		}
	default:
		t.Fatal("accessory state event was not published")
	}
}

type accessoryEventStation struct {
	events   chan AccessoryStateEvent
	feedback chan FeedbackEvent
}

func newAccessoryEventStation() *accessoryEventStation {
	return &accessoryEventStation{
		events:   make(chan AccessoryStateEvent, 1),
		feedback: make(chan FeedbackEvent),
	}
}

func (*accessoryEventStation) Connect(context.Context) error { return nil }
func (*accessoryEventStation) Close() error                  { return nil }
func (*accessoryEventStation) Capabilities() Capabilities    { return Capabilities{} }
func (*accessoryEventStation) SetTrackPower(context.Context, bool) error {
	return nil
}
func (*accessoryEventStation) EmergencyStop(context.Context) error { return nil }
func (*accessoryEventStation) SetLocoSpeed(context.Context, int, float64, Direction) error {
	return nil
}
func (*accessoryEventStation) SetLocoFunction(context.Context, int, int, bool) error {
	return nil
}
func (*accessoryEventStation) SetBasicAccessory(context.Context, AccessoryCommand) error {
	return nil
}
func (s *accessoryEventStation) Feedback() <-chan FeedbackEvent { return s.feedback }
func (s *accessoryEventStation) AccessoryStateEvents() <-chan AccessoryStateEvent {
	return s.events
}

var _ CommandStation = (*accessoryEventStation)(nil)
var _ AccessoryStateEventProvider = (*accessoryEventStation)(nil)
