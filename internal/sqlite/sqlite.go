// Package sqlite is a deliberately small, synchronous binding to the system
// SQLite library. It uses prepared statements, transient copies for Go data,
// one guarded connection and BEGIN IMMEDIATE transactions. No external Go modules.
package sqlite

/*
#cgo LDFLAGS: -lsqlite3
#include <sqlite3.h>
#include <stdlib.h>
static int bind_text_copy(sqlite3_stmt *s, int i, const char *v, int n) {
 return sqlite3_bind_text(s, i, v, n, SQLITE_TRANSIENT);
}
*/
import "C"

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unsafe"
)

type DB struct {
	metrics dbMetrics
	mu      sync.Mutex
	ptr     *C.sqlite3
}
type Tx struct{ db *DB }
type Row map[string]string

func (r Row) Int(k string) int64 {
	v, e := strconv.ParseInt(r[k], 10, 64)
	if e != nil && r[k] != "" {
		panic("数据库整数损坏: " + k)
	}
	return v
}
func Version() string { return C.GoString(C.sqlite3_libversion()) }

func Open(path string) (*DB, error) {
	return open(path, false, false)
}

// OpenReadOnly never creates a missing database or changes its journal mode.
// Offline verification must not initialize the app or replay pending messages.
func OpenReadOnly(path string) (*DB, error) {
	return open(path, true, false)
}

// OpenBackup is only for a standalone, unchanging SQLite .backup image, never a
// live database. Immutable mode can read WAL-format headers on read-only media
// without creating sidecars. Reject sidecars rather than silently ignoring WAL.
func OpenBackup(path string) (*DB, error) {
	abs, e := filepath.Abs(path)
	if e != nil {
		return nil, e
	}
	abs, e = filepath.EvalSymlinks(abs)
	if e != nil {
		return nil, e
	}
	info, e := os.Stat(abs)
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("备份必须是普通文件")
	}
	for _, suffix := range []string{"-wal", "-journal"} {
		if _, e = os.Stat(abs + suffix); e == nil {
			return nil, errors.New("文件旁存在WAL或日志；请先使用SQLite .backup创建独立快照")
		} else if !os.IsNotExist(e) {
			return nil, e
		}
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs), RawQuery: "mode=ro&immutable=1"}
	return open(u.String(), true, true)
}

func open(path string, readOnly, uri bool) (*DB, error) {
	if strings.IndexByte(path, 0) >= 0 {
		return nil, errors.New("数据库路径不能含空字符")
	}
	p := C.CString(path)
	defer C.free(unsafe.Pointer(p))
	d := &DB{}
	flags := C.int(C.SQLITE_OPEN_READWRITE | C.SQLITE_OPEN_CREATE | C.SQLITE_OPEN_FULLMUTEX)
	if readOnly {
		flags = C.SQLITE_OPEN_READONLY | C.SQLITE_OPEN_FULLMUTEX
	}
	if uri {
		flags |= C.SQLITE_OPEN_URI
	}
	rc := C.sqlite3_open_v2(p, &d.ptr, flags, nil)
	if rc != C.SQLITE_OK {
		e := d.err(rc)
		if d.ptr != nil {
			C.sqlite3_close(d.ptr)
		}
		return nil, e
	}
	C.sqlite3_busy_timeout(d.ptr, 5000)
	if readOnly {
		if _, e := d.Exec("PRAGMA query_only=ON"); e != nil {
			d.Close()
			return nil, e
		}
		return d, nil
	}
	if _, e := d.Exec("PRAGMA foreign_keys=ON"); e != nil {
		d.Close()
		return nil, e
	}
	if _, e := d.Exec("PRAGMA journal_mode=WAL"); e != nil {
		d.Close()
		return nil, e
	}
	if _, e := d.Exec("PRAGMA synchronous=FULL"); e != nil {
		d.Close()
		return nil, e
	}
	return d, nil
}
func (d *DB) err(rc C.int) error {
	if d.ptr == nil {
		return fmt.Errorf("sqlite错误 %d", int(rc))
	}
	return fmt.Errorf("sqlite错误 %d: %s", int(rc), C.GoString(C.sqlite3_errmsg(d.ptr)))
}
func (d *DB) Close() error {
	unlock := d.measuredLock()
	defer unlock()
	if d.ptr == nil {
		return nil
	}
	rc := C.sqlite3_close(d.ptr)
	if rc != C.SQLITE_OK {
		return d.err(rc)
	}
	d.ptr = nil
	return nil
}
func (d *DB) Exec(q string, args ...any) (int64, error) {
	unlock := d.measuredLock()
	defer unlock()
	_, n, e := d.run(q, args...)
	return n, e
}
func (d *DB) Query(q string, args ...any) ([]Row, error) {
	unlock := d.measuredLock()
	defer unlock()
	r, _, e := d.run(q, args...)
	return r, e
}
func (t *Tx) Exec(q string, args ...any) (int64, error) { _, n, e := t.db.run(q, args...); return n, e }
func (t *Tx) Query(q string, args ...any) ([]Row, error) {
	r, _, e := t.db.run(q, args...)
	return r, e
}
func (t *Tx) One(q string, args ...any) (Row, error) {
	r, e := t.Query(q, args...)
	if e != nil {
		return nil, e
	}
	if len(r) == 0 {
		return nil, nil
	}
	return r[0], nil
}

