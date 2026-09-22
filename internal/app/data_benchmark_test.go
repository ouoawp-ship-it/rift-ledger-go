package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"riftledger/internal/sqlite"
)

// Diagnostic fixture, never a live DB. Financial acceptance is tested separately.
func BenchmarkDataLayer(b *testing.B) {
	players := 1000
	if raw := os.Getenv("RIFT_BENCH_PLAYERS"); raw != "" {
		var err error
		players, err = strconv.Atoi(raw)
		if err != nil || players < 100 || players > 10000 {
			b.Fatal("RIFT_BENCH_PLAYERS must be 100..10000")
		}
	}
	db, err := sqlite.Open(filepath.Join(b.TempDir(), "scale.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	s, err := New(db, RuntimeConfig{})
	if err != nil {
		b.Fatal(err)
	}
	if err = s.EnablePlayerOnly(); err != nil {
		b.Fatal(err)
	}
	err = db.Transaction(func(tx *sqlite.Tx) error {
		queries := []string{
			fmt.Sprintf(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<%d) INSERT INTO accounts(id,name,role,telegram_id,enabled,balance,locked,created_at) SELECT 'p'||x,'synthetic '||x,'player',x,1,9800000,100000,x FROM n`, players),
			`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<100) INSERT INTO rounds(id,number,state,banker,heroes,rules,rules_version,created_at,settled_at) SELECT 'r'||x,'round'||x,'SETTLED',1,'[]','{}',1,x,x FROM n`,
			`INSERT INTO bets(id,round_id,account_id,position,stake,fee,state,game_delta,net_delta,created_at,settled_at) SELECT a.id||':'||r.id,r.id,a.id,2,100000,0,'WIN',98000,98000,r.created_at,r.settled_at FROM accounts a CROSS JOIN rounds r`,
			fmt.Sprintf(`INSERT INTO entries(batch,account_id,round_id,bet_id,kind,delta,balance_after,note,created_at) SELECT 'game:'||b.id,b.account_id,b.round_id,b.id,'GAME',98000,b.settled_at*98000,'synthetic',%d FROM bets b`, now()),
			`INSERT INTO rounds(id,number,state,banker,heroes,rules,rules_version,created_at) VALUES('active','active','OPEN',1,'[]','{}',1,101)`,
			`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<10) INSERT INTO bets(id,round_id,account_id,position,stake,fee,state,created_at) SELECT 'active:'||a.id||':'||n.x,'active',a.id,2,10000,0,'RESERVED',n.x FROM accounts a CROSS JOIN n`,
			`INSERT INTO outbox(key,chat_id,payload,state,created_at) SELECT id,1,'{"chat_id":1,"text":"synthetic history"}','SENT',1 FROM bets WHERE state='WIN'`,
		}
		for _, q := range queries {
			if _, e := tx.Exec(q); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
	b.Logf("players=%d settled_bets=%d active_bets=%d entries=%d sent_outbox=%d", players, players*100, players*10, players*100, players*100)
	for _, c := range []struct {
		name string
		run  func() error
	}{
		{"state", func() error { _, e := s.State(); return e }},
		{"players", func() error { _, e := s.PlayerSummary(50, 0); return e }},
		{"entries", func() error { _, e := s.Rows("entries", "p1", 50, 0); return e }},
		{"history", func() error { _, e := s.History(50, 0); return e }},
		{"queue", func() error { _, e := s.Operations(); return e }},
		{"reconcile", func() error { _, e := s.Reconcile(); return e }},
	} {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if e := c.run(); e != nil {
					b.Fatal(e)
				}
			}
		})
	}
}
