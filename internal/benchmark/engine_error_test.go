package benchmark

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
