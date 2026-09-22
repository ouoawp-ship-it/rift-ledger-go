package app

import "riftledger/internal/sqlite"

// Derived data only: immutable entries remain authoritative. Each game's daily
// total is updated by SQLite in the very same transaction as its ledger entry.
// Day buckets use Beijing time, matching the admin's existing statistics.
func migrateDailyTotals(tx *sqlite.Tx) error {
	for _, q := range []string{
		`CREATE TABLE daily_game_totals (day INTEGER NOT NULL,account_id TEXT NOT NULL REFERENCES accounts(id),delta INTEGER NOT NULL,PRIMARY KEY(day,account_id)) WITHOUT ROWID`,
		`INSERT INTO daily_game_totals(day,account_id,delta) SELECT (created_at+28800)/86400,account_id,SUM(delta) FROM entries WHERE kind='GAME' GROUP BY (created_at+28800)/86400,account_id`,
		`CREATE TRIGGER entries_daily_game AFTER INSERT ON entries WHEN NEW.kind='GAME' BEGIN INSERT INTO daily_game_totals(day,account_id,delta) VALUES((NEW.created_at+28800)/86400,NEW.account_id,NEW.delta) ON CONFLICT(day,account_id) DO UPDATE SET delta=delta+excluded.delta; END`,
	} {
		if _, e := tx.Exec(q); e != nil {
			return e
		}
	}
	return nil
}

func dailyTotalIssues(tx *sqlite.Tx) (int64, error) {
	r, e := tx.One(`WITH truth AS (SELECT (created_at+28800)/86400 AS day,account_id,SUM(delta) AS delta FROM entries WHERE kind='GAME' GROUP BY (created_at+28800)/86400,account_id), differences AS (
	 SELECT t.day,t.account_id FROM truth t LEFT JOIN daily_game_totals d ON d.day=t.day AND d.account_id=t.account_id WHERE d.account_id IS NULL OR d.delta!=t.delta
	 UNION ALL SELECT d.day,d.account_id FROM daily_game_totals d LEFT JOIN truth t ON t.day=d.day AND t.account_id=d.account_id WHERE t.account_id IS NULL)
	 SELECT COUNT(*) AS n FROM differences`)
	if e != nil {
		return 0, e
	}
	return r.Int("n"), nil
}
