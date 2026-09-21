package app

import (
	"path/filepath"
	"riftledger/internal/sqlite"
	"testing"
)

func TestPlayerOnlySettlementAndDecimalPayout(t *testing.T) {
	db, e := sqlite.Open(filepath.Join(t.TempDir(), "player.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s, e := New(db, RuntimeConfig{})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.EnablePlayerOnly(); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Query("SELECT * FROM accounts WHERE id IN ('house','fees','external')"); e != nil {
		t.Fatal(e)
	}
	if e = db.Transaction(func(tx *sqlite.Tx) error {
		_, e := tx.One("SELECT id FROM accounts WHERE id='house'")
		if e != nil {
			return e
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if e = db.Transaction(func(tx *sqlite.Tx) error { _, e := s.SetPlayer(tx, 123, "测试玩家", true); return e }); e != nil {
		t.Fatal(e)
	}
	if e = db.Transaction(func(tx *sqlite.Tx) error {
		_, e := s.Adjust(tx, "tg:123", Points(1000), "测试上分", newID())
		return e
	}); e != nil {
		t.Fatal(e)
	}
	rules := DefaultRules()
	rules.Confirmed = true
	rules.Payout[9] = 1.2
	if e = db.Transaction(func(tx *sqlite.Tx) error { _, e := s.SaveRules(tx, 1, rules); return e }); e != nil {
		t.Fatal(e)
	}
	r0 := ConfigureRound{Number: "2026-09-20-9001", Banker: 1, Heroes: heroes()}
	var r Round
	if e = db.Transaction(func(tx *sqlite.Tx) error { v, e := s.CreateDraft(tx, r0); r = v.(Round); return e }); e != nil {
		t.Fatal(e)
	}
	if e = db.Transaction(func(tx *sqlite.Tx) error { _, e := s.OpenRound(tx, r.ID); return e }); e != nil {
		t.Fatal(e)
	}
	if e = db.Transaction(func(tx *sqlite.Tx) error { _, e := s.PlaceBet(tx, BetInput{"tg:123", r.ID, 2, Points(33)}); return e }); e != nil {
		t.Fatal(e)
	}
	if e = db.Transaction(func(tx *sqlite.Tx) error { _, e := s.CloseRound(tx, r.ID); return e }); e != nil {
		t.Fatal(e)
	}
	p, e := s.Preview(r.ID, SettleInput{DurationSeconds: 1200, Damages: []string{"12710", "12745", "99999", "12737", "12345"}})
	if e != nil {
		t.Fatal(e)
	}
	p2, e := func() (Preview, error) {
		var v Preview
		err := db.Transaction(func(tx *sqlite.Tx) error {
			x, e := s.Settle(tx, r.ID, SettleInput{DurationSeconds: 1200, Damages: p.Damages, PreviewToken: p.Token})
			if x != nil {
				v = x.(Preview)
			}
			return e
		})
		return v, err
	}()
	if e != nil {
		t.Fatal(e)
	}
	if p2.Lines[0].GameDelta != Money(39600) {
		t.Fatalf("expected rounded 33*1.2=39.600, got %d", p2.Lines[0].GameDelta)
	}
	a := acc(t, s, "tg:123")
	if a.Balance != Money(1039600) || a.Locked != 0 {
		t.Fatalf("unexpected player %+v", a)
	}
	if summary, e := s.PlayerSummary(50, 0); e != nil || summary == nil {
		t.Fatalf("player summary: %v", e)
	}
}
