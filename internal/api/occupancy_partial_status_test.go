package api

import (
	"context"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/service"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/station/z21"
)

func TestZ21SystemOnlyPollingKeepsOccupancyWithoutRepeatedScans(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := newZ21OccupancyServer(t)
	fake.omitXStatus.Store(true)
	driver := z21.New(fake.conn.LocalAddr().String(), station.DefaultOfflineAfter, time.Millisecond)
	defer driver.Close()
	if err := driver.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	server, c, token, railway, api := newZ21OccupancyHTTPFixtureWithAPI(t, ctx, driver)
	// Exercise the real background status consumer, including the signal
	// service's concurrent initial probe, as in dccd startup.
	signals := service.NewSignalService(api.store, driver, api.events, railway.OccupancyService())
	api.control.SetSignalService(signals)
	api.control.Start()
	defer api.control.Close()
	if err := signals.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer signals.Close()
	if _, err := c.StationStatus(ctx); err != nil {
		t.Fatal(err)
	}
	fake.replyToScan(t)
	want := map[string]model.OccupancyState{"block-a": model.OccupancyOccupied, "block-b": model.OccupancyFree, "block-c": model.OccupancyOccupied}
	waitForStartupOccupancy(t, railway, want)
	before := driver.FeedbackState()
	for len(fake.requests) > 0 {
		<-fake.requests
	}
	for len(fake.systemProbes) > 0 {
		<-fake.systemProbes
	}
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	for i := 0; i < 3; i++ {
		select {
		case <-fake.systemProbes:
		case <-deadline.C:
			t.Fatal("background status polling stopped")
		}
		if got := driver.FeedbackState(); !got.Available || got.Generation != before.Generation || got.PowerGeneration != before.PowerGeneration {
			t.Fatal("partial status restarted synchronization", before, got)
		}
		for id, state := range want {
			if got := railway.OccupancyService().BlockState(id).State; got != state {
				t.Fatal("partial status invalidated detector", id, got, state)
			}
		}
	}
	if len(fake.requests) != 0 {
		t.Fatal("unchanged detectors were queried again", len(fake.requests))
	}
	blocks, err := c.Blocks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertStartupBlocks(t, blocks, want)
	ws := dialTestWebSocket(t, server.URL, token)
	defer ws.close()
	assertStartupBlocks(t, readTestSnapshot(t, ws).Payload.Blocks, want)
}
