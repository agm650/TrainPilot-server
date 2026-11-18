package scenario

import (
	"context"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/station/simulator"
)

func TestExtendedAccessoryScenarioReplay(t *testing.T) {
	const content = `{"version":2,"name":"extended","initial":{},"steps":[{"at":"0s","action":"extended_accessory.set","address":1,"aspect":255},{"at":"1s","action":"extended_accessory.set","address":2,"aspect":0}]}`
	definition, err := Parse([]byte(content))
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(time.Now())
	s := simulator.NewWithClock(clk)
	_ = s.Connect(context.Background())
	defer s.Close()
	for i := 0; i < 2; i++ {
		r, err := New(definition, s, clk)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := r.Advance(context.Background(), time.Second); err != nil {
			t.Fatal(err)
		}
		if len(s.ExtendedAccessoryCommands()) != 2 || *s.Snapshot().ExtendedAccessories[1].Aspect != 255 {
			t.Fatal("non-deterministic replay")
		}
		r.Close()
		s.Reset()
	}
}
