package dccex

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/station"
)

func TestCapabilitiesAndCommandBounds(t *testing.T) {
	d := NewTCP("unused")
	caps := d.Capabilities()
	if caps.Functions != 69 || caps.MaxFunctionNumber != 68 {
		t.Fatalf("capabilities=%+v", caps)
	}
	if err := d.SetLocoSpeed(context.Background(), 3, 0.5, station.Direction("sideways")); err != station.ErrUnsupported {
		t.Fatalf("invalid direction error=%v", err)
	}
	if err := d.SetLocoFunction(context.Background(), 3, 69, true); err != station.ErrUnsupported {
		t.Fatalf("out-of-range function error=%v", err)
	}
}

func TestThrottleCommand(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	received := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		line, _ := bufio.NewReader(c).ReadString('\n')
		received <- line
	}()
	d := NewTCP(ln.Addr().String())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := d.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.SetLocoSpeed(ctx, 42, 0.5, station.Forward); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-received:
		if strings.TrimSpace(got) != "<t 42 63 1>" {
			t.Fatalf("got %q", got)
		}
	case <-ctx.Done():
		t.Fatal("timeout")
	}
}
