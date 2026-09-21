package app

import (
	"encoding/json"
	"path/filepath"
	"riftledger/internal/sqlite"
	"sync"
	"testing"
)

func reliabilityService(t *testing.T) *Service {
	db, e := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	s, e := New(db, RuntimeConfig{})
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func TestInvalidCommandResponseRollsBack(t *testing.T) {
	s := reliabilityService(t)
	_, e := s.Command("bad-response-key", "test", nil, func(tx *sqlite.Tx) (any, error) {
		_, e := tx.Exec("INSERT INTO meta(key,value) VALUES('must_rollback','1')")
		return json.RawMessage(`{"broken"`), e
	})
	if e == nil {
		t.Fatal("expected error")
	}
	rows, _ := s.DB.Query("SELECT key FROM meta WHERE key='must_rollback'")
	if len(rows) != 0 {
		t.Fatal("mutation committed without serializable response")
	}
}
func TestOutboxParallelClaimsPreserveChatOrder(t *testing.T) {
	s := reliabilityService(t)
	for _, row := range []struct {
		key  string
		chat int64
	}{{"a1", 1}, {"a2", 1}, {"b1", 2}, {"b2", 2}} {
		payload, _ := json.Marshal(MessagePayload{ChatID: row.chat, Text: row.key})
		if _, e := s.DB.Exec("INSERT INTO outbox(key,chat_id,payload,created_at) VALUES(?,?,?,1)", row.key, row.chat, string(payload)); e != nil {
			t.Fatal(e)
		}
	}
	var wg sync.WaitGroup
	got := make(chan *OutboxItem, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			item, e := s.ClaimOutbox()
			if e != nil {
				t.Error(e)
			}
			if item != nil {
				got <- item
			}
		}()
	}
	wg.Wait()
	close(got)
	seen := map[int64]bool{}
	for item := range got {
		if seen[item.Payload.ChatID] {
			t.Fatal("same chat claimed concurrently")
		}
		seen[item.Payload.ChatID] = true
	}
	if len(seen) != 2 {
		t.Fatal("different chats did not progress", seen)
	}
}
func TestMalformedOutboxDoesNotStarveOtherChats(t *testing.T) {
	s := reliabilityService(t)
	s.DB.Exec("INSERT INTO outbox(key,chat_id,payload,created_at) VALUES('bad',1,'broken',1)")
	s.DB.Exec(`INSERT INTO outbox(key,chat_id,payload,created_at) VALUES('good',2,'{"chat_id":2,"text":"ok"}',1)`)
	if item, e := s.ClaimOutbox(); e != nil || item != nil {
		t.Fatal(item, e)
	}
	if item, e := s.ClaimOutbox(); e != nil || item == nil || item.Payload.ChatID != 2 {
		t.Fatal(item, e)
	}
}
func TestSentChatCooldownAndDeliveryWarning(t *testing.T) {
	s := reliabilityService(t)
	s.DB.Exec(`INSERT INTO outbox(key,chat_id,payload,state,next_at,created_at) VALUES('old',1,'{}','SENT',?,1)`, now()+30)
	s.DB.Exec(`INSERT INTO outbox(key,chat_id,payload,created_at) VALUES('next',1,'{"chat_id":1,"text":"wait"}',1)`)
	if item, e := s.ClaimOutbox(); e != nil || item != nil {
		t.Fatal("cooldown ignored", item, e)
	}
	s.DB.Exec("UPDATE outbox SET state='FAILED',next_at=0 WHERE key='old'")
	s.BotStatus("接收正常")
	status, e := s.BotConnection()
	if e != nil || status.State != "degraded" || status.NeedsReview != 1 || status.Pending != 1 {
		t.Fatal(status, e)
	}
}
