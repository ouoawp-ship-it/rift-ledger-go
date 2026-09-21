package app

import (
	"path/filepath"
	"riftledger/internal/sqlite"
	"testing"
)

// Synthetic isolated data only. No network, live token, or production database.
func BenchmarkPlayerSummary(b *testing.B) {
	db, e := sqlite.Open(filepath.Join(b.TempDir(), "bench.db"))
	if e != nil {
		b.Fatal(e)
	}
	defer db.Close()
	s, e := New(db, RuntimeConfig{})
	if e != nil {
		b.Fatal(e)
	}
	queries := []string{
		`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<500) INSERT INTO accounts(id,name,role,telegram_id,enabled,balance,created_at) SELECT 'p'||x,'synthetic '||x,'player',x,1,1000000,x FROM n`,
		`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<100) INSERT INTO rounds(id,number,state,banker,heroes,rules,rules_version,created_at,settled_at) SELECT 'r'||x,'round'||x,'SETTLED',1,'[]','{}',1,x,x FROM n`,
		`INSERT INTO bets(id,round_id,account_id,position,stake,fee,state,game_delta,net_delta,created_at,settled_at) SELECT a.id||':'||r.id,r.id,a.id,2,100000,0,'WIN',98000,98000,r.created_at,r.settled_at FROM accounts a CROSS JOIN rounds r WHERE a.role='player'`,
	}
	for _, q := range queries {
		if _, e = db.Exec(q); e != nil {
			b.Fatal(e)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, e = s.PlayerSummary(50, 0); e != nil {
			b.Fatal(e)
		}
	}
}
