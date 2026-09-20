package app

import "testing"

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
