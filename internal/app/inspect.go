package app

import (
	"fmt"
	"riftledger/internal/sqlite"
)

// InspectDatabase verifies a restored snapshot without New(), migrations, HTTP,
// Telegram, or recovery writes. The caller must use sqlite.OpenBackup for an
// immutable snapshot or OpenReadOnly for a live snapshot with its WAL present.
func InspectDatabase(db *sqlite.DB) (map[string]any, error) {
	out := map[string]any{"ok": false}
	err := db.Snapshot(func(tx *sqlite.Tx) error {
		v, e := tx.One("SELECT value FROM meta WHERE key='schema_version'")
		if e != nil {
			return e
		}
		if v == nil || (v["value"] != "5" && v["value"] != "6") {
			return fmt.Errorf("仅验证版本5或6的备份；旧库请先在隔离副本完成迁移")
		}
		mode, e := tx.One("SELECT value FROM meta WHERE key='balance_mode'")
		if e != nil {
			return e
		}
		playerOnly := mode != nil && mode["value"] == "player_only"
		if mode != nil && !playerOnly {
			return fmt.Errorf("未知账本模式，拒绝猜测")
		}
		out["schema_version"] = v["value"]
		out["player_only"] = playerOnly
		integrity, e := tx.Query("PRAGMA integrity_check")
		if e != nil {
			return e
		}
		valid := len(integrity) == 1 && integrity[0]["integrity_check"] == "ok"
		out["integrity_ok"] = valid
		foreign, e := tx.Query("PRAGMA foreign_key_check")
		if e != nil {
			return e
		}
		out["foreign_key_issues"] = len(foreign)
		counts := map[string]int64{}
		for _, table := range []string{"accounts", "rounds", "bets", "entries", "outbox", "idempotency", "tg_updates", "message_templates", "message_images", "group_permissions"} {
			r, e := tx.One("SELECT COUNT(*) AS n FROM " + table)
			if e != nil {
				return e
			}
			counts[table] = r.Int("n")
		}
		out["counts"] = counts
		ledger, e := reconcileTx(tx, playerOnly)
		if e != nil {
			return e
		}
		out["ledger"] = ledger
		// A correct final sum alone cannot detect a corrupt historical balance_after.
		chain, e := tx.One(`SELECT COUNT(*) AS n FROM (SELECT balance_after,SUM(delta) OVER (PARTITION BY account_id ORDER BY id ROWS UNBOUNDED PRECEDING) AS expected FROM entries) WHERE balance_after!=expected`)
		if e != nil {
			return e
		}
		out["ledger_chain_issues"] = chain.Int("n")
		dailyIssues := int64(0)
		if v["value"] == "6" {
			trigger, e := tx.One("SELECT name FROM sqlite_master WHERE type='trigger' AND name='entries_daily_game'")
			if e != nil {
				return e
			}
			if trigger == nil {
				return fmt.Errorf("备份缺少日统计维护触发器")
			}
			dailyIssues, e = dailyTotalIssues(tx)
			if e != nil {
				return e
			}
			out["daily_total_issues"] = dailyIssues
		}
		queue, e := tx.Query("SELECT state,COUNT(*) AS count FROM outbox GROUP BY state")
		if e != nil {
			return e
		}
		out["queue"] = queue
		offset, e := tx.One("SELECT value FROM meta WHERE key='tg_offset'")
		if e != nil {
			return e
		}
		out["telegram_offset"] = offset
		page, e := tx.One("PRAGMA page_count")
		if e != nil {
			return e
		}
		size, e := tx.One("PRAGMA page_size")
		if e != nil {
			return e
		}
		free, e := tx.One("PRAGMA freelist_count")
		if e != nil {
			return e
		}
		out["database_bytes"] = page.Int("page_count") * size.Int("page_size")
		out["free_bytes"] = free.Int("freelist_count") * size.Int("page_size")
		out["ok"] = valid && len(foreign) == 0 && chain.Int("n") == 0 && dailyIssues == 0 && ledger.(map[string]any)["balanced"] == true
		return nil
	})
	return out, err
}
