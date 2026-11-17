package api

import (
	"context"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/station/z21"
)

func TestZ21NetworkRecoveryRefreshesRESTAndWebSocketWithoutPhysicalChange(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := newZ21OccupancyServer(t)
	driver := z21.New(fake.conn.LocalAddr().String(), station.DefaultOfflineAfter, time.Millisecond)
	defer driver.Close()
	if err := driver.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	// Keep an unanswered startup retry, as happens when fixture initialization
	// exceeds the driver's polling interval on a loaded CI runner.
	if err := driver.RefreshFeedback(ctx); err != nil {
		t.Fatal(err)
	}
	server, apiClient, token, railway := newZ21OccupancyHTTPFixture(t, ctx, driver)
	startup := fake.replyToScan(t)
	waitForStartupOccupancy(t, railway, map[string]model.OccupancyState{"block-a": model.OccupancyOccupied, "block-b": model.OccupancyFree, "block-c": model.OccupancyOccupied})
	websocket := dialTestWebSocket(t, server.URL, token)
	defer websocket.close()
	readTestSnapshot(t, websocket)
	fake.drop.Store(true)
	if _, err := driver.Status(ctx); err != nil {
		t.Fatal(err)
	}
	unknown := map[string]model.OccupancyState{"block-a": model.OccupancyUnknown, "block-b": model.OccupancyUnknown, "block-c": model.OccupancyUnknown}
	waitForStartupOccupancy(t, railway, unknown)
	blocks, err := apiClient.Blocks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertStartupBlocks(t, blocks, unknown)
	fake.drop.Store(false)
	statusDone := make(chan error, 1)
	go func() { _, err := driver.Status(ctx); statusDone <- err }()
	requests := fake.scanAfter(t, startup[0].from)
	// The train moved from input 80 to 81 during the outage. No spontaneous
	// feedback is sent; only responses to the explicit group queries follow.
	payload := make([]byte, 11)
	if _, err := fake.conn.WriteToUDP(apiZ21LAN(0x0080, payload), requests[0].from); err != nil {
		t.Fatal(err)
	}
	waitForStartupOccupancy(t, railway, map[string]model.OccupancyState{"block-a": model.OccupancyFree, "block-b": model.OccupancyUnknown, "block-c": model.OccupancyUnknown})
	payload[0], payload[1] = 1, 1
	if _, err := fake.conn.WriteToUDP(apiZ21LAN(0x0080, payload), requests[1].from); err != nil {
		t.Fatal(err)
	}
	if err := <-statusDone; err != nil {
		t.Fatal(err)
	}
	want := map[string]model.OccupancyState{"block-a": model.OccupancyFree, "block-b": model.OccupancyOccupied, "block-c": model.OccupancyFree}
	waitForStartupOccupancy(t, railway, want)
	blocks, err = apiClient.Blocks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertStartupBlocks(t, blocks, want)
	websocket.writeJSON(t, map[string]string{"type": "client.snapshot_request"})
	for i := 0; i < 16; i++ {
		var message systemSnapshot
		websocket.readJSON(t, &message)
		if message.Type == "system.snapshot" {
			assertStartupBlocks(t, message.Payload.Blocks, want)
			return
		}
	}
	t.Fatal("missing fresh WebSocket snapshot after network recovery")
}
