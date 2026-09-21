package app

import (
	"fmt"
	"strings"
	"testing"

	"riftledger/internal/sqlite"
)

func update(id, uid, date int64, text string) TGUpdate {
	return TGUpdate{ID: id, Message: &TGMessage{ID: id, Date: date, From: &TGUser{ID: uid, FirstName: "测试"}, Chat: TGChat{ID: uid, Type: "private"}, Text: text}}
}
func TestShortcutParser(t *testing.T) {
	for _, x := range []string{"1.100", "1 . 100U", "  5.20u  "} {
		p, a, ok := ParseBet(x)
		if !ok || p < 1 || a < Points(20) {
			t.Fatal(x)
		}
	}
	for _, x := range []string{"1.100.1234", "0.100", "6.100", "1.-1", "1.2e3", "1.100000000", "1.20hello"} {
		if _, _, ok := ParseBet(x); ok {
			t.Fatal(x)
		}
	}
}
func TestTelegramDuplicateUpdates(t *testing.T) {
	s, r := fixture(t, "acceptance", "refund", "fees")
	u := update(100, 111, r.OpenedAt+1, "2.100")
	if e := s.HandleUpdate(u); e != nil {
		t.Fatal(e)
	}
	if e := s.HandleUpdate(u); e != nil {
		t.Fatal(e)
	}
	if acc(t, s, "tg:111").Balance != Points(999) || acc(t, s, "tg:111").Locked != Points(100) {
		t.Fatal("duplicate debit")
	}
	rows, _ := s.DB.Query("SELECT * FROM bets")
	if len(rows) != 1 {
		t.Fatal("duplicate bet")
	}
	offset, e := s.Offset()
	if e != nil || offset != 101 {
		t.Fatalf("offset %d %v", offset, e)
	}
	reply, _ := s.DB.Query("SELECT * FROM outbox WHERE key='reply:100'")
	if len(reply) != 1 {
		t.Fatal("duplicate reply")
	}
	balanced(t, s)
}
func TestRejectedTelegramUpdateNeverBecomesNewBet(t *testing.T) {
	s, r := fixture(t, "acceptance", "refund", "fees")
	exec(t, s, func(tx *sqlite.Tx) (any, error) {
		return s.Adjust(tx, "house", Points(-100000), "暂停承付", newID())
	})
	u := update(101, 111, r.OpenedAt+1, "2.100")
	if e := s.HandleUpdate(u); e != nil {
		t.Fatal(e)
	}
	exec(t, s, func(tx *sqlite.Tx) (any, error) {
		return s.Adjust(tx, "house", Points(100000), "恢复承付", newID())
	})
	if e := s.HandleUpdate(u); e != nil {
		t.Fatal(e)
	}
	rows, _ := s.DB.Query("SELECT * FROM bets")
	if len(rows) != 0 {
		t.Fatal("previously rejected update accepted after replay")
	}
	if acc(t, s, "tg:111").Balance != Points(1000) {
		t.Fatal("fee charged")
	}
	balanced(t, s)
}
func TestUnknownPlayerWhitelistAndAdminNotice(t *testing.T) {
	s, r := fixture(t, "settlement", "refund", "fees")
	s.Config.NotifyAdminID = 999999
	for i := int64(1); i <= 2; i++ {
		if e := s.HandleUpdate(update(200+i, 333, r.OpenedAt+1, "2.100")); e != nil {
			t.Fatal(e)
		}
	}
	a := acc(t, s, "tg:333")
	if a.Enabled || a.Balance != 0 {
		t.Fatal("auto approved or gifted")
	}
	rows, _ := s.DB.Query("SELECT * FROM outbox WHERE key='join:tg:333'")
	if len(rows) != 1 {
		t.Fatal("duplicate admin notice")
	}
	balanced(t, s)
}
func TestPrivateOnlyAndOldMessageGuard(t *testing.T) {
	s, r := fixture(t, "settlement", "refund", "fees")
	u := update(300, 111, r.OpenedAt+1, "2.100")
	u.Message.Chat = TGChat{ID: -100123, Type: "supergroup"}
	if e := s.HandleUpdate(u); e != nil {
		t.Fatal(e)
	}
	u = update(301, 111, r.OpenedAt+1, "2.100")
	u.Message.Chat.ID = 222
	if e := s.HandleUpdate(u); e != nil {
		t.Fatal(e)
	}
	if e := s.HandleUpdate(update(302, 111, r.OpenedAt, "2.100")); e != nil {
		t.Fatal(e)
	}
	rows, _ := s.DB.Query("SELECT * FROM bets")
	if len(rows) != 0 {
		t.Fatal("group/spoof/old message accepted")
	}
	balanced(t, s)
}
func TestQueriesWorkAfterClosingAndOnlyShowOwnBets(t *testing.T) {
	s, r := fixture(t, "settlement", "refund", "fees")
	b := place(t, s, r, "tg:222", 3, 100)
	exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.CloseRound(tx, r.ID) })
	for i, text := range []string{"/balance", "/bets", "/history", "/support"} {
		u := update(int64(400+i), 111, r.OpenedAt+1, text)
		if e := s.HandleUpdate(u); e != nil {
			t.Fatal(e)
		}
	}
	rows, _ := s.DB.Query("SELECT payload FROM outbox WHERE key='reply:401'")
	if len(rows) != 1 || strings.Contains(rows[0]["payload"], b.ID) || !strings.Contains(rows[0]["payload"], "暂无更多本人注单") {
		t.Fatalf("privacy leak %+v", rows)
	}
	balanced(t, s)
}
func TestEditedUpdateIgnoredAndCallbacksPrivate(t *testing.T) {
	s, r := fixture(t, "settlement", "refund", "fees")
	if e := s.HandleUpdate(TGUpdate{ID: 500}); e != nil {
		t.Fatal(e)
	}
	u := TGUpdate{ID: 501, Callback: &TGCallback{ID: "cb1", From: TGUser{ID: 111}, Message: &TGMessage{Chat: TGChat{ID: 111, Type: "private"}}, Data: "home"}}
	if e := s.HandleUpdate(u); e != nil {
		t.Fatal(e)
	}
	rows, _ := s.DB.Query("SELECT payload FROM outbox WHERE key='reply:501'")
	if len(rows) != 1 || !strings.Contains(rows[0]["payload"], "个人中心") {
		t.Fatal("private callback")
	}
	all, _ := s.DB.Query("SELECT * FROM bets WHERE round_id=?", r.ID)
	if len(all) != 0 {
		t.Fatal("edited update changed bets")
	}
}
func TestOutboxOrderingCardReuseAndUnknownRecovery(t *testing.T) {
	s, _ := fixture(t, "settlement", "refund", "fees")
	e := s.DB.Transaction(func(tx *sqlite.Tx) error {
		for _, v := range []struct {
			key  string
			chat int64
			card string
		}{{"a", 10, "card1"}, {"b", 10, "card1"}, {"c", 20, ""}} {
			if e := s.queue(tx, v.key, v.chat, v.card, v.key, false); e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	item, e := s.ClaimOutbox()
	if e != nil || item == nil || item.Payload.Text != "a" {
		t.Fatalf("%+v %v", item, e)
	}
	if e = s.CompleteOutbox(*item, "SENT", 42, "", 0); e != nil {
		t.Fatal(e)
	}
	item, e = s.ClaimOutbox()
	if e != nil || item.CardMessageID != 42 || item.Payload.Text != "b" {
		t.Fatalf("%+v %v", item, e)
	}
	if _, e = New(s.DB, s.Config); e != nil {
		t.Fatal(e)
	} // simulate interrupted send after claiming b
	rows, _ := s.DB.Query("SELECT state FROM outbox WHERE key='b'")
	if rows[0]["state"] != "UNKNOWN" {
		t.Fatal("interrupted send retried automatically")
	}
	next, e := s.ClaimOutbox()
	if e != nil || next == nil || next.Payload.Text != "c" {
		t.Fatal("unrelated chat blocked")
	}
	expectFailure(t, s, func(tx *sqlite.Tx) (any, error) { return s.ResolveOutbox(tx, item.ID, "ack", 0) })
	exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.ResolveOutbox(tx, item.ID, "ack", 43) })
}
func TestBotIdentityCannotBeSilentlyChanged(t *testing.T) {
	s, _ := fixture(t, "settlement", "refund", "fees")
	if e := s.BindBot(123); e != nil {
		t.Fatal(e)
	}
	if e := s.BindBot(123); e != nil {
		t.Fatal(e)
	}
	if e := s.BindBot(456); e == nil {
		t.Fatal("changed bot accepted")
	}
}
func TestDisabledAccountStillSettles(t *testing.T) {
	s, r := fixture(t, "settlement", "refund", "fees")
	place(t, s, r, "tg:111", 2, 100)
	exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.SetPlayer(tx, 111, "停用玩家", false) })
	finish(t, s, r, 301, damages("12710", "12745"))
	if acc(t, s, "tg:111").Balance != Points(1299) {
		t.Fatal("disabled account lost accepted payout")
	}
	balanced(t, s)
}
func TestRowCountsDoNotChangeOnRepeatedSettlementCommand(t *testing.T) {
	s, r := fixture(t, "settlement", "refund", "fees")
	place(t, s, r, "tg:111", 2, 100)
	exec(t, s, func(tx *sqlite.Tx) (any, error) { return s.CloseRound(tx, r.ID) })
	in := SettleInput{DurationSeconds: 301, Damages: damages("12710", "12745")}
	p, e := s.Preview(r.ID, in)
	if e != nil {
		t.Fatal(e)
	}
	in.PreviewToken = p.Token
	for i := 0; i < 3; i++ {
		if _, e = s.Command("settlement-replay-0001", "settle "+r.ID, in, func(tx *sqlite.Tx) (any, error) { return s.Settle(tx, r.ID, in) }); e != nil {
			t.Fatal(e)
		}
	}
	for table, want := range map[string]int{"bets": 1, "rounds": 2, "idempotency": 1} {
		rows, e := s.DB.Query(fmt.Sprintf("SELECT * FROM %s", table))
		if e != nil || len(rows) != want {
			t.Fatalf("%s %d %v", table, len(rows), e)
		}
	}
	balanced(t, s)
}
