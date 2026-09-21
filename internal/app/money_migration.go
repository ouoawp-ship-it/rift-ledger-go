package app

import (
	"fmt"
	"riftledger/internal/sqlite"
	"strings"
)

// The caller owns the initialization transaction. JSON uses public point units
// and must not be scaled: historical results and idempotency responses stay exact.
func migrateMoney(tx *sqlite.Tx) error {
	tables := []struct {
		name    string
		columns []string
	}{
		{"accounts", []string{"balance", "locked"}},
		{"bets", []string{"stake", "fee", "game_delta", "net_delta"}},
		{"entries", []string{"delta", "balance_after"}},
		{"balance_requests", []string{"amount", "balance_at_request"}},
	}
	for _, table := range tables {
		for _, col := range table.columns {
			row, err := tx.One("SELECT COUNT(*) AS n FROM " + table.name + " WHERE typeof(" + col + ")!='integer' OR " + col + " < -1000000000000 OR " + col + " > 1000000000000")
			if err != nil {
				return err
			}
			if row.Int("n") != 0 {
				return fmt.Errorf("%s.%s 存在无法安全升级的金额", table.name, col)
			}
		}
	}
	if _, err := tx.Exec("DROP TRIGGER entries_no_update"); err != nil {
		return err
	}
	for _, table := range tables[:3] {
		sets := []string{}
		for _, col := range table.columns {
			sets = append(sets, col+"="+col+"*1000")
		}
		if _, err := tx.Exec("UPDATE " + table.name + " SET " + strings.Join(sets, ",")); err != nil {
			return err
		}
	}
	queries := []string{
		`CREATE TRIGGER entries_no_update BEFORE UPDATE ON entries BEGIN SELECT RAISE(ABORT,'immutable ledger'); END`,
		`CREATE TABLE balance_requests_v3 (id INTEGER PRIMARY KEY AUTOINCREMENT, update_id INTEGER NOT NULL UNIQUE, account_id TEXT NOT NULL REFERENCES accounts(id), kind TEXT NOT NULL CHECK(kind IN ('CREDIT','DEBIT')), amount INTEGER NOT NULL CHECK(amount>0 AND amount<=1000000000000000), balance_at_request INTEGER NOT NULL, state TEXT NOT NULL DEFAULT 'PENDING' CHECK(state IN ('PENDING','APPROVED','REJECTED')), created_at INTEGER NOT NULL, processed_at INTEGER NOT NULL DEFAULT 0, note TEXT NOT NULL DEFAULT '')`,
		`INSERT INTO balance_requests_v3 SELECT id,update_id,account_id,kind,amount*1000,balance_at_request*1000,state,created_at,processed_at,note FROM balance_requests`,
		`DROP TABLE balance_requests`,
		`ALTER TABLE balance_requests_v3 RENAME TO balance_requests`,
		`CREATE INDEX balance_requests_pending ON balance_requests(state,id)`,
		`UPDATE meta SET value='3' WHERE key='schema_version'`,
	}
	for _, query := range queries {
		if _, err := tx.Exec(query); err != nil {
			return err
		}
	}
	return nil
}
