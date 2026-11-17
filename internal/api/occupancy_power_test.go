package api

import (
	"context"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/station/z21"
)

func TestZ21TrackPowerRecoveryQueriesFreshOccupancyAndUpdatesClients(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := newZ21OccupancyServer(t)
	driver := z21.New(fake.conn.LocalAddr().String(), station.DefaultOfflineAfter, time.Millisecond)
	defer driver.Close()
	if err := driver.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	// Slow CI can trigger a retry before startup replies are consumed. Leave
	// that retry queued to verify that recovery selects the renewed endpoint.
	if err := driver.RefreshFeedback(ctx); err != nil {
		t.Fatal(err)
	}
	server, apiClient, token, railway := newZ21OccupancyHTTPFixture(t, ctx, driver)
	startup := fake.replyToScan(t)
	waitForStartupOccupancy(t, railway, map[string]model.OccupancyState{"block-a": model.OccupancyOccupied, "block-b": model.OccupancyFree, "block-c": model.OccupancyOccupied})
	ws := dialTestWebSocket(t, server.URL, token)
	defer ws.close()
	readTestSnapshot(t, ws)
	before := driver.FeedbackState()
	if err := apiClient.SetTrackPower(ctx, false); err != nil {
		t.Fatal(err)
	}
	unknown := map[string]model.OccupancyState{"block-a": model.OccupancyUnknown, "block-b": model.OccupancyUnknown, "block-c": model.OccupancyUnknown}
	waitForStartupOccupancy(t, railway, unknown)
	if driver.Health().Connectivity != station.Online {
		t.Fatal("track power cut was treated as network loss")
	}
	if err := apiClient.SetTrackPower(ctx, true); err != nil {
		t.Fatal(err)
	}
	after := driver.FeedbackState()
	if after.Generation != before.Generation || after.PowerGeneration <= before.PowerGeneration {
		t.Fatalf("wrong epoch transition: before=%+v after=%+v", before, after)
	}
	waitForStartupOccupancy(t, railway, unknown)
	requests := fake.scanAfter(t, startup[0].from)
	// The train moved to input 81 while power was OFF. Group 1 is deliberately
	// withheld until after checking that partial data cannot revive old states.
	payload := make([]byte, 11)
	if _, err := fake.conn.WriteToUDP(apiZ21LAN(0x0080, payload), requests[0].from); err != nil {
		t.Fatal(err)
	}
	waitForStartupOccupancy(t, railway, map[string]model.OccupancyState{"block-a": model.OccupancyFree, "block-b": model.OccupancyUnknown, "block-c": model.OccupancyUnknown})
	payload[0], payload[1] = 1, 1
	if _, err := fake.conn.WriteToUDP(apiZ21LAN(0x0080, payload), requests[1].from); err != nil {
		t.Fatal(err)
	}
	want := map[string]model.OccupancyState{"block-a": model.OccupancyFree, "block-b": model.OccupancyOccupied, "block-c": model.OccupancyFree}
	waitForStartupOccupancy(t, railway, want)
	blocks, err := apiClient.Blocks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertStartupBlocks(t, blocks, want)
	ws.writeJSON(t, map[string]string{"type": "client.snapshot_request"})
	for i := 0; i < 20; i++ {
		var message systemSnapshot
		ws.readJSON(t, &message)
		if message.Type == "system.snapshot" {
			assertStartupBlocks(t, message.Payload.Blocks, want)
			return
		}
	}
	t.Fatal("power recovery snapshot missing")
}

func TestZ21PowerOnCommandCannotOverrideObservedPowerOff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := newZ21OccupancyServer(t)
	driver := z21.New(fake.conn.LocalAddr().String(), station.DefaultOfflineAfter, time.Millisecond)
	defer driver.Close()
	if err := driver.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	_, apiClient, _, railway := newZ21OccupancyHTTPFixture(t, ctx, driver)
	fake.replyToScan(t)
	waitForStartupOccupancy(t, railway, map[string]model.OccupancyState{"block-a": model.OccupancyOccupied, "block-b": model.OccupancyFree, "block-c": model.OccupancyOccupied})
	if err := apiClient.SetTrackPower(ctx, false); err != nil {
		t.Fatal(err)
	}
	fake.ignorePowerOn.Store(true)
	if err := apiClient.SetTrackPower(ctx, true); err != nil {
		t.Fatal(err)
	}
	waitForStartupOccupancy(t, railway, map[string]model.OccupancyState{"block-a": model.OccupancyUnknown, "block-b": model.OccupancyUnknown, "block-c": model.OccupancyUnknown})
	if railway.OccupancyService().TrackPowerAvailable() {
		t.Fatal("ON write was trusted although station still reports OFF")
	}
}
