package benchmark

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/client"
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
