package benchmark

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/client"
	"github.com/agm650/TrainPilot-server/internal/service"
)

func TestRunRejectsInvalidServerJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"serverVersion":`))
	}))
	defer server.Close()
	_, err := Run(context.Background(), RunOptions{
		Server: server.URL,
		Profile: Profile{
			SchemaVersion: ProfileSchemaVersion,
			Name:          "invalid-json", Duration: Duration{Duration: time.Second},
			Clients: ClientProfile{Users: 1},
		},
		Credentials: []Credential{{Username: "test", Password: "never-sent"}},
	})
	if err == nil || !strings.Contains(err.Error(), "unexpected EOF") {
		t.Fatalf("error=%v", err)
	}
}

func TestExpectedErrorsAreExplicitlyMatchedByOperation(t *testing.T) {
	engine := &runEngine{profile: Profile{ExpectedErrors: []ExpectedErrorRule{{Operation: "lease_contention", HTTPStatuses: []int{http.StatusConflict}}}}}
	err := &client.HTTPError{StatusCode: http.StatusConflict}
	if !engine.isExpectedError("lease_contention", err) {
		t.Fatal("declared contention error was not expected")
	}
	if engine.isExpectedError("throttle", err) {
		t.Fatal("error expectation leaked to another operation")
	}
	engine.profile.ExpectedErrors = []ExpectedErrorRule{{Operation: "throttle", Kinds: []string{"timeout"}}}
	if !engine.isExpectedError("throttle", context.DeadlineExceeded) {
		t.Fatal("declared timeout was not expected")
	}
}

func TestExecuteLoginUsesDedicatedTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			timer := time.NewTimer(50 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-r.Context().Done():
				return
			}
			_ = json.NewEncoder(w).Encode(service.TokenPair{
				AccessToken: "access", RefreshToken: "refresh",
				AccessExpiresAt: time.Now().Add(time.Minute),
			})
		case "/api/v1/auth/logout":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	recorder := newOperationRecorder()
	recorder.StartMeasurement(time.Now())
	engine := &runEngine{
		options: RunOptions{
			Server:      server.URL,
			Credentials: []Credential{{Username: "benchmark", Password: "benchmark-secret"}},
		},
		profile: Profile{
			OperationTimeout: Duration{Duration: 10 * time.Millisecond},
			LoginTimeout:     Duration{Duration: 200 * time.Millisecond},
		},
		recorder: recorder,
	}
	engine.executeJob(context.Background(), scheduledJob{operation: "login", seed: 650})

	stats := recorder.stats["login"]
	if stats == nil || stats.Successes != 1 || stats.UnexpectedErrors != 0 {
		t.Fatalf("login stats=%+v", stats)
	}
}
