package sqlite

/*
#cgo linux LDFLAGS: -lsqlite3
#cgo darwin LDFLAGS: -lsqlite3
#include <stdlib.h>
#include <sqlite3.h>

static int bind_text(sqlite3_stmt *stmt, int index, const char *value) {
    return sqlite3_bind_text(stmt, index, value, -1, SQLITE_TRANSIENT);
}
*/
import "C"

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"unsafe"
)

type DB struct {
	mu  sync.Mutex
	ptr *C.sqlite3
}

type Result struct{ affected int64 }

func (r Result) RowsAffected() (int64, error) { return r.affected, nil }

type Rows struct {
	db     *DB
	stmt   *C.sqlite3_stmt
	closed bool
	err    error
	hasRow bool
}

type Row struct{ rows *Rows }

func Open(path string) (*DB, error) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	var ptr *C.sqlite3
	flags := C.SQLITE_OPEN_READWRITE | C.SQLITE_OPEN_CREATE | C.SQLITE_OPEN_FULLMUTEX
	if rc := C.sqlite3_open_v2(cpath, &ptr, C.int(flags), nil); rc != C.SQLITE_OK {
		msg := "sqlite open failed"
		if ptr != nil {
			msg = C.GoString(C.sqlite3_errmsg(ptr))
			C.sqlite3_close(ptr)
		}
		return nil, errors.New(msg)
	}
	db := &DB{ptr: ptr}
	for _, pragma := range []string{"PRAGMA foreign_keys=ON", "PRAGMA journal_mode=WAL", "PRAGMA busy_timeout=5000"} {
		if _, err := db.ExecContext(context.Background(), pragma); err != nil {
			db.Close()
			return nil, err
		}
	}
	return db, nil
}

func (d *DB) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ptr == nil {
		return nil
	}
	rc := C.sqlite3_close(d.ptr)
	if rc != C.SQLITE_OK {
		return errors.New(C.GoString(C.sqlite3_errmsg(d.ptr)))
	}
	d.ptr = nil
	return nil
}

func (d *DB) prepareLocked(query string, args ...any) (*C.sqlite3_stmt, error) {
	cq := C.CString(query)
	defer C.free(unsafe.Pointer(cq))
	var stmt *C.sqlite3_stmt
	if rc := C.sqlite3_prepare_v2(d.ptr, cq, -1, &stmt, nil); rc != C.SQLITE_OK {
		return nil, errors.New(C.GoString(C.sqlite3_errmsg(d.ptr)))
	}
	for i, arg := range args {
		if err := bind(stmt, i+1, arg); err != nil {
			C.sqlite3_finalize(stmt)
			return nil, err
		}
	}
	return stmt, nil
}
func bind(stmt *C.sqlite3_stmt, index int, value any) error {
	var rc C.int
	switch v := value.(type) {
	case nil:
		rc = C.sqlite3_bind_null(stmt, C.int(index))
	case string:
		cs := C.CString(v)
		rc = C.bind_text(stmt, C.int(index), cs)
		C.free(unsafe.Pointer(cs))
	case []byte:
		if len(v) == 0 {
			rc = C.sqlite3_bind_blob(stmt, C.int(index), nil, 0, C.SQLITE_TRANSIENT)
		} else {
			rc = C.sqlite3_bind_blob(stmt, C.int(index), unsafe.Pointer(&v[0]), C.int(len(v)), C.SQLITE_TRANSIENT)
		}
	case int:
		rc = C.sqlite3_bind_int64(stmt, C.int(index), C.sqlite3_int64(v))
	case int64:
		rc = C.sqlite3_bind_int64(stmt, C.int(index), C.sqlite3_int64(v))
	case uint32:
		rc = C.sqlite3_bind_int64(stmt, C.int(index), C.sqlite3_int64(v))
	case bool:
		iv := 0
		if v {
			iv = 1
		}
		rc = C.sqlite3_bind_int(stmt, C.int(index), C.int(iv))
	case float64:
		rc = C.sqlite3_bind_double(stmt, C.int(index), C.double(v))
	default:
		rv := reflect.ValueOf(value)
		switch rv.Kind() {
		case reflect.String:
			return bind(stmt, index, rv.String())
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return bind(stmt, index, rv.Int())
		default:
			return fmt.Errorf("unsupported sqlite parameter %T", value)
		}
	}
	if rc != C.SQLITE_OK {
		return fmt.Errorf("sqlite bind parameter %d failed", index)
	}
	return nil
}

