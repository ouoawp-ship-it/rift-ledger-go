package app

import (
	"path/filepath"
	"strconv"
	"testing"

	"riftledger/internal/sqlite"
)

func TestBotConnectionTracksReceiverAndExpires(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s, err := New(db, RuntimeConfig{})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ message, state string }{
		{"未启用Telegram；网页与计算服务可用", "disabled"},
		{"正在连接Telegram", "connecting"},
		{"已连接 @testbot", "connecting"},
		{"接收正常｜最近轮询", "online"},
		{"接收异常，将重试：网络异常", "error"},
		{"已停止：Telegram错误401", "error"},
		{"接收正常｜最近轮询", "online"},
	} {
		if err := s.BotStatus(test.message); err != nil {
			t.Fatal(err)
		}
		status, err := s.BotConnection()
		if err != nil || status.State != test.state || status.UpdatedAt == 0 || status.Message != test.message {
			t.Fatalf("%s: %+v, %v", test.message, status, err)
		}
	}
	if _, err := db.Exec("UPDATE meta SET value=? WHERE key='bot_status_at'", strconv.FormatInt(now()-botStatusMaxAge-1, 10)); err != nil {
		t.Fatal(err)
	}
	status, err := s.BotConnection()
	if err != nil || status.State != "stale" {
		t.Fatalf("old healthy status must expire: %+v, %v", status, err)
	}
	if _, err := db.Exec("DELETE FROM meta WHERE key='bot_status_at'"); err != nil {
		t.Fatal(err)
	}
	status, err = s.BotConnection()
	if err != nil || status.State != "stale" {
		t.Fatalf("legacy status without timestamp cannot claim online: %+v, %v", status, err)
	}
	if err := s.BotStatus("未启用Telegram"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE meta SET value='1' WHERE key='bot_status_at'"); err != nil {
		t.Fatal(err)
	}
	status, err = s.BotConnection()
	if err != nil || status.State != "disabled" {
		t.Fatalf("disabled is not a missing heartbeat: %+v, %v", status, err)
	}
}
