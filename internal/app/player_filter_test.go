package app

import (
	"fmt"
	"strings"
	"testing"

	"riftledger/internal/sqlite"
)

func TestPlayerBalanceFiltersBeforePagination(t *testing.T) {
	s, _ := historyFixture(t)
	for i := int64(300); i < 360; i++ {
		exec(t, s, func(tx *sqlite.Tx) (any, error) {
			return s.SetPlayer(tx, i, fmt.Sprintf("零分玩家%d", i), true)
		})
	}
	exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.Adjust(tx, "tg:359", 1, "最小余额", newID()) })
	check := func(mode, query string, offset, n int, total, sum string) []sqlite.Row {
		t.Helper()
		v, e := s.FilteredPlayerSummary(50, offset, mode, query)
		if e != nil {
			t.Fatal(e)
		}
		page := v.(map[string]any)
		rows := page["rows"].([]sqlite.Row)
		counts := page["filtered"].(sqlite.Row)
		stats := page["stats"].(sqlite.Row)
		if len(rows) != n || counts["players"] != total || counts["balance"] != sum {
			t.Fatalf("%s %q offset %d: rows=%d counts=%v", mode, query, offset, len(rows), counts)
		}
		if stats["players"] != "62" || stats["positive"] != "3" || stats["zero"] != "59" || stats["balance"] != "2000.399" {
			t.Fatalf("global stats changed under filter: %v", stats)
		}
		return rows
	}
	check("all", "", 0, 50, "62", "2000.399")
	check("positive", "", 0, 3, "3", "2000.399")
	first := check("zero", "", 0, 50, "59", "0.000")
	second := check("zero", "", 50, 9, "59", "0.000")
	seen := map[string]bool{}
	for _, row := range append(first, second...) {
		if seen[row["id"]] || row["balance"] != "0.000" {
			t.Fatal("duplicate or wrong balance", row)
		}
		seen[row["id"]] = true
	}
	check("positive", "359", 0, 1, "1", "0.001")
	check("zero", "359", 0, 0, "0", "0.000")
	check("all", "零分玩家", 0, 50, "60", "0.001")
	check("all", "%", 0, 0, "0", "0.000")
	check("all", "' OR 1=1 --", 0, 0, "0", "0.000")
	if _, e := s.FilteredPlayerSummary(50, 0, "bogus", ""); e == nil {
		t.Fatal("accepted invalid filter")
	}
	if _, e := s.FilteredPlayerSummary(50, 0, "all", strings.Repeat("字", 81)); e == nil {
		t.Fatal("accepted overlong search")
	}
	if _, e := s.DB.Exec("INSERT INTO telegram_users VALUES(359,'tiny_balance','','',1,1)"); e != nil {
		t.Fatal(e)
	}
	check("positive", "@tiny_balance", 0, 1, "1", "0.001")
	check("positive", "tiny%balance", 0, 0, "0", "0.000")
	// Eligibility and available balance do not change the meaning of ledger balance.
	exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.SetPlayer(tx, 359, "停用玩家", false) })
	if _, e := s.DB.Exec("UPDATE accounts SET locked=balance WHERE id='tg:359'"); e != nil {
		t.Fatal(e)
	}
	check("positive", "359", 0, 1, "1", "0.001")
}
