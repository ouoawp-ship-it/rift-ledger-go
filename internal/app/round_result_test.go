package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"riftledger/internal/sqlite"
)

func TestSettlementQueuesNewRoundResult(t *testing.T) {
	for _, tc := range []struct {
		name   string
		group  int64
		banker string
		bet    bool
	}{
		{"normal", -100123, "12710", true},
		{"no bets", -100123, "12710", false},
		{"banker void", -100123, "123", true},
		{"no group", 0, "12710", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := reliabilityService(t)
			s.Config.GroupID = tc.group
			if err := s.EnablePlayerOnly(); err != nil {
				t.Fatal(err)
			}
			rules := DefaultRules()
			rules.Confirmed = true
			exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.SaveRules(tx, 1, rules) })
			r := exec(t, s, func(tx *sqlite.Tx) (any, error) {
				return s.CreateDraft(tx, ConfigureRound{Number: "2026-09-21-0001", Banker: 1, Heroes: heroes()})
			}).(Round)
			exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.OpenRound(tx, r.ID) })
			if tc.bet {
				exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.SetPlayer(tx, 111, "测试玩家", true) })
				exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.Adjust(tx, "tg:111", 1000, "测试", newID()) })
				place(t, s, r, "tg:111", 2, 100)
			}
			exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.CloseRound(tx, r.ID) })
			in := SettleInput{Damages: damages(tc.banker, "12745")}
			p, err := s.Preview(r.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			// A failed confirmation must neither advance the round nor announce it.
			expectFailure(t, s, func(tx *sqlite.Tx) (any, error) { return s.Settle(tx, r.ID, in) })
			before, err := s.DB.Query("SELECT id FROM outbox WHERE key=?", "round-result:"+r.ID)
			if err != nil || len(before) != 0 {
				t.Fatal("premature announcement", before, err)
			}
			in.PreviewToken = p.Token
			if tc.name == "normal" {
				beforeAccount := acc(t, s, "tg:111")
				if _, err := s.DB.Exec(`CREATE TEMP TRIGGER fail_result_notice BEFORE INSERT ON outbox
				 WHEN NEW.key LIKE 'round-result:%' BEGIN SELECT RAISE(ABORT,'simulated queue failure'); END`); err != nil {
					t.Fatal(err)
				}
				expectFailure(t, s, func(tx *sqlite.Tx) (any, error) { return s.Settle(tx, r.ID, in) })
				afterAccount := acc(t, s, "tg:111")
				unchanged, err := s.ReadRound(r.ID)
				if err != nil || unchanged.State != "CLOSED" || unchanged.NextRoundID != "" || afterAccount.Balance != beforeAccount.Balance || afterAccount.Locked != beforeAccount.Locked {
					t.Fatal("queue failure partially committed settlement", unchanged, afterAccount, err)
				}
				if _, err := s.DB.Exec("DROP TRIGGER fail_result_notice"); err != nil {
					t.Fatal(err)
				}
			}
			// Exercise both persisted command replay and a different request key.
			for _, key := range []string{"result-confirm-0001", "result-confirm-0001", "result-confirm-0002"} {
				if _, err := s.Command(key, "settle "+r.ID, in, func(tx *sqlite.Tx) (any, error) { return s.Settle(tx, r.ID, in) }); err != nil {
					t.Fatal(err)
				}
			}
			rows, err := s.DB.Query("SELECT * FROM outbox WHERE key=?", "round-result:"+r.ID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.group == 0 {
				if len(rows) != 0 {
					t.Fatal("announcement without destination")
				}
				return
			}
			if len(rows) != 1 {
				t.Fatalf("expected exactly one announcement, got %d", len(rows))
			}
			row := rows[0]
			if row["card_key"] != "" || row["state"] != "PENDING" || row.Int("chat_id") != tc.group {
				t.Fatal("must queue a new group message", row)
			}
			var payload MessagePayload
			if err := json.Unmarshal([]byte(row["payload"]), &payload); err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{r.Number + "期", "开奖结果", "伤害 12745", "牛9", "【庄家】", "玩家本期游戏合计"} {
				if !strings.Contains(payload.Text, want) {
					t.Fatalf("missing %q: %s", want, payload.Text)
				}
			}
			for i, h := range heroes() {
				if !strings.Contains(payload.Text, fmt.Sprintf("%d号 %s", i+1, h.Name)) {
					t.Fatal("missing hero", payload.Text)
				}
			}
			if tc.banker == "123" {
				if !strings.Contains(payload.Text, "整期流局") || strings.Contains(payload.Text, "结果：闲赢") {
					t.Fatal(payload.Text)
				}
			} else if !strings.Contains(payload.Text, "结果：闲赢") || !strings.Contains(payload.Text, "结果：庄赢") {
				t.Fatal(payload.Text)
			}
			next, err := s.DB.Query("SELECT id FROM outbox WHERE key=?", "next:"+r.ID)
			if err != nil || len(next) != 1 || row.Int("id") >= next[0].Int("id") {
				t.Fatal("result must precede next-round notice", next, err)
			}
		})
	}
}
