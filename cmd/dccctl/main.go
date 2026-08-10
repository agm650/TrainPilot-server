package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/agm650/TrainPilot-server/internal/client"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station"
)

func main() {
	defaultState, err := defaultStatePath()
	if err != nil {
		fatal(err)
	}
	fs := flag.NewFlagSet("dccctl", flag.ExitOnError)
	server := fs.String("server", "http://127.0.0.1:8080", "server URL")
	username := fs.String("username", "", "username")
	passwordEnv := fs.String("password-env", "", "environment variable containing password")
	statePath := fs.String("state-file", defaultState, "persistent session and lease state file")
	_ = fs.Parse(os.Args[1:])
	args := fs.Args()
	if len(args) == 0 {
		fatal(errors.New("command required: locomotives | locomotive-show | locomotive-add | locomotive-update | locomotive-delete | acquire | throttle | release | export-rolling-stock | import-rolling-stock | export-layout | import-layout"))
	}
	if *username == "" {
		fatal(errors.New("--username is required"))
	}
	state, err := loadState(*statePath)
	if err != nil {
		fatal(fmt.Errorf("load dccctl state: %w", err))
	}
	profile := state.profile(strings.TrimRight(*server, "/"), *username)
	c := client.New(*server)
	if err := authenticate(context.Background(), c, profile, *username, *passwordEnv); err != nil {
		fatal(err)
	}
	if err := saveState(*statePath, state); err != nil {
		fatal(fmt.Errorf("save dccctl state: %w", err))
	}
	switch args[0] {
	case "locomotives":
		items, err := c.Locomotives(context.Background())
		if err != nil {
			fatal(err)
		}
		for _, l := range items {
			fmt.Printf("%s\t%d\t%s\n", l.ID, l.DCCAddress, l.Name)
		}
	case "locomotive-show":
		if len(args) != 2 {
			fatal(errors.New("locomotive-show requires locomotive ID"))
		}
		l, err := c.Locomotive(context.Background(), args[1])
		if err != nil {
			fatal(err)
		}
		printLocomotive(l)
	case "locomotive-add":
		input := locomotiveArgs(args, 1)
		l, err := c.CreateLocomotive(context.Background(), input)
		if err != nil {
			fatal(err)
		}
		printLocomotive(l)
	case "locomotive-update":
		if len(args) < 4 {
			fatal(errors.New("locomotive-update requires ID, name and DCC address"))
		}
		input := locomotiveArgs(args, 2)
		l, err := c.UpdateLocomotive(context.Background(), args[1], input)
		if err != nil {
			fatal(err)
		}
		printLocomotive(l)
	case "locomotive-delete":
		if len(args) != 2 {
			fatal(errors.New("locomotive-delete requires locomotive ID"))
		}
		if err := c.DeleteLocomotive(context.Background(), args[1]); err != nil {
			fatal(err)
		}
	case "acquire":
		if len(args) < 2 {
			fatal(errors.New("acquire requires locomotive ID"))
		}
		lease, err := c.Acquire(context.Background(), args[1])
		if err != nil {
			fatal(err)
		}
		profile.setLease(lease)
		if err := saveState(*statePath, state); err != nil {
			fatal(fmt.Errorf("save lease: %w", err))
		}
		fmt.Println(lease.ID)
	case "throttle":
		if len(args) < 3 {
			fatal(errors.New("throttle requires locomotive ID and speed 0..1"))
		}
		lease, ok := profile.Leases[args[1]]
		if !ok || lease.ID == "" {
			fatal(fmt.Errorf("no saved lease for locomotive %q; run acquire first", args[1]))
		}
		speed, err := strconv.ParseFloat(args[2], 64)
		if err != nil {
			fatal(err)
		}
		direction := station.Forward
		if len(args) > 3 {
			direction = station.Direction(args[3])
		}
		if err := c.Throttle(context.Background(), args[1], lease.ID, speed, direction); err != nil {
			forgetRejectedLease(*statePath, state, profile, args[1], err)
			fatal(err)
		}
	case "release":
		if len(args) != 2 {
			fatal(errors.New("release requires locomotive ID"))
		}
		lease, ok := profile.Leases[args[1]]
		if !ok || lease.ID == "" {
			fatal(fmt.Errorf("no saved lease for locomotive %q", args[1]))
		}
		if err := c.Release(context.Background(), lease.ID); err != nil {
			forgetRejectedLease(*statePath, state, profile, args[1], err)
			fatal(err)
		}
		delete(profile.Leases, args[1])
		if err := saveState(*statePath, state); err != nil {
			fatal(fmt.Errorf("save lease state: %w", err))
		}
	case "export-rolling-stock":
		if len(args) != 2 {
			fatal(errors.New("export-rolling-stock requires an output file"))
		}
		data, err := c.ExportRollingStock(context.Background())
		if err != nil {
			fatal(err)
		}
		writeFile(args[1], data)
	case "import-rolling-stock":
		path, replace := importArgs(args)
		data, err := os.ReadFile(path)
		if err != nil {
			fatal(err)
		}
		if err := c.ImportRollingStock(context.Background(), data, replace); err != nil {
			fatal(err)
		}
	case "export-layout":
		if len(args) != 2 {
			fatal(errors.New("export-layout requires an output file"))
		}
		data, err := c.ExportLayout(context.Background())
		if err != nil {
			fatal(err)
		}
		writeFile(args[1], data)
	case "import-layout":
		path, replace := importArgs(args)
		data, err := os.ReadFile(path)
		if err != nil {
			fatal(err)
		}
		if err := c.ImportLayout(context.Background(), data, replace); err != nil {
			fatal(err)
		}
	default:
		fatal(fmt.Errorf("unknown command %q", args[0]))
	}
}

