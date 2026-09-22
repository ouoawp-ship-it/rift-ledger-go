package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"riftledger/internal/app"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func Test429PausesOtherChatsAndScheduler(t *testing.T) {
	s := svc(t)
	queue(t, s, "limited", "")
	if _, err := s.DB.Exec(`INSERT INTO outbox(key,chat_id,payload,created_at) VALUES('other',456,'{"chat_id":456,"text":"other"}',1)`); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	c := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"parameters":{"retry_after":20}}`))
	})
	c.scheduler = newSendScheduler()
	if _, err := c.SendOne(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if sent, err := c.SendOne(context.Background(), s); err != nil || sent {
		t.Fatal("other chat bypassed flood limit", sent, err)
	}
	if calls.Load() != 1 || c.scheduler.reserve(999, time.Now()) < 19*time.Second {
		t.Fatal("shared scheduler not paused", calls.Load())
	}
}

func TestTransientCompletionFailureDoesNotRepeatNetworkSend(t *testing.T) {
	s := svc(t)
	queue(t, s, "commit-retry", "")
	if _, err := s.DB.Exec(`CREATE TRIGGER fail_delivery BEFORE UPDATE ON outbox WHEN NEW.state='SENT' BEGIN SELECT RAISE(ABORT,'injected temporary storage failure'); END`); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	recovered := make(chan error, 1)
	c := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		go func() {
			time.Sleep(150 * time.Millisecond)
			_, err := s.DB.Exec("DROP TRIGGER fail_delivery")
			recovered <- err
		}()
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":91}}`))
	})
	if _, err := c.SendOne(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if err := <-recovered; err != nil {
		t.Fatal(err)
	}
	rows, err := s.DB.Query("SELECT state,message_id FROM outbox")
	if err != nil || rows[0]["state"] != "SENT" || rows[0].Int("message_id") != 91 || calls.Load() != 1 {
		t.Fatal(rows, calls.Load(), err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDNSFailureRecoversWithoutMarkingUnknown(t *testing.T) {
	s := svc(t)
	queue(t, s, "dns-recovery", "")
	var calls atomic.Int64
	c := New("isolated-token")
	c.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return nil, &net.DNSError{Err: "injected failure", Name: "api.telegram.org"}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{"message_id":92}}`)), Header: make(http.Header)}, nil
	})}
	if _, err := c.SendOne(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.DB.Query("SELECT state,next_at FROM outbox")
	if rows[0]["state"] != "PENDING" || rows[0].Int("next_at") <= time.Now().Unix() {
		t.Fatal(rows)
	}
	if sent, err := c.SendOne(context.Background(), s); err != nil || sent {
		t.Fatal("retry deadline ignored", sent, err)
	}
	if _, err := s.DB.Exec("UPDATE outbox SET next_at=0"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SendOne(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	rows, _ = s.DB.Query("SELECT state FROM outbox")
	if rows[0]["state"] != "SENT" || calls.Load() != 2 {
		t.Fatal(rows, calls.Load())
	}
}

func TestReceiverRecoversAndReplayedUpdatesDoNotDuplicateReplies(t *testing.T) {
	s := svc(t)
	var polls atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	update := app.TGUpdate{ID: 71, Message: &app.TGMessage{ID: 1, Date: time.Now().Unix(), From: &app.TGUser{ID: 123, FirstName: "test"}, Chat: app.TGChat{ID: 123, Type: "private"}, Text: "查"}}
	c := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		var result any = true
		switch {
		case strings.HasSuffix(r.URL.Path, "getMe"):
			result = map[string]any{"id": 1, "username": "TestBot"}
		case strings.HasSuffix(r.URL.Path, "getWebhookInfo"):
			result = map[string]any{"url": ""}
		case strings.HasSuffix(r.URL.Path, "getUpdates"):
			n := polls.Add(1)
			if n == 1 {
				w.WriteHeader(503)
				_, _ = w.Write([]byte(`{"ok":false,"error_code":503}`))
				return
			}
			if n <= 3 {
				result = []app.TGUpdate{update}
			} else {
				cancel()
				result = []app.TGUpdate{}
			}
		case strings.HasSuffix(r.URL.Path, "sendMessage"):
			result = map[string]any{"message_id": 1}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	})
	ctx, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	if err := c.Run(ctx, s); err != nil {
		t.Fatal(err)
	}
	rows, err := s.DB.Query("SELECT id FROM tg_updates")
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	rows, err = s.DB.Query("SELECT id FROM outbox")
	if err != nil || len(rows) != 1 {
		t.Fatal("reply duplicated", rows, err)
	}
	offset, err := s.Offset()
	if err != nil || offset != 72 || polls.Load() < 4 {
		t.Fatal(offset, err, polls.Load())
	}
}

func TestReadBackoffAndResponseBounds(t *testing.T) {
	for i, want := range []time.Duration{5, 10, 20, 30, 30} {
		got, retry := readRetryDelay(&connectionError{}, i+1)
		if !retry || got != want*time.Second {
			t.Fatal(i, got, retry)
		}
	}
	if d, _ := readRetryDelay(&APIError{Code: 429, RetryAfter: 90}, 5); d != 90*time.Second {
		t.Fatal(d)
	}
	if _, retry := readRetryDelay(errors.New("identity conflict"), 5); retry {
		t.Fatal("fatal config retried")
	}
	c := New("test-token")
	for _, body := range []string{`{"ok":true,"result":{}}` + strings.Repeat(" ", 2<<20), `{"ok":false,"error_code":429,"parameters":{"retry_after":9223372036854775807}}`} {
		err := c.decodeResponse(&http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil)
		if err == nil {
			t.Fatal("invalid response accepted")
		}
		if _, safe := retryUnsent(err, 1); safe {
			t.Fatal("ambiguous response retried")
		}
	}
}
