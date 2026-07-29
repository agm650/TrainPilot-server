package config

import (
	"encoding/json"
	"errors"
	"os"
	"time"
)

type Config struct {
	HTTP struct {
		Listen  string `json:"listen"`
		TLSCert string `json:"tlsCert,omitempty"`
		TLSKey  string `json:"tlsKey,omitempty"`
	} `json:"http"`
	Admin struct {
		Socket string `json:"socket"`
		Mode   uint32 `json:"mode"`
	} `json:"admin"`
	Database struct {
		Path string `json:"path"`
	} `json:"database"`
	Station struct {
		Driver    string `json:"driver"`
		Address   string `json:"address"`
		Port      int    `json:"port"`
		Transport string `json:"transport"`
	} `json:"station"`
	Security struct {
		AccessTokenTTL      time.Duration `json:"-"`
		RefreshTokenTTL     time.Duration `json:"-"`
		AccessTokenTTLText  string        `json:"accessTokenTTL"`
		RefreshTokenTTLText string        `json:"refreshTokenTTL"`
	} `json:"security"`
	Control struct {
		LeaseTTL          time.Duration `json:"-"`
		StopGrace         time.Duration `json:"-"`
		MonitorPeriod     time.Duration `json:"-"`
		LeaseTTLText      string        `json:"leaseTTL"`
		StopGraceText     string        `json:"stopGrace"`
		MonitorPeriodText string        `json:"monitorPeriod"`
	} `json:"control"`
	TestAPI  bool `json:"testAPI"`
	SeedDemo bool `json:"seedDemo"`
}

func Default() Config {
	var c Config
	c.HTTP.Listen = "127.0.0.1:8080"
	c.Admin.Socket = "/tmp/dccd-admin.sock"
	c.Admin.Mode = 0o660
	c.Database.Path = "./dcc-control.db"
	c.Station.Driver = "simulator"
	c.Security.AccessTokenTTL = 15 * time.Minute
	c.Security.RefreshTokenTTL = 30 * 24 * time.Hour
	c.Control.LeaseTTL = 15 * time.Second
	c.Control.StopGrace = 2 * time.Second
	c.Control.MonitorPeriod = 250 * time.Millisecond
	return c
}

func Load(path string) (Config, error) {
	c := Default()
	if path == "" {
		return c, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	parse := func(text string, target *time.Duration) error {
		if text == "" {
			return nil
		}
		d, err := time.ParseDuration(text)
		if err != nil {
			return err
		}
		*target = d
		return nil
	}
	if err := parse(c.Security.AccessTokenTTLText, &c.Security.AccessTokenTTL); err != nil {
		return c, err
	}
	if err := parse(c.Security.RefreshTokenTTLText, &c.Security.RefreshTokenTTL); err != nil {
		return c, err
	}
	if err := parse(c.Control.LeaseTTLText, &c.Control.LeaseTTL); err != nil {
		return c, err
	}
	if err := parse(c.Control.StopGraceText, &c.Control.StopGrace); err != nil {
		return c, err
	}
	if err := parse(c.Control.MonitorPeriodText, &c.Control.MonitorPeriod); err != nil {
		return c, err
	}
	if c.HTTP.Listen == "" || c.Admin.Socket == "" || c.Database.Path == "" {
		return c, errors.New("http.listen, admin.socket and database.path are required")
	}
	return c, nil
}
