package httpapi

import (
	"encoding/json"
	"math"
	"net/http/httptest"
	"testing"
)

func TestInvalidResponseReturnsJSONError(t *testing.T) {
	for _, value := range []any{math.NaN(), json.RawMessage(`{"broken"`)} {
		w := httptest.NewRecorder()
		(&API{}).respond(w, value, nil)
		var reply struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		}
		if w.Code != 500 || json.Unmarshal(w.Body.Bytes(), &reply) != nil || reply.OK || reply.Error == "" {
			t.Fatalf("invalid response: %d %s", w.Code, w.Body.String())
		}
	}
}
func TestRequestIDOnSuccessAndError(t *testing.T) {
	h := server(t)
	for _, token := range []string{testToken, ""} {
		w := request(h, "GET", "/api/state", "", token, "", "")
		if len(w.Header().Get("X-Request-ID")) != 24 {
			t.Fatal("missing request ID")
		}
	}
}
