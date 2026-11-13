package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/config"
	"github.com/agm650/TrainPilot-server/internal/station"
)

func TestLoadServeConfigFeedbackLogging(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"diagnostics":{"logFeedback":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
		want bool
	}{
		{name: "disabled by default"},
		{name: "enabled by flag", args: []string{"--log-feedback"}, want: true},
		{name: "enabled by config", args: []string{"--config", path}, want: true},
		{name: "flag overrides config", args: []string{"--config", path, "--log-feedback=false"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := loadServeConfig(tc.args)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Diagnostics.LogFeedback != tc.want {
				t.Fatalf("logFeedback=%t, want %t", cfg.Diagnostics.LogFeedback, tc.want)
			}
			if cfg.Diagnostics.Enabled {
				t.Fatal("feedback logging enabled the diagnostics listener")
			}
		})
	}
}

func TestBuildStationPassesOfflineAfterToDCCEX(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan net.Conn, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			accepted <- connection
		}
	}()

	cfg := config.Default()
	cfg.Station.Driver = "dccex"
	cfg.Station.Address = host
	cfg.Station.Port = port
	cfg.Station.OfflineAfter = 50 * time.Millisecond
	commandStation, simulator, err := buildStation(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if simulator != nil {
		t.Fatal("DCC-EX build returned a simulator")
	}
	health, ok := commandStation.(station.HealthProvider)
	if !ok {
		t.Fatal("DCC-EX driver does not implement station.HealthProvider")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := commandStation.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = commandStation.Close() })

	var connection net.Conn
	select {
	case connection = <-accepted:
	case <-ctx.Done():
		t.Fatal("timeout waiting for DCC-EX connection")
	}
	if got := health.Health().Connectivity; got != station.Online {
		t.Fatalf("connectivity=%s want online", got)
	}
	_ = listener.Close()
	if tcp, ok := connection.(*net.TCPConn); ok {
		_ = tcp.SetLinger(0)
	}
	_ = connection.Close()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) && health.Health().Connectivity != station.Offline {
		time.Sleep(5 * time.Millisecond)
	}
	if got := health.Health().Connectivity; got != station.Offline {
		t.Fatalf("connectivity=%s want offline with configured 50ms threshold", got)
	}
}
