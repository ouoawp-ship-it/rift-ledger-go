package app

import (
	"fmt"
	"riftledger/internal/sqlite"
)

const sendPauseKey = "telegram_send_not_before"

func sendNotBefore(tx *sqlite.Tx) (int64, error) {
	r, err := tx.One("SELECT value FROM meta WHERE key=?", sendPauseKey)
	if err != nil || r == nil {
		return 0, err
	}
	return r.Int("value"), nil
}

// A flood limit may apply to the bot, not just one chat. Pause all outgoing
// lanes conservatively, in the same transaction as the rejected task's retry.
// Epoch seconds are rounded up so a restart never shortens retry_after.
func (s *Service) RateLimitOutbox(item OutboxItem, seconds int64) (int64, error) {
	defer s.NotifyOutbox()
	if seconds < 1 {
		seconds = 5
	}
	if seconds > 365*86400 {
		return 0, bad("Telegram限流等待时间异常，需要核实")
	}
	until := now() + seconds + 1
	err := s.DB.Transaction(func(tx *sqlite.Tx) error {
		old, err := sendNotBefore(tx)
		if err != nil {
			return err
		}
		if old > until {
			until = old
		}
		if _, err = tx.Exec("INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", sendPauseKey, fmt.Sprint(until)); err != nil {
			return err
		}
		_, err = tx.Exec("UPDATE outbox SET state='PENDING',next_at=?,last_error='Telegram限流，发送端统一等待后重试' WHERE id=? AND state='INFLIGHT'", until, item.ID)
		return err
	})
	return until, err
}

// Recheck after an in-memory pacing wait: another worker could have received
// a 429 since this task was claimed. Already transmitted requests cannot be recalled.
func (s *Service) DeferLimitedOutbox(item OutboxItem) (bool, error) {
	deferred := false
	err := s.DB.Transaction(func(tx *sqlite.Tx) error {
		until, err := sendNotBefore(tx)
		if err != nil || until <= now() {
			return err
		}
		_, err = tx.Exec("UPDATE outbox SET state='PENDING',next_at=MAX(next_at,?),attempts=MAX(0,attempts-1) WHERE id=? AND state='INFLIGHT'", until, item.ID)
		deferred = err == nil
		return err
	})
	return deferred, err
}
