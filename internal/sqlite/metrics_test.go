package sqlite

import (
	"errors"
	"testing"
	"time"
)

func TestMetricsRecordContentionAndRollback(t *testing.T) {
	db := testDB(t)
	locked := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- db.Transaction(func(tx *Tx) error { close(locked); <-release; return errors.New("rollback") })
	}()
	<-locked
	readDone := make(chan error, 1)
	go func() { _, e := db.Query("SELECT 1"); readDone <- e }()
	time.Sleep(20 * time.Millisecond)
	close(release)
	if <-done == nil {
		t.Fatal("missing rollback error")
	}
	if e := <-readDone; e != nil {
		t.Fatal(e)
	}
	m := db.Metrics()
	if m.Operations < 5 || m.MaxWorkMS < 15 || m.WorkMS < 15 || m.WaitMS < 0 {
		t.Fatal(m)
	}
	// A rolled-back operation must still release the mutex and report its duration.
	if _, e := db.Query("SELECT 2"); e != nil {
		t.Fatal(e)
	}
}
