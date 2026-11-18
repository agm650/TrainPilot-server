package service

import (
	"bufio"
	"context"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/station/dccex"
)

func TestSignalDCCEXStartsWithoutClientsAfterPowerStreamRenewal(t *testing.T) {
	db, _, bus := signalServiceFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := db.SetOccupancyProvider(ctx, model.OccupancyProvider{ID: "dccex", Type: "current-detection", Required: true}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetOccupancySensorMapping(ctx, model.OccupancySensorMapping{ProviderID: "dccex", SensorID: "1", BlockID: "block"}); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	commands := make(chan string, 16)
	var mu sync.Mutex
	connections := []net.Conn{}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			if ctx.Err() != nil {
				mu.Unlock()
				_ = c.Close()
				return
			}
			connections = append(connections, c)
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer c.Close()
				scanner := bufio.NewScanner(c)
				for scanner.Scan() {
					line := strings.TrimSpace(scanner.Text())
					switch {
					case line == "<s>":
						_, _ = io.WriteString(c, "<iDCC-EX V-5.6.6 / MEGA / STANDARD>\n<p0 B>\n<p1 MAIN>\n")
					case line == "<Q>":
						_, _ = io.WriteString(c, "<q 1>\n")
					case strings.HasPrefix(line, "<A "):
						select {
						case commands <- line:
						case <-ctx.Done():
							return
						}
					}
				}
			}()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		mu.Lock()
		for _, c := range connections {
			_ = c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	driver := dccex.NewTCP(listener.Addr().String(), station.DefaultOfflineAfter)
	if err := driver.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = driver.Close() })
	railway := NewRailwayService(db, driver, bus)
	railway.StartFeedback(ctx)
	control := NewControlService(db, driver, bus, clock.Real{}, time.Minute, time.Second, time.Hour)
	control.SetOccupancyProvider(railway.OccupancyService(), "dccex")
	signals := NewSignalService(db, driver, bus, railway.OccupancyService())
	control.SetSignalService(signals)
	control.Start()
	t.Cleanup(control.Close)
	if err := signals.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(signals.Close)
	select {
	case command := <-commands:
		if command != "<A 1 0>" {
			t.Fatal(command)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("no startup stop after power resynchronization")
	}
	state := awaitSignal(t, signals, func(v model.SignalState) bool { return v.CommandedAspect != nil && !v.Pending })
	if *state.CommandedAspect != "S" || state.ReportedAspect != nil || state.ReportQuality != nil {
		t.Fatal("DCC-EX write manufactured feedback", state)
	}
}
