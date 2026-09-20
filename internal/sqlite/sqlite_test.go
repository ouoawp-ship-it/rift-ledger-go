package sqlite

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func testDB(t *testing.T) *DB {
	t.Helper()
	d, e := Open(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { d.Close() })
	return d
}
func TestPreparedStatementsAndNullBytes(t *testing.T) {
	d := testDB(t)
	if _, e := d.Exec("CREATE TABLE texts (v TEXT)"); e != nil {
		t.Fatal(e)
	}
	value := "中文\x00'; DROP TABLE texts; --"
	if _, e := d.Exec("INSERT INTO texts(v) VALUES(?)", value); e != nil {
		t.Fatal(e)
	}
	rows, e := d.Query("SELECT v FROM texts")
	if e != nil || len(rows) != 1 || rows[0]["v"] != value {
		t.Fatalf("%+v %v", rows, e)
	}
	if _, e = d.Exec("DELETE FROM texts; DELETE FROM texts"); e == nil {
		t.Fatal("multi-statement accepted")
	}
	if _, e = d.Exec("SELECT ?"); e == nil {
		t.Fatal("parameter mismatch accepted")
	}
}
func TestRollbackIncludingPanic(t *testing.T) {
	d := testDB(t)
	d.Exec("CREATE TABLE x (v INTEGER)")
	e := d.Transaction(func(tx *Tx) error { tx.Exec("INSERT INTO x VALUES(1)"); return errors.New("abort") })
	if e == nil {
		t.Fatal("no rollback")
	}
	func() {
		defer func() { recover() }()
		d.Transaction(func(tx *Tx) error { tx.Exec("INSERT INTO x VALUES(2)"); panic("abort") })
	}()
	rows, e := d.Query("SELECT * FROM x")
	if e != nil || len(rows) != 0 {
		t.Fatalf("%+v %v", rows, e)
	}
}
func TestConcurrentTransactions(t *testing.T) {
	d := testDB(t)
	d.Exec("CREATE TABLE x (v INTEGER)")
	d.Exec("INSERT INTO x VALUES(0)")
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := d.Transaction(func(tx *Tx) error { _, e := tx.Exec("UPDATE x SET v=v+1"); return e }); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	r, e := d.Query("SELECT v FROM x")
	if e != nil || r[0].Int("v") != 100 {
		t.Fatalf("%+v %v", r, e)
	}
}
func TestPersistenceAndClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	d, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	d.Exec("CREATE TABLE x (v INTEGER)")
	d.Exec("INSERT INTO x VALUES(42)")
	d.Close()
	if _, e = d.Exec("SELECT 1"); e == nil {
		t.Fatal("closed DB accepted")
	}
	d, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	r, e := d.Query("SELECT v FROM x")
	if e != nil || r[0].Int("v") != 42 {
		t.Fatalf("%+v %v", r, e)
	}
}
