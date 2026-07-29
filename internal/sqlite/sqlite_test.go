package sqlite

import (
	"context"
	"errors"
	"testing"
)

func TestOpenUsesSingleConnection(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if got := db.db.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("expected one SQLite connection, got %d", got)
	}
}

func TestOpenEnablesForeignKeys(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE parent(id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE child(parent_id INTEGER REFERENCES parent(id))`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO child(parent_id) VALUES(42)`); err == nil {
		t.Fatal("expected foreign key violation")
	}
}

func TestWithTransactionCommits(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE values_table(value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}

	if err := db.WithTransaction(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO values_table(value) VALUES(?)`, "committed")
		return err
	}); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM values_table WHERE value=?`, "committed").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected one committed row, got %d", count)
	}
}

func TestWithTransactionRollsBack(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE values_table(value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}

	expected := errors.New("stop")
	err = db.WithTransaction(ctx, func(tx *Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO values_table(value) VALUES(?)`, "rolled-back"); err != nil {
			return err
		}
		return expected
	})
	if !errors.Is(err, expected) {
		t.Fatalf("expected %v, got %v", expected, err)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM values_table`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected rollback to leave no rows, got %d", count)
	}
}
