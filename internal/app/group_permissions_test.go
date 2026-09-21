package app

import (
	"encoding/json"
	"riftledger/internal/sqlite"
	"testing"
)

func TestCloseAnnouncementAndOptionalGroupControl(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "off", true: "on"}[enabled], func(t *testing.T) {
			s, r := fixture(t, "settlement", "refund", "fees")
			s.Config.GroupID = -88
			s.Config.MuteOnClose = enabled
			for i := 0; i < 2; i++ {
				if _, err := s.Command("close-request-key", "close", r.ID, func(tx *sqlite.Tx) (any, error) { return s.CloseRound(tx, r.ID) }); err != nil {
					t.Fatal(err)
				}
			}
			rows, err := s.DB.Query("SELECT card_key,payload FROM outbox WHERE key=?", "round-close:"+r.ID)
			if err != nil || len(rows) != 1 || rows[0]["card_key"] != "" {
				t.Fatalf("missing standalone close notice %v %v", rows, err)
			}
			jobs, _ := s.DB.Query("SELECT * FROM group_permissions WHERE round_id=?", r.ID)
			if (len(jobs) == 1) != enabled {
				t.Fatal("mute toggle ignored")
			}
			// Disabling the option or changing the group must not strand the original group.
			s.Config.GroupID = -99
			s.Config.MuteOnClose = false
			exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.CancelRound(tx, r.ID, "测试取消") })
			release, _ := s.DB.Query("SELECT payload FROM outbox WHERE key=?", "group-restore:"+r.ID)
			if (len(release) == 1) != enabled {
				t.Fatal("restore missing or unsolicited")
			}
			if enabled {
				var payload MessagePayload
				if err = json.Unmarshal([]byte(release[0]["payload"]), &payload); err != nil || payload.ChatID != -88 {
					t.Fatal("restored wrong group")
				}
			}
		})
	}
}

func TestCloseNotificationFailureRollsBack(t *testing.T) {
	s, r := fixture(t, "settlement", "refund", "fees")
	s.Config.GroupID = -88
	s.Config.MuteOnClose = true
	if _, err := s.DB.Exec(`CREATE TEMP TRIGGER reject_close BEFORE INSERT ON outbox WHEN NEW.key LIKE 'round-close:%' BEGIN SELECT RAISE(ABORT,'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	expectFailure(t, s, func(tx *sqlite.Tx) (any, error) { return s.CloseRound(tx, r.ID) })
	after, _ := s.ReadRound(r.ID)
	if after.State != "OPEN" {
		t.Fatal("partial close")
	}
	rows, _ := s.DB.Query("SELECT * FROM group_permissions")
	if len(rows) != 0 {
		t.Fatal("partial mute")
	}
}

func TestCorruptNoticeCannotBlockGroupRecoveryLane(t *testing.T) {
	s, r := fixture(t, "settlement", "refund", "fees")
	s.Config.GroupID = -88
	s.Config.MuteOnClose = true
	if _, err := s.DB.Exec("INSERT INTO outbox(key,chat_id,payload,created_at) VALUES('broken-notice',-88,'{',1)"); err != nil {
		t.Fatal(err)
	}
	exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.CloseRound(tx, r.ID) })
	job, err := s.ClaimOutbox()
	if err != nil || job == nil || job.Payload.GroupAction != "mute" {
		t.Fatalf("claim %v %v", job, err)
	}
	if err = s.CompleteOutbox(*job, "SENT", 0, "test", 0); err != nil {
		t.Fatal(err)
	}
	job, err = s.ClaimOutbox()
	if err != nil || job != nil {
		t.Fatal("malformed notice not isolated", err)
	}
	rows, _ := s.DB.Query("SELECT id,state FROM outbox WHERE key='broken-notice'")
	if rows[0]["state"] != "FAILED" {
		t.Fatal(rows)
	}
	exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.ResolveOutbox(tx, rows[0].Int("id"), "skip", 0) })
}

func TestVersionThreeUpgradeDoesNotRescaleMoney(t *testing.T) {
	s, _ := fixture(t, "settlement", "refund", "fees")
	before := acc(t, s, "tg:111").Balance
	if _, err := s.DB.Exec("UPDATE meta SET value='3' WHERE key='schema_version'"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("DROP TABLE group_permissions"); err != nil {
		t.Fatal(err)
	}
	s2, err := New(s.DB, s.Config)
	if err != nil {
		t.Fatal(err)
	}
	if acc(t, s2, "tg:111").Balance != before {
		t.Fatal("version 3 money was scaled twice")
	}
	if _, err = s.DB.Query("SELECT * FROM group_permissions"); err != nil {
		t.Fatal(err)
	}
}

func TestSettlementQueuesRestoreWithoutDependingOnCurrentSettings(t *testing.T) {
	s, r := fixture(t, "settlement", "refund", "fees")
	s.Config.GroupID = -88
	s.Config.MuteOnClose = true
	finish(t, s, r, 1200, damages("12710", "12745"))
	rows, err := s.DB.Query("SELECT payload FROM outbox WHERE key=?", "group-restore:"+r.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("restore %v %v", rows, err)
	}
}
