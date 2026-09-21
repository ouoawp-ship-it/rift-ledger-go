package champion

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestBackgroundRefreshReturnsPromptlyAndKeepsOldCache(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
		w.WriteHeader(502)
	}))
	defer ts.Close()
	s, e := New(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	s.BaseURL = ts.URL
	s.snapshot = Snapshot{Version: "1.0.0", Champions: []Champion{{ID: "Garen"}}}
	start := time.Now()
	s.StartRefresh()
	if time.Since(start) > time.Second {
		t.Fatal("start blocked on network")
	}
	<-entered
	s.StartRefresh()
	if !s.View().Refreshing || len(s.View().Champions) != 1 {
		t.Fatal("old snapshot unavailable")
	}
	close(release)
	deadline := time.Now().Add(3 * time.Second)
	for s.View().Refreshing && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.View().Refreshing || s.View().Version != "1.0.0" || calls.Load() != 1 {
		t.Fatalf("unexpected snapshot: %+v calls=%d", s.View(), calls.Load())
	}
}
