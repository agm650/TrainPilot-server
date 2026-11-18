package contracttest

import (
	"context"
	"errors"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/station"
)

type ExtendedAccessoryHarness struct {
	Station  station.CommandStation
	Commands func() []station.ExtendedAccessoryCommand
}

func ExtendedAccessoryContract(t *testing.T, factory func(*testing.T) ExtendedAccessoryHarness) {
	t.Helper()
	t.Run("limits", func(t *testing.T) {
		h := factory(t)
		for _, command := range []station.ExtendedAccessoryCommand{{Address: 1, Aspect: 0}, {Address: 2040, Aspect: 255}} {
			if err := station.SendExtendedAccessory(context.Background(), h.Station, command); err != nil {
				t.Fatal(err)
			}
		}
		if len(h.Commands()) != 2 {
			t.Fatal("valid commands were not dispatched")
		}
	})
	t.Run("invalid_and_cancelled_do_not_dispatch", func(t *testing.T) {
		h := factory(t)
		for _, tc := range []struct {
			command station.ExtendedAccessoryCommand
			want    error
		}{
			{station.ExtendedAccessoryCommand{Address: 0}, station.ErrInvalidExtendedAccessoryAddress},
			{station.ExtendedAccessoryCommand{Address: 2041}, station.ErrInvalidExtendedAccessoryAddress},
			{station.ExtendedAccessoryCommand{Address: 1, Aspect: -1}, station.ErrInvalidExtendedAccessoryAspect},
			{station.ExtendedAccessoryCommand{Address: 1, Aspect: 256}, station.ErrInvalidExtendedAccessoryAspect},
		} {
			if err := station.SendExtendedAccessory(context.Background(), h.Station, tc.command); !errors.Is(err, tc.want) {
				t.Fatalf("error=%v want %v", err, tc.want)
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := station.SendExtendedAccessory(ctx, h.Station, station.ExtendedAccessoryCommand{Address: 1}); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if len(h.Commands()) != 0 {
			t.Fatal("rejected command was dispatched")
		}
	})
}
