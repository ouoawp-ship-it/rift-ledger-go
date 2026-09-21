package httpapi

import (
	"encoding/json"
	"testing"
)

func TestOperationsEndpointIsAuthenticated(t *testing.T) {
	h := server(t)
	if w := request(h, "GET", "/api/operations", "", "", "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	w := request(h, "GET", "/api/operations", "", testToken, "", "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var payload map[string]any
	if e := json.Unmarshal(w.Body.Bytes(), &payload); e != nil {
		t.Fatal(e)
	}
	data := payload["data"].(map[string]any)
	for _, field := range []string{"receiver", "business", "sender", "database"} {
		if data[field] == nil {
			t.Fatal("missing health lane", field)
		}
	}
}
