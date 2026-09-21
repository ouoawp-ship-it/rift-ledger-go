package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"riftledger/internal/app"
	"strings"
	"testing"
)

func TestCustomPhotoSequenceAndFailureIsolation(t *testing.T) {
	for _, mode := range []string{"success", "rejected", "unknown", "limited"} {
		t.Run(mode, func(t *testing.T) {
			s := svc(t)
			var data bytes.Buffer
			_ = png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 16, 16)))
			asset, e := s.SaveMessageImage(data.Bytes())
			if e != nil {
				t.Fatal(e)
			}
			p, _ := json.Marshal(app.MessagePayload{ChatID: 123, Text: "自定义消息图片", ImageID: asset.(map[string]any)["id"].(string)})
			if _, e = s.DB.Exec("INSERT INTO outbox(key,chat_id,payload,created_at) VALUES('photo',123,?,1)", string(p)); e != nil {
				t.Fatal(e)
			}
			queue(t, s, "text", "")
			var methods []string
			c := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/sendPhoto") {
					methods = append(methods, "photo")
					if e := r.ParseMultipartForm(3 << 20); e != nil {
						t.Error(e)
						return
					}
					defer r.MultipartForm.RemoveAll()
					file, _, e := r.FormFile("photo")
					if e != nil {
						t.Error(e)
						return
					}
					defer file.Close()
					raw, _ := io.ReadAll(file)
					if !bytes.Equal(raw, data.Bytes()) || r.FormValue("chat_id") != "123" || r.FormValue("caption") != "" {
						t.Error("wrong multipart photo")
					}
					switch mode {
					case "rejected":
						w.WriteHeader(400)
						_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"bad photo"}`))
						return
					case "unknown":
						_, _ = w.Write([]byte(`{"ok":`))
						return
					case "limited":
						w.WriteHeader(429)
						_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"parameters":{"retry_after":20}}`))
						return
					}
				} else {
					methods = append(methods, "text")
				}
				_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":88}}`))
			})
			if _, e = c.SendOne(context.Background(), s); e != nil {
				t.Fatal(e)
			}
			if mode == "limited" {
				if sent, e := c.SendOne(context.Background(), s); sent || e != nil {
					t.Fatal("rate-limited photo lost ordering", e)
				}
				return
			}
			if sent, e := c.SendOne(context.Background(), s); !sent || e != nil {
				t.Fatal("photo blocked following text", e)
			}
			if strings.Join(methods, ",") != "photo,text" {
				t.Fatal(methods)
			}
			rows, _ := s.DB.Query("SELECT state,media_result,last_error FROM outbox WHERE key='photo'")
			if rows[0]["state"] != "SENT" {
				t.Fatal(rows)
			}
			if mode != "success" && rows[0]["last_error"] == "" {
				t.Fatal("failure must remain visible")
			}
			if sent, e := c.SendOne(context.Background(), s); sent || e != nil {
				t.Fatal("unexpected photo replay", e)
			}
		})
	}
}