func (d *DB) ExecContext(ctx context.Context, query string, args ...any) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.execLocked(query, args...)
}
func (d *DB) QueryContext(ctx context.Context, query string, args ...any) (*Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.Lock()
	stmt, err := d.prepareLocked(query, args...)
	if err != nil {
		d.mu.Unlock()
		return nil, err
	}
	return &Rows{db: d, stmt: stmt}, nil
}
func (d *DB) QueryRowContext(ctx context.Context, query string, args ...any) *Row {
	rows, err := d.QueryContext(ctx, query, args...)
	if err != nil {
		return &Row{rows: &Rows{err: err, closed: true}}
	}
	return &Row{rows: rows}
}
func (r *Row) Scan(dest ...any) error {
	if r.rows.err != nil {
		return r.rows.err
	}
	defer r.rows.Close()
	if !r.rows.Next() {
		if r.rows.err != nil {
			return r.rows.err
		}
		return sql.ErrNoRows
	}
	return r.rows.Scan(dest...)
}
func (r *Rows) Next() bool {
	if r.closed || r.err != nil {
		return false
	}
	rc := C.sqlite3_step(r.stmt)
	switch rc {
	case C.SQLITE_ROW:
		r.hasRow = true
		return true
	case C.SQLITE_DONE:
		r.Close()
		return false
	default:
		r.err = errors.New(C.GoString(C.sqlite3_errmsg(r.db.ptr)))
		r.Close()
		return false
	}
}
func (r *Rows) Scan(dest ...any) error {
	if !r.hasRow {
		return errors.New("Scan called without current row")
	}
	count := int(C.sqlite3_column_count(r.stmt))
	if len(dest) != count {
		return fmt.Errorf("expected %d scan destinations, got %d", count, len(dest))
	}
	for i, dst := range dest {
		if err := scanColumn(r.stmt, i, dst); err != nil {
			return err
		}
	}
	r.hasRow = false
	return nil
}
func (r *Rows) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	C.sqlite3_finalize(r.stmt)
	r.db.mu.Unlock()
	return nil
}
func (r *Rows) Err() error { return r.err }
func scanColumn(stmt *C.sqlite3_stmt, index int, dst any) error {
	if ns, ok := dst.(*sql.NullString); ok {
		if C.sqlite3_column_type(stmt, C.int(index)) == C.SQLITE_NULL {
			ns.String = ""
			ns.Valid = false
		} else {
			ns.String = C.GoString((*C.char)(unsafe.Pointer(C.sqlite3_column_text(stmt, C.int(index)))))
			ns.Valid = true
		}
		return nil
	}
	rv := reflect.ValueOf(dst)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("scan destination %d must be non-nil pointer", index)
	}
	elem := rv.Elem()
	if C.sqlite3_column_type(stmt, C.int(index)) == C.SQLITE_NULL {
		elem.Set(reflect.Zero(elem.Type()))
		return nil
	}
	switch elem.Kind() {
	case reflect.String:
		elem.SetString(C.GoString((*C.char)(unsafe.Pointer(C.sqlite3_column_text(stmt, C.int(index))))))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		elem.SetInt(int64(C.sqlite3_column_int64(stmt, C.int(index))))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		elem.SetUint(uint64(C.sqlite3_column_int64(stmt, C.int(index))))
	case reflect.Bool:
		elem.SetBool(C.sqlite3_column_int(stmt, C.int(index)) != 0)
	case reflect.Float32, reflect.Float64:
		elem.SetFloat(float64(C.sqlite3_column_double(stmt, C.int(index))))
	default:
		return fmt.Errorf("unsupported scan destination %T", dst)
	}
	return nil
}

type Tx struct {
	db       *DB
	finished bool
}

func (d *DB) WithTransaction(ctx context.Context, fn func(*Tx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, err := d.execLocked("BEGIN IMMEDIATE"); err != nil {
		return err
	}
	tx := &Tx{db: d}
	if err := fn(tx); err != nil {
		_, _ = d.execLocked("ROLLBACK")
		tx.finished = true
		return err
	}
	_, err := d.execLocked("COMMIT")
	tx.finished = true
	return err
}

func (d *DB) execLocked(query string, args ...any) (Result, error) {
	stmt, err := d.prepareLocked(query, args...)
	if err != nil {
		return Result{}, err
	}
	defer C.sqlite3_finalize(stmt)
	rc := C.sqlite3_step(stmt)
	if rc != C.SQLITE_DONE && rc != C.SQLITE_ROW {
		return Result{}, errors.New(C.GoString(C.sqlite3_errmsg(d.ptr)))
	}
	return Result{affected: int64(C.sqlite3_changes(d.ptr))}, nil
}

func (tx *Tx) ExecContext(ctx context.Context, query string, args ...any) (Result, error) {
	if tx.finished {
		return Result{}, errors.New("transaction already finished")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	return tx.db.execLocked(query, args...)
}
