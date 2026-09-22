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

func TestCombinedPhotoCaptionDelivery(t *testing.T) {
	for _, mode := range []string{"success", "rejected", "unknown", "limited"} {
		t.Run(mode, func(t *testing.T) {
			s := svc(t)
			var data bytes.Buffer
			_ = png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 16, 16)))
			asset, err := s.SaveMessageImage(data.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			caption := "2026-09-22-0001期\n已封盘 🔔\n最低20.001"
			markup := &app.Keyboard{Rows: [][]app.Button{{{Text: "查看", URL: "https://t.me/example_bot"}}}}
			payload, _ := json.Marshal(app.MessagePayload{ChatID: 123, Text: caption, Caption: caption, ImageID: asset.(map[string]any)["id"].(string), Markup: markup})
			_, err = s.DB.Exec("INSERT INTO outbox(key,chat_id,payload,created_at) VALUES('caption',123,?,1)", string(payload))
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			c := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if !strings.HasSuffix(r.URL.Path, "/sendPhoto") {
					t.Error("expected a single sendPhoto")
				}
				if err := r.ParseMultipartForm(3 << 20); err != nil {
					t.Error(err)
					return
				}
				defer r.MultipartForm.RemoveAll()
				if r.FormValue("caption") != caption || !strings.Contains(r.FormValue("reply_markup"), "example_bot") {
					t.Error("caption or buttons missing")
				}
				switch mode {
				case "rejected":
					w.WriteHeader(400)
					_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"bad photo"}`))
				case "unknown":
					_, _ = w.Write([]byte(`{"ok":`))
				case "limited":
					w.WriteHeader(429)
					_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"parameters":{"retry_after":20}}`))
				default:
					_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":88}}`))
				}
			})
			if sent, err := c.SendOne(context.Background(), s); !sent || err != nil {
				t.Fatal(sent, err)
			}
			rows, _ := s.DB.Query("SELECT state,message_id FROM outbox WHERE key='caption'")
			want := map[string]string{"success": "SENT", "rejected": "FAILED", "unknown": "UNKNOWN", "limited": "PENDING"}[mode]
			if len(rows) != 1 || rows[0]["state"] != want {
				t.Fatal(rows)
			}
			if sent, err := c.SendOne(context.Background(), s); sent || err != nil {
				t.Fatal("duplicate or unwanted fallback", sent, err)
			}
			if calls != 1 {
				t.Fatal(calls)
			}
		})
	}
}
