package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"riftledger/internal/app"
	"riftledger/internal/sqlite"
)

func svc(t *testing.T) *app.Service {
	t.Helper()
	db, e := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	s, e := app.New(db, app.RuntimeConfig{BotUsername: "TestBot"})
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func queue(t *testing.T, s *app.Service, key, card string) {
	t.Helper()
	payload := `{"chat_id":123,"message_thread_id":7,"text":"测试卡片"}`
	if _, e := s.DB.Exec("INSERT INTO outbox(key,chat_id,card_key,payload,created_at) VALUES(?,123,?,?,1)", key, card, payload); e != nil {
		t.Fatal(e)
	}
}
func mockClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	c := New("test-token-never-log")
	c.BaseURL = server.URL
	c.HTTP = server.Client()
	return c
}
func TestSendAndEditSameCard(t *testing.T) {
	s := svc(t)
	calls := 0
	c := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		var payload map[string]any
		json.NewDecoder(r.Body).Decode(&payload)
		if calls == 1 {
			if !strings.HasSuffix(r.URL.Path, "sendMessage") || payload["message_thread_id"] != float64(7) {
				t.Error("send payload")
			}
		} else {
			if !strings.HasSuffix(r.URL.Path, "editMessageText") || payload["message_id"] != float64(42) {
				t.Error("edit payload")
			}
			if _, ok := payload["message_thread_id"]; ok {
				t.Error("invalid edit thread field")
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true,"result":{"message_id":42}}`))
	})
	queue(t, s, "first", "round:one")
	if sent, e := c.SendOne(context.Background(), s); e != nil || !sent {
		t.Fatal(e)
	}
	queue(t, s, "second", "round:one")
	if sent, e := c.SendOne(context.Background(), s); e != nil || !sent {
		t.Fatal(e)
	}
	if calls != 2 {
		t.Fatal(calls)
	}
	rows, _ := s.DB.Query("SELECT state FROM outbox")
	for _, r := range rows {
		if r["state"] != "SENT" {
			t.Fatal(r)
		}
	}
}
func TestUnknownSendDoesNotAutoRetry(t *testing.T) {
	s := svc(t)
	c := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		w.Write([]byte(`{"ok":false,"error_code":503,"description":"backend failed"}`))
	})
	queue(t, s, "unknown", "")
	if _, e := c.SendOne(context.Background(), s); e != nil {
		t.Fatal(e)
	}
	rows, _ := s.DB.Query("SELECT state FROM outbox")
	if rows[0]["state"] != "UNKNOWN" {
		t.Fatal(rows)
	}
	if sent, e := c.SendOne(context.Background(), s); e != nil || sent {
		t.Fatal("ambiguous response retried")
	}
}
func TestRateLimitRetryAndDefinitiveFailure(t *testing.T) {
	for _, status := range []int{429, 403} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			s := svc(t)
			c := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": status, "description": "mock", "parameters": map[string]any{"retry_after": 30}})
			})
			queue(t, s, "test", "")
			if _, e := c.SendOne(context.Background(), s); e != nil {
				t.Fatal(e)
			}
			rows, _ := s.DB.Query("SELECT state,next_at FROM outbox")
			want := "FAILED"
			if status == 429 {
				want = "PENDING"
				if rows[0].Int("next_at") <= time.Now().Unix() {
					t.Fatal("retry_after ignored")
				}
			}
			if rows[0]["state"] != want {
				t.Fatal(rows)
			}
		})
	}
}
func TestWebhookConflictNeverDeleted(t *testing.T) {
	s := svc(t)
	c := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "getMe"):
			w.Write([]byte(`{"ok":true,"result":{"id":123,"username":"TestBot"}}`))
		case strings.HasSuffix(r.URL.Path, "getWebhookInfo"):
			w.Write([]byte(`{"ok":true,"result":{"url":"https://existing.example/webhook"}}`))
		default:
			t.Error("unexpected takeover action", r.URL.Path)
			w.WriteHeader(500)
		}
	})
	e := c.Run(context.Background(), s)
	if e == nil || !strings.Contains(e.Error(), "Webhook") {
		t.Fatal(e)
	}
}
func TestNetworkErrorsNeverExposeToken(t *testing.T) {
	c := New("super-secret-token")
	c.BaseURL = "http://127.0.0.1:1"
	c.HTTP.Timeout = time.Second
	e := c.Call(context.Background(), "sendMessage", map[string]any{}, nil)
	if e == nil || strings.Contains(e.Error(), c.Token) {
		t.Fatal("token leaked")
	}
}
func TestNotModifiedEditIsTreatedAsSent(t *testing.T) {
	s := svc(t)
	s.DB.Exec("INSERT INTO cards(key,message_id) VALUES('card',77)")
	c := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: message is not modified"}`))
	})
	queue(t, s, "edit", "card")
	if _, e := c.SendOne(context.Background(), s); e != nil {
		t.Fatal(e)
	}
	rows, _ := s.DB.Query("SELECT state,message_id FROM outbox")
	if rows[0]["state"] != "SENT" || rows[0].Int("message_id") != 77 {
		t.Fatal(rows)
	}
}
