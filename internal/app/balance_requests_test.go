package app

import (
	"testing"

	"riftledger/internal/sqlite"
)

func TestParseBalanceRequest(t *testing.T) {
	valid := []string{"上分100", "上分 100", "上分+100", "上分 +100", "上100", "下分100", "回100", "回 +100"}
	for _, text := range valid {
		kind, amount, ok := ParseBalanceRequest(text)
		if !ok || amount != 100 || (kind != "CREDIT" && kind != "DEBIT") {
			t.Fatalf("%q => %q %d %v", text, kind, amount, ok)
		}
	}
	for _, text := range []string{"上分0", "下分-1", "回1.5", "上分1000000000001", "上分1e2", "上分abc", "上分"} {
		if _, _, ok := ParseBalanceRequest(text); ok {
			t.Fatalf("invalid request accepted: %q", text)
		}
	}
}

func TestApprovedTopUpAutomaticallyEnablesPlayer(t *testing.T) {
	s, r := fixture(t, "settlement", "refund", "fees")
	exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.SetPlayer(tx, 333, "待上分玩家", false) })
	if e := s.HandleUpdate(update(700, 333, r.OpenedAt+1, "上分100")); e != nil {
		t.Fatal(e)
	}
	if acc(t, s, "tg:333").Enabled {
		t.Fatal("top-up request enabled player before approval")
	}
	rows, e := s.DB.Query("SELECT id FROM balance_requests WHERE account_id='tg:333' AND state='PENDING'")
	if e != nil || len(rows) != 1 {
		t.Fatalf("pending top-up request missing: %v", e)
	}
	id := rows[0].Int("id")
	exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.ResolveBalanceRequest(tx, id, "APPROVED", "") })
	a := acc(t, s, "tg:333")
	if !a.Enabled || a.Balance != 100 {
		t.Fatalf("approved top-up did not enable and credit player: enabled=%v balance=%d", a.Enabled, a.Balance)
	}
}
