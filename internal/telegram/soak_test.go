package telegram

import (
	"context"
	"encoding/json"
	"math"
	"net"
	"net/http"
	"os"
	"riftledger/internal/app"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Explicit opt-in sustained exercise of the real receiver, business transactions,
// four paced senders and durable queue. All HTTP traffic stays on httptest localhost.
func TestTelegramSoak(t *testing.T) {
	raw := os.Getenv("RIFT_SOAK_SECONDS")
	if raw == "" {
		t.Skip("set RIFT_SOAK_SECONDS=60 (60..600) for isolated sustained fault testing")
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 60 || seconds > 600 {
		t.Fatal("RIFT_SOAK_SECONDS must be 60..600")
	}
	s := svc(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start := time.Now()
	ready := make(chan struct{}, 1)
	var mu sync.Mutex
	updates := []app.TGUpdate{}
	enqueued := map[int64][]time.Time{}
	accepted := map[int64]int{}
	latencies := []float64{}
	var sendCalls, dialCalls, received atomic.Int64
	var injectedPoll, injected429 atomic.Bool
	c := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		var result any = true
		switch {
		case strings.HasSuffix(r.URL.Path, "getMe"):
			result = map[string]any{"id": 1, "username": "TestBot"}
		case strings.HasSuffix(r.URL.Path, "getWebhookInfo"):
			result = map[string]any{"url": ""}
		case strings.HasSuffix(r.URL.Path, "getUpdates"):
			var query struct {
				Offset int64 `json:"offset"`
			}
			if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
				t.Error(err)
				return
			}
			if time.Since(start) > 20*time.Second && injectedPoll.CompareAndSwap(false, true) {
				w.WriteHeader(503)
				_, _ = w.Write([]byte(`{"ok":false,"error_code":503}`))
				return
			}
			batch := []app.TGUpdate{}
			mu.Lock()
			for _, u := range updates {
				if u.ID >= query.Offset && len(batch) < 50 {
					batch = append(batch, u)
				}
			}
			mu.Unlock()
			if len(batch) == 0 {
				select {
				case <-r.Context().Done():
					return
				case <-ready:
				case <-time.After(250 * time.Millisecond):
				}
			}
			// Replaying one duplicate in each nonempty batch verifies durable dedup.
			if len(batch) > 0 {
				batch = append(batch, batch[len(batch)-1])
			}
			result = batch
		case strings.HasSuffix(r.URL.Path, "sendMessage"):
			var p struct {
				ChatID int64 `json:"chat_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
				t.Error(err)
				return
			}
			n := sendCalls.Add(1)
			if n >= 100 && injected429.CompareAndSwap(false, true) {
				w.WriteHeader(429)
				_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"parameters":{"retry_after":2}}`))
				return
			}
			if p.ChatID == 1001 {
				time.Sleep(300 * time.Millisecond)
			}
			mu.Lock()
			i := accepted[p.ChatID]
			if i >= len(enqueued[p.ChatID]) {
				t.Error("duplicate/unexpected network delivery", p.ChatID)
			} else {
				latencies = append(latencies, float64(time.Since(enqueued[p.ChatID][i]).Microseconds())/1000)
			}
			accepted[p.ChatID]++
			mu.Unlock()
			result = map[string]any{"message_id": received.Add(1)}
		default:
			t.Error("unexpected mock method", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	})
	transport := c.HTTP.Transport
	c.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "sendMessage") && dialCalls.Add(1) == 180 {
			return nil, &net.DNSError{Err: "injected DNS outage", Name: "api.telegram.org"}
		}
		return transport.RoundTrip(r)
	})
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, s) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(20 * time.Second):
			t.Error("receiver did not shut down")
		}
	}()
	maxPending := int64(0)
	total := seconds * 10
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for i := 1; i <= total; i++ {
		<-ticker.C
		chat := int64(1000 + i%40)
		u := app.TGUpdate{ID: int64(i), Message: &app.TGMessage{ID: int64(i), Date: time.Now().Unix(), From: &app.TGUser{ID: chat, FirstName: "isolated"}, Chat: app.TGChat{ID: chat, Type: "private"}, Text: "查"}}
		mu.Lock()
		updates = append(updates, u)
		enqueued[chat] = append(enqueued[chat], time.Now())
		mu.Unlock()
		select {
		case ready <- struct{}{}:
		default:
		}
		if i%10 == 0 {
			q, err := s.BotConnection()
			if err != nil {
				t.Fatal(err)
			}
			if q.Sender.Pending > maxPending {
				maxPending = q.Sender.Pending
			}
		}
	}
	producedAt := time.Now()
	deadline := producedAt.Add(60 * time.Second)
	for time.Now().Before(deadline) {
		rows, err := s.DB.Query("SELECT COUNT(*) AS n FROM outbox WHERE state='SENT'")
		if err != nil {
			t.Fatal(err)
		}
		if rows[0].Int("n") == int64(total) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	rows, err := s.DB.Query("SELECT state,COUNT(*) AS n FROM outbox GROUP BY state")
	if err != nil || len(rows) != 1 || rows[0]["state"] != "SENT" || rows[0].Int("n") != int64(total) {
		t.Fatal("backlog failed to drain", rows, err)
	}
	rows, err = s.DB.Query("SELECT COUNT(*) AS n FROM tg_updates")
	if err != nil || rows[0].Int("n") != int64(total) || received.Load() != int64(total) {
		t.Fatal("dedup/delivery mismatch", rows, received.Load(), err)
	}
	if !injectedPoll.Load() || !injected429.Load() || dialCalls.Load() < 180 {
		t.Fatal("fault injection not exercised")
	}
	offset, err := s.Offset()
	if err != nil || offset != int64(total+1) {
		t.Fatal(offset, err)
	}
	mu.Lock()
	defer mu.Unlock()
	for chat, times := range enqueued {
		if accepted[chat] != len(times) {
			t.Error("chat lost replies", chat, accepted[chat], len(times))
		}
	}
	sort.Float64s(latencies)
	report := map[string]any{"passed": !t.Failed(), "real_telegram": false, "duration_seconds": seconds, "updates": total, "delivered": received.Load(), "max_pending_sampled": maxPending, "drain_after_producer_ms": time.Since(producedAt).Milliseconds(), "end_to_end_ms": map[string]float64{"p50": latencies[len(latencies)/2], "p95": latencies[int(math.Ceil(float64(len(latencies))*.95))-1], "max": latencies[len(latencies)-1]}, "faults": []string{"duplicate updates", "poll HTTP 503", "send HTTP 429 retry_after=2", "send DNS failure", "slow chat"}}
	output, _ := json.Marshal(report)
	t.Log(string(output))
}
