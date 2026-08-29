package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/agm650/TrainPilot-server/internal/client"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station"
)

type result struct {
	name string
	err  error
}

type configuration struct {
	server              string
	user1               string
	pass1               string
	user2               string
	pass2               string
	admin               string
	adminPass           string
	allowActiveCommands bool
	allowConfigChanges  bool
}

func main() {
	server := flag.String("server", "http://127.0.0.1:8080", "server URL")
	user1 := flag.String("user1", "alice", "first driver")
	pass1 := flag.String("pass1", "correct-horse-1", "first password")
	user2 := flag.String("user2", "bob", "second driver")
	pass2 := flag.String("pass2", "correct-horse-2", "second password")
	admin := flag.String("admin", "", "administrator used for optional configuration checks")
	adminPass := flag.String("admin-pass", "", "administrator password")
	allowActive := flag.Bool("allow-active-commands", false, "explicitly allow power, lease, throttle and function commands")
	allowConfig := flag.Bool("allow-configuration-mutations", false, "explicitly allow temporary CRUD and archive import checks")
	listEndpoints := flag.Bool("list-endpoints", false, "list the public endpoint inventory and its conformance class")
	flag.Parse()
	if *listEndpoints {
		writeEndpointInventory(os.Stdout)
		return
	}

	failed := run(context.Background(), configuration{
		server:              *server,
		user1:               *user1,
		pass1:               *pass1,
		user2:               *user2,
		pass2:               *pass2,
		admin:               *admin,
		adminPass:           *adminPass,
		allowActiveCommands: *allowActive,
		allowConfigChanges:  *allowConfig,
	}, os.Stdout)
	if failed > 0 {
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg configuration, output io.Writer) int {
	c1 := client.New(cfg.server)
	c2 := client.New(cfg.server)
	results := make([]result, 0)
	add := func(name string, err error) { results = append(results, result{name: name, err: err}) }

	info, err := c1.SystemInfo(ctx)
	add("system information exposes compatible API versions", requireVersions(info, err))
	status, err := c1.Do(ctx, http.MethodGet, "/healthz", nil, nil)
	if err == nil && status != http.StatusOK {
		err = fmt.Errorf("expected HTTP 200, got %d", status)
	}
	add("health endpoint is reachable", err)
	_, err = c1.Me(ctx)
	add("protected endpoint rejects missing token", expectHTTPError(err, http.StatusUnauthorized, "authentication"))

	pair1, login1Err := c1.Login(ctx, cfg.user1, cfg.pass1, "conformance-1")
	add("valid user can authenticate", login1Err)
	_, login2Err := c2.Login(ctx, cfg.user2, cfg.pass2, "conformance-2")
	add("second user can authenticate", login2Err)
	if login1Err == nil {
		oldAccess := c1.AccessToken
		_, refreshErr := c1.Refresh(ctx, pair1.RefreshToken)
		add("refresh rotates the token pair", refreshErr)
		if refreshErr == nil {
			oldClient := client.New(cfg.server)
			oldClient.AccessToken = oldAccess
			_, oldTokenErr := oldClient.Me(ctx)
			add("rotated access token is immediately rejected", expectHTTPError(oldTokenErr, http.StatusUnauthorized, "authentication", "invalid_token"))
		}
	}
	_, err = c1.Me(ctx)
	add("authenticated client reads its identity", err)

	locomotives, err := c1.Locomotives(ctx)
	add("authenticated client lists locomotives", err)
	_, err = c1.Blocks(ctx)
	add("authenticated client lists blocks", err)
	_, err = c1.Turnouts(ctx)
	add("authenticated client lists turnouts", err)
	_, err = c1.Routes(ctx)
	add("authenticated client lists routes", err)
	_, err = c1.StationStatus(ctx)
	add("authenticated client reads station status", err)
	_, err = c1.TrackPowerStatus(ctx)
	add("authenticated client reads track-power status", err)
	_, err = c1.Locomotive(ctx, "conformance-unknown-locomotive")
	add("unknown locomotive returns a structured not-found error", expectHTTPError(err, http.StatusNotFound, "not_found", "locomotive_lookup_failed"))

	var adminClient *client.Client
	if cfg.admin != "" && cfg.adminPass != "" {
		adminClient = client.New(cfg.server)
		_, err = adminClient.Login(ctx, cfg.admin, cfg.adminPass, "conformance-admin")
		add("administrator can authenticate", err)
	} else {
		fmt.Fprintln(output, "SKIP  administrator checks (provide --admin and --admin-pass)")
	}

	if cfg.allowActiveCommands {
		if len(locomotives) == 0 {
			add("active driving scenario has a locomotive", errors.New("no locomotive available"))
		} else {
			runActiveDrivingChecks(ctx, c1, c2, info, locomotives[0].ID, add)
		}
		if adminClient != nil {
			runDispatchChecks(ctx, adminClient, add)
		}
	} else {
		fmt.Fprintln(output, "SKIP  active driving checks (use --allow-active-commands only with an explicitly selected test station)")
	}

	status, remoteUserErr := c1.Do(ctx, http.MethodPost, "/api/v1/users", map[string]string{"username": "forbidden"}, nil)
	if remoteUserErr == nil || status != http.StatusNotFound {
		remoteUserErr = fmt.Errorf("expected HTTP 404, got status %d err %v", status, remoteUserErr)
	} else {
		remoteUserErr = nil
	}
	add("public API does not expose user creation", remoteUserErr)

	archive, exportErr := c1.ExportRollingStock(ctx)
	if exportErr == nil && len(archive) == 0 {
		exportErr = errors.New("empty archive")
	}
	add("authenticated client exports rolling stock", exportErr)
	if exportErr == nil {
		add("non-administrator cannot import rolling stock", expectHTTPError(c1.ImportRollingStock(ctx, archive, false), http.StatusForbidden, "authorization", "permission_denied"))
	}
	layoutArchive, layoutExportErr := c1.ExportLayout(ctx)
	if layoutExportErr == nil && len(layoutArchive) == 0 {
		layoutExportErr = errors.New("empty layout archive")
	}
	add("authenticated client exports layout", layoutExportErr)

	if cfg.allowConfigChanges {
		if adminClient == nil {
			add("configuration mutation checks have an administrator", errors.New("--admin and --admin-pass are required"))
		} else {
			runConfigurationChecks(ctx, adminClient, archive, layoutArchive, add)
		}
	} else {
		fmt.Fprintln(output, "SKIP  configuration mutation checks (use --allow-configuration-mutations with a disposable server)")
	}

	oldSecondAccess := c2.AccessToken
	add("logout revokes the current session", c2.Logout(ctx))
	revokedClient := client.New(cfg.server)
	revokedClient.AccessToken = oldSecondAccess
	_, revokedErr := revokedClient.Me(ctx)
	add("logged-out access token is rejected", expectHTTPError(revokedErr, http.StatusUnauthorized, "authentication", "invalid_token"))

	failed := 0
	for _, result := range results {
		if result.err != nil {
			failed++
			fmt.Fprintf(output, "FAIL  %s: %v\n", result.name, result.err)
		} else {
			fmt.Fprintf(output, "PASS  %s\n", result.name)
		}
	}
	fmt.Fprintf(output, "\nResult: %d passed, %d failed\n", len(results)-failed, failed)
	return failed
}

func runActiveDrivingChecks(ctx context.Context, c1, c2 *client.Client, info client.SystemInfo, locomotiveID string, add func(string, error)) {
	lease, err := c1.Acquire(ctx, locomotiveID)
	add("free locomotive can be reserved", err)
	if err != nil {
		return
	}
	_, conflictErr := c2.Acquire(ctx, locomotiveID)
	add("second session cannot reserve the same locomotive", expectHTTPError(conflictErr, http.StatusConflict, "conflict"))
	_, heartbeatErr := c1.Heartbeat(ctx, lease.ID)
	add("lease owner can renew the lease", heartbeatErr)
	if err := c1.SetTrackPower(ctx, true); err != nil {
		add("test station enables track power", err)
		return
	}
	defer func() { _ = c1.SetTrackPower(context.Background(), false) }()
	add("lease owner can change speed", c1.Throttle(ctx, locomotiveID, lease.ID, 20, station.Forward))
	add("invalid direction is rejected", expectHTTPError(c1.Throttle(ctx, locomotiveID, lease.ID, 20, station.Direction("sideways")), http.StatusBadRequest, "validation", "invalid_direction"))
	if info.Station.Functions > 0 {
		add("lease owner can change function zero", c1.Function(ctx, locomotiveID, lease.ID, 0, true))
		add("function above station capability is rejected", expectHTTPError(c1.Function(ctx, locomotiveID, lease.ID, info.Station.MaxFunctionNumber+1, true), http.StatusBadRequest, "validation", "validation_failed"))
	}
	add("emergency stop preempts ordinary driving", c1.EmergencyStop(ctx))
	add("throttle is blocked while emergency stop is active", expectHTTPError(c1.Throttle(ctx, locomotiveID, lease.ID, 20, station.Forward), http.StatusConflict, "safety", "emergency_stop_active"))
	add("track power command clears the emergency stop latch", c1.SetTrackPower(ctx, true))
	add("lease owner can command speed zero", c1.Throttle(ctx, locomotiveID, lease.ID, 0, station.Forward))
	add("lease owner can release control", c1.Release(ctx, lease.ID))
}

func runDispatchChecks(ctx context.Context, admin *client.Client, add func(string, error)) {
	turnouts, err := admin.Turnouts(ctx)
	if err != nil || len(turnouts) == 0 {
		if err == nil {
			err = errors.New("no turnout available")
		}
		add("dispatch scenario has a turnout", err)
	} else {
		add("dispatcher can command a turnout", admin.SetTurnout(ctx, turnouts[0].ID, "straight"))
	}
	routes, err := admin.Routes(ctx)
	if err != nil || len(routes) == 0 {
		if err == nil {
			err = errors.New("no route available")
		}
		add("dispatch scenario has a route", err)
		return
	}
	add("dispatcher can reserve a route", admin.ReserveRoute(ctx, routes[0].ID))
	add("dispatcher can activate a reserved route", admin.ActivateRoute(ctx, routes[0].ID))
	add("dispatcher can release a route", admin.ReleaseRoute(ctx, routes[0].ID))
}

func runConfigurationChecks(ctx context.Context, admin *client.Client, rollingStockArchive, layoutArchive []byte, add func(string, error)) {
	input := model.LocomotiveInput{Name: "Conformance temporary", DCCAddress: 9999, AddressKind: "long", SpeedSteps: 128}
	created, err := admin.CreateLocomotive(ctx, input)
	add("administrator can create a locomotive", err)
	if err == nil {
		_, getErr := admin.Locomotive(ctx, created.ID)
		add("administrator can read the created locomotive", getErr)
		input.Name = "Conformance temporary updated"
		_, updateErr := admin.UpdateLocomotive(ctx, created.ID, input)
		add("administrator can update a locomotive", updateErr)
		add("administrator can delete a locomotive", admin.DeleteLocomotive(ctx, created.ID))
	}
	if len(rollingStockArchive) > 0 {
		add("administrator can import rolling stock in merge mode", admin.ImportRollingStock(ctx, rollingStockArchive, false))
	}
	if len(layoutArchive) > 0 {
		add("administrator can import layout in merge mode", admin.ImportLayout(ctx, layoutArchive, false))
	}
}

func requireVersions(info client.SystemInfo, err error) error {
	if err != nil {
		return err
	}
	if info.APIVersion == "" || info.MinimumClientAPIVersion == "" || info.EventAPIVersion == "" || info.MinimumClientEventAPIVersion == "" {
		return fmt.Errorf("incomplete version information: %+v", info)
	}
	return nil
}

func expectHTTPError(err error, status int, category string, codes ...string) error {
	var httpErr *client.HTTPError
	if !errors.As(err, &httpErr) {
		return fmt.Errorf("expected HTTP %d error, got %v", status, err)
	}
	if httpErr.StatusCode != status {
		return fmt.Errorf("expected HTTP %d, got %d", status, httpErr.StatusCode)
	}
	if httpErr.Problem == nil || httpErr.Problem.Category != category || httpErr.Problem.Code == "" {
		return fmt.Errorf("expected category %q and stable code, got %+v", category, httpErr.Problem)
	}
	if len(codes) > 0 {
		matched := false
		for _, code := range codes {
			matched = matched || httpErr.Problem.Code == code
		}
		if !matched {
			return fmt.Errorf("expected one of codes %v, got %q", codes, httpErr.Problem.Code)
		}
	}
	return nil
}
