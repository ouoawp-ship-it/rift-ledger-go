package app

import (
	"fmt"
	"riftledger/internal/sqlite"
)

var schema = []string{
	`CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY,value TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS settings (id INTEGER PRIMARY KEY CHECK(id=1),version INTEGER NOT NULL,rules TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS accounts (id TEXT PRIMARY KEY,name TEXT NOT NULL,role TEXT NOT NULL CHECK(role IN ('player','house','fees','external')),telegram_id INTEGER UNIQUE,enabled INTEGER NOT NULL DEFAULT 0,balance INTEGER NOT NULL DEFAULT 0,locked INTEGER NOT NULL DEFAULT 0,created_at INTEGER NOT NULL,CHECK(locked>=0),CHECK(role='external' OR (balance>=locked AND balance>=0)))`,
	`CREATE TABLE IF NOT EXISTS rounds (id TEXT PRIMARY KEY,number TEXT NOT NULL UNIQUE,state TEXT NOT NULL CHECK(state IN ('DRAFT','OPEN','CLOSED','SETTLED','VOID')),banker INTEGER NOT NULL DEFAULT 0,heroes TEXT NOT NULL,rules TEXT NOT NULL,rules_version INTEGER NOT NULL,revision INTEGER NOT NULL DEFAULT 1,created_at INTEGER NOT NULL,opened_at INTEGER NOT NULL DEFAULT 0,settled_at INTEGER NOT NULL DEFAULT 0,next_round_id TEXT NOT NULL DEFAULT '',result TEXT NOT NULL DEFAULT '')`,
	`CREATE UNIQUE INDEX IF NOT EXISTS one_active_round ON rounds((1)) WHERE state IN ('DRAFT','OPEN','CLOSED')`,
	`CREATE TABLE IF NOT EXISTS bets (id TEXT PRIMARY KEY,round_id TEXT NOT NULL REFERENCES rounds(id),account_id TEXT NOT NULL REFERENCES accounts(id),position INTEGER NOT NULL CHECK(position BETWEEN 1 AND 5),stake INTEGER NOT NULL CHECK(stake>0),fee INTEGER NOT NULL CHECK(fee>=0),state TEXT NOT NULL CHECK(state IN ('RESERVED','WIN','LOSS','VOID')),game_delta INTEGER NOT NULL DEFAULT 0,net_delta INTEGER NOT NULL DEFAULT 0,created_at INTEGER NOT NULL,settled_at INTEGER NOT NULL DEFAULT 0)`,
	`CREATE INDEX IF NOT EXISTS bets_round ON bets(round_id)`,
	`CREATE INDEX IF NOT EXISTS bets_account ON bets(account_id,created_at DESC)`,
	`CREATE TABLE IF NOT EXISTS entries (id INTEGER PRIMARY KEY AUTOINCREMENT,batch TEXT NOT NULL,account_id TEXT NOT NULL REFERENCES accounts(id),round_id TEXT NOT NULL DEFAULT '',bet_id TEXT NOT NULL DEFAULT '',kind TEXT NOT NULL,delta INTEGER NOT NULL,balance_after INTEGER NOT NULL,note TEXT NOT NULL,created_at INTEGER NOT NULL,UNIQUE(batch,account_id))`,
	`CREATE INDEX IF NOT EXISTS entries_account ON entries(account_id,created_at DESC)`,
	`CREATE TRIGGER IF NOT EXISTS entries_no_update BEFORE UPDATE ON entries BEGIN SELECT RAISE(ABORT,'immutable ledger'); END`,
	`CREATE TRIGGER IF NOT EXISTS entries_no_delete BEFORE DELETE ON entries BEGIN SELECT RAISE(ABORT,'immutable ledger'); END`,
	`CREATE TABLE IF NOT EXISTS idempotency (key TEXT PRIMARY KEY,fingerprint TEXT NOT NULL,response TEXT NOT NULL,created_at INTEGER NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS audit (id INTEGER PRIMARY KEY AUTOINCREMENT,actor TEXT NOT NULL,action TEXT NOT NULL,request_key TEXT NOT NULL,detail TEXT NOT NULL,created_at INTEGER NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS tg_updates (id INTEGER PRIMARY KEY,created_at INTEGER NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS cards (key TEXT PRIMARY KEY,message_id INTEGER NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS outbox (id INTEGER PRIMARY KEY AUTOINCREMENT,key TEXT NOT NULL UNIQUE,chat_id INTEGER NOT NULL,card_key TEXT NOT NULL DEFAULT '',payload TEXT NOT NULL,state TEXT NOT NULL DEFAULT 'PENDING' CHECK(state IN ('PENDING','INFLIGHT','SENT','FAILED','UNKNOWN')),attempts INTEGER NOT NULL DEFAULT 0,next_at INTEGER NOT NULL DEFAULT 0,last_error TEXT NOT NULL DEFAULT '',message_id INTEGER NOT NULL DEFAULT 0,created_at INTEGER NOT NULL)`,
	`CREATE INDEX IF NOT EXISTS outbox_pending ON outbox(state,next_at,id)`,
}

