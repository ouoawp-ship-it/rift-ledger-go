package app

import (
	"fmt"
	"regexp"
	"riftledger/internal/sqlite"
	"strconv"
	"strings"
)

var balancePattern = regexp.MustCompile(`^(上分|下分|上|下|回)\s*\+?([0-9]{1,13})$`)

func ParseBalanceRequest(text string) (string, int64, bool) {
	m := balancePattern.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return "", 0, false
	}
	amount, e := strconv.ParseInt(m[2], 10, 64)
	if e != nil || amount <= 0 || amount > MoneyLimit {
		return "", 0, false
	}
	kind := "DEBIT"
	if strings.HasPrefix(m[1], "上") {
		kind = "CREDIT"
	}
	return kind, amount, true
}

func (s *Service) requestBalance(tx *sqlite.Tx, u TGUpdate, a Account, kind string, amount int64) (string, error) {
	if !a.Enabled {
		return "您还没有开通积分账户，请先联系管理员。", nil
	}
	duplicate, e := tx.One("SELECT id FROM balance_requests WHERE account_id=? AND kind=? AND amount=? AND state='PENDING'", a.ID, kind, amount)
	if e != nil {
		return "", e
	}
	if duplicate != nil {
		return "相同金额的申请仍在等待管理员处理，请勿重复提交。", nil
	}
	if kind == "DEBIT" && a.Available < amount {
		return "下分申请未受理：可用积分不足。", nil
	}
	if _, e = tx.Exec("INSERT INTO balance_requests(update_id,account_id,kind,amount,balance_at_request,created_at) VALUES(?,?,?,?,?,?)", u.ID, a.ID, kind, amount, a.Balance, now()); e != nil {
		return "", e
	}
	label := "下分"
	if kind == "CREDIT" {
		label = "上分"
	}
	username := "未设置"
	if u.Message != nil && u.Message.From.Username != "" {
		username = "@" + u.Message.From.Username
	}
	content := fmt.Sprintf("%s申请\n玩家：%s\nTelegram：%s\nTG ID：%d\n申请金额：%d\n当前余额：%d\n申请时间：%s", label, a.Name, username, a.TelegramID, amount, a.Balance, timeText())
	if e = s.queue(tx, fmt.Sprintf("balance-admin:%d", u.ID), s.Config.NotifyAdminID, "", "🔔 新的"+content+"\n请进入后台处理。", false); e != nil {
		return "", e
	}
	return "📥 " + content + "\n申请状态：等待管理员处理\n请勿重复提交相同申请。", nil
}

func (s *Service) BalanceRequests(limit, offset int) (any, error) {
	var result any
	e := s.DB.Read(func(tx *sqlite.Tx) error {
		rows, e := tx.Query(`SELECT b.*,a.telegram_id,a.name,a.balance AS current_balance,COALESCE(u.username,'') AS username,COALESCE(u.first_name,'') AS first_name,COALESCE(u.last_name,'') AS last_name FROM balance_requests b JOIN accounts a ON a.id=b.account_id LEFT JOIN telegram_users u ON u.telegram_user_id=a.telegram_id ORDER BY b.id DESC LIMIT ? OFFSET ?`, limit, offset)
		if e != nil {
			return e
		}
		counts, e := tx.One(`SELECT COALESCE(SUM(kind='CREDIT'),0) AS credit,COALESCE(SUM(kind='DEBIT'),0) AS debit,COALESCE(MAX(id),0) AS latest FROM balance_requests WHERE state='PENDING'`)
		result = map[string]any{"rows": rows, "counts": counts}
		return e
	})
	return result, e
}

func (s *Service) ResolveBalanceRequest(tx *sqlite.Tx, id int64, action, note string) (any, error) {
	if action != "APPROVED" && action != "REJECTED" {
		return nil, bad("请选择批准或拒绝")
	}
	if len([]rune(note)) > 180 {
		return nil, bad("备注不能超过180字")
	}
	r, e := tx.One("SELECT * FROM balance_requests WHERE id=?", id)
	if e != nil {
		return nil, e
	}
	if r == nil {
		return nil, notfound("申请不存在")
	}
	if r["state"] != "PENDING" {
		return nil, conflict("申请已处理，不能重复审批")
	}
	if action == "APPROVED" {
		delta := r.Int("amount")
		if r["kind"] == "DEBIT" {
			delta = -delta
		}
		if _, e = s.Adjust(tx, r["account_id"], delta, "申请审批："+note, fmt.Sprintf("balance-request:%d", id)); e != nil {
			return nil, e
		}
	}
	if _, e = tx.Exec("UPDATE balance_requests SET state=?,processed_at=?,note=? WHERE id=? AND state='PENDING'", action, now(), note, id); e != nil {
		return nil, e
	}
	a, e := getAccount(tx, r["account_id"])
	if e != nil {
		return nil, e
	}
	label := "已批准"
	if action == "REJECTED" {
		label = "已拒绝"
	}
	if e = s.queue(tx, fmt.Sprintf("balance-result:%d", id), a.TelegramID, "", fmt.Sprintf("积分申请 #%d %s\n金额：%d\n当前余额：%d\n备注：%s", id, label, r.Int("amount"), a.Balance, note), false); e != nil {
		return nil, e
	}
	return map[string]any{"id": id, "state": action}, nil
}
