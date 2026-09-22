package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"riftledger/internal/app"
	"riftledger/internal/sqlite"
)

const testToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func server(t *testing.T) http.Handler {
	t.Helper()
	db, e := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	s, e := app.New(db, app.RuntimeConfig{})
	if e != nil {
		t.Fatal(e)
	}
	return New(s, testToken)
}
func request(h http.Handler, method, path, body, token, key, origin string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestAuthenticationAndCSRF(t *testing.T) {
	h := server(t)
	for _, token := range []string{"", "invalid"} {
		w := request(h, "GET", "/api/state", "", token, "", "")
		if w.Code != 401 {
			t.Fatal(w.Code)
		}
	}
	w := request(h, "GET", "/api/state", "", testToken, "", "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = request(h, "POST", "/api/calculate", `{"damage":"12745"}`, testToken, "", "https://malicious.example")
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}
func TestBotConnectionRequiresAuthentication(t *testing.T) {
	h := server(t)
	if w := request(h, "GET", "/api/bot-connection", "", "", "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	w := request(h, "GET", "/api/bot-connection", "", testToken, "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"disabled"`) || !strings.Contains(w.Body.String(), `"checked_at":`) {
		t.Fatal(w.Body.String())
	}
}

func TestStaticAndHealth(t *testing.T) {
	h := server(t)
	for _, path := range []string{"/", "/app.js", "/bot-connection.js", "/style.css", "/healthz"} {
		w := request(h, "GET", path, "", "", "", "")
		if w.Code != 200 {
			t.Fatalf("%s %d", path, w.Code)
		}
		if strings.Contains(w.Body.String(), testToken) {
			t.Fatal("secret in assets")
		}
		if w.Header().Get("X-Frame-Options") != "DENY" {
			t.Fatal("missing security header")
		}
	}
}

func TestPlayerHistoryAuthenticationAndValidation(t *testing.T) {
	h := server(t)
	for _, path := range []string{"/api/player-search?q=111", "/api/player-history?account_id=tg:111&view=bets"} {
		if w := request(h, "GET", path, "", "", "", ""); w.Code != 401 {
			t.Fatal(w.Code)
		}
	}
	if w := request(h, "GET", "/api/player-search?q=111", "", testToken, "", ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := request(h, "GET", "/api/player-history?view=bets", "", testToken, "", ""); w.Code != 400 {
		t.Fatal(w.Body.String())
	}
	if w := request(h, "GET", "/api/player-history?view=bets&account_id=tg:missing", "", testToken, "", ""); w.Code != 404 {
		t.Fatal(w.Body.String())
	}
}
func TestStrictJSONAndMoneyPrecision(t *testing.T) {
	h := server(t)
	for _, body := range []string{`{"damage":"12745","unknown":1}`, `{"damage":"12745"} {}`, `{"damage":12745}`, `{"damage":"01234"}`} {
		w := request(h, "POST", "/api/calculate", body, testToken, "", "")
		if w.Code != 400 {
			t.Fatalf("%s -> %d", body, w.Code)
		}
	}
	w := request(h, "POST", "/api/adjustments", `{"account_id":"house","delta":1.0001,"note":"x"}`, testToken, "test-key-0001", "")
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
	for i, delta := range []string{"1000.199", `"0.001"`, "-0.001"} {
		w = request(h, "POST", "/api/adjustments", `{"account_id":"house","delta":`+delta+`,"note":"precision"}`, testToken, fmt.Sprintf("money-key-%d", i), "")
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	if !strings.Contains(w.Body.String(), `"balance":1000.199`) {
		t.Fatal(w.Body.String())
	}
}
func TestDamageCompareAndMissingIdempotencyKey(t *testing.T) {
	h := server(t)
	w := request(h, "POST", "/api/calculate", `{"damage":"12745","banker_damage":"12710"}`, testToken, "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"outcome":"WIN"`) {
		t.Fatal(w.Body.String())
	}
	w = request(h, "POST", "/api/rounds", `{"number":"2026-09-20-0001","heroes":[],"banker":0}`, testToken, "", "")
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
}
func TestHTTPIdempotencyReplay(t *testing.T) {
	h := server(t)
	body := `{"account_id":"house","delta":1000,"note":"test credit"}`
	var first []byte
	for i := 0; i < 2; i++ {
		w := request(h, "POST", "/api/adjustments", body, testToken, "adjust-replay-key", "")
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		if i == 0 {
			first = append([]byte{}, w.Body.Bytes()...)
		} else if !bytes.Equal(first, w.Body.Bytes()) {
			t.Fatal("different replay response")
		}
	}
	w := request(h, "POST", "/api/adjustments", strings.Replace(body, "1000", "2000", 1), testToken, "adjust-replay-key", "")
	if w.Code != 409 {
		t.Fatal(w.Body.String())
	}
	w = request(h, "GET", "/api/reconcile", "", testToken, "", "")
	var envelope map[string]any
	if e := json.Unmarshal(w.Body.Bytes(), &envelope); e != nil {
		t.Fatal(e)
	}
	if !envelope["data"].(map[string]any)["balanced"].(bool) {
		t.Fatal("not balanced")
	}
}
func TestRequestBodyLimit(t *testing.T) {
	h := server(t)
	w := request(h, "POST", "/api/calculate", `{"damage":"`+strings.Repeat("1", (1<<20)+1)+`"}`, testToken, "", "")
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
}
