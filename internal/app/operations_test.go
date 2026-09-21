package app

import (
	"errors"
	"riftledger/internal/sqlite"
	"testing"
	"time"
)

func TestOperationalHealthSeparatesReceiverBusinessAndSender(t *testing.T) {
	s := reliabilityService(t)
	if e := s.BotStatus("接收正常"); e != nil {
		t.Fatal(e)
	}
	s.recordUpdate(99, time.Now(), errors.New("storage failure"))
	st, e := s.BotConnection()
	if e != nil || st.State != "degraded" || st.ReceiverState != "online" || st.Business.State != "error" || st.Business.FailedUpdate != 99 {
		t.Fatal(st, e)
	}
	s.recordUpdate(99, time.Now(), nil)
	if _, e = s.DB.Exec(`INSERT INTO outbox(key,chat_id,payload,created_at) VALUES('old',123,'{}',?)`, now()-80); e != nil {
		t.Fatal(e)
	}
	st, e = s.BotConnection()
	if e != nil || st.Sender.State != "delayed" || st.Sender.OldestAge < 80 || st.State != "degraded" || st.Business.State != "healthy" {
		t.Fatal(st, e)
	}
	if _, e = s.DB.Exec("UPDATE outbox SET state='INFLIGHT'"); e != nil {
		t.Fatal(e)
	}
	st, e = s.BotConnection()
	if e != nil || st.Sender.Inflight != 1 || st.Sender.State != "delayed" {
		t.Fatal(st, e)
	}
	if _, e = s.DB.Exec("UPDATE outbox SET state='UNKNOWN'"); e != nil {
		t.Fatal(e)
	}
	st, e = s.BotConnection()
	if e != nil || st.NeedsReview != 1 || st.Sender.State != "blocked" {
		t.Fatal(st, e)
	}
	if _, e = s.Operations(); e != nil {
		t.Fatal(e)
	}
}
func TestPlayerSummaryLatestRoundIsNotAllRoundsInSameSecond(t *testing.T) {
	s := reliabilityService(t)
	for _, q := range []string{
		`INSERT INTO accounts(id,name,role,telegram_id,enabled,balance,created_at) VALUES('p','p','player',123,1,1000000,1)`,
		`INSERT INTO rounds(id,number,state,banker,heroes,rules,rules_version,created_at,settled_at) VALUES('old','old','SETTLED',1,'[]','{}',1,1,99),('new','new','SETTLED',1,'[]','{}',1,2,99)`,
		`INSERT INTO bets(id,round_id,account_id,position,stake,fee,state,game_delta,net_delta,created_at,settled_at) VALUES('b1','old','p',2,100000,0,'WIN',98000,98000,1,99),('b2','new','p',2,100000,0,'LOSS',-100000,-100000,2,99),('b3','new','p',3,20000,0,'WIN',19600,19600,2,99)`,
	} {
		if _, e := s.DB.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	v, e := s.PlayerSummary(50, 0)
	if e != nil {
		t.Fatal(e)
	}
	rows := v.(map[string]any)["rows"].([]sqlite.Row)
	if len(rows) != 1 || rows[0]["last_profit"] != "-80.400" {
		t.Fatal(rows)
	}
}
func TestCommittedCommandWakesSender(t *testing.T) {
	s := reliabilityService(t)
	_, e := s.Command("wake-command", "queue", nil, func(tx *sqlite.Tx) (any, error) { return true, s.queue(tx, "wake", 123, "", "hello", false) })
	if e != nil {
		t.Fatal(e)
	}
	select {
	case <-s.OutboxWake():
	default:
		t.Fatal("sender not notified")
	}
	rows, e := s.DB.Query("SELECT state FROM outbox WHERE key='wake'")
	if e != nil || len(rows) != 1 {
		t.Fatal("wake before durable record", rows, e)
	}
}
