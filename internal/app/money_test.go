package app

import (
	"encoding/json"
	"path/filepath"
	"riftledger/internal/sqlite"
	"strings"
	"testing"
)

func TestMoneyPrecisionAndWireCompatibility(t *testing.T) {
	for input, want := range map[string]Money{"0": 0, "0.001": 1, "1000.199": 1000199, "-0.001": -1, "1000000000000": MoneyLimit} {
		got, err := ParseMoney(input)
		if err != nil || got != want {
			t.Fatalf("%s: %v %v", input, got, err)
		}
		raw, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		var back Money
		if err = json.Unmarshal(raw, &back); err != nil || back != want {
			t.Fatalf("roundtrip %s: %v", raw, err)
		}
		if err = json.Unmarshal([]byte(`"`+input+`"`), &back); err != nil || back != want {
			t.Fatalf("string %s: %v", input, err)
		}
	}
	for _, input := range []string{"1.0001", "0.0005", "1000000000000.001", "1e3", "NaN", "", "null", "true"} {
		var m Money
		if json.Unmarshal([]byte(input), &m) == nil {
			t.Fatalf("accepted %q", input)
		}
	}
	if asJSON(Points(1000)) != "1000" || asJSON(Money(1000199)) != "1000.199" {
		t.Fatal("public point units changed")
	}
	for _, tc := range []struct {
		stake Money
		rate  float64
		want  Money
	}{{Points(33), 1.2, 39600}, {10005, 1.23, 12306}, {1, .5, 1}, {1, .49, 0}, {Points(33), 0, 0}, {Points(100), .29, Points(29)}} {
		if got := profitFor(tc.stake, tc.rate); got != tc.want {
			t.Fatalf("profit %v * %v = %v, want %v", tc.stake, tc.rate, got, tc.want)
		}
	}
	if _, err := payoutHundredths(.29); err != nil {
		t.Fatal(err)
	}
}

func TestFractionalMoneyFullLifecycle(t *testing.T) {
	for _, outcome := range []string{"WIN", "LOSS", "VOID"} {
		t.Run(outcome, func(t *testing.T) {
			db, err := sqlite.Open(filepath.Join(t.TempDir(), "fraction.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			s, err := New(db, RuntimeConfig{GroupID: -123})
			if err != nil {
				t.Fatal(err)
			}
			if err = s.EnablePlayerOnly(); err != nil {
				t.Fatal(err)
			}
			exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.SetPlayer(tx, 111, "小数玩家", true) })
			exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.Adjust(tx, "tg:111", 1000199, "初始化", newID()) })
			rules := DefaultRules()
			rules.Confirmed = true
			rules.MinStake = 1
			rules.Payout[9] = 1.23
			exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.SaveRules(tx, 1, rules) })
			r := exec(t, s, func(tx *sqlite.Tx) (any, error) {
				return s.CreateDraft(tx, ConfigureRound{Number: "2026-09-21-0123", Banker: 1, Heroes: heroes()})
			}).(Round)
			exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.OpenRound(tx, r.ID) })
			r, _ = s.ReadRound(r.ID)
			for _, amount := range []Money{1, -1} {
				exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.Adjust(tx, "tg:111", amount, "最小单位", newID()) })
			}
			if acc(t, s, "tg:111").Balance != 1000199 {
				t.Fatal("fractional adjustment drift")
			}
			if err = s.HandleUpdate(update(700, 111, r.OpenedAt+1, "上分0.001")); err != nil {
				t.Fatal(err)
			}
			requests, err := s.BalanceRequests(10, 0)
			if err != nil {
				t.Fatal(err)
			}
			rows := requests.(map[string]any)["rows"].([]sqlite.Row)
			if len(rows) != 1 || rows[0]["amount"] != "0.001" || rows[0]["current_balance"] != "1000.199" {
				t.Fatalf("requests %v", rows)
			}
			exec(t, s, func(tx *sqlite.Tx) (any, error) {
				return s.ResolveBalanceRequest(tx, rows[0].Int("id"), "APPROVED", "")
			})
			if err = s.HandleUpdate(update(701, 111, r.OpenedAt+1, "下分0.001")); err != nil {
				t.Fatal(err)
			}
			pending, _ := db.Query("SELECT id FROM balance_requests WHERE state='PENDING'")
			exec(t, s, func(tx *sqlite.Tx) (any, error) {
				return s.ResolveBalanceRequest(tx, pending[0].Int("id"), "APPROVED", "")
			})
			if err = s.HandleUpdate(update(702, 111, r.OpenedAt+1, "2.10.005")); err != nil {
				t.Fatal(err)
			}
			if err = s.HandleUpdate(update(702, 111, r.OpenedAt+1, "2.10.005")); err != nil {
				t.Fatal(err)
			}
			if acc(t, s, "tg:111").Locked != 10005 {
				t.Fatal("fractional bet not reserved exactly once")
			}
			expectFailure(t, s, func(tx *sqlite.Tx) (any, error) {
				return s.Adjust(tx, "tg:111", -1000199, "冻结不可下分", newID())
			})
			d := damages("12710", "12745")
			want := Money(1012505)
			profit := Money(12306)
			if outcome == "LOSS" {
				d = damages("12737", "12745")
				want = 990194
				profit = -10005
			}
			if outcome == "VOID" {
				d = damages("999", "12745")
				want = 1000199
				profit = 0
			}
			p := finish(t, s, r, 1200, d)
			if len(p.Lines) != 1 || p.Lines[0].GameDelta != profit {
				t.Fatalf("settlement %+v", p.Lines)
			}
			a := acc(t, s, "tg:111")
			if a.Balance != want || a.Locked != 0 {
				t.Fatalf("balance %+v", a)
			}
			// Replaying a settled round must not credit twice.
			exec(t, s, func(tx *sqlite.Tx) (any, error) {
				return s.Settle(tx, r.ID, SettleInput{DurationSeconds: 1200, Damages: d, PreviewToken: p.Token})
			})
			balanced(t, s)
			s2, err := New(db, RuntimeConfig{})
			if err != nil {
				t.Fatal(err)
			}
			if err = s2.EnablePlayerOnly(); err != nil {
				t.Fatal(err)
			}
			if acc(t, s2, "tg:111").Balance != want {
				t.Fatal("restart changed balance")
			}
			summary, err := s2.PlayerSummary(50, 0)
			if err != nil {
				t.Fatal(err)
			}
			if summary.(map[string]any)["rows"].([]sqlite.Row)[0]["balance"] != want.String() {
				t.Fatal("summary unit mismatch")
			}
			messages, _ := db.Query("SELECT payload FROM outbox WHERE key=?", "round-result:"+r.ID)
			if len(messages) != 1 || !strings.Contains(messages[0]["payload"], profit.String()) {
				t.Fatal("group result lost decimals")
			}
		})
	}
}
