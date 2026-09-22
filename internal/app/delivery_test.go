package app

import (
	"encoding/json"
	"path/filepath"
	"riftledger/internal/sqlite"
	"testing"
)

func TestRateLimitSurvivesRestartAndDefersClaimedPeers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "delivery.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(db, RuntimeConfig{})
	if err != nil {
		t.Fatal(err)
	}
	for i := int64(1); i <= 3; i++ {
		p, _ := json.Marshal(MessagePayload{ChatID: i, Text: "test"})
		if _, err = db.Exec("INSERT INTO outbox(key,chat_id,payload,created_at) VALUES(?,?,?,1)", i, i, string(p)); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.ClaimOutbox()
	if err != nil || first == nil {
		t.Fatal(first, err)
	}
	peer, err := s.ClaimOutbox()
	if err != nil || peer == nil {
		t.Fatal(peer, err)
	}
	until, err := s.RateLimitOutbox(*first, 30)
	if err != nil || until < now()+30 {
		t.Fatal(until, err)
	}
	if item, err := s.ClaimOutbox(); err != nil || item != nil {
		t.Fatal("new claim ignored global pause", item, err)
	}
	if deferred, err := s.DeferLimitedOutbox(*peer); err != nil || !deferred {
		t.Fatal("claimed worker bypassed pause", deferred, err)
	}
	q, err := s.BotConnection()
	if err != nil || q.Sender.State != "rate_limited" || q.Sender.RetryAt != until {
		t.Fatal(q, err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s, err = New(db, RuntimeConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if item, err := s.ClaimOutbox(); err != nil || item != nil {
		t.Fatal("restart bypassed pause", item, err)
	}
	rows, err := db.Query("SELECT state,attempts FROM outbox WHERE id=?", peer.ID)
	if err != nil || rows[0]["state"] != "PENDING" || rows[0].Int("attempts") != 0 {
		t.Fatal(rows, err)
	}
	// Simulate the deadline passing, without sleeping in a unit test.
	if _, err = db.Exec("UPDATE meta SET value='0' WHERE key=?", sendPauseKey); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE outbox SET next_at=0"); err != nil {
		t.Fatal(err)
	}
	item, err := s.ClaimOutbox()
	if err != nil || item == nil || item.ID != first.ID {
		t.Fatal("queued task not resumed", item, err)
	}
}

func TestRateLimitCannotShortenExistingPause(t *testing.T) {
	s := reliabilityService(t)
	item := OutboxItem{ID: 999}
	long, err := s.RateLimitOutbox(item, 60)
	if err != nil {
		t.Fatal(err)
	}
	short, err := s.RateLimitOutbox(item, 1)
	if err != nil || short != long {
		t.Fatal(long, short, err)
	}
}
