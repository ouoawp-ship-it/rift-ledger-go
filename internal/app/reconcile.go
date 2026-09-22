package app

import (
	"fmt"
	"riftledger/internal/sqlite"
)

func (s *Service) Reconcile() (any, error) {
	var result any
	read := s.DB.Read
	if s.AuditDB != nil {
		read = s.AuditDB.Snapshot
	}
	e := read(func(tx *sqlite.Tx) error {
		var err error
		result, err = reconcileTx(tx, s.PlayerOnly)
		return err
	})
	return result, e
}

func reconcileTx(tx *sqlite.Tx, playerOnly bool) (any, error) {
	issues := []string{}
	expected := map[string]Money{}
	pendingBets := int64(0)
	query := "SELECT b.*,r.rules FROM bets b JOIN rounds r ON r.id=b.round_id WHERE b.state='RESERVED'"
	if playerOnly {
		query = "SELECT account_id,SUM(stake) AS stake,COUNT(*) AS n FROM bets WHERE state='RESERVED' GROUP BY account_id"
	}
	bets, e := tx.Query(query)
	if e != nil {
		return nil, e
	}
	for _, b := range bets {
		if playerOnly {
			expected[b["account_id"]] += moneyRow(b, "stake")
			pendingBets += b.Int("n")
			continue
		}
		pendingBets++
		rules, e := decodeRules(b["rules"])
		if e != nil {
			return nil, e
		}
		reserve := riskFor(moneyRow(b, "stake"), rules.MaxLossMultiplier())
		if !playerOnly && rules.FeeTiming == "settlement" {
			reserve += moneyRow(b, "fee")
		}
		expected[b["account_id"]] += reserve
		if !playerOnly {
			expected["house"] += exposureFor(moneyRow(b, "stake"), rules.MaxPayout())
		}
		if rules.FeeTiming == "acceptance" && rules.VoidFee == "refund" {
			expected[rules.FeeRecipient] += moneyRow(b, "fee")
		}
	}
	rows, e := tx.Query("SELECT a.*,COALESCE((SELECT SUM(delta) FROM entries e WHERE e.account_id=a.id),0) AS ledger_total FROM accounts a")
	if e != nil {
		return nil, e
	}
	sum := Money(0)
	for _, r := range rows {
		sum += moneyRow(r, "balance")
		if moneyRow(r, "balance") != moneyRow(r, "ledger_total") {
			issues = append(issues, "余额与流水不符："+r["id"])
		}
		if moneyRow(r, "locked") != expected[r["id"]] {
			issues = append(issues, "冻结与待结算注单不符："+r["id"])
		}
	}
	if !playerOnly && sum != 0 {
		issues = append(issues, fmt.Sprintf("所有账户合计不为零：%d", sum))
	}
	if !playerOnly {
		batches, e := tx.Query("SELECT batch,SUM(delta) AS net,COUNT(*) AS n FROM entries GROUP BY batch HAVING SUM(delta)!=0 OR COUNT(*)!=2")
		if e != nil {
			return nil, e
		}
		for _, b := range batches {
			issues = append(issues, "不平衡流水批次："+b["batch"])
		}
	}
	count, e := tx.One("SELECT COUNT(*) AS n FROM entries")
	if e != nil {
		return nil, e
	}
	return map[string]any{"balanced": len(issues) == 0, "account_count": len(rows), "entry_count": count.Int("n"), "pending_bets": pendingBets, "balance_sum": sum, "issues": issues}, nil
}
func (s *Service) CurrentRules() (Rules, error) {
	var r Rules
	e := s.DB.Read(func(tx *sqlite.Tx) error { var e error; r, _, e = getRules(tx); return e })
	return r, e
}
