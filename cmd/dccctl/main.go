package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/agm650/TrainPilot-server/internal/client"
	"github.com/agm650/TrainPilot-server/internal/station"
)

func main() {
	fs := flag.NewFlagSet("dccctl", flag.ExitOnError)
	server := fs.String("server", "http://127.0.0.1:8080", "server URL")
	username := fs.String("username", "", "username")
	passwordEnv := fs.String("password-env", "", "environment variable containing password")
	_ = fs.Parse(os.Args[1:])
	args := fs.Args()
	if len(args) == 0 {
		fatal(errors.New("command required: locomotives | acquire | throttle | export-rolling-stock | import-rolling-stock | export-layout | import-layout"))
	}
	if *username == "" {
		fatal(errors.New("--username is required"))
	}
	password := ""
	if *passwordEnv != "" {
		password = os.Getenv(*passwordEnv)
	} else {
		password = readPassword()
	}
	c := client.New(*server)
	if _, err := c.Login(context.Background(), *username, password, "dccctl"); err != nil {
		fatal(err)
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
	case "acquire":
		if len(args) < 2 {
			fatal(errors.New("acquire requires locomotive ID"))
		}
		lease, err := c.Acquire(context.Background(), args[1])
		if err != nil {
			fatal(err)
		}
		fmt.Println(lease.ID)
	case "throttle":
		if len(args) < 4 {
			fatal(errors.New("throttle requires locomotive ID, lease ID and speed 0..1"))
		}
		speed, err := strconv.ParseFloat(args[3], 64)
		if err != nil {
			fatal(err)
		}
		direction := station.Forward
		if len(args) > 4 {
			direction = station.Direction(args[4])
		}
		if err := c.Throttle(context.Background(), args[1], args[2], speed, direction); err != nil {
			fatal(err)
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
