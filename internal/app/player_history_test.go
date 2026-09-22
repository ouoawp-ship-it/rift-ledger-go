package app

import (
	"path/filepath"
	"testing"

	"riftledger/internal/sqlite"
)

func historyFixture(t *testing.T, payouts ...float64) (*Service, Round) {
	t.Helper()
	db, e := sqlite.Open(filepath.Join(t.TempDir(), "history.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	s, e := New(db, RuntimeConfig{})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.EnablePlayerOnly(); e != nil {
		t.Fatal(e)
	}
	rules := DefaultRules()
	rules.Confirmed = true
	rules.MinStake = 1
	rules.Payout[10] = 1.2
	if len(payouts) > 0 {
		rules.Payout[10] = payouts[0]
	}
	exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.SaveRules(tx, 1, rules) })
	for _, id := range []int64{111, 222} {
		a := exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.SetPlayer(tx, id, "历史玩家", true) }).(Account)
		exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.Adjust(tx, a.ID, Money(1000199), "初始上分", newID()) })
	}
	r := exec(t, s, func(tx *sqlite.Tx) (any, error) {
		return s.CreateDraft(tx, ConfigureRound{Number: "2026-09-22-0001", Banker: 1, Heroes: heroes()})
	}).(Round)
	exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.OpenRound(tx, r.ID) })
	return s, r
}

func TestPlayerHistoryPendingAndZeroOddsWin(t *testing.T) {
	s, r := historyFixture(t, 0)
	exec(t, s, func(tx *sqlite.Tx) (any, error) {
		return s.PlaceBet(tx, BetInput{AccountID: "tg:111", RoundID: r.ID, Position: 2, Stake: 1001})
	})
	q := PlayerHistoryQuery{AccountID: "tg:111", View: "bets", Filter: "RESERVED", Limit: 25}
	page, e := s.PlayerHistory(q)
	if e != nil || len(page.Rows) != 1 || page.Rows[0]["settled_at"] != "0" {
		t.Fatal(page, e)
	}
	finish(t, s, r, 1200, damages("12710", "12737"))
	q.Filter = "WIN"
	page, e = s.PlayerHistory(q)
	if e != nil || len(page.Rows) != 1 || page.Rows[0]["net_delta"] != "0.000" {
		t.Fatal(page, e)
	}
}

func TestPlayerHistoryPersistedOutcomesAndMoney(t *testing.T) {
	s, r := historyFixture(t)
	for _, pos := range []int{2, 3, 4} {
		exec(t, s, func(tx *sqlite.Tx) (any, error) {
			return s.PlaceBet(tx, BetInput{AccountID: "tg:111", RoundID: r.ID, Position: pos, Stake: 1001})
		})
	}
	exec(t, s, func(tx *sqlite.Tx) (any, error) {
		return s.PlaceBet(tx, BetInput{AccountID: "tg:222", RoundID: r.ID, Position: 2, Stake: 1001})
	})
	finish(t, s, r, 1200, []string{"12745", "12737", "12710", "123", "12710"})
	// Later odds must not rewrite what this history endpoint displays.
	exec(t, s, func(tx *sqlite.Tx) (any, error) {
		rules := DefaultRules()
		rules.Confirmed = true
		rules.Payout[10] = 100
		return s.SaveRules(tx, 2, rules)
	})
	page, e := s.PlayerHistory(PlayerHistoryQuery{AccountID: "tg:111", View: "bets", Limit: 25})
	if e != nil {
		t.Fatal(e)
	}
	if len(page.Rows) != 3 {
		t.Fatal(page)
	}
	deltas := map[string]string{}
	for _, row := range page.Rows {
		if row["account_id"] != "tg:111" || row["round_number"] != r.Number || row["stake"] != "1.001" || row["hero"] == "" {
			t.Fatal(row)
		}
		deltas[row["state"]] = row["net_delta"]
	}
	if deltas["WIN"] != "1.201" || deltas["LOSS"] != "-1.001" || deltas["VOID"] != "0.000" {
		t.Fatal(deltas)
	}
	page, e = s.PlayerHistory(PlayerHistoryQuery{AccountID: "tg:111", View: "entries", Filter: "GAME", Limit: 25})
	if e != nil {
		t.Fatal(e)
	}
	for _, row := range page.Rows {
		if row["kind"] != "GAME" || row["round_number"] != r.Number {
			t.Fatal(row)
		}
	}
	balanced(t, s)
}

