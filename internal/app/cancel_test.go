package app

import (
	"riftledger/internal/sqlite"
	"testing"
)

func TestCancelRoundRefundsAndCannotRepeat(t *testing.T) {
	for _, timing := range []string{"acceptance", "settlement"} {
		for _, closed := range []bool{false, true} {
			t.Run(timing+map[bool]string{true: "closed", false: "open"}[closed], func(t *testing.T) {
				s, r := fixture(t, timing, "keep", "fees")
				before := acc(t, s, "tg:111").Balance
				place(t, s, r, "tg:111", 2, 100)
				if closed {
					exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.CloseRound(tx, r.ID) })
				}
				expectFailure(t, s, func(tx *sqlite.Tx) (any, error) { return s.CancelRound(tx, r.ID, "") })
				p := exec(t, s, func(tx *sqlite.Tx) (any, error) {
					return s.CancelRound(tx, r.ID, "服务器中断，无法确认伤害")
				}).(Preview)
				a := acc(t, s, "tg:111")
				if a.Balance != before || a.Locked != 0 || !p.WholeVoid || p.NextRoundID == "" {
					t.Fatalf("refund incorrect: %+v %+v", a, p)
				}
				if len(p.Lines) != 1 || p.Lines[0].Outcome != "VOID" || p.Lines[0].Fee != 0 {
					t.Fatalf("unexpected lines: %+v", p.Lines)
				}
				expectFailure(t, s, func(tx *sqlite.Tx) (any, error) { return s.CancelRound(tx, r.ID, "重复退款") })
				if acc(t, s, "tg:111").Balance != before {
					t.Fatal("duplicate refund")
				}
				balanced(t, s)
			})
		}
	}
}
