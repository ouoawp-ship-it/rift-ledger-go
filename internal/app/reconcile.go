package app

import (
	"encoding/json"
	"fmt"
	"riftledger/internal/sqlite"
)

func (s *Service) Reconcile() (any, error) {
	var result any
	e := s.DB.Read(func(tx *sqlite.Tx) error {
		issues := []string{}
		expected := map[string]int64{}
		bets, e := tx.Query("SELECT b.*,r.rules FROM bets b JOIN rounds r ON r.id=b.round_id WHERE b.state='RESERVED'")
		if e != nil {
			return e
		}
		for _, b := range bets {
			var rules Rules
			if e = json.Unmarshal([]byte(b["rules"]), &rules); e != nil {
				return e
			}
			reserve := b.Int("stake")
			if rules.FeeTiming == "settlement" {
				reserve += b.Int("fee")
			}
			expected[b["account_id"]] += reserve
			expected["house"] += b.Int("stake") * rules.MaxPayout()
			if rules.FeeTiming == "acceptance" && rules.VoidFee == "refund" {
				expected[rules.FeeRecipient] += b.Int("fee")
			}
		}
		rows, e := tx.Query("SELECT a.*,COALESCE((SELECT SUM(delta) FROM entries e WHERE e.account_id=a.id),0) AS ledger_total FROM accounts a")
		if e != nil {
			return e
		}
		sum := int64(0)
		for _, r := range rows {
			sum += r.Int("balance")
			if r.Int("balance") != r.Int("ledger_total") {
				issues = append(issues, "余额与流水不符："+r["id"])
			}
			if r.Int("locked") != expected[r["id"]] {
				issues = append(issues, "冻结与待结算注单不符："+r["id"])
			}
		}
		if sum != 0 {
			issues = append(issues, fmt.Sprintf("所有账户合计不为零：%d", sum))
		}
		batches, e := tx.Query("SELECT batch,SUM(delta) AS net,COUNT(*) AS n FROM entries GROUP BY batch HAVING SUM(delta)!=0 OR COUNT(*)!=2")
		if e != nil {
			return e
		}
		for _, b := range batches {
			issues = append(issues, "不平衡流水批次："+b["batch"])
		}
		count, e := tx.One("SELECT COUNT(*) AS n FROM entries")
		if e != nil {
			return e
		}
		result = map[string]any{"balanced": len(issues) == 0, "account_count": len(rows), "entry_count": count.Int("n"), "pending_bets": len(bets), "balance_sum": sum, "issues": issues}
		return nil
	})
	return result, e
}
func (s *Service) CurrentRules() (Rules, error) {
	var r Rules
	e := s.DB.Read(func(tx *sqlite.Tx) error { var e error; r, _, e = getRules(tx); return e })
	return r, e
}
