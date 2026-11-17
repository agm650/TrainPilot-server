package z21

import (
	"bytes"
	"context"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/station"
)

func TestRBusRecoveryRenewsEndpointAndRejectsOldGeneration(t *testing.T) {
	server := newUDPServer(t)
	driver := newConnectedTestDriver(t, server, DefaultAccessoryPulse)
	driver.started = true
	for cycle := 0; cycle < 4; cycle++ {
		payload := make([]byte, 11)
		payload[1] = 1
		driver.parse(lan(lanRMBusDataChanged, payload...))
		oldConnection, oldState := driver.conn, driver.FeedbackState()
		oldAddress := oldConnection.LocalAddr().String()
		driver.loseConnection(oldConnection)
		state := driver.FeedbackState()
		if state.Generation <= oldState.Generation || state.Available || driver.Health().Connectivity != station.Degraded {
			t.Fatalf("cycle %d: loss state=%+v health=%+v", cycle, state, driver.Health())
		}
		if driver.rbusReceived != [2]bool{} {
			t.Fatal("old groups survived invalidation")
		}
		for len(driver.feedback) > 0 {
			<-driver.feedback
		}
		driver.parseConnection(oldConnection, oldState.Generation, oldState.PowerGeneration, lan(lanRMBusDataChanged, payload...))
		if len(driver.feedback) != 0 || driver.FeedbackState().Available {
			t.Fatal("late old-generation reply restored feedback")
		}
		if err := driver.ensureConnection(context.Background()); err != nil {
			t.Fatal(err)
		}
		if driver.conn.LocalAddr().String() == oldAddress {
			t.Fatal("recovery reused old UDP endpoint")
		}
		for _, want := range [][]byte{broadcastFlagsPacket(), lan(lanRMBusGetData, 0), lan(lanRMBusGetData, 1)} {
			if got := server.next(t).data; !bytes.Equal(got, want) {
				t.Fatalf("recovery packet=% x want=% x", got, want)
			}
		}
		driver.parse(lan(lanRMBusDataChanged, payload...))
		if !driver.FeedbackState().Available {
			t.Fatal("new observation was not available")
		}
		if err := driver.requestMissingRBusData(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got, want := server.next(t).data, lan(lanRMBusGetData, 1); !bytes.Equal(got, want) {
			t.Fatalf("partial recovery retry=% x want=% x", got, want)
		}
		for len(driver.feedback) > 0 {
			<-driver.feedback
		}
	}
}

func TestStatusTimeoutInvalidatesRBusBeforeOffline(t *testing.T) {
	server := newUDPServer(t)
	driver := newConnectedTestDriver(t, server, DefaultAccessoryPulse)
	driver.started = true
	before := driver.FeedbackState()
	status, err := driver.Status(context.Background()) // Central receives probes but does not answer.
	if err != nil {
		t.Fatal(err)
	}
	after := driver.FeedbackState()
	if status.Connectivity != station.Degraded || after.Available || after.Generation <= before.Generation {
		t.Fatalf("timeout: status=%+v state=%+v", status, after)
	}
}
