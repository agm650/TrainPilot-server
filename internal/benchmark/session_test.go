package benchmark

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/client"
	"github.com/agm650/TrainPilot-server/internal/service"
	ws "github.com/agm650/TrainPilot-server/internal/websocket"
)

func TestBenchSessionRefreshesAcrossMultipleExpiryCycles(t *testing.T) {
	var mu sync.Mutex
	refreshCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/refresh":
			mu.Lock()
			refreshCount++
			count := refreshCount
			mu.Unlock()
			var request struct {
				RefreshToken string `json:"refreshToken"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if request.RefreshToken != fmt.Sprintf("refresh-%d", count-1) {
				http.Error(w, "unexpected refresh token", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(service.TokenPair{
				AccessToken:     fmt.Sprintf("access-%d", count),
				RefreshToken:    fmt.Sprintf("refresh-%d", count),
				AccessExpiresAt: time.Now().Add(15 * time.Minute),
			})
		case "/protected":
			mu.Lock()
			count := refreshCount
			mu.Unlock()
			if r.Header.Get("Authorization") != fmt.Sprintf("Bearer access-%d", count) {
				http.Error(w, "unexpected access token", http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	c := client.New(server.URL)
	c.AccessToken = "access-0"
	c.RefreshToken = "refresh-0"
	session := &benchSession{
		client:          c,
		accessExpiresAt: time.Now().Add(-time.Second),
		accessRefreshAt: time.Now().Add(-time.Second),
	}

	for cycle := 1; cycle <= 2; cycle++ {
		err := session.withClient(context.Background(), func(c *client.Client) error {
			_, err := c.Do(context.Background(), http.MethodGet, "/protected", nil, nil)
			return err
		})
		if err != nil {
			t.Fatalf("cycle %d: %v", cycle, err)
		}
		session.mu.Lock()
		if cycle < 2 {
			session.accessRefreshAt = time.Now().Add(-time.Second)
		}
		session.mu.Unlock()
	}
	if refreshCount != 2 {
		t.Fatalf("refreshes=%d, want 2", refreshCount)
	}
}

func TestTokenRefreshAt(t *testing.T) {
	now := time.Date(2026, time.September, 21, 0, 0, 0, 0, time.UTC)
	if got, want := tokenRefreshAt(now, now.Add(15*time.Minute)), now.Add(14*time.Minute); !got.Equal(want) {
		t.Fatalf("long-lived refresh=%s, want %s", got, want)
	}
	if got, want := tokenRefreshAt(now, now.Add(30*time.Second)), now.Add(20*time.Second); !got.Equal(want) {
		t.Fatalf("short-lived refresh=%s, want %s", got, want)
	}
}

func TestBenchSessionRefreshesAfterWebSocketAuthenticationFailure(t *testing.T) {
	refreshes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/refresh":
			refreshes++
			_ = json.NewEncoder(w).Encode(service.TokenPair{
				AccessToken:     "new-access",
				RefreshToken:    "new-refresh",
				AccessExpiresAt: time.Now().Add(15 * time.Minute),
			})
		case "/api/v1/events":
			if r.Header.Get("Authorization") != "Bearer new-access" {
				http.Error(w, "expired", http.StatusUnauthorized)
				return
			}
			connection, err := ws.Accept(w, r)
			if err == nil {
				_ = connection.Close()
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	c := client.New(server.URL)
	c.AccessToken = "old-access"
	c.RefreshToken = "old-refresh"
	session := &benchSession{
		client:          c,
		accessExpiresAt: time.Now().Add(10 * time.Minute),
		accessRefreshAt: time.Now().Add(9 * time.Minute),
	}
	websocket, err := session.connectWebSocket(context.Background(), server.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = websocket.Close()
	if refreshes != 1 {
		t.Fatalf("refreshes=%d, want 1", refreshes)
	}
}
