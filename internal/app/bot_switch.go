package app

import (
	"fmt"
	"riftledger/internal/sqlite"
)

// BindBot runs before the receiver and send workers start. Config changes use
// a graceful process restart, so the previous bot's workers have already exited.
// Business data is shared; transport state is saved independently for each bot.
func (s *Service) BindBot(botID int64) error {
	if botID <= 0 {
		return bad("无效机器人ID")
	}
	return s.DB.Transaction(func(tx *sqlite.Tx) error {
		current, err := tx.One("SELECT CAST(value AS INTEGER) AS id FROM meta WHERE key='bot_id'")
		if err != nil {
			return err
		}
		oldID := botID
		if current != nil {
			oldID = current.Int("id")
		}
		legacy, err := tx.One("SELECT bot_id FROM bot_sessions WHERE bot_id=?", oldID)
		if err != nil {
			return err
		}
		if legacy == nil {
			// Adopt pre-upgrade transport records once, without deleting any history.
			for _, q := range []string{
				"UPDATE bot_update_ids SET bot_id=? WHERE bot_id=0",
				"INSERT OR IGNORE INTO bot_update_ids(id,bot_id,update_id) SELECT id,?,id FROM tg_updates",
				"INSERT OR IGNORE INTO bot_update_ids(id,bot_id,update_id) SELECT update_id,?,update_id FROM balance_requests",
				"INSERT OR IGNORE INTO outbox_bots(outbox_id,bot_id) SELECT id,? FROM outbox",
				"UPDATE outbox_bots SET bot_id=? WHERE bot_id=0",
			} {
				if _, err = tx.Exec(q, oldID); err != nil {
					return err
				}
			}
		}
		if _, err = tx.Exec(`INSERT INTO bot_sessions(bot_id,offset,pause_until) VALUES(?,0,0) ON CONFLICT(bot_id) DO NOTHING`, oldID); err != nil {
			return err
		}
		if oldID != botID {
			if _, err = tx.Exec("DELETE FROM bot_cards WHERE bot_id=?", oldID); err != nil {
				return err
			}
			if _, err = tx.Exec("INSERT INTO bot_cards(bot_id,key,message_id) SELECT ?,key,message_id FROM cards", oldID); err != nil {
				return err
			}
			if _, err = tx.Exec("DELETE FROM cards"); err != nil {
				return err
			}
			if _, err = tx.Exec("INSERT INTO cards(key,message_id) SELECT key,message_id FROM bot_cards WHERE bot_id=?", botID); err != nil {
				return err
			}
			if _, err = tx.Exec("INSERT OR IGNORE INTO bot_sessions(bot_id) VALUES(?)", botID); err != nil {
				return err
			}
			if _, err = tx.Exec("INSERT INTO meta(key,value) VALUES('tg_offset',COALESCE((SELECT offset FROM bot_sessions WHERE bot_id=?),0)),('telegram_send_not_before',COALESCE((SELECT pause_until FROM bot_sessions WHERE bot_id=?),0)) ON CONFLICT(key) DO UPDATE SET value=excluded.value", botID, botID); err != nil {
				return err
			}
			if _, err = tx.Exec("INSERT INTO audit(actor,action,request_key,detail,created_at) VALUES('system','bot_switch',?,?,?)", fmt.Sprint(botID), fmt.Sprintf("机器人 %d 切换为 %d；保留账本，独立接收进度及发送队列", oldID, botID), now()); err != nil {
				return err
			}
		}
		_, err = tx.Exec("INSERT INTO meta(key,value) VALUES('bot_id',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", fmt.Sprint(botID))
		return err
	})
}

func scopedUpdateID(tx *sqlite.Tx, rawID int64) (int64, error) {
	owner, err := tx.One("SELECT COALESCE((SELECT CAST(value AS INTEGER) FROM meta WHERE key='bot_id'),0) AS id")
	if err != nil {
		return 0, err
	}
	botID := owner.Int("id")
	r, err := tx.One("SELECT id FROM bot_update_ids WHERE bot_id=? AND update_id=?", botID, rawID)
	if err != nil {
		return 0, err
	}
	if r != nil {
		return r.Int("id"), nil
	}
	// Keep original IDs when available for existing logs and compatibility. On
	// collision use an internal surrogate, never the surrogate as a poll offset.
	if _, err = tx.Exec("INSERT OR IGNORE INTO bot_update_ids(id,bot_id,update_id) VALUES(?,?,?)", rawID, botID, rawID); err != nil {
		return 0, err
	}
	if _, err = tx.Exec("INSERT OR IGNORE INTO bot_update_ids(bot_id,update_id) VALUES(?,?)", botID, rawID); err != nil {
		return 0, err
	}
	r, err = tx.One("SELECT id FROM bot_update_ids WHERE bot_id=? AND update_id=?", botID, rawID)
	if err != nil {
		return 0, err
	}
	return r.Int("id"), nil
}
