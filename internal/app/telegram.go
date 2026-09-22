package app

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"riftledger/internal/sqlite"
)

type TGUser struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}
type TGChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}
type TGMessage struct {
	ID   int64   `json:"message_id"`
	Date int64   `json:"date"`
	From *TGUser `json:"from"`
	Chat TGChat  `json:"chat"`
	Text string  `json:"text"`
}
type TGCallback struct {
	ID      string     `json:"id"`
	From    TGUser     `json:"from"`
	Message *TGMessage `json:"message"`
	Data    string     `json:"data"`
}
type TGUpdate struct {
	ID       int64       `json:"update_id"`
	Message  *TGMessage  `json:"message"`
	Callback *TGCallback `json:"callback_query"`
}

var betPattern = regexp.MustCompile(`^([1-5])\s*\.\s*([0-9]{1,7}(?:\.[0-9]{1,3})?)\s*[uU]?$`)

func ParseBet(text string) (int, Money, bool) {
	m := betPattern.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return 0, 0, false
	}
	p, _ := strconv.Atoi(m[1])
	a, e := ParseMoney(m[2])
	return p, a, e == nil
}
func (s *Service) Offset() (int64, error) {
	rows, e := s.DB.Query("SELECT COALESCE((SELECT offset FROM bot_sessions WHERE bot_id=(SELECT CAST(value AS INTEGER) FROM meta WHERE key='bot_id')),(SELECT value FROM meta WHERE key='tg_offset'),'0') AS value")
	if e != nil || len(rows) == 0 {
		return 0, e
	}
	n, e := strconv.ParseInt(rows[0]["value"], 10, 64)
	return n, e
}
func (s *Service) BotStatus(text string) error {
	return s.DB.Transaction(func(tx *sqlite.Tx) error {
		_, e := tx.Exec("INSERT INTO meta(key,value) VALUES('bot_status',?),('bot_status_at',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", text, strconv.FormatInt(now(), 10))
		return e
	})
}

