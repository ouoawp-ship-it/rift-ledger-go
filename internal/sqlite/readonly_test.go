package sqlite

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadOnlyMissingDoesNotCreate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	if db, err := OpenReadOnly(path); err == nil {
		db.Close()
		t.Fatal("missing database accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("read-only open created a file")
	}
}

func TestReadOnlySnapshotAndWriteRejection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.db")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err = w.Exec("CREATE TABLE x(v INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if _, err = w.Exec("INSERT INTO x VALUES(1)"); err != nil {
		t.Fatal(err)
	}
	r, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err = r.Exec("UPDATE x SET v=9"); err == nil {
		t.Fatal("read-only DB allowed a write")
	}
	err = r.Snapshot(func(tx *Tx) error {
		first, e := tx.One("SELECT v FROM x")
		if e != nil {
			return e
		}
		if _, e = w.Exec("UPDATE x SET v=2"); e != nil {
			return e
		}
		second, e := tx.One("SELECT v FROM x")
		if e != nil {
			return e
		}
		if first.Int("v") != 1 || second.Int("v") != 1 {
			t.Fatal("inconsistent snapshot")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := r.Query("SELECT v FROM x")
	if err != nil || rows[0].Int("v") != 2 {
		t.Fatalf("snapshot never released: %v %v", rows, err)
	}
}

func TestBackupReadOnlyWithoutSidecars(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot ?# 中文.db")
	w, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = w.Exec("CREATE TABLE x(v INTEGER)"); e != nil {
		t.Fatal(e)
	}
	if _, e = w.Exec("INSERT INTO x VALUES(7)"); e != nil {
		t.Fatal(e)
	}
	if db, e := OpenBackup(path); e == nil {
		db.Close()
		t.Fatal("accepted live WAL")
	}
	if e = w.Close(); e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(path, 0400); e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(dir, 0500); e != nil {
		t.Fatal(e)
	}
	defer os.Chmod(dir, 0700)
	r, e := OpenBackup(path)
	if e != nil {
		t.Fatal(e)
	}
	rows, e := r.Query("SELECT v FROM x")
	if e != nil || rows[0].Int("v") != 7 {
		t.Fatal(rows, e)
	}
	if _, e = r.Exec("UPDATE x SET v=8"); e == nil {
		t.Fatal("backup write succeeded")
	}
	r.Close()
	after, e := os.ReadFile(path)
	if e != nil || string(after) != string(before) {
		t.Fatal("backup bytes changed", e)
	}
	files, e := os.ReadDir(dir)
	if e != nil || len(files) != 1 {
		t.Fatal("sidecars created", files, e)
	}
}
