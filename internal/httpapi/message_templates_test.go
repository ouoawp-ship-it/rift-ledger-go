package httpapi

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"testing"
)

func TestMessageEditorEndpoints(t *testing.T) {
	h := server(t)
	if w := request(h, "GET", "/api/message-templates", "", "", "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	var data bytes.Buffer
	_ = png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 16, 16)))
	w := request(h, "POST", "/api/message-images", data.String(), testToken, "", "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var asset struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &asset)
	w = request(h, "GET", "/api/message-images/"+asset.Data.ID, "", testToken, "", "")
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), data.Bytes()) || w.Header().Get("Content-Type") != "image/png" {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request(h, "GET", "/api/message-images/"+asset.Data.ID, "", "", "", ""); w.Code != 401 {
		t.Fatal("image must require auth")
	}
	body := `{"id":"round_close","revision":0,"blocks":[{"type":"text","text":"🔔 {当前期数} 自定义"}]}`
	w = request(h, "POST", "/api/message-templates/preview", body, testToken, "", "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = request(h, "POST", "/api/message-templates", body, testToken, "template-save-key", "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	saved := w.Body.String()
	w = request(h, "POST", "/api/message-templates", body, testToken, "template-save-key", "")
	if w.Code != 200 || w.Body.String() != saved {
		t.Fatal("save retry not idempotent", w.Body.String())
	}
	if w = request(h, "POST", "/api/message-templates", body, testToken, "another-template-key", ""); w.Code != 409 {
		t.Fatal("stale edit must conflict", w.Code)
	}
	if w = request(h, "POST", "/api/message-images", data.String(), testToken, "", "https://evil.example"); w.Code != 403 {
		t.Fatal("cross origin upload allowed")
	}
}
