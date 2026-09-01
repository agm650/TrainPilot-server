package service

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station/dccex"
	"github.com/agm650/TrainPilot-server/internal/store"
)

func TestRailwayServiceCommandsSimpleTurnoutThroughDCCEX(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	commands := make(chan string, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		scanner := bufio.NewScanner(connection)
		if scanner.Scan() {
			commands <- strings.TrimSpace(scanner.Text())
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	driver := dccex.NewTCP(listener.Addr().String(), 500*time.Millisecond)
	if err := driver.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = driver.Close() })

	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	turnout := model.Turnout{
		ID:   "simple-dccex",
		Name: "Simple DCC-EX",
		Kind: model.TurnoutKindSimple,
		Endpoints: []model.AccessoryEndpoint{
			{ID: "main", LinearAddress: 44},
		},
		Positions: []model.TurnoutPositionDefinition{
			{ID: "straight", Endpoints: map[string]model.AccessoryPosition{"main": model.AccessoryPosition1}},
			{ID: "diverging", Endpoints: map[string]model.AccessoryPosition{"main": model.AccessoryPosition2}},
		},
	}
	if err := db.ImportLayout(ctx, model.LayoutDefinition{Turnouts: []model.Turnout{turnout}}, false); err != nil {
		t.Fatal(err)
	}
	railway := NewRailwayService(db, driver, events.New())
	railway.StartFeedback(ctx)
	if err := railway.SetTurnout(ctx, model.User{Role: model.RoleDispatcher}, turnout.ID, "diverging"); err != nil {
		t.Fatal(err)
	}

	select {
	case command := <-commands:
		if command != "<a 44 1>" {
			t.Fatalf("DCC-EX command=%q want %q", command, "<a 44 1>")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for DCC-EX accessory command")
	}
	waitForTurnoutPosition(t, ctx, db, turnout.ID, "diverging", false)
}