func initialize(db *sqlite.DB) error {
	return db.Transaction(func(tx *sqlite.Tx) error {
		for _, q := range schema {
			if _, e := tx.Exec(q); e != nil {
				return e
			}
		}
		v, e := tx.One("SELECT value FROM meta WHERE key='schema_version'")
		if e != nil {
			return e
		}
		if v != nil && v["value"] != "1" && v["value"] != "2" {
			return fmt.Errorf("数据库版本不匹配，拒绝自动降级")
		}
		if _, e = tx.Exec("INSERT OR IGNORE INTO meta(key,value) VALUES('schema_version','1')"); e != nil {
			return e
		}
		if v == nil || v["value"] == "1" {
			for _, q := range migration2 {
				if _, e = tx.Exec(q); e != nil {
					return fmt.Errorf("migration 2: %w", e)
				}
			}
			if _, e = tx.Exec("UPDATE meta SET value='2' WHERE key='schema_version'"); e != nil {
				return e
			}
		}
		if _, e = tx.Exec("INSERT OR IGNORE INTO settings(id,version,rules) VALUES(1,1,?)", asJSON(DefaultRules())); e != nil {
			return e
		}
		for _, a := range []struct{ id, name, role string }{{"house", "群主／运营方", "house"}, {"fees", "独立费用账户", "fees"}, {"external", "人工调分对手账户", "external"}} {
			if _, e = tx.Exec("INSERT OR IGNORE INTO accounts(id,name,role,enabled,created_at) VALUES(?,?,?,1,?)", a.id, a.name, a.role, now()); e != nil {
				return e
			}
		}
		// An interrupted network send is NOT blindly sent a second time.
		_, e = tx.Exec("UPDATE outbox SET state='UNKNOWN',last_error='进程重启时发现发送中任务；请核实Telegram后处理' WHERE state='INFLIGHT'")
		return e
	})
}

// Version 2 is additive and runs in the same transaction as its version marker.
var migration2 = []string{
	`CREATE TABLE telegram_users (telegram_user_id INTEGER PRIMARY KEY REFERENCES accounts(telegram_id), username TEXT NOT NULL, first_name TEXT NOT NULL, last_name TEXT NOT NULL, first_contact INTEGER NOT NULL, last_contact INTEGER NOT NULL)`,
	`CREATE TABLE balance_requests (id INTEGER PRIMARY KEY AUTOINCREMENT, update_id INTEGER NOT NULL UNIQUE, account_id TEXT NOT NULL REFERENCES accounts(id), kind TEXT NOT NULL CHECK(kind IN ('CREDIT','DEBIT')), amount INTEGER NOT NULL CHECK(amount>0 AND amount<=1000000000000), balance_at_request INTEGER NOT NULL, state TEXT NOT NULL DEFAULT 'PENDING' CHECK(state IN ('PENDING','APPROVED','REJECTED')), created_at INTEGER NOT NULL, processed_at INTEGER NOT NULL DEFAULT 0, note TEXT NOT NULL DEFAULT '')`,
	`CREATE INDEX balance_requests_pending ON balance_requests(state,id)`,
	`ALTER TABLE outbox ADD COLUMN media_result TEXT NOT NULL DEFAULT ''`,
}
