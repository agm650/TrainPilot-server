package integration

import (
	"context"
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
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	users := service.NewUserServiceWithPasswordParams(db, clock.Real{}, auth.PasswordParams{Iterations: 100_000, SaltLength: 16, KeyLength: 32})
	socket := filepath.Join(t.TempDir(), "admin.sock")
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
