package app

import (
	"path/filepath"
	"riftledger/internal/sqlite"
	"testing"
)

func legacyMoneyDB(t *testing.T) *sqlite.DB {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	err = db.Transaction(func(tx *sqlite.Tx) error {
		for _, q := range append(append([]string{}, schema...), migration2...) {
			if _, err := tx.Exec(q); err != nil {
				return err
			}
		}
		rules := DefaultRules()
		rules.Confirmed = true
		queries := []string{
			`INSERT INTO meta VALUES('schema_version','2'),('balance_mode','player_only')`,
			`INSERT INTO accounts(id,name,role,telegram_id,enabled,balance,locked,created_at) VALUES('tg:111','legacy','player',111,1,1040,100,1)`,
			`INSERT INTO entries(batch,account_id,kind,delta,balance_after,note,created_at) VALUES('old-credit','tg:111','ADJUST',1000,1000,'',1),('old-game','tg:111','GAME',40,1040,'',2)`,
			`INSERT INTO balance_requests(update_id,account_id,kind,amount,balance_at_request,created_at) VALUES(1,'tg:111','DEBIT',100,1040,1)`,
		}
		for _, q := range queries {
			if _, err := tx.Exec(q); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`INSERT INTO settings VALUES(1,1,?)`, asJSON(rules)); err != nil {
			return err
		}
		for _, r := range []struct{ id, state, result string }{{"old-settled", "SETTLED", `{"round_id":"old-settled","player_game_delta":40,"lines":[{"stake":33,"game_delta":40,"net_delta":40}]}`}, {"old-open", "OPEN", ""}} {
			if _, err := tx.Exec(`INSERT INTO rounds(id,number,state,banker,heroes,rules,rules_version,created_at,result) VALUES(?,?,?,1,?,?,1,1,?)`, r.id, r.id, r.state, asJSON(heroes()), asJSON(rules), r.result); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`INSERT INTO bets(id,round_id,account_id,position,stake,fee,state,created_at) VALUES('open-bet','old-open','tg:111',2,100,0,'RESERVED',1)`); err != nil {
			return err
		}
		oldPayload := struct {
			Delta int64 `json:"delta"`
		}{1000}
		hash := fingerprint(struct {
			Action  string
			Payload any
		}{"adjust", oldPayload})
		_, err := tx.Exec(`INSERT INTO idempotency VALUES('legacy-credit-key',?,'{"balance":1000}',1)`, hash)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestMoneyMigrationPreservesLedgerSnapshotsAndReplay(t *testing.T) {
	db := legacyMoneyDB(t)
	before, _ := db.Query(`SELECT result FROM rounds WHERE id='old-settled'`)
	for i := 0; i < 2; i++ {
		s, err := New(db, RuntimeConfig{})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.EnablePlayerOnly(); err != nil {
			t.Fatal(err)
		}
		a := acc(t, s, "tg:111")
		if a.Balance != Points(1040) || a.Locked != Points(100) {
			t.Fatalf("migration %d: %+v", i, a)
		}
		balanced(t, s)
		r, err := s.ReadRound("old-settled")
		if err != nil || r.Result.PlayerGameDelta != Points(40) {
			t.Fatalf("history %+v %v", r, err)
		}
		after, _ := db.Query(`SELECT result FROM rounds WHERE id='old-settled'`)
		if before[0]["result"] != after[0]["result"] {
			t.Fatal("rewrote settled history")
		}
		response, err := s.Command("legacy-credit-key", "adjust", struct {
			Delta Money `json:"delta"`
		}{Points(1000)}, func(tx *sqlite.Tx) (any, error) { t.Fatal("replayed old credit"); return nil, nil })
		if err != nil || string(response) != `{"balance":1000}` {
			t.Fatalf("old replay %s %v", response, err)
		}
		rows, _ := db.Query("SELECT amount,balance_at_request FROM balance_requests")
		if rows[0].Int("amount") != 100000 || rows[0].Int("balance_at_request") != 1040000 {
			t.Fatal(rows)
		}
	}
	if err := db.Transaction(func(tx *sqlite.Tx) error { _, err := tx.Exec("UPDATE entries SET delta=0"); return err }); err == nil {
		t.Fatal("ledger update trigger lost")
	}
	if err := db.Transaction(func(tx *sqlite.Tx) error { _, err := tx.Exec("DELETE FROM entries"); return err }); err == nil {
		t.Fatal("ledger delete trigger lost")
	}
	v, _ := db.Query("SELECT value FROM meta WHERE key='schema_version'")
	if v[0]["value"] != "5" {
		t.Fatal(v)
	}
}

func TestMoneyMigrationFailureIsAtomic(t *testing.T) {
	db := legacyMoneyDB(t)
	if err := db.Transaction(func(tx *sqlite.Tx) error {
		_, err := tx.Exec("UPDATE accounts SET balance=1000000000001 WHERE id='tg:111'")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := New(db, RuntimeConfig{}); err == nil {
		t.Fatal("unsafe legacy amount accepted")
	}
	v, _ := db.Query("SELECT value FROM meta WHERE key='schema_version'")
	if v[0]["value"] != "2" {
		t.Fatal("advanced failed migration")
	}
	rows, _ := db.Query("SELECT amount FROM balance_requests")
	if rows[0].Int("amount") != 100 {
		t.Fatal("partial migration")
	}
	if err := db.Transaction(func(tx *sqlite.Tx) error { _, err := tx.Exec("UPDATE entries SET delta=0"); return err }); err == nil {
		t.Fatal("lost ledger protection on rollback")
	}
}
