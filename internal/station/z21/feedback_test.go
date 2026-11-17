package z21

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/station"
)

func TestConnectRequestsBothRBusGroups(t *testing.T) {
	server := newUDPServer(t)
	driver := New(server.address(), station.DefaultOfflineAfter, DefaultAccessoryPulse)
	t.Cleanup(func() { _ = driver.Close() })
	if err := driver.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, want := range [][]byte{
		broadcastFlagsPacket(),
		{0x05, 0x00, 0x81, 0x00, 0x00},
		{0x05, 0x00, 0x81, 0x00, 0x01},
	} {
		if got := server.next(t).data; !bytes.Equal(got, want) {
			t.Fatalf("startup packet=% x want=% x", got, want)
		}
	}
}

func TestRBusSnapshotRetainsAllInputsBeforeFeedbackConsumerStarts(t *testing.T) {
	driver := New("unused", station.DefaultOfflineAfter, DefaultAccessoryPulse)
	for group := byte(0); group < 2; group++ {
		payload := make([]byte, 11)
		payload[0], payload[1], payload[10] = group, 0x01, 0x80
		driver.parse(lan(0x0080, payload...))
	}
	for group := 0; group < 2; group++ {
		select {
		case event := <-driver.Feedback():
			if event.Source != "z21-rbus" || event.Kind != "occupancy" || len(event.Inputs) != 80 || event.Generation == 0 {
				t.Fatalf("invalid group frame: %+v", event)
			}
			for i, input := range event.Inputs {
				address := group*80 + i + 1
				wantActive := address == 1 || address == 80 || address == 81 || address == 160
				if input.Address != address || input.Active != wantActive {
					t.Fatalf("input %d: got=%+v want active=%v", address, input, wantActive)
				}
			}
		default:
			t.Fatalf("startup snapshot lost group %d", group)
		}
	}
	if len(driver.feedback) != 0 {
		t.Fatal("frame was also emitted as individual observations")
	}
}

func TestRBusScanRetriesOnlyMissingGroups(t *testing.T) {
	server := newUDPServer(t)
	driver := newConnectedTestDriver(t, server, DefaultAccessoryPulse)
	if err := driver.requestMissingRBusData(context.Background()); err != nil {
		t.Fatal(err)
	}
	server.next(t)
	server.next(t)

	payload := make([]byte, 11)
	driver.parse(lan(0x0080, payload...)) // Group 0 replied; group 1 was lost.
	if err := driver.requestMissingRBusData(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := server.next(t).data, []byte{0x05, 0x00, 0x81, 0x00, 0x01}; !bytes.Equal(got, want) {
		t.Fatalf("retry packet=% x want=% x", got, want)
	}
	payload[0] = 1
	driver.parse(lan(0x0080, payload...))
	if err := driver.requestMissingRBusData(context.Background()); err != nil {
		t.Fatal(err)
	}
	if packet, ok := server.nextWithin(20 * time.Millisecond); ok {
		t.Fatalf("completed snapshot was requested again: % x", packet.data)
	}
}

func TestMalformedRBusReplyDoesNotCompleteScan(t *testing.T) {
	driver := New("unused", station.DefaultOfflineAfter, DefaultAccessoryPulse)
	for _, payload := range [][]byte{make([]byte, 10), make([]byte, 12), append([]byte{2}, make([]byte, 10)...)} {
		driver.parse(lan(0x0080, payload...))
	}
	if len(driver.feedback) != 0 || driver.rbusReceived != [2]bool{} {
		t.Fatalf("malformed replies produced feedback or completed a group")
	}
}

func TestFeedbackBackpressurePreservesObservationsAndCloseUnblocks(t *testing.T) {
	driver := New("unused", station.DefaultOfflineAfter, DefaultAccessoryPulse)
	t.Cleanup(func() { _ = driver.Close() })
	capacity := cap(driver.feedback)
	for address := 1; address <= capacity; address++ {
		driver.publish(station.FeedbackEvent{Address: address})
	}
	done := make(chan struct{})
	go func() {
		driver.publish(station.FeedbackEvent{Address: 1, Active: true})
		close(done)
	}()
	for i := 0; i <= capacity; i++ {
		select {
		case event := <-driver.Feedback():
			if i == capacity && (event.Address != 1 || !event.Active) {
				t.Fatalf("last observation=%+v", event)
			}
		case <-time.After(time.Second):
			t.Fatal("observation lost when feedback queue was full")
		}
	}
	<-done
	for address := 1; address <= capacity; address++ {
		driver.publish(station.FeedbackEvent{Address: address})
	}
	done = make(chan struct{})
	go func() {
		driver.publish(station.FeedbackEvent{Address: 1, Active: true})
		close(done)
	}()
	_ = driver.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("close did not unblock feedback delivery")
	}
}
