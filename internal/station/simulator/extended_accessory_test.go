package simulator

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/station/contracttest"
)

func TestExtendedAccessoryContract(t *testing.T) {
	contracttest.ExtendedAccessoryContract(t, func(t *testing.T) contracttest.ExtendedAccessoryHarness {
		s := New()
		if err := s.Connect(context.Background()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return contracttest.ExtendedAccessoryHarness{Station: s, Commands: func() []station.ExtendedAccessoryCommand {
			result := []station.ExtendedAccessoryCommand{}
			for _, record := range s.ExtendedAccessoryCommands() {
				result = append(result, record.Command)
			}
			return result
		}}
	})
}

func TestExtendedAccessoryStatesJournalResetAndReconnect(t *testing.T) {
	s := New()
	ctx := context.Background()
	_ = s.Connect(ctx)
	defer s.Close()
	commands := []station.ExtendedAccessoryCommand{{Address: 1, Aspect: 0}, {Address: 2, Aspect: 255}, {Address: 1, Aspect: 0}}
	for _, c := range commands {
		if err := s.SetExtendedAccessory(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.ExtendedAccessoryCommands()) != 3 || len(s.ExtendedAccessoryStateEvents()) != 2 {
		t.Fatal("incorrect no-op/journal behavior")
	}
	for len(s.ExtendedAccessoryStateEvents()) > 0 {
		e := <-s.ExtendedAccessoryStateEvents()
		if !e.HasKnownAspect() || e.Quality != station.AccessoryReportAssumed {
			t.Fatalf("false confirmation: %+v", e)
		}
		*e.Aspect = 42 // A consumer must not mutate the simulator's state.
	}
	snapshot := s.Snapshot()
	if *snapshot.ExtendedAccessories[1].Aspect != 0 || *snapshot.ExtendedAccessories[2].Aspect != 255 {
		t.Fatal(snapshot)
	}
	*snapshot.ExtendedAccessories[1].Aspect = 9
	if *s.Snapshot().ExtendedAccessories[1].Aspect != 0 {
		t.Fatal("snapshot aliases simulator state")
	}
	if err := s.SetBasicAccessory(ctx, station.AccessoryCommand{Address: 1, Position: station.AccessoryPosition2}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLocoSpeed(ctx, 3, .25, station.Forward); err != nil {
		t.Fatal(err)
	}
	if *s.Snapshot().ExtendedAccessories[1].Aspect != 0 {
		t.Fatal("binary command changed extended state")
	}
	_ = s.SetConnectivity(station.Offline)
	if err := s.SetExtendedAccessory(ctx, station.ExtendedAccessoryCommand{Address: 1, Aspect: 5}); !errors.Is(err, station.ErrOffline) {
		t.Fatal(err)
	}
	_ = s.SetConnectivity(station.Online)
	if s.Snapshot().ExtendedAccessories[1].Aspect != nil || len(s.ExtendedAccessoryCommands()) != 3 {
		t.Fatal("reconnect replayed or restored an observation")
	}
	s.Reset()
	if len(s.ExtendedAccessoryCommands()) != 0 || len(s.Snapshot().ExtendedAccessories) != 0 || len(s.ExtendedAccessoryStateEvents()) != 0 {
		t.Fatal("reset retained extended state")
	}
}

func TestExtendedAccessoryFaultAndConcurrency(t *testing.T) {
	s := New()
	ctx := context.Background()
	_ = s.Connect(ctx)
	defer s.Close()
	failure := errors.New("injected")
	if err := s.SetOperationFault(OpExtendedAccessory, OperationFault{Error: failure, Address: 2, Remaining: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetExtendedAccessory(ctx, station.ExtendedAccessoryCommand{Address: 2, Aspect: 7}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if len(s.ExtendedAccessoryCommands()) != 0 || len(s.Snapshot().ExtendedAccessories) != 0 {
		t.Fatal("failed write mutated state")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- s.SetExtendedAccessory(ctx, station.ExtendedAccessoryCommand{Address: i%5 + 1, Aspect: i})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(s.ExtendedAccessoryCommands()) != 100 || len(s.Snapshot().ExtendedAccessories) != 5 {
		t.Fatal("concurrent command lost")
	}
}

func TestExtendedAccessoryDelayCancellation(t *testing.T) {
	s := New()
	_ = s.Connect(context.Background())
	defer s.Close()
	_ = s.SetOperationFault(OpExtendedAccessory, OperationFault{Delay: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.SetExtendedAccessory(ctx, station.ExtendedAccessoryCommand{Address: 1}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(s.ExtendedAccessoryCommands()) != 0 {
		t.Fatal("cancelled command applied")
	}
}

func TestExtendedAccessoryResetCancelsWaitingWrite(t *testing.T) {
	clk := newControlledWaitClock()
	s := NewWithClock(clk)
	ctx := context.Background()
	_ = s.Connect(ctx)
	defer s.Close()
	_ = s.SetOperationFault(OpExtendedAccessory, OperationFault{Delay: time.Hour})
	done := make(chan error, 1)
	go func() { done <- s.SetExtendedAccessory(ctx, station.ExtendedAccessoryCommand{Address: 1, Aspect: 42}) }()
	waitForControlledClock(t, clk)
	s.Reset()
	close(clk.release)
	if err := waitForOperation(t, done); !errors.Is(err, ErrOperationCanceled) {
		t.Fatal(err)
	}
	if len(s.ExtendedAccessoryCommands()) != 0 || len(s.Snapshot().ExtendedAccessories) != 0 {
		t.Fatal("old write survived reset")
	}
}
