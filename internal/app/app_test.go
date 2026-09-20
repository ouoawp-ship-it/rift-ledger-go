package app

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"riftledger/internal/sqlite"
)

func fixture(t *testing.T, timing, voidFee, recipient string) (*Service, Round) {
	t.Helper()
	db, e := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	s, e := New(db, RuntimeConfig{})
	if e != nil {
		t.Fatal(e)
	}
	rules := DefaultRules()
	rules.Confirmed = true
	rules.FeeTiming = timing
	rules.VoidFee = voidFee
	rules.FeeRecipient = recipient
	exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.SaveRules(tx, 1, rules) })
	for _, id := range []int64{111, 222} {
		exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.SetPlayer(tx, id, fmt.Sprintf("玩家%d", id), true) })
		exec(t, s, func(tx *sqlite.Tx) (any, error) {
			return s.Adjust(tx, fmt.Sprintf("tg:%d", id), 1000, "测试初始化", newID())
		})
	}
	exec(t, s, func(tx *sqlite.Tx) (any, error) {
		return s.Adjust(tx, "house", 100000, "测试运营方积分", newID())
	})
	in := ConfigureRound{Number: "2026-09-20-0001", Banker: 1, Heroes: heroes()}
	obj := exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.CreateDraft(tx, in) })
	r := obj.(Round)
	exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.OpenRound(tx, r.ID) })
	r, _ = s.ReadRound(r.ID)
	return s, r
}
func heroes() []Hero {
	return []Hero{{"Aatrox", "剑魔"}, {"Ahri", "阿狸"}, {"Akali", "阿卡丽"}, {"Ashe", "艾希"}, {"Braum", "布隆"}}
}
func exec(t *testing.T, s *Service, f func(*sqlite.Tx) (any, error)) any {
	t.Helper()
	var v any
	e := s.DB.Transaction(func(tx *sqlite.Tx) error { var e error; v, e = f(tx); return e })
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func expectFailure(t *testing.T, s *Service, f func(*sqlite.Tx) (any, error)) {
	t.Helper()
	e := s.DB.Transaction(func(tx *sqlite.Tx) error { _, e := f(tx); return e })
	if e == nil {
		t.Fatal("expected rejection")
	}
}
func acc(t *testing.T, s *Service, id string) Account {
	t.Helper()
	var a Account
	e := s.DB.Read(func(tx *sqlite.Tx) error { var e error; a, e = getAccount(tx, id); return e })
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func place(t *testing.T, s *Service, r Round, id string, pos int, stake int64) Bet {
	t.Helper()
	return exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.PlaceBet(tx, BetInput{id, r.ID, pos, stake}) }).(Bet)
}
func finish(t *testing.T, s *Service, r Round, seconds int, damage []string) Preview {
	t.Helper()
	exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.CloseRound(tx, r.ID) })
	in := SettleInput{DurationSeconds: seconds, Damages: damage}
	p, e := s.Preview(r.ID, in)
	if e != nil {
		t.Fatal(e)
	}
	in.PreviewToken = p.Token
	return exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.Settle(tx, r.ID, in) }).(Preview)
}
func damages(banker, player string) []string {
	return []string{banker, player, "12745", "12710", "99999"}
}
func balanced(t *testing.T, s *Service) {
	t.Helper()
	v, e := s.Reconcile()
	if e != nil {
		t.Fatal(e)
	}
	if !v.(map[string]any)["balanced"].(bool) {
		t.Fatalf("reconcile %+v", v)
	}
}
func TestConfirmedLedgerExamples(t *testing.T) {
	cases := []struct {
		name                 string
		stakes               []int64
		banker, player       string
		balance, house, fees int64
	}{{"A_two_50_losses", []int64{50, 50}, "12737", "12745", 898, 100100, 2}, {"B_three_60_losses", []int64{60, 60, 60}, "12737", "12745", 817, 100180, 3}, {"C_100_win_bull9", []int64{100}, "12710", "12745", 1299, 99700, 1}, {"D_loss_is_one_times", []int64{100}, "12737", "12745", 899, 100100, 1}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, r := fixture(t, "settlement", "refund", "fees")
			for _, stake := range c.stakes {
				place(t, s, r, "tg:111", 2, stake)
			}
			balanced(t, s)
			finish(t, s, r, 1200, damages(c.banker, c.player))
			if a := acc(t, s, "tg:111"); a.Balance != c.balance || a.Locked != 0 {
				t.Fatalf("player %+v", a)
			}
			if a := acc(t, s, "house"); a.Balance != c.house || a.Locked != 0 {
				t.Fatalf("house %+v", a)
			}
			if a := acc(t, s, "fees"); a.Balance != c.fees || a.Locked != 0 {
				t.Fatalf("fees %+v", a)
			}
			balanced(t, s)
		})
	}
}
func TestFeePoliciesAndRecipients(t *testing.T) {
	for _, timing := range []string{"acceptance", "settlement"} {
		for _, policy := range []string{"refund", "charge"} {
			for _, recipient := range []string{"fees", "house"} {
				t.Run(timing+"_"+policy+"_"+recipient, func(t *testing.T) {
					s, r := fixture(t, timing, policy, recipient)
					place(t, s, r, "tg:111", 2, 101)
					a := acc(t, s, "tg:111")
					locked := int64(103)
					balance := int64(1000)
					if timing == "acceptance" {
						locked = 101
						balance = 998
					}
					if a.Balance != balance || a.Locked != locked {
						t.Fatalf("before %+v", a)
					}
					balanced(t, s)
					p := finish(t, s, r, 300, []string{"", "", "", "", ""})
					if !p.WholeVoid {
						t.Fatal("duration boundary")
					}
					want := int64(1000)
					if policy == "charge" {
						want = 998
					}
					if a := acc(t, s, "tg:111"); a.Balance != want || a.Locked != 0 {
						t.Fatalf("after %+v", a)
					}
					balanced(t, s)
				})
			}
		}
	}
}
func TestHeroVoidAndBankerVoid(t *testing.T) {
	for _, bankerVoid := range []bool{false, true} {
		t.Run(fmt.Sprint(bankerVoid), func(t *testing.T) {
			s, r := fixture(t, "acceptance", "refund", "fees")
			place(t, s, r, "tg:111", 2, 100)
			place(t, s, r, "tg:222", 3, 100)
			d := damages("12710", "999")
			if bankerVoid {
				d[0] = "999"
			}
			p := finish(t, s, r, 301, d)
			if p.WholeVoid != bankerVoid {
				t.Fatal("void scope")
			}
			if acc(t, s, "tg:111").Balance != 1000 {
				t.Fatal("hero refund")
			}
			want := int64(1299)
			if bankerVoid {
				want = 1000
			}
			if acc(t, s, "tg:222").Balance != want {
				t.Fatal("other hero")
			}
			balanced(t, s)
		})
	}
}
func TestCumulativeQuarterAndSingleLossFreeze(t *testing.T) {
	s, r := fixture(t, "acceptance", "refund", "fees")
	place(t, s, r, "tg:111", 2, 100)
	place(t, s, r, "tg:111", 2, 100)
	place(t, s, r, "tg:111", 2, 50)
	a := acc(t, s, "tg:111")
	if a.Locked != 250 || a.Balance != 997 {
		t.Fatalf("%+v", a)
	}
	expectFailure(t, s, func(tx *sqlite.Tx) (any, error) { return s.PlaceBet(tx, BetInput{"tg:111", r.ID, 2, 20}) })
	expectFailure(t, s, func(tx *sqlite.Tx) (any, error) { return s.Adjust(tx, "tg:111", -1, "违反累计比例", newID()) })
	balanced(t, s)
}
func TestAdmissionRejectionsHaveNoFees(t *testing.T) {
	s, r := fixture(t, "acceptance", "refund", "fees")
	for _, input := range []BetInput{{"tg:111", r.ID, 1, 100}, {"tg:111", r.ID, 2, 19}, {"tg:111", r.ID, 2, 301}, {"house", r.ID, 2, 20}, {"tg:111", r.ID, 6, 20}} {
		expectFailure(t, s, func(tx *sqlite.Tx) (any, error) { return s.PlaceBet(tx, input) })
	}
	if acc(t, s, "tg:111").Balance != 1000 || acc(t, s, "fees").Balance != 0 {
		t.Fatal("charged rejected order")
	}
	exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.Adjust(tx, "house", -100000, "测试余额不足", newID()) })
	expectFailure(t, s, func(tx *sqlite.Tx) (any, error) { return s.PlaceBet(tx, BetInput{"tg:111", r.ID, 2, 100}) })
	balanced(t, s)
}
func TestImmutableRulesSnapshot(t *testing.T) {
	s, r := fixture(t, "settlement", "refund", "fees")
	place(t, s, r, "tg:111", 2, 100)
	rules := DefaultRules()
	rules.Confirmed = true
	rules.Payout[9] = 8
	exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.SaveRules(tx, 2, rules) })
	p := finish(t, s, r, 1200, damages("12710", "12745"))
	if p.PlayerGameDelta != 300 {
		t.Fatal("old snapshot mutated")
	}
	next, e := s.ReadRound(p.NextRoundID)
	if e != nil || next.Rules.Payout[9] != 8 || next.Banker != 0 || len(next.Heroes) != 0 || next.State != "DRAFT" {
		t.Fatalf("next %+v %v", next, e)
	}
	balanced(t, s)
}
func TestIdempotentConcurrentBetAndPayloadConflict(t *testing.T) {
	s, r := fixture(t, "acceptance", "refund", "fees")
	input := BetInput{"tg:111", r.ID, 2, 100}
	key := "same-order-000001"
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := s.Command(key, "bet", input, func(tx *sqlite.Tx) (any, error) { return s.PlaceBet(tx, input) }); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if acc(t, s, "tg:111").Balance != 999 || acc(t, s, "tg:111").Locked != 100 {
		t.Fatal("duplicate fee or reservation")
	}
	rows, _ := s.DB.Query("SELECT * FROM bets")
	if len(rows) != 1 {
		t.Fatal("duplicate bets")
	}
	input.Stake = 50
	if _, e := s.Command(key, "bet", input, func(tx *sqlite.Tx) (any, error) { return s.PlaceBet(tx, input) }); e == nil {
		t.Fatal("reused key accepted different payload")
	}
	balanced(t, s)
}
func TestPreviewInvalidationAndSettlementReplay(t *testing.T) {
	s, r := fixture(t, "settlement", "refund", "fees")
	place(t, s, r, "tg:111", 2, 100)
	exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.CloseRound(tx, r.ID) })
	in := SettleInput{DurationSeconds: 1200, Damages: damages("12710", "12745")}
	p, e := s.Preview(r.ID, in)
	if e != nil {
		t.Fatal(e)
	}
	in.PreviewToken = p.Token
	changed := in
	changed.DurationSeconds = 1201
	expectFailure(t, s, func(tx *sqlite.Tx) (any, error) { return s.Settle(tx, r.ID, changed) })
	if acc(t, s, "tg:111").Balance != 1000 {
		t.Fatal("preview changed balance")
	}
	expectFailure(t, s, func(tx *sqlite.Tx) (any, error) { return s.PlaceBet(tx, BetInput{"tg:111", r.ID, 2, 20}) })
	exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.Settle(tx, r.ID, in) })
	before, _ := s.DB.Query("SELECT * FROM entries")
	exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.Settle(tx, r.ID, in) })
	after, _ := s.DB.Query("SELECT * FROM entries")
	if len(before) != len(after) {
		t.Fatal("settlement replay wrote entries")
	}
	expectFailure(t, s, func(tx *sqlite.Tx) (any, error) { return s.Settle(tx, r.ID, changed) })
	balanced(t, s)
}
func TestNoDefaultBankerAndUniqueHeroes(t *testing.T) {
	in := ConfigureRound{Number: "2026-09-20-0001", Heroes: heroes()}
	if validateConfiguration(in, true) == nil {
		t.Fatal("default banker")
	}
	in.Banker = 3
	in.Heroes[1] = in.Heroes[0]
	if validateConfiguration(in, true) == nil {
		t.Fatal("duplicate hero")
	}
	for banker := 1; banker <= 5; banker++ {
		in.Heroes = heroes()
		in.Banker = banker
		if e := validateConfiguration(in, true); e != nil {
			t.Fatal(e)
		}
	}
	s, r := fixture(t, "settlement", "refund", "fees")
	expectFailure(t, s, func(tx *sqlite.Tx) (any, error) { return s.Configure(tx, r.ID, ConfigureRound{r.Number, 2, heroes()}) })
	expectFailure(t, s, func(tx *sqlite.Tx) (any, error) { return s.CreateDraft(tx, ConfigureRound{Number: "2026-09-20-0002"}) })
}
func TestUnconfirmedRulesBlockOpening(t *testing.T) {
	s, r := fixture(t, "settlement", "refund", "fees")
	p := finish(t, s, r, 301, damages("12710", "12745"))
	rules := DefaultRules()
	exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.SaveRules(tx, 2, rules) })
	exec(t, s, func(tx *sqlite.Tx) (any, error) {
		return s.Configure(tx, p.NextRoundID, ConfigureRound{Number: "2026-09-20-0002", Banker: 3, Heroes: heroes()})
	})
	expectFailure(t, s, func(tx *sqlite.Tx) (any, error) { return s.OpenRound(tx, p.NextRoundID) })
}
func TestLedgerImmutableAndRollback(t *testing.T) {
	s, r := fixture(t, "settlement", "refund", "fees")
	before := acc(t, s, "tg:111")
	if _, e := s.DB.Exec("UPDATE entries SET delta=0"); e == nil {
		t.Fatal("ledger mutable")
	}
	if _, e := s.DB.Exec("DELETE FROM entries"); e == nil {
		t.Fatal("ledger deletable")
	}
	e := s.DB.Transaction(func(tx *sqlite.Tx) error {
		if _, e := s.PlaceBet(tx, BetInput{"tg:111", r.ID, 2, 100}); e != nil {
			return e
		}
		return fmt.Errorf("simulated downstream failure")
	})
	if e == nil {
		t.Fatal("no error")
	}
	after := acc(t, s, "tg:111")
	if before != after {
		t.Fatal("partial reservation escaped rollback")
	}
	balanced(t, s)
}
func TestSettingsValidationAndOptimisticVersion(t *testing.T) {
	s, _ := fixture(t, "settlement", "refund", "fees")
	r := DefaultRules()
	r.Payout = []int64{1}
	expectFailure(t, s, func(tx *sqlite.Tx) (any, error) { return s.SaveRules(tx, 2, r) })
	r = DefaultRules()
	expectFailure(t, s, func(tx *sqlite.Tx) (any, error) { return s.SaveRules(tx, 1, r) })
	r.MinStake = 10
	r.MaxStake = 9
	if r.Validate() == nil {
		t.Fatal("invalid limits")
	}
}
func TestDatabaseRestartPreservesSettings(t *testing.T) {
	s, r := fixture(t, "acceptance", "refund", "fees")
	place(t, s, r, "tg:111", 2, 100)
	if _, e := New(s.DB, RuntimeConfig{}); e != nil {
		t.Fatal(e)
	}
	a := acc(t, s, "tg:111")
	if a.Balance != 999 || a.Locked != 100 {
		t.Fatal("restart reset data")
	}
	rules, e := s.CurrentRules()
	if e != nil || !rules.Confirmed || rules.FeeTiming != "acceptance" {
		t.Fatal("restart reset settings")
	}
	balanced(t, s)
}
func TestThreeDigitIsNotNoBull(t *testing.T) {
	s, r := fixture(t, "settlement", "refund", "fees")
	place(t, s, r, "tg:111", 2, 100)
	p := finish(t, s, r, 301, damages("12710", "99999"))
	if p.Lines[0].Outcome != "LOSS" {
		t.Fatal("no bull treated as void")
	}
	balanced(t, s)
}
func TestLowAndHighInvalidDamagePreventPartialSettlement(t *testing.T) {
	s, r := fixture(t, "settlement", "refund", "fees")
	place(t, s, r, "tg:111", 2, 100)
	exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.CloseRound(tx, r.ID) })
	for _, raw := range []string{"99", "1000000", "00123"} {
		if _, e := s.Preview(r.ID, SettleInput{DurationSeconds: 301, Damages: damages("12710", raw)}); e == nil {
			t.Fatal("invalid damage accepted")
		}
	}
	if acc(t, s, "tg:111").Balance != 1000 {
		t.Fatal("invalid changed money")
	}
	balanced(t, s)
}
func TestAllValidBankerPositions(t *testing.T) {
	for n := 1; n <= 5; n++ {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			s, r := fixture(t, "settlement", "refund", "fees")
			p := finish(t, s, r, 301, damages("12710", "12745"))
			exec(t, s, func(tx *sqlite.Tx) (any, error) {
				return s.Configure(tx, p.NextRoundID, ConfigureRound{Number: "2026-09-20-0002", Banker: n, Heroes: heroes()})
			})
			obj := exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.OpenRound(tx, p.NextRoundID) })
			r = obj.(Round)
			for pos := 1; pos <= 5; pos++ {
				if pos == n {
					expectFailure(t, s, func(tx *sqlite.Tx) (any, error) { return s.PlaceBet(tx, BetInput{"tg:111", r.ID, pos, 20}) })
				} else {
					place(t, s, r, "tg:111", pos, 20)
				}
			}
			balanced(t, s)
		})
	}
}
func TestJSONRoundTripOfMoney(t *testing.T) {
	a := Account{Balance: MoneyLimit}
	raw := asJSON(a)
	var b Account
	if e := json.Unmarshal([]byte(raw), &b); e != nil || a.Balance != b.Balance {
		t.Fatal("precision")
	}
}
