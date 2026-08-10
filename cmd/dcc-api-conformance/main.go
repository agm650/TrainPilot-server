package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/agm650/TrainPilot-server/internal/client"
	"github.com/agm650/TrainPilot-server/internal/station"
)

type result struct {
	name string
	err  error
}

func main() {
	server := flag.String("server", "http://127.0.0.1:8080", "server URL")
	user1 := flag.String("user1", "alice", "first driver")
	pass1 := flag.String("pass1", "correct-horse-1", "first password")
	user2 := flag.String("user2", "bob", "second driver")
	pass2 := flag.String("pass2", "correct-horse-2", "second password")
	flag.Parse()
	ctx := context.Background()
	c1 := client.New(*server)
	c2 := client.New(*server)
	results := []result{}
	_, err := c1.Login(ctx, *user1, *pass1, "conformance-1")
	results = append(results, result{"valid user can authenticate", err})
	_, err = c2.Login(ctx, *user2, *pass2, "conformance-2")
	results = append(results, result{"second user can authenticate", err})
	locos, err := c1.Locomotives(ctx)
	results = append(results, result{"authenticated client lists locomotives", err})
	if err == nil && len(locos) > 0 {
		lease, acqErr := c1.Acquire(ctx, locos[0].ID)
		results = append(results, result{"free locomotive can be reserved", acqErr})
		if acqErr == nil {
			status, conflictErr := c2.Do(ctx, http.MethodPost, "/api/v1/locomotives/"+locos[0].ID+"/control-lease", nil, nil)
			if conflictErr == nil || status != http.StatusConflict {
				conflictErr = fmt.Errorf("expected HTTP 409, got status %d err %v", status, conflictErr)
			} else {
				conflictErr = nil
			}
			results = append(results, result{"second user cannot reserve same locomotive", conflictErr})
			results = append(results, result{"lease owner can change speed", c1.Throttle(ctx, locos[0].ID, lease.ID, 20, station.Forward)})
			results = append(results, result{"lease owner can change functions", c1.Function(ctx, locos[0].ID, lease.ID, 0, true)})
			results = append(results, result{"lease owner can release control", c1.Release(ctx, lease.ID)})
		}
	}
	status, remoteUserErr := c1.Do(ctx, http.MethodPost, "/api/v1/users", map[string]string{"username": "forbidden"}, nil)
	if remoteUserErr == nil || status != http.StatusNotFound {
		remoteUserErr = fmt.Errorf("remote user administration must be absent, got status %d err %v", status, remoteUserErr)
	} else {
		remoteUserErr = nil
	}
	results = append(results, result{"public API does not expose user creation", remoteUserErr})
	archive, exportErr := c1.ExportRollingStock(ctx)
	if exportErr == nil && len(archive) == 0 {
		exportErr = fmt.Errorf("empty archive")
	}
	results = append(results, result{"authenticated client can export rolling stock", exportErr})
	var importErr error
	if exportErr == nil {
		importErr = c1.ImportRollingStock(ctx, archive, false)
		if importErr == nil {
			importErr = fmt.Errorf("driver import unexpectedly succeeded")
		} else if !strings.Contains(importErr.Error(), "403") {
			importErr = fmt.Errorf("expected HTTP 403, got %v", importErr)
		} else {
			importErr = nil
		}
	}
	results = append(results, result{"non-administrator cannot import rolling stock", importErr})
	failed := 0
	for _, r := range results {
		if r.err != nil {
			failed++
			fmt.Printf("FAIL  %s: %v\n", r.name, r.err)
		} else {
			fmt.Printf("PASS  %s\n", r.name)
		}
	}
	fmt.Printf("\nResult: %d passed, %d failed\n", len(results)-failed, failed)
	if failed > 0 {
		os.Exit(1)
	}
}