// HandleUpdate commits the update ID, any accepted bet, its ledger effects and
// replies together. Rejected bets are also consumed, so replay cannot make a
// previously rejected request succeed later. Only genuine private messages and
// private callbacks are routed; edited messages never modify accepted orders.
func (s *Service) HandleUpdate(u TGUpdate) (err error) {
	started := time.Now()
	rawID := u.ID
	defer func() { s.recordUpdate(rawID, started, err); s.NotifyOutbox() }()
	if u.ID < 0 {
		return bad("无效更新ID")
	}
	return s.DB.Transaction(func(tx *sqlite.Tx) error {
		// Telegram update IDs are unique only within one bot. The internal ID
		// also isolates balance request and outbox deduplication keys.
		var e error
		if u.ID, e = scopedUpdateID(tx, rawID); e != nil {
			return e
		}
		old, e := tx.One("SELECT id FROM tg_updates WHERE id=?", u.ID)
		if e != nil {
			return e
		}
		if old != nil {
			return nil
		}
		var user *TGUser
		var chat TGChat
		var text string
		var date int64
		if u.Message != nil {
			user = u.Message.From
			chat = u.Message.Chat
			text = u.Message.Text
			date = u.Message.Date
		}
		if u.Callback != nil && u.Callback.Message != nil {
			user = &u.Callback.From
			chat = u.Callback.Message.Chat
			text = u.Callback.Data
			date = now()
		}
		if user != nil && !user.IsBot && user.ID > 0 && user.ID == chat.ID && chat.Type == "private" {
			text = strings.TrimSpace(text)
			if len([]rune(text)) > 2000 {
				text = "/help"
			}
			id := "tg:" + strconv.FormatInt(user.ID, 10)
			row, e := tx.One("SELECT * FROM accounts WHERE id=?", id)
			if e != nil {
				return e
			}
			if row == nil {
				name := strings.TrimSpace(user.FirstName)
				if name == "" {
					name = "待确认玩家"
				}
				if len([]rune(name)) > 60 {
					name = string([]rune(name)[:60])
				}
				if _, e = tx.Exec("INSERT INTO accounts(id,name,role,telegram_id,enabled,created_at) VALUES(?,?,'player',?,0,?)", id, name, user.ID, now()); e != nil {
					return e
				}
			}
			a, e := getAccount(tx, id)
			if e != nil {
				return e
			}
			if _, e = tx.Exec(`INSERT INTO telegram_users(telegram_user_id,username,first_name,last_name,first_contact,last_contact) VALUES(?,?,?,?,?,?) ON CONFLICT(telegram_user_id) DO UPDATE SET username=excluded.username,first_name=excluded.first_name,last_name=excluded.last_name,last_contact=excluded.last_contact`, user.ID, user.Username, user.FirstName, user.LastName, now(), now()); e != nil {
				return e
			}
			var reply string
			kind, amount, requestOK := ParseBalanceRequest(text)
			if isSupport(text) || text == "查" || text == "查询" || text == "余额" {
				reply, e = s.privateQuery(tx, a, text)
				if e != nil {
					return e
				}
			} else if requestOK {
				reply, e = s.requestBalance(tx, u, a, kind, amount)
				if e != nil {
					return e
				}
			} else if strings.HasPrefix(text, "上") || strings.HasPrefix(text, "下") || strings.HasPrefix(text, "回") {
				reply = "申请金额必须为0.001至1000000000000积分，最多三位小数，不接受负数或其它内容。"
			} else if !a.Enabled {
				reply = fmt.Sprintf("您还没有加入战斗，或账户已停用。\n您的Telegram ID：%d\n请先提交上分申请，管理员批准后会自动开通；上分不会自动到账。", user.ID)
				if s.Config.SupportUsername != "" {
					reply += "\n客服：@" + s.Config.SupportUsername
				}
				if s.Config.NotifyAdminID != 0 {
					if e = s.queue(tx, "join:"+id, s.Config.NotifyAdminID, "", fmt.Sprintf("待确认玩家\nID：%d\n称呼：%s\n可引导玩家提交上分申请，批准上分后会自动开通；不要仅凭显示名认人。", user.ID, a.Name), false); e != nil {
						return e
					}
				}
			} else {
				pos, amount, isBet := ParseBet(text)
				if isBet {
					r, e := activeRound(tx)
					if e != nil {
						return e
					}
					if r == nil || r.State != "OPEN" {
						reply = "下注未受理：当前未开放下注。余额、历史与个人查询不受影响。"
					} else if date <= r.OpenedAt {
						reply = "下注未受理：消息早于开盘或处于开盘同一秒。请查看本期期号后重新发送。"
					} else {
						if _, e = tx.Exec("SAVEPOINT bet_attempt"); e != nil {
							return e
						}
						b, be := s.PlaceBet(tx, BetInput{AccountID: id, RoundID: r.ID, Position: pos, Stake: amount})
						if be != nil {
							var f *Fault
							if !errors.As(be, &f) {
								return be
							}
							if _, e = tx.Exec("ROLLBACK TO bet_attempt"); e != nil {
								return e
							}
							reply = "下注未受理：" + f.Message
						} else {
							after, e := getAccount(tx, id)
							if e != nil {
								return e
							}
							reply = fmt.Sprintf("下注成功\n%s期｜%d号 %s\n本金%d\n余额%d｜冻结%d｜可用%d\n注单：%s\n已受理注单不能通过编辑、撤回消息修改。", r.Number, b.Position, r.Heroes[b.Position-1].Name, b.Stake, after.Balance, after.Locked, after.Available, b.ID)
						}
						if _, e = tx.Exec("RELEASE bet_attempt"); e != nil {
							return e
						}
					}
				} else {
					reply, e = s.privateQuery(tx, a, text)
					if e != nil {
						return e
					}
				}
			}
			if e = s.queue(tx, fmt.Sprintf("reply:%d", u.ID), user.ID, "", reply, false); e != nil {
				return e
			}
			if _, e = tx.Exec("INSERT INTO audit(actor,action,request_key,detail,created_at) VALUES(?, 'telegram_update',?,?,?)", id, strconv.FormatInt(u.ID, 10), text, now()); e != nil {
				return e
			}
		}
		if _, e = tx.Exec("INSERT INTO tg_updates(id,created_at) VALUES(?,?)", u.ID, now()); e != nil {
			return e
		}
		_, e = tx.Exec("INSERT INTO meta(key,value) VALUES('tg_offset',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", strconv.FormatInt(rawID+1, 10))
		if e == nil {
			_, e = tx.Exec("UPDATE bot_sessions SET offset=? WHERE bot_id=(SELECT CAST(value AS INTEGER) FROM meta WHERE key='bot_id')", rawID+1)
		}
		return e
	})
}
func pageOffset(text, prefix string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(text, prefix))
	if n < 0 || n > 100000 {
		return 0
	}
	return n
}
func (s *Service) privateQuery(tx *sqlite.Tx, a Account, text string) (string, error) {
	if strings.HasPrefix(text, "/start") {
		fields := strings.Fields(text)
		text = "home"
		if len(fields) > 1 {
			switch fields[1] {
			case "history":
				text = "history:0"
			case "bets":
				text = "bets:0"
			case "bet":
				text = "help"
			}
		}
	}
	switch {
	case text == "home" || text == "/balance" || text == "/me" || text == "查" || text == "查询" || text == "余额" || text == "个人中心":
		zone := time.FixedZone("CST", 8*3600)
		t := time.Now().In(zone)
		start := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, zone).AddDate(0, 0, -2).Unix()
		totals, e := tx.One(`SELECT COALESCE(SUM(CASE WHEN kind='GAME' THEN delta ELSE 0 END),0) AS game,COALESCE(SUM(CASE WHEN kind IN ('FEE','FEE_REFUND') THEN delta ELSE 0 END),0) AS fees FROM entries WHERE account_id=? AND created_at>=?`, a.ID, start)
		if e != nil {
			return "", e
		}
		return fmt.Sprintf("个人中心\nTelegram ID：%d\n余额%d｜冻结%d｜可用%d\n最近三个北京时间自然日：\n游戏变化%+d\n人工调分不计入游戏战绩。\n登记时间：%s", a.TelegramID, a.Balance, a.Locked, a.Available, moneyRow(totals, "game"), time.Unix(a.CreatedAt, 0).In(zone).Format("2006-01-02 15:04")), nil
	case isSupport(text):
		if s.Config.SupportUsername != "" {
			return "联系客服：\n@" + s.Config.SupportUsername, nil
		}
		return "当前暂未配置客服账号，请稍后联系群管理员。", nil
	case strings.HasPrefix(text, "history:") || text == "/history":
		offset := pageOffset(text, "history:")
		rows, e := tx.Query("SELECT * FROM rounds WHERE state IN ('SETTLED','VOID') ORDER BY settled_at DESC,rowid DESC LIMIT 5 OFFSET ?", offset)
		if e != nil {
			return "", e
		}
		if len(rows) == 0 {
			return "暂无更多开奖记录。", nil
		}
		out := "历史开奖（已保存的结算事实）\n"
		for _, row := range rows {
			r, e := roundFrom(row)
			if e != nil {
				return "", e
			}
			out += r.Number + "期\n"
			if r.Result != nil {
				for _, p := range r.Result.Positions {
					mark := ""
					if p.Banker {
						mark = "[庄]"
					}
					out += fmt.Sprintf("%d%s %s → %s\n", p.Position, mark, p.Hand.Raw, p.Hand.Label)
				}
				if r.Result.WholeVoid {
					out += "整期流局：" + r.Result.Reason + "\n"
				}
			}
			out += "\n"
		}
		out += fmt.Sprintf("下一页：发送 history:%d", offset+5)
		return out, nil
	case strings.HasPrefix(text, "bets:") || text == "/bets":
		offset := pageOffset(text, "bets:")
		rows, e := tx.Query("SELECT b.*,r.number FROM bets b JOIN rounds r ON r.id=b.round_id WHERE b.account_id=? ORDER BY b.created_at DESC,b.rowid DESC LIMIT 10 OFFSET ?", a.ID, offset)
		if e != nil {
			return "", e
		}
		if len(rows) == 0 {
			return "暂无更多本人注单。", nil
		}
		out := "我的注单（仅本人）\n"
		for _, r := range rows {
			b := betFrom(r)
			state := map[string]string{"RESERVED": "待结算", "WIN": "赢", "LOSS": "输", "VOID": "流局"}[b.State]
			out += fmt.Sprintf("%s｜%d号｜本金%d｜%s", r["number"], b.Position, b.Stake, state)
			if b.State != "RESERVED" {
				out += fmt.Sprintf("｜游戏%+d｜净变化%+d", b.GameDelta, b.NetDelta)
			}
			out += "\n"
		}
		out += fmt.Sprintf("下一页：发送 bets:%d", offset+10)
		return out, nil
	default:
		r, e := activeRound(tx)
		if e != nil {
			return "", e
		}
		out := "快捷下注：发送 1.100 或 1 . 100U\n含义：1号英雄，本金100积分。\n金额最多三位小数，例如 1.100.199 表示1号英雄下注100.199积分；仅与庄家比牛型。赢按自己的牛型倍率，输固定扣本金1倍。\n"
		if r == nil {
			return out + "当前没有活动期次。", nil
		}
		out += fmt.Sprintf("当前：%s期｜%s\n", r.Number, map[string]string{"DRAFT": "待配置", "OPEN": "受理中", "CLOSED": "已封盘"}[r.State])
		for i, h := range r.Heroes {
			mark := "可选"
			if i+1 == r.Banker {
				mark = "庄家，不可下注"
			}
			out += fmt.Sprintf("%d号 %s [%s]\n", i+1, h.Name, mark)
		}
		return out, nil
	}
}

func isSupport(text string) bool {
	switch text {
	case "客服", "联系管理员", "联系", "人工", "管理员", "support", "/support":
		return true
	}
	return false
}
func timeText() string {
	return time.Now().In(time.FixedZone("CST", 8*3600)).Format("2006-01-02 15:04:05")
}
