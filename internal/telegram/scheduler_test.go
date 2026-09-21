package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"riftledger/internal/app"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSchedulerEnforcesGlobalAndChatPacingWithoutCrossChatBlocking(t *testing.T) {
	s := newSendScheduler()
	start := time.Now()
	if d := s.reserve(-1, start); d != 0 {
		t.Fatal(d)
	}
	if d := s.reserve(-1, start.Add(50*time.Millisecond)); d != 2950*time.Millisecond {
		t.Fatal("group pacing", d)
	}
	if d := s.reserve(123, start.Add(50*time.Millisecond)); d != 0 {
		t.Fatal("group delayed private reply", d)
	}
	if d := s.reserve(124, start.Add(60*time.Millisecond)); d != 40*time.Millisecond {
		t.Fatal("global pacing", d)
	}
	if d := s.reserve(123, start.Add(100*time.Millisecond)); d != 950*time.Millisecond {
		t.Fatal("private pacing", d)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if s.wait(ctx, 123) {
		t.Fatal("ignored cancellation")
	}
}
func TestSlowCallbackDoesNotHoldNextUpdate(t *testing.T) {
	s := svc(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ackStarted := make(chan struct{}, 1)
	releaseAck := make(chan struct{})
	defer close(releaseAck)
	var polls atomic.Int64
	c := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		result := any(true)
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			result = map[string]any{"id": 99, "username": "TestBot"}
		case strings.HasSuffix(r.URL.Path, "/getWebhookInfo"):
			result = map[string]any{"url": ""}
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			if polls.Add(1) == 1 {
				result = []app.TGUpdate{{ID: 1, Callback: &app.TGCallback{ID: "slow", From: app.TGUser{ID: 123, FirstName: "tester"}, Message: &app.TGMessage{Chat: app.TGChat{ID: 123, Type: "private"}}, Data: "help"}}, {ID: 2, Message: &app.TGMessage{ID: 2, Date: time.Now().Unix(), From: &app.TGUser{ID: 123, FirstName: "tester"}, Chat: app.TGChat{ID: 123, Type: "private"}, Text: "查"}}}
			} else {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(30 * time.Millisecond):
				}
				result = []app.TGUpdate{}
			}
		case strings.HasSuffix(r.URL.Path, "/answerCallbackQuery"):
			select {
			case ackStarted <- struct{}{}:
			default:
			}
			select {
			case <-r.Context().Done():
			case <-releaseAck:
			}
			return
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			result = map[string]any{"message_id": 5}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	})
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, s) }()
	select {
	case <-ackStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("callback not started")
	}
	deadline := time.Now().Add(time.Second)
	processed := false
	for time.Now().Before(deadline) {
		rows, e := s.DB.Query("SELECT id FROM tg_updates WHERE id=2")
		if e != nil {
			t.Fatal(e)
		}
		if len(rows) == 1 {
			processed = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("receiver did not stop")
	}
	if !processed {
		t.Fatal("callback network request blocked next business update")
	}
}
