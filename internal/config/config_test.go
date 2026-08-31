package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefault(t *testing.T) {
	cfg := Default()
	if cfg.HTTP.Listen != "127.0.0.1:8080" {
		t.Fatalf("HTTP listen=%q", cfg.HTTP.Listen)
	}
	if cfg.Admin.Socket != "/tmp/dccd-admin.sock" || cfg.Admin.Mode != 0o660 {
		t.Fatalf("admin defaults=%q %#o", cfg.Admin.Socket, cfg.Admin.Mode)
	}
	if cfg.Database.Path != "./dcc-control.db" || cfg.Station.Driver != "simulator" {
		t.Fatalf("database/station defaults=%q/%q", cfg.Database.Path, cfg.Station.Driver)
	}
	if cfg.Station.OfflineAfter != 10*time.Second {
		t.Fatalf("station offlineAfter=%v", cfg.Station.OfflineAfter)
	}
	if cfg.Station.AccessoryPulse != 100*time.Millisecond {
		t.Fatalf("station accessoryPulse=%v", cfg.Station.AccessoryPulse)
	}
	if cfg.Security.AccessTokenTTL != 15*time.Minute || cfg.Security.RefreshTokenTTL != 30*24*time.Hour {
		t.Fatalf("security defaults=%v/%v", cfg.Security.AccessTokenTTL, cfg.Security.RefreshTokenTTL)
	}
	if cfg.Control.LeaseTTL != 10*time.Minute || cfg.Control.StopGrace != 2*time.Second || cfg.Control.MonitorPeriod != 250*time.Millisecond {
		t.Fatalf("control defaults=%v/%v/%v", cfg.Control.LeaseTTL, cfg.Control.StopGrace, cfg.Control.MonitorPeriod)
	}
}

func TestLoadEmptyPathReturnsDefaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP.Listen != Default().HTTP.Listen {
		t.Fatalf("listen=%q", cfg.HTTP.Listen)
	}
	if cfg.Station.OfflineAfter != 10*time.Second {
		t.Fatalf("station offlineAfter=%v", cfg.Station.OfflineAfter)
	}
	cfg, err = Load(writeConfig(t, `{}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Station.AccessoryPulse != 100*time.Millisecond {
		t.Fatalf("absent station accessoryPulse=%v", cfg.Station.AccessoryPulse)
	}
}

func TestLoadParsesStationOfflineAfter(t *testing.T) {
	for _, tc := range []struct {
		text string
		want time.Duration
	}{
		{"500ms", 500 * time.Millisecond},
		{"30s", 30 * time.Second},
		{"1m", time.Minute},
	} {
		t.Run(tc.text, func(t *testing.T) {
			cfg, err := Load(writeConfig(t, `{"station":{"offlineAfter":"`+tc.text+`"}}`))
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Station.OfflineAfter != tc.want {
				t.Fatalf("station offlineAfter=%v want %v", cfg.Station.OfflineAfter, tc.want)
			}
		})
	}
}

func TestLoadParsesStationAccessoryPulse(t *testing.T) {
	for _, tc := range []struct {
		text string
		want time.Duration
	}{
		{"25ms", 25 * time.Millisecond},
		{"250ms", 250 * time.Millisecond},
		{"1s", time.Second},
	} {
		t.Run(tc.text, func(t *testing.T) {
			cfg, err := Load(writeConfig(t, `{"station":{"accessoryPulse":"`+tc.text+`"}}`))
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Station.AccessoryPulse != tc.want {
				t.Fatalf("station accessoryPulse=%v want %v", cfg.Station.AccessoryPulse, tc.want)
			}
		})
	}
}

func TestLoadParsesDurationsAndOverrides(t *testing.T) {
	path := writeConfig(t, `{
		"http":{"listen":"0.0.0.0:9090"},
		"admin":{"socket":"/tmp/trainpilot.sock","mode":384},
		"database":{"path":"/tmp/trainpilot.db"},
		"station":{"driver":"z21","address":"192.0.2.10","port":21105,"transport":"udp"},
		"security":{"accessTokenTTL":"30m","refreshTokenTTL":"48h"},
		"control":{"leaseTTL":"20s","stopGrace":"3s","monitorPeriod":"500ms"},
		"testAPI":true,
		"seedDemo":true
	}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP.Listen != "0.0.0.0:9090" || cfg.Admin.Socket != "/tmp/trainpilot.sock" || cfg.Database.Path != "/tmp/trainpilot.db" {
		t.Fatalf("unexpected paths: %+v", cfg)
	}
	if cfg.Security.AccessTokenTTL != 30*time.Minute || cfg.Security.RefreshTokenTTL != 48*time.Hour {
		t.Fatalf("security durations=%v/%v", cfg.Security.AccessTokenTTL, cfg.Security.RefreshTokenTTL)
	}
	if cfg.Control.LeaseTTL != 20*time.Second || cfg.Control.StopGrace != 3*time.Second || cfg.Control.MonitorPeriod != 500*time.Millisecond {
		t.Fatalf("control durations=%v/%v/%v", cfg.Control.LeaseTTL, cfg.Control.StopGrace, cfg.Control.MonitorPeriod)
	}
	if !cfg.TestAPI || !cfg.SeedDemo {
		t.Fatal("boolean options were not loaded")
	}
}

func TestLoadErrors(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		if _, err := Load(filepath.Join(t.TempDir(), "missing.json")); err == nil {
			t.Fatal("expected read error")
		}
	})
	t.Run("invalid json", func(t *testing.T) {
		if _, err := Load(writeConfig(t, `{`)); err == nil {
			t.Fatal("expected JSON error")
		}
	})

	invalidDurations := []struct {
		name string
		json string
	}{
		{"access token", `{"security":{"accessTokenTTL":"bad"}}`},
		{"refresh token", `{"security":{"refreshTokenTTL":"bad"}}`},
		{"lease", `{"control":{"leaseTTL":"bad"}}`},
		{"stop grace", `{"control":{"stopGrace":"bad"}}`},
		{"monitor", `{"control":{"monitorPeriod":"bad"}}`},
	}
	for _, tc := range invalidDurations {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, tc.json)); err == nil {
				t.Fatal("expected duration error")
			}
		})
	}

	for _, value := range []string{"bad", "0s", "-1s"} {
		t.Run("station offline after "+value, func(t *testing.T) {
			_, err := Load(writeConfig(t, `{"station":{"offlineAfter":"`+value+`"}}`))
			if err == nil {
				t.Fatal("expected duration error")
			}
			if !strings.Contains(err.Error(), "station.offlineAfter") {
				t.Fatalf("error=%q does not identify station.offlineAfter", err)
			}
		})
	}

	for _, value := range []string{"bad", "0s", "-1ms"} {
		t.Run("station accessory pulse "+value, func(t *testing.T) {
			_, err := Load(writeConfig(t, `{"station":{"accessoryPulse":"`+value+`"}}`))
			if err == nil {
				t.Fatal("expected duration error")
			}
			if !strings.Contains(err.Error(), "station.accessoryPulse") {
				t.Fatalf("error=%q does not identify station.accessoryPulse", err)
			}
		})
	}

	for _, tc := range []struct {
		name string
		json string
	}{
		{"http listen", `{"http":{"listen":""}}`},
		{"admin socket", `{"admin":{"socket":""}}`},
		{"database path", `{"database":{"path":""}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, tc.json)); err == nil {
				t.Fatal("expected required field error")
			}
		})
	}
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
