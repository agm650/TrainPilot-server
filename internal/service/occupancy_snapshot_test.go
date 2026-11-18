package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/model"
)

func TestOccupancySnapshotsNeverSplitAnObservationBatch(t *testing.T) {
	_, o, _, _ := automaticSignalFixture(t, false)
	observeAutomaticBlocks(t, o, 1, model.OccupancyOccupied, model.OccupancyFree, model.OccupancyFree)
	var wg sync.WaitGroup
	done := make(chan struct{})
	defer func() { close(done); wg.Wait() }()
	failures := make(chan OccupancySnapshot, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			snapshot := o.Snapshot()
			a, b := snapshot.Blocks["a"].State, snapshot.Blocks["b"].State
			if (a == model.OccupancyOccupied) == (b == model.OccupancyOccupied) {
				select {
				case failures <- snapshot:
				default:
				}
				return
			}
		}
	}()
	for i := uint64(2); i < 100; i++ {
		a, b := model.OccupancyFree, model.OccupancyOccupied
		if i%2 == 1 {
			a, b = b, a
		}
		if err := o.ObserveBatch(context.Background(), []model.OccupancyObservation{{ProviderID: "external", SensorID: "a", State: a, Sequence: i, ObservedAt: time.Now()}, {ProviderID: "external", SensorID: "b", State: b, Sequence: i, ObservedAt: time.Now()}}); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case snapshot := <-failures:
		t.Fatal("mixed batch", snapshot)
	default:
	}
	snapshot := o.Snapshot()
	delete(snapshot.Blocks, "a")
	if _, found := o.Snapshot().Blocks["a"]; !found {
		t.Fatal("snapshot aliases runtime map")
	}
}
