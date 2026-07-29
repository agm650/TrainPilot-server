package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// DB wraps database/sql while keeping the small API used by the store package.
//
// The connection pool is deliberately limited to one connection. Besides
// matching the previous implementation's serialized access, this is important
// for SQLite connection-local settings such as foreign_keys and busy_timeout,
// and for :memory: databases used by the tests.
//
// Result preserves the package API while delegating to database/sql.
type Result = sql.Result

type DB struct {
	db *sql.DB
}

// Tx is an explicit BEGIN IMMEDIATE transaction bound to one database
// connection. Only the operations currently required by the store are exposed.
type Tx struct {
	conn     *sql.Conn
	finished bool
}

func Open(path string) (*DB, error) {
	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}

	// A single connection preserves the former wrapper's behaviour, prevents
	// separate :memory: databases from being created by the pool, and ensures
	// all connection-local PRAGMAs remain effective.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(0)

	db := &DB{db: sqlDB}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("connect to sqlite database: %w", err)
	}

	for _, pragma := range []string{
		"PRAGMA foreign_keys=ON",
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := sqlDB.ExecContext(ctx, pragma); err != nil {
			_ = sqlDB.Close()
			return nil, fmt.Errorf("configure sqlite (%s): %w", pragma, err)
		}
	}

	return db, nil
}

func (d *DB) Close() error {
	if d == nil || d.db == nil {
		return nil
	}
	return d.db.Close()
}

func (d *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return d.db.ExecContext(ctx, query, args...)
}

func (d *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return d.db.QueryContext(ctx, query, args...)
}

func (d *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return d.db.QueryRowContext(ctx, query, args...)
}

// WithTransaction executes fn in a BEGIN IMMEDIATE transaction. BEGIN
// IMMEDIATE acquires the write reservation at the start of the operation, which
// makes conflicts deterministic for imports and other multi-statement writes.
func (d *DB) WithTransaction(ctx context.Context, fn func(*Tx) error) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if fn == nil {
		return errors.New("transaction function is nil")
	}

	conn, err := d.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire sqlite connection: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin sqlite transaction: %w", err)
	}

	tx := &Tx{conn: conn}
	rollback := func() error {
		if tx.finished {
			return nil
		}
		tx.finished = true
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, rollbackErr := conn.ExecContext(rollbackCtx, "ROLLBACK")
		return rollbackErr
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			_ = rollback()
			panic(recovered)
		}
	}()

	if err := fn(tx); err != nil {
		if rollbackErr := rollback(); rollbackErr != nil {
			return errors.Join(err, fmt.Errorf("rollback sqlite transaction: %w", rollbackErr))
		}
		return err
	}

	if err := ctx.Err(); err != nil {
		if rollbackErr := rollback(); rollbackErr != nil {
			return errors.Join(err, fmt.Errorf("rollback sqlite transaction: %w", rollbackErr))
		}
		return err
	}

	commitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := conn.ExecContext(commitCtx, "COMMIT"); err != nil {
		_ = rollback()
		return fmt.Errorf("commit sqlite transaction: %w", err)
	}

	tx.finished = true

	return nil
}

func (tx *Tx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if tx == nil || tx.conn == nil {
		return nil, errors.New("invalid transaction")
	}
	if tx.finished {

		return nil, errors.New("transaction already finished")
	}
	if err := ctx.Err(); err != nil {

		return nil, err
	}

	return tx.conn.ExecContext(ctx, query, args...)
}