func authenticate(ctx context.Context, c *client.Client, profile *savedProfile, username, passwordEnv string) error {
	now := time.Now()
	if profile.AccessToken != "" && now.Before(profile.AccessExpiresAt) {
		c.AccessToken = profile.AccessToken
		c.RefreshToken = profile.RefreshToken
		return nil
	}
	if profile.RefreshToken != "" && now.Before(profile.RefreshExpiresAt) {
		pair, err := c.Refresh(ctx, profile.RefreshToken)
		if err == nil {
			profile.setTokens(pair)
			return nil
		}
	}
	password := ""
	if passwordEnv != "" {
		password = os.Getenv(passwordEnv)
	} else {
		password = readPassword()
	}
	pair, err := c.Login(ctx, username, password, "dccctl")
	if err != nil {
		return err
	}
	profile.setTokens(pair)
	// A new session cannot own leases saved for an older session.
	profile.Leases = make(map[string]savedLease)
	return nil
}

func forgetRejectedLease(path string, state *cliState, profile *savedProfile, locomotiveID string, err error) {
	var httpErr *client.HTTPError
	if errors.As(err, &httpErr) && (httpErr.StatusCode == http.StatusNotFound || httpErr.StatusCode == http.StatusConflict) {
		delete(profile.Leases, locomotiveID)
		_ = saveState(path, state)
	}
}
func readPassword() string {
	fmt.Fprint(os.Stderr, "Password: ")
	_ = exec.Command("stty", "-echo").Run()
	defer func() { _ = exec.Command("stty", "echo").Run(); fmt.Fprintln(os.Stderr) }()
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		fatal(err)
	}
	return strings.TrimSpace(line)
}
func fatal(err error) { fmt.Fprintln(os.Stderr, "error:", err); os.Exit(1) }

func importArgs(args []string) (string, bool) {
	if len(args) < 2 || len(args) > 3 {
		fatal(errors.New(args[0] + " requires an input file and optional --replace"))
	}
	replace := false
	if len(args) == 3 {
		if args[2] != "--replace" {
			fatal(errors.New("only --replace is accepted after the input file"))
		}
		replace = true
	}
	return args[1], replace
}

func writeFile(path string, data []byte) {
	if err := os.WriteFile(path, data, 0o600); err != nil {
		fatal(err)
	}
	fmt.Printf("wrote %s (%d bytes)\n", path, len(data))
}

func locomotiveArgs(args []string, offset int) model.LocomotiveInput {
	if len(args) < offset+2 || len(args) > offset+6 {
		fatal(errors.New("locomotive arguments: <name> <dcc-address> [short|long] [14|28|128] [manufacturer] [model]"))
	}
	address, err := strconv.Atoi(args[offset+1])
	if err != nil {
		fatal(fmt.Errorf("invalid DCC address: %w", err))
	}
	kind := "short"
	if address >= 128 {
		kind = "long"
	}
	if len(args) > offset+2 {
		kind = args[offset+2]
	}
	steps := 128
	if len(args) > offset+3 {
		steps, err = strconv.Atoi(args[offset+3])
		if err != nil {
			fatal(fmt.Errorf("invalid speed steps: %w", err))
		}
	}
	manufacturer := ""
	if len(args) > offset+4 {
		manufacturer = args[offset+4]
	}
	modelName := ""
	if len(args) > offset+5 {
		modelName = args[offset+5]
	}
	return model.LocomotiveInput{Name: args[offset], DCCAddress: address, AddressKind: kind, SpeedSteps: steps, Manufacturer: manufacturer, Model: modelName}
}

func printLocomotive(l model.Locomotive) {
	fmt.Printf("%s\t%d\t%s\t%s\t%d\t%s\t%s\n", l.ID, l.DCCAddress, l.AddressKind, l.Name, l.SpeedSteps, l.Manufacturer, l.Model)
}
