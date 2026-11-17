package dccex

import (
	"bufio"
	"context"
	"net"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/station"
)

func TestPowerRecoveryQueriesSensorsOnNewTCPStreamWithoutReplayingCommands(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	connections := make(chan net.Conn, 2)
	go func() {
		for i := 0; i < 2; i++ {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			connections <- c
		}
	}()
	driver := NewTCP(listener.Addr().String(), station.DefaultOfflineAfter)
	defer driver.Close()
	if err := driver.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	initial := <-connections
	defer initial.Close()
	before := driver.FeedbackState()
	driver.BeginFeedbackPowerSync()
	if err := driver.RefreshFeedback(context.Background()); err != nil {
		t.Fatal(err)
	}
	var recovered net.Conn
	select {
	case recovered = <-connections:
	case <-time.After(time.Second):
		t.Fatal("power recovery did not renew TCP stream")
	}
	defer recovered.Close()
	_ = recovered.SetReadDeadline(time.Now().Add(time.Second))
	line, err := bufio.NewReader(recovered).ReadString('\n')
	if err != nil || line != "<Q>\n" {
		t.Fatalf("sensor refresh command=%q err=%v", line, err)
	}
	after := driver.FeedbackState()
	if after.Generation != before.Generation || after.PowerGeneration <= before.PowerGeneration || !after.Available {
		t.Fatalf("power was treated as network loss: before=%+v after=%+v", before, after)
	}
	if _, err := recovered.Write([]byte("<q 1><Q 2>")); err != nil {
		t.Fatal(err)
	}
	for _, want := range []station.FeedbackInput{{Address: 1, Active: false}, {Address: 2, Active: true}} {
		select {
		case event := <-driver.Feedback():
			if event.Source != "dccex" || event.Address != want.Address || event.Active != want.Active || event.Generation != after.Generation || event.PowerGeneration != after.PowerGeneration {
				t.Fatalf("fresh sensor report=%+v", event)
			}
		case <-time.After(time.Second):
			t.Fatal("sensor response missing")
		}
	}
}
