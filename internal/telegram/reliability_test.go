package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"riftledger/internal/app"
	"riftledger/internal/champion"
	"strings"
	"testing"
	"time"
)

func TestDialFailureRequeuesUnsentMessage(t *testing.T) {
	s := svc(t)
	queue(t, s, "dial", "")
	c := New("secret-token")
	c.BaseURL = "http://127.0.0.1:1"
	if _, e := c.SendOne(context.Background(), s); e != nil {
		t.Fatal(e)
	}
	rows, _ := s.DB.Query("SELECT state,next_at,last_error FROM outbox")
	if rows[0]["state"] != "PENDING" || rows[0].Int("next_at") <= time.Now().Unix() || strings.Contains(rows[0]["last_error"], c.Token) {
		t.Fatal(rows)
	}
}
func TestInterruptedResponseRemainsUnknown(t *testing.T) {
	s := svc(t)
	queue(t, s, "truncated", "")
	c := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.Write([]byte(`{"ok":true`))
	})
	if _, e := c.SendOne(context.Background(), s); e != nil {
		t.Fatal(e)
	}
	rows, _ := s.DB.Query("SELECT state FROM outbox")
	if rows[0]["state"] != "UNKNOWN" {
		t.Fatal(rows)
	}
	if sent, e := c.SendOne(context.Background(), s); e != nil || sent {
		t.Fatal("ambiguous message resent")
	}
}

func TestMalformedEnvelopesRemainRetryableForReads(t *testing.T) {
	for _, body := range []string{`{}`, `{"ok":null}`, `{"ok":true}`, `{"ok":true,"result":null}`} {
		t.Run(body, func(t *testing.T) {
			c := New("test-token")
			response := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
			var result []app.TGUpdate
			err := c.decodeResponse(response, &result)
			if _, retry := ConnectionRetryDelay(err); err == nil || !retry {
				t.Fatalf("malformed read permanently stopped receiver: %v", err)
			}
			if _, safe := retryUnsent(err, 1); safe {
				t.Fatal("ambiguous write must not be automatically replayed")
			}
		})
	}
}
func TestSingleCompositeUsesSendPhoto(t *testing.T) {
	s := svc(t)
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "1.0.0"), 0700)
	var img bytes.Buffer
	png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 8, 8)))
	os.WriteFile(filepath.Join(dir, "1.0.0", "Garen.png"), img.Bytes(), 0600)
	s.Champions, _ = champion.New(dir)
	p := app.MessagePayload{ChatID: 123, Text: "five heroes"}
	for i := 0; i < 5; i++ {
		p.Media = append(p.Media, app.MediaPhoto{ID: "Garen", Version: "1.0.0", Caption: "hero"})
	}
	raw, _ := json.Marshal(p)
	s.DB.Exec("INSERT INTO outbox(key,chat_id,payload,created_at) VALUES('image',123,?,1)", string(raw))
	called := false
	c := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		if !strings.HasSuffix(r.URL.Path, "/sendPhoto") {
			t.Error("single image sent via wrong method")
		}
		if e := r.ParseMultipartForm(4 << 20); e != nil {
			t.Error(e)
			return
		}
		defer r.MultipartForm.RemoveAll()
		f, _, e := r.FormFile("photo")
		if e != nil {
			t.Error(e)
			return
		}
		defer f.Close()
		b, _ := io.ReadAll(f)
		if _, e = png.Decode(bytes.NewReader(b)); e != nil {
			t.Error("invalid photo", e)
		}
		if r.FormValue("chat_id") != "123" || r.FormValue("caption") != "five heroes" {
			t.Error("missing destination/caption")
		}
		w.Write([]byte(`{"ok":true,"result":{"message_id":99}}`))
	})
	if _, e := c.SendOne(context.Background(), s); e != nil {
		t.Fatal(e)
	}
	rows, _ := s.DB.Query("SELECT state,message_id FROM outbox")
	if !called || rows[0]["state"] != "SENT" || rows[0].Int("message_id") != 99 {
		t.Fatal(rows)
	}
}
func TestGracefulStopFinishesClaimedSend(t *testing.T) {
	s := svc(t)
	queue(t, s, "drain", "")
	entered, release := make(chan struct{}), make(chan struct{})
	c := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.Write([]byte(`{"ok":true,"result":{"message_id":88}}`))
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, e := c.SendOne(ctx, s); done <- e }()
	<-entered
	cancel()
	close(release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	rows, _ := s.DB.Query("SELECT state FROM outbox")
	if rows[0]["state"] != "SENT" {
		t.Fatal(rows)
	}
}
func TestSlowChatDoesNotBlockOtherChat(t *testing.T) {
	s := svc(t)
	queue(t, s, "slow", "")
	s.DB.Exec(`INSERT INTO outbox(key,chat_id,payload,created_at) VALUES('fast',456,'{"chat_id":456,"text":"fast"}',1)`)
	slow, fast, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "getMe"):
			w.Write([]byte(`{"ok":true,"result":{"id":123,"username":"TestBot"}}`))
		case strings.HasSuffix(r.URL.Path, "getWebhookInfo"):
			w.Write([]byte(`{"ok":true,"result":{"url":""}}`))
		case strings.HasSuffix(r.URL.Path, "getUpdates"):
			io.Copy(io.Discard, r.Body)
			select {
			case <-r.Context().Done():
			case <-release:
			}
		case strings.HasSuffix(r.URL.Path, "sendMessage"):
			var p struct {
				ChatID int64 `json:"chat_id"`
			}
			json.NewDecoder(r.Body).Decode(&p)
			if p.ChatID == 123 {
				close(slow)
				<-release
			} else {
				close(fast)
			}
			w.Write([]byte(`{"ok":true,"result":{"message_id":11}}`))
		}
	}))
	defer ts.Close()
	c := New("test-token")
	c.BaseURL = ts.URL
	c.HTTP = ts.Client()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, s) }()
	defer func() { close(release); cancel(); <-done }()
	select {
	case <-slow:
	case <-time.After(3 * time.Second):
		t.Fatal("slow chat not claimed")
	}
	select {
	case <-fast:
	case <-time.After(3 * time.Second):
		t.Fatal("slow chat blocked fast chat")
	}
}