func (d *DB) Transaction(f func(*Tx) error) (err error) {
	return d.transaction("BEGIN IMMEDIATE", f)
}

// Snapshot also stays consistent against writers using other connections.
// Use it on an OpenReadOnly connection for offline diagnostics.
func (d *DB) Snapshot(f func(*Tx) error) error {
	return d.transaction("BEGIN", f)
}

func (d *DB) transaction(begin string, f func(*Tx) error) (err error) {
	unlock := d.measuredLock()
	defer unlock()
	t := &Tx{d}
	if _, err = t.Exec(begin); err != nil {
		return
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = t.Exec("ROLLBACK")
		}
	}()
	if err = f(t); err != nil {
		return
	}
	_, err = t.Exec("COMMIT")
	committed = err == nil
	return
}

// Read keeps multi-query snapshots internally consistent without permitting a
// recursive call to DB methods. Use the Tx supplied to the closure throughout.
func (d *DB) Read(f func(*Tx) error) error {
	unlock := d.measuredLock()
	defer unlock()
	return f(&Tx{d})
}

func (d *DB) run(q string, args ...any) ([]Row, int64, error) {
	rows := []Row{}
	if d.ptr == nil {
		return rows, 0, errors.New("数据库已关闭")
	}
	if strings.IndexByte(q, 0) >= 0 {
		return rows, 0, errors.New("SQL包含空字符")
	}
	cq := C.CString(q)
	defer C.free(unsafe.Pointer(cq))
	var stmt *C.sqlite3_stmt
	var tail *C.char
	rc := C.sqlite3_prepare_v2(d.ptr, cq, -1, &stmt, &tail)
	if rc != C.SQLITE_OK {
		return rows, 0, d.err(rc)
	}
	if stmt == nil {
		return rows, 0, errors.New("空SQL语句")
	}
	defer C.sqlite3_finalize(stmt)
	if strings.TrimSpace(C.GoString(tail)) != "" {
		return rows, 0, errors.New("只接受单条预编译语句")
	}
	if int(C.sqlite3_bind_parameter_count(stmt)) != len(args) {
		return rows, 0, errors.New("SQL参数数量不符")
	}
	for i, a := range args {
		idx := C.int(i + 1)
		switch v := a.(type) {
		case nil:
			rc = C.sqlite3_bind_null(stmt, idx)
		case string:
			p := C.CString(v)
			rc = C.bind_text_copy(stmt, idx, p, C.int(len(v)))
			C.free(unsafe.Pointer(p))
		case int:
			rc = C.sqlite3_bind_int64(stmt, idx, C.sqlite3_int64(v))
		case int64:
			rc = C.sqlite3_bind_int64(stmt, idx, C.sqlite3_int64(v))
		case interface{ SQLiteInt64() int64 }:
			rc = C.sqlite3_bind_int64(stmt, idx, C.sqlite3_int64(v.SQLiteInt64()))
		case bool:
			if v {
				rc = C.sqlite3_bind_int(stmt, idx, 1)
			} else {
				rc = C.sqlite3_bind_int(stmt, idx, 0)
			}
		default:
			return rows, 0, fmt.Errorf("不支持SQL参数类型 %T", a)
		}
		if rc != C.SQLITE_OK {
			return rows, 0, d.err(rc)
		}
	}
	for {
		rc = C.sqlite3_step(stmt)
		if rc == C.SQLITE_DONE {
			break
		}
		if rc != C.SQLITE_ROW {
			return rows, 0, d.err(rc)
		}
		row := Row{}
		for i := C.int(0); i < C.sqlite3_column_count(stmt); i++ {
			name := C.GoString(C.sqlite3_column_name(stmt, i))
			p := C.sqlite3_column_text(stmt, i)
			n := C.sqlite3_column_bytes(stmt, i)
			if p == nil {
				row[name] = ""
			} else {
				row[name] = C.GoStringN((*C.char)(unsafe.Pointer(p)), n)
			}
		}
		rows = append(rows, row)
	}
	return rows, int64(C.sqlite3_changes(d.ptr)), nil
}
