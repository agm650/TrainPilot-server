package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/admin"
	"github.com/agm650/TrainPilot-server/internal/auth"
	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/service"
	"github.com/agm650/TrainPilot-server/internal/store"
)

func TestUserAdministrationOverUnixSocket(t *testing.T) {
	ctx := context.Background()

	if os.Getenv("GITHUB_ACTIONS") == "true" {
		t.Skip("test d'intégration du socket Unix réservé aux exécutions locales")
	}

	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	users := service.NewUserServiceWithPasswordParams(db, clock.Real{}, auth.PasswordParams{Iterations: 100_000, SaltLength: 16, KeyLength: 32})
	// t.TempDir() includes the full test name. On macOS, that can exceed the
	// sockaddr_un.sun_path limit and make net.Listen fail with EINVAL.
	socketDir, err := os.MkdirTemp("", "dcc-admin-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(socketDir); err != nil {
			t.Errorf("remove socket directory: %v", err)
		}
	})
	socket := filepath.Join(socketDir, "admin.sock")
	srv := admin.NewServer(socket, 0o600, users)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Close(ctx)
	client := admin.NewClient(socket)
	created, err := client.CreateUser(ctx, "admin", "Local Admin", "a-long-local-password", model.RoleAdministrator, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if created.Username != "admin" {
		t.Fatalf("username=%s", created.Username)
	}
	items, err := client.ListUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("users=%d", len(items))
	}
	if err := client.SetEnabled(ctx, "admin", false); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	items, err = client.ListUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Enabled {
		t.Fatal("user still enabled")
	}
}