func TestPlayerHistoryDateBoundariesCursorAndRequests(t *testing.T) {
	s, _ := historyFixture(t)
	start, end, e := historyDates("2026-09-22", "2026-09-22")
	if e != nil || end-start != 86400 {
		t.Fatal(start, end, e)
	}
	for i, at := range []int64{start - 1, start, start, start, end - 1, end} {
		_, e = s.DB.Exec(`INSERT INTO balance_requests(update_id,account_id,kind,amount,balance_at_request,state,created_at,processed_at,note) VALUES(?,'tg:111','CREDIT',1000199,0,?,?,?,'历史申请')`, 100+i, []string{"PENDING", "APPROVED", "REJECTED", "PENDING", "APPROVED", "PENDING"}[i], at, at+1)
		if e != nil {
			t.Fatal(e)
		}
	}
	query := PlayerHistoryQuery{AccountID: "tg:111", View: "requests", From: "2026-09-22", To: "2026-09-22", Limit: 2}
	seen := map[string]bool{}
	for {
		page, e := s.PlayerHistory(query)
		if e != nil {
			t.Fatal(e)
		}
		for _, row := range page.Rows {
			if seen[row["id"]] || row.Int("created_at") < start || row.Int("created_at") >= end || row["amount"] != "1000.199" {
				t.Fatal(row)
			}
			seen[row["id"]] = true
		}
		if page.Next == "" {
			break
		}
		query.Cursor = page.Next
	}
	if len(seen) != 4 {
		t.Fatal(seen)
	}
	query.Cursor = ""
	query.Filter = "REJECTED"
	page, e := s.PlayerHistory(query)
	if e != nil || len(page.Rows) != 1 || page.Rows[0]["state"] != "REJECTED" {
		t.Fatal(page, e)
	}
	// No account rows are ever returned for the house or unknown IDs.
	query.AccountID = "house"
	if _, e = s.PlayerHistory(query); e == nil {
		t.Fatal("non-player accepted")
	}
}

func TestPlayerHistoryApprovedRequestsAndManualAdjustments(t *testing.T) {
	s, _ := historyFixture(t)
	for i, kind := range []string{"CREDIT", "DEBIT"} {
		_, e := s.DB.Exec(`INSERT INTO balance_requests(update_id,account_id,kind,amount,balance_at_request,created_at) VALUES(?,'tg:111',?,1234,1000199,?)`, i+1, kind, now())
		if e != nil {
			t.Fatal(e)
		}
		exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.ResolveBalanceRequest(tx, int64(i+1), "APPROVED", "同意") })
	}
	page, e := s.PlayerHistory(PlayerHistoryQuery{AccountID: "tg:111", View: "entries", Filter: "DEBIT", Limit: 25})
	if e != nil || len(page.Rows) != 1 || page.Rows[0]["delta"] != "-1.234" || page.Rows[0]["adjustment_source"] != "REQUEST" {
		t.Fatal(page, e)
	}
	page, e = s.PlayerHistory(PlayerHistoryQuery{AccountID: "tg:111", View: "entries", Filter: "CREDIT", Limit: 25})
	if e != nil || len(page.Rows) != 2 {
		t.Fatal(page, e)
	}
	if page.Rows[1]["adjustment_source"] != "MANUAL" {
		t.Fatal(page)
	}
}

func TestPlayerHistorySearchAndInvalidFilters(t *testing.T) {
	s, _ := historyFixture(t)
	_, e := s.DB.Exec(`INSERT INTO telegram_users(telegram_user_id,username,first_name,last_name,first_contact,last_contact) VALUES(111,'player_one','','',1,1)`)
	if e != nil {
		t.Fatal(e)
	}
	for _, q := range []string{"@player_one", "111", "tg:111"} {
		v, e := s.SearchPlayers(q)
		if e != nil {
			t.Fatal(e)
		}
		rows := v.(map[string]any)["rows"].([]Account)
		if len(rows) != 1 || rows[0].ID != "tg:111" {
			t.Fatal(v)
		}
	}
	v, e := s.SearchPlayers("%_")
	if e != nil || len(v.(map[string]any)["rows"].([]Account)) != 0 {
		t.Fatal(v, e)
	}
	for _, q := range []PlayerHistoryQuery{
		{View: "bets", Limit: 25},
		{AccountID: "tg:111", View: "wrong", Limit: 25},
		{AccountID: "tg:111", View: "bets", Filter: "APPROVED", Limit: 25},
		{AccountID: "tg:111", View: "bets", From: "2026-09-23", To: "2026-09-22", Limit: 25},
		{AccountID: "tg:111", View: "bets", From: "2026-02-30", Limit: 25},
		{AccountID: "tg:111", View: "entries", Cursor: "bad", Limit: 25},
	} {
		if _, e := s.PlayerHistory(q); e == nil {
			t.Fatalf("accepted invalid query %+v", q)
		}
	}
}
