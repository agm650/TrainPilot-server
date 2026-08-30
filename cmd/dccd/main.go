package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	adminapi "github.com/agm650/TrainPilot-server/internal/admin"
	httpapi "github.com/agm650/TrainPilot-server/internal/api"
	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/config"
	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/service"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/station/dccex"
	"github.com/agm650/TrainPilot-server/internal/station/simulator"
	"github.com/agm650/TrainPilot-server/internal/station/z21"
	"github.com/agm650/TrainPilot-server/internal/store"
	"github.com/agm650/TrainPilot-server/internal/transfer"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve(os.Args[2:])
	case "user":
		err = userCommand(os.Args[2:])
	default:
		usage()
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		log.Fatal(err)
	}
}
func usage() {
	fmt.Fprintln(os.Stderr, "usage: dccd serve [--config file] | dccd user <bootstrap|add|list|enable|disable|role|passwd> [options]")
}
func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "JSON configuration file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	db, err := store.Open(cfg.Database.Path)
	if err != nil {
		return err
	}
	defer db.Close()
	if cfg.SeedDemo {
		if err := db.SeedDemo(context.Background()); err != nil {
			return err
		}
	}
	clk := clock.Real{}
	bus := events.New()
	userSvc := service.NewUserService(db, clk)
	authSvc := service.NewAuthService(db, userSvc, clk, cfg.Security.AccessTokenTTL, cfg.Security.RefreshTokenTTL)
	st, sim, err := buildStation(cfg)
	if err != nil {
		return err
	}
	if err := st.Connect(context.Background()); err != nil {
		return err
	}
	defer st.Close()
	railway := service.NewRailwayService(db, st, bus)
	control := service.NewControlService(db, st, bus, clk, cfg.Control.LeaseTTL, cfg.Control.StopGrace, cfg.Control.MonitorPeriod)
	routes := service.NewRouteService(db, railway, bus)
	transferSvc := transfer.New(db, bus, clk)
	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()
	railway.StartFeedback(runCtx)
	control.Start()
	defer control.Close()
	api := httpapi.New(authSvc, control, railway, routes, transferSvc, db, bus, st, sim, cfg.TestAPI)
	httpServer := &http.Server{Addr: cfg.HTTP.Listen, Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second}
	adminServer := adminapi.NewServer(cfg.Admin.Socket, os.FileMode(cfg.Admin.Mode), userSvc)
	if err := adminServer.Start(); err != nil {
		return err
	}
	defer adminServer.Close(context.Background())
	errCh := make(chan error, 1)
	go func() {
		log.Printf("public API listening on %s", cfg.HTTP.Listen)
		if (cfg.HTTP.TLSCert == "") != (cfg.HTTP.TLSKey == "") {
			errCh <- errors.New("http.tlsCert and http.tlsKey must be configured together")
			return
		}
		if cfg.HTTP.TLSCert != "" {
			errCh <- httpServer.ListenAndServeTLS(cfg.HTTP.TLSCert, cfg.HTTP.TLSKey)
			return
		}
		errCh <- httpServer.ListenAndServe()
	}()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case s := <-sig:
		log.Printf("received %s", s)
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpServer.Shutdown(ctx)
}
func buildStation(cfg config.Config) (station.CommandStation, *simulator.Simulator, error) {
	switch cfg.Station.Driver {
	case "simulator":
		s := simulator.New()
		return s, s, nil
	case "dccex":
		addr := cfg.Station.Address
		if cfg.Station.Port > 0 {
			addr = fmt.Sprintf("%s:%d", addr, cfg.Station.Port)
		}
		return dccex.NewTCP(addr, cfg.Station.OfflineAfter), nil, nil
	case "z21":
		addr := cfg.Station.Address
		port := cfg.Station.Port
		if port == 0 {
			port = 21105
		}
		return z21.New(fmt.Sprintf("%s:%d", addr, port), cfg.Station.OfflineAfter), nil, nil
	default:
		return nil, nil, fmt.Errorf("unknown station driver %q", cfg.Station.Driver)
	}
}
func userCommand(args []string) error {
	if len(args) < 1 {
		return errors.New("missing user subcommand")
	}
	sub := args[0]
	fs := flag.NewFlagSet("user "+sub, flag.ContinueOnError)
	socket := fs.String("socket", "/tmp/dccd-admin.sock", "administration socket")
	username := fs.String("username", "", "username")
	display := fs.String("display-name", "", "display name")
	roleText := fs.String("role", "driver", "role")
	mustChange := fs.Bool("must-change", false, "force password change")
	passwordStdin := fs.Bool("password-stdin", false, "read one password line from stdin without confirmation")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	client := adminapi.NewClient(*socket)
	ctx := context.Background()
	switch sub {
	case "bootstrap", "add":
		if *username == "" {
			return errors.New("--username is required")
		}
		var password string
		var err error
		if *passwordStdin {
			password, err = readPasswordLine()
		} else {
			password, err = readPassword("Password: ")
			if err == nil {
				var confirm string
				confirm, err = readPassword("Confirm password: ")
				if err == nil && password != confirm {
					err = errors.New("passwords do not match")
				}
			}
		}
		if err != nil {
			return err
		}
		u, err := client.CreateUser(ctx, *username, *display, password, model.Role(*roleText), *mustChange, sub == "bootstrap")
		if err != nil {
			return err
		}
		fmt.Printf("created %s (%s)\n", u.Username, u.Role)
		return nil
	case "list":
		users, err := client.ListUsers(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("%-20s %-15s %-8s %s\n", "USERNAME", "ROLE", "ENABLED", "DISPLAY NAME")
		for _, u := range users {
			fmt.Printf("%-20s %-15s %-8v %s\n", u.Username, u.Role, u.Enabled, u.DisplayName)
		}
		return nil
	case "enable", "disable":
		if *username == "" {
			return errors.New("--username is required")
		}
		return client.SetEnabled(ctx, *username, sub == "enable")
	case "role":
		if *username == "" {
			return errors.New("--username is required")
		}
		return client.SetRole(ctx, *username, model.Role(*roleText))
	case "passwd":
		if *username == "" {
			return errors.New("--username is required")
		}
		var password string
		var err error
		if *passwordStdin {
			password, err = readPasswordLine()
		} else {
			password, err = readPassword("New password: ")
		}
		if err != nil {
			return err
		}
		return client.SetPassword(ctx, *username, password, *mustChange)
	default:
		return fmt.Errorf("unknown user subcommand %q", sub)
	}
}
func readPasswordLine() (string, error) {
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimSpace(line), err
}

func readPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	_ = exec.Command("stty", "-echo").Run()
	defer func() { _ = exec.Command("stty", "echo").Run(); fmt.Fprintln(os.Stderr) }()
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimSpace(line), err
}
