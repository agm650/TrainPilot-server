package api

import (
	"context"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/station/z21"
)

func TestZ21SameFrameTransferNeverPublishesFreeToWebSocket(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := newZ21OccupancyServer(t)
	driver := z21.New(fake.conn.LocalAddr().String(), station.DefaultOfflineAfter, time.Millisecond)
	defer driver.Close()
	if err := driver.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	// Inputs 79 and 80 belong to the same block and to the same wire frame.
	server, apiClient, token, railway := newZ21OccupancyHTTPFixture(t, ctx, driver, model.FeedbackMapping{Provider: "z21-rbus", Address: 79, BlockID: "block-a"})
	var destination z21OccupancyRequest
	for group := byte(0); group < 2; group++ {
		select {
		case request := <-fake.requests:
			payload := make([]byte, 11)
			payload[0] = request.group
			if request.group == 0 {
				payload[10] = 0x40
				destination = request
			}
			if _, err := fake.conn.WriteToUDP(apiZ21LAN(0x0080, payload), request.from); err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("initial scan request missing")
		}
	}
	want := map[string]model.OccupancyState{"block-a": model.OccupancyOccupied, "block-b": model.OccupancyFree, "block-c": model.OccupancyFree}
	waitForStartupOccupancy(t, railway, want)
	ws := dialTestWebSocket(t, server.URL, token)
	defer ws.close()
	assertStartupBlocks(t, readTestSnapshot(t, ws).Payload.Blocks, want)
	payload := make([]byte, 11)
	payload[10] = 0x80
	if _, err := fake.conn.WriteToUDP(apiZ21LAN(0x0080, payload), destination.from); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	processed := false
	for time.Now().Before(deadline) {
		sources, err := railway.OccupancyService().ProviderStates(ctx, "block-a")
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range sources {
			if source.ProviderID == "z21-rbus" && source.SensorID == "80" && source.Sequence >= 2 {
				processed = true
			}
		}
		if processed {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !processed {
		t.Fatal("transfer frame was not processed")
	}
	blocks, err := apiClient.Blocks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertStartupBlocks(t, blocks, want)
	ws.writeJSON(t, map[string]string{"type": "client.snapshot_request"})
	// Any false free/occupied transition would be queued before this snapshot.
	assertStartupBlocks(t, readTestSnapshot(t, ws).Payload.Blocks, want)
}
