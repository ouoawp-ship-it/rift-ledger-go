package app

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"riftledger/internal/sqlite"
)

func TestStateOnlyReturnsLatestTwoHundredBetsInOriginalOrder(t *testing.T) {
	s := reliabilityService(t)
	if e := s.EnablePlayerOnly(); e != nil {
		t.Fatal(e)
	}
	e := s.DB.Transaction(func(tx *sqlite.Tx) error {
		for _, q := range []string{
			`INSERT INTO accounts(id,name,role,created_at) VALUES('p','p','player',1)`,
			`INSERT INTO rounds(id,number,state,heroes,rules,rules_version,created_at) VALUES('active','active','OPEN','[]','{}',1,1)`,
			`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<450) INSERT INTO bets(id,round_id,account_id,position,stake,fee,state,created_at) SELECT printf('b%04d',x),'active','p',2,1000,0,'RESERVED',x/10 FROM n`,
		} {
			if _, e := tx.Exec(q); e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	v, e := s.State()
	if e != nil {
		t.Fatal(e)
	}
	bets := v.(map[string]any)["bets"].([]Bet)
	if len(bets) != 200 {
		t.Fatal(len(bets))
	}
	for i, b := range bets {
		if b.ID != fmt.Sprintf("b%04d", i+251) {
			t.Fatalf("wrong tail/order at %d: %s", i, b.ID)
		}
	}
}

func TestDailyTotalsBoundaryRollbackAndMigration(t *testing.T) {
	s := reliabilityService(t)
	if _, e := s.DB.Exec(`INSERT INTO accounts(id,name,role,created_at) VALUES('p','p','player',1)`); e != nil {
		t.Fatal(e)
	}
	insert := func(tx *sqlite.Tx, batch, kind string, at, delta int64) error {
		_, e := tx.Exec(`INSERT INTO entries(batch,account_id,kind,delta,balance_after,note,created_at) VALUES(?,'p',?,?,0,'',?)`, batch, kind, delta, at)
		return e
	}
	for _, x := range []struct {
		key, kind string
		at, delta int64
	}{{"a", "GAME", 57599, 1001}, {"b", "GAME", 57600, -333}, {"c", "ADJUST", 57601, 9999}, {"d", "GAME", 57601, 100}} {
		if e := s.DB.Transaction(func(tx *sqlite.Tx) error { return insert(tx, x.key, x.kind, x.at, x.delta) }); e != nil {
			t.Fatal(e)
		}
	}
	err := s.DB.Transaction(func(tx *sqlite.Tx) error {
		if e := insert(tx, "rollback", "GAME", 57602, 999); e != nil {
			return e
		}
		return errors.New("rollback")
	})
	if err == nil {
		t.Fatal("expected rollback")
	}
	err = s.DB.Transaction(func(tx *sqlite.Tx) error { return insert(tx, "b", "GAME", 57600, 999) })
	if err == nil {
		t.Fatal("duplicate entry accepted")
	}
	before, e := s.DB.Query("SELECT day,delta FROM daily_game_totals ORDER BY day")
	if e != nil || len(before) != 2 || before[0].Int("delta") != 1001 || before[1].Int("delta") != -233 {
		t.Fatal(before, e)
	}
	// Simulate a genuine v5 DB: it has neither the summary table nor its trigger.
	for _, q := range []string{"DROP TRIGGER entries_daily_game", "DROP TABLE daily_game_totals", "UPDATE meta SET value='5' WHERE key='schema_version'"} {
		if _, e = s.DB.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	for i := 0; i < 2; i++ {
		if _, e = New(s.DB, RuntimeConfig{}); e != nil {
			t.Fatal(e)
		}
	}
	after, e := s.DB.Query("SELECT day,delta FROM daily_game_totals ORDER BY day")
	if e != nil || asJSON(before) != asJSON(after) {
		t.Fatal("backfill changed amounts", after, e)
	}
	if e = s.DB.Read(func(tx *sqlite.Tx) error {
		n, e := dailyTotalIssues(tx)
		if n != 0 {
			t.Fatal(n)
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
}

func TestOfflineBackupVerificationPreservesQueueAndDetectsCorruption(t *testing.T) {
	s := reliabilityService(t)
	if e := s.EnablePlayerOnly(); e != nil {
		t.Fatal(e)
	}
	exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.SetPlayer(tx, 111, "test", true) })
	exec(t, s, func(tx *sqlite.Tx) (any, error) {
		return s.Adjust(tx, "tg:111", Money(1000199), "seed", "restore-credit")
	})
	exec(t, s, func(tx *sqlite.Tx) (any, error) {
		return nil, playerGameDelta(tx, "tg:111", Money(392004), "game-test", "", "")
	})
	for _, q := range []string{
		`INSERT INTO outbox(key,chat_id,payload,state,created_at) VALUES('pending',111,'{}','PENDING',1),('inflight',111,'{}','INFLIGHT',1)`,
		`INSERT INTO meta(key,value) VALUES('tg_offset','987')`,
	} {
		if _, e := s.DB.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	backup := filepath.Join(t.TempDir(), "backup.db")
	if _, e := s.DB.Exec("VACUUM INTO ?", backup); e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(backup)
	if e != nil {
		t.Fatal(e)
	}
	r, e := sqlite.OpenReadOnly(backup)
	if e != nil {
		t.Fatal(e)
	}
	report, e := InspectDatabase(r)
	if e != nil || report["ok"] != true {
		t.Fatal(report, e)
	}
	rows, e := r.Query("SELECT state FROM outbox WHERE key='inflight'")
	if e != nil || rows[0]["state"] != "INFLIGHT" {
		t.Fatal("verification ran startup recovery", rows, e)
	}
	r.Close()
	after, e := os.ReadFile(backup)
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("verification modified backup", e)
	}
	w, e := sqlite.Open(backup)
	if e != nil {
		t.Fatal(e)
	}
	defer w.Close()
	if _, e = w.Exec("UPDATE daily_game_totals SET delta=delta+1"); e != nil {
		t.Fatal(e)
	}
	r, e = sqlite.OpenReadOnly(backup)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	report, e = InspectDatabase(r)
	if e != nil || report["ok"] != false || report["daily_total_issues"] != int64(1) {
		t.Fatal("bad aggregate missed", report, e)
	}
	for _, q := range []string{"UPDATE daily_game_totals SET delta=delta-1", "DROP TRIGGER entries_no_update", "UPDATE entries SET balance_after=balance_after+1 WHERE kind='ADJUST'"} {
		if _, e = w.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	report, e = InspectDatabase(r)
	if e != nil || report["ok"] != false || report["ledger_chain_issues"] != int64(1) {
		t.Fatal("historical chain corruption missed", report, e)
	}
}

func TestDailyMigrationFailureRollsBackSchemaAndSummary(t *testing.T) {
	s := reliabilityService(t)
	for _, q := range []string{"DROP TRIGGER entries_daily_game", "DROP TABLE daily_game_totals", "UPDATE meta SET value='5' WHERE key='schema_version'", `CREATE TRIGGER entries_daily_game AFTER INSERT ON accounts BEGIN SELECT 1; END`} {
		if _, e := s.DB.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := New(s.DB, RuntimeConfig{}); e == nil {
		t.Fatal("expected migration trigger collision")
	}
	rows, e := s.DB.Query("SELECT value FROM meta WHERE key='schema_version'")
	if e != nil || rows[0]["value"] != "5" {
		t.Fatal("failed migration changed version", rows, e)
	}
	rows, e = s.DB.Query("SELECT name FROM sqlite_master WHERE name='daily_game_totals'")
	if e != nil || len(rows) != 0 {
		t.Fatal("partial migration left a summary", rows, e)
	}
	if _, e = s.DB.Exec("DROP TRIGGER entries_daily_game"); e != nil {
		t.Fatal(e)
	}
	if _, e = New(s.DB, RuntimeConfig{}); e != nil {
		t.Fatal(e)
	}
}
