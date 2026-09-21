package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"riftledger/internal/app"
	"riftledger/internal/sqlite"
	"strings"
	"testing"
)

func closedGroup(t *testing.T) (*app.Service, string) {
	t.Helper()
	s := svc(t)
	var id string
	err := s.DB.Transaction(func(tx *sqlite.Tx) error {
		rules := app.DefaultRules()
		rules.Confirmed = true
		if _, err := s.SaveRules(tx, 1, rules); err != nil {
			return err
		}
		v, err := s.CreateDraft(tx, app.ConfigureRound{Number: "2026-09-22-0001", Banker: 1, Heroes: []app.Hero{{ID: "A", Name: "A"}, {ID: "B", Name: "B"}, {ID: "C", Name: "C"}, {ID: "D", Name: "D"}, {ID: "E", Name: "E"}}})
		if err != nil {
			return err
		}
		id = v.(app.Round).ID
		if _, err = s.OpenRound(tx, id); err != nil {
			return err
		}
		s.Config.GroupID = -88
		s.Config.MuteOnClose = true
		_, err = s.CloseRound(tx, id)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	// A failed notification must not prevent mute/restore from running.
	if _, err = s.DB.Exec("UPDATE outbox SET state='FAILED' WHERE COALESCE(json_extract(payload,'$.group_action'),'')=''"); err != nil {
		t.Fatal(err)
	}
	return s, id
}
func cancelGroup(t *testing.T, s *app.Service, id string) {
	t.Helper()
	if err := s.DB.Transaction(func(tx *sqlite.Tx) error { _, err := s.CancelRound(tx, id, "测试取消"); return err }); err != nil {
		t.Fatal(err)
	}
}

func TestGroupPermissionsRetryRestartAndRestore(t *testing.T) {
	s, id := closedGroup(t)
	original := map[string]bool{"can_send_messages": true, "can_send_photos": false, "can_send_polls": true, "can_invite_users": true, "can_pin_messages": false}
	gets, sets := 0, 0
	var restored map[string]bool
	c := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			ChatID      int64           `json:"chat_id"`
			Permissions map[string]bool `json:"permissions"`
			Independent bool            `json:"use_independent_chat_permissions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload.ChatID != -88 {
			t.Error("wrong target group")
		}
		if strings.HasSuffix(r.URL.Path, "getChat") {
			gets++
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"type": "supergroup", "permissions": original}})
			return
		}
		if !strings.HasSuffix(r.URL.Path, "setChatPermissions") {
			t.Error("unexpected method", r.URL.Path)
		}
		if !payload.Independent {
			t.Error("permission implications could widen access")
		}
		sets++
		if sets <= 2 {
			for key, value := range payload.Permissions {
				if strings.HasPrefix(key, "can_send_") && value {
					t.Error("not muted", key)
				}
			}
			if !payload.Permissions["can_invite_users"] {
				t.Error("unrelated permission lost")
			}
		} else {
			restored = payload.Permissions
		}
		if sets == 1 {
			w.Write([]byte(`{"ok":false,"error_code":500,"description":"lost confirmation"}`))
			return
		}
		w.Write([]byte(`{"ok":true,"result":true}`))
	})
	send := func() {
		t.Helper()
		if ok, err := c.SendOne(context.Background(), s); err != nil || !ok {
			t.Fatalf("send %v %v", ok, err)
		}
	}
	send()
	// Simulate termination while a retry is in flight. Initialization must recover it.
	if _, err := s.DB.Exec("UPDATE outbox SET state='INFLIGHT',next_at=0 WHERE key=?", "group-mute:"+id); err != nil {
		t.Fatal(err)
	}
	restarted, err := app.New(s.DB, s.Config)
	if err != nil {
		t.Fatal(err)
	}
	s = restarted
	send()
	s.Config.GroupID = -99
	s.Config.MuteOnClose = false
	cancelGroup(t, s, id)
	send()
	if gets != 1 || sets != 3 || !reflect.DeepEqual(restored, original) {
		t.Fatalf("get=%d set=%d restored=%v", gets, sets, restored)
	}
	rows, _ := s.DB.Query("SELECT state,last_error FROM outbox WHERE key=?", "group-restore:"+id)
	if rows[0]["state"] != "SENT" || !strings.Contains(rows[0]["last_error"], "已恢复") {
		t.Fatal(rows)
	}
}

func TestLateMuteDoesNotSilenceSettledGroup(t *testing.T) {
	s, id := closedGroup(t)
	cancelGroup(t, s, id)
	c := mockClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("late mute made a Telegram call") })
	for i := 0; i < 2; i++ {
		if ok, err := c.SendOne(context.Background(), s); err != nil || !ok {
			t.Fatalf("send %v %v", ok, err)
		}
	}
	rows, _ := s.DB.Query("SELECT state FROM outbox WHERE key IN (?,?)", "group-mute:"+id, "group-restore:"+id)
	for _, row := range rows {
		if row["state"] != "SENT" {
			t.Fatal(row)
		}
	}
}

func TestPermissionDeniedDoesNotBlockAnnouncementsOrRestore(t *testing.T) {
	s, id := closedGroup(t)
	calls := 0
	c := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch {
		case strings.HasSuffix(r.URL.Path, "getChat"):
			fmt.Fprint(w, `{"ok":true,"result":{"type":"supergroup","permissions":{"can_send_messages":true}}}`)
		case strings.HasSuffix(r.URL.Path, "setChatPermissions"):
			fmt.Fprint(w, `{"ok":false,"error_code":403,"description":"not enough rights"}`)
		case strings.HasSuffix(r.URL.Path, "sendMessage"):
			fmt.Fprint(w, `{"ok":true,"result":{"message_id":7}}`)
		default:
			t.Error(r.URL.Path)
		}
	})
	if _, err := c.SendOne(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.DB.Query("SELECT state FROM outbox WHERE key=?", "group-mute:"+id)
	if rows[0]["state"] != "FAILED" {
		t.Fatal(rows)
	}
	s.DB.Exec("UPDATE outbox SET state='PENDING' WHERE key=?", "round-close:"+id)
	if ok, err := c.SendOne(context.Background(), s); err != nil || !ok {
		t.Fatal("permission failure blocked close announcement", err)
	}
	cancelGroup(t, s, id)
	if ok, err := c.SendOne(context.Background(), s); err != nil || !ok {
		t.Fatal("failed mute blocked recovery", err)
	}
	rows, _ = s.DB.Query("SELECT id,state FROM outbox WHERE key=?", "group-restore:"+id)
	if rows[0]["state"] != "FAILED" {
		t.Fatal("permission failure hidden", rows)
	}
	if err := s.DB.Transaction(func(tx *sqlite.Tx) error { _, err := s.ResolveOutbox(tx, rows[0].Int("id"), "skip", 0); return err }); err == nil {
		t.Fatal("unsafe restore skip allowed")
	}
	if calls != 4 {
		t.Fatal("unexpected network calls", calls)
	}
}
