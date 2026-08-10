package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/agm650/TrainPilot-server/internal/client"
	"github.com/spf13/cobra"
)

type commandContext struct {
	server      string
	username    string
	passwordEnv string
	statePath   string

	client  *client.Client
	state   *cliState
	profile *savedProfile
}

func newRootCommand() (*cobra.Command, error) {
	defaultState, err := defaultStatePath()
	if err != nil {
		return nil, err
	}
	app := &commandContext{}
	cmd := &cobra.Command{
		Use:           "dccctl",
		Short:         "Command-line client for the DCC control server",
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return app.initialize(cmd.Context())
		},
	}
	cmd.PersistentFlags().StringVar(&app.server, "server", "http://127.0.0.1:8080", "server URL")
	cmd.PersistentFlags().StringVar(&app.username, "username", "", "username")
	cmd.PersistentFlags().StringVar(&app.passwordEnv, "password-env", "", "environment variable containing password")
	cmd.PersistentFlags().StringVar(&app.statePath, "state-file", defaultState, "persistent session and lease state file")
	cmd.AddCommand(
		newLocomotivesCommand(app),
		newLocomotiveShowCommand(app),
		newLocomotiveAddCommand(app),
		newLocomotiveUpdateCommand(app),
		newLocomotiveDeleteCommand(app),
		newAcquireCommand(app),
		newThrottleCommand(app),
		newFunctionCommand(app),
		newReleaseCommand(app),
		newPowerCommand(app),
		newEmergencyStopCommand(app),
		newExportRollingStockCommand(app),
		newImportRollingStockCommand(app),
		newExportLayoutCommand(app),
		newImportLayoutCommand(app),
	)
	return cmd, nil
}

func (a *commandContext) initialize(ctx context.Context) error {
	if a.username == "" {
		return errors.New("--username is required")
	}
	state, err := loadState(a.statePath)
	if err != nil {
		return fmt.Errorf("load dccctl state: %w", err)
	}
	a.state = state
	a.profile = state.profile(strings.TrimRight(a.server, "/"), a.username)
	a.client = client.New(a.server)
	if err := authenticate(ctx, a.client, a.profile, a.username, a.passwordEnv); err != nil {
		return err
	}
	if err := saveState(a.statePath, a.state); err != nil {
		return fmt.Errorf("save dccctl state: %w", err)
	}
	return nil
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
		var err error
		password, err = readPassword()
		if err != nil {
			return err
		}
	}
	pair, err := c.Login(ctx, username, password, "dccctl")
	if err != nil {
		return err
	}
	profile.setTokens(pair)
	profile.Leases = make(map[string]savedLease)
	return nil
}

func (a *commandContext) savedLease(locomotiveID string) (savedLease, error) {
	lease, ok := a.profile.Leases[locomotiveID]
	if !ok || lease.ID == "" {
		return savedLease{}, fmt.Errorf("no saved lease for locomotive %q; run acquire first", locomotiveID)
	}
	return lease, nil
}

func (a *commandContext) forgetRejectedLease(locomotiveID string, err error) {
	var httpErr *client.HTTPError
	if errors.As(err, &httpErr) && (httpErr.StatusCode == http.StatusNotFound || httpErr.StatusCode == http.StatusConflict) {
		delete(a.profile.Leases, locomotiveID)
		_ = saveState(a.statePath, a.state)
	}
}

func readPassword() (string, error) {
	fmt.Fprint(os.Stderr, "Password: ")
	_ = exec.Command("stty", "-echo").Run()
	defer func() { _ = exec.Command("stty", "echo").Run(); fmt.Fprintln(os.Stderr) }()
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
