package app

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"riftledger/internal/sqlite"
)

type Button struct {
	Text string `json:"text"`
	URL  string `json:"url,omitempty"`
	Data string `json:"callback_data,omitempty"`
}
type Keyboard struct {
	Rows [][]Button `json:"inline_keyboard"`
}
type MessagePayload struct {
	ImageID     string       `json:"image_id,omitempty"`
	GroupAction string       `json:"group_action,omitempty"`
	RoundID     string       `json:"round_id,omitempty"`
	Media       []MediaPhoto `json:"media,omitempty"`
	ChatID      int64        `json:"chat_id"`
	Text        string       `json:"text"`
	Markup      *Keyboard    `json:"reply_markup,omitempty"`
}
type MediaPhoto struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Caption string `json:"caption"`
}

func (s *Service) QueueGroupTest(tx *sqlite.Tx, key string) (any, error) {
	e := s.queue(tx, "test-group:"+key, s.Config.GroupID, "", "峡谷账房：管理员手动测试群发送。", false)
	return map[string]any{"queued": e == nil}, e
}

func (s *Service) queueHeroes(tx *sqlite.Tx, r Round) error {
	if s.Config.GroupID == 0 || s.Champions == nil {
		return nil
	}
	snapshot := s.Champions.View()
	p := MessagePayload{ChatID: s.Config.GroupID, Text: fmt.Sprintf("峡谷账房｜%s期\n开始答题：仅机器人私聊受理，封盘后仅可查询。\n", r.Number)}
	for i, h := range r.Heroes {
		mark := " [闲]"
		if i+1 == r.Banker {
			mark = " [庄]"
		}
		caption := fmt.Sprintf("%d号：%s%s", i+1, h.Name, mark)
		p.Text += caption + "\n"
		p.Media = append(p.Media, MediaPhoto{ID: h.ID, Version: snapshot.Version, Caption: caption})
	}
	p.Text += fmt.Sprintf("单笔%d–%d；累计不超过余额1/4。玩家模式不收取费用。", r.Rules.MinStake, r.Rules.MaxStake)
	_, e := tx.Exec("INSERT OR IGNORE INTO outbox(key,chat_id,payload,created_at) VALUES(?,?,?,?)", "round-media:"+r.ID, p.ChatID, asJSON(p), now())
	return e
}

type OutboxItem struct {
	ID            int64
	CardKey       string
	CardMessageID int64
	Payload       MessagePayload
	Attempts      int64
}

func menu() *Keyboard {
	return &Keyboard{Rows: [][]Button{{{Text: "私聊下注", Data: "help"}, {Text: "个人中心", Data: "home"}}, {{Text: "我的注单", Data: "bets:0"}, {Text: "历史开奖", Data: "history:0"}}, {{Text: "联系管理员", Data: "support"}}}}
}
func (s *Service) queue(tx *sqlite.Tx, key string, chat int64, cardKey, text string, groupButtons bool) error {
	if chat == 0 {
		return nil
	}
	p := MessagePayload{ChatID: chat, Text: text}
	if chat == s.Config.GroupID {
		if groupButtons && s.Config.BotUsername != "" {
			base := "https://t.me/" + s.Config.BotUsername
			p.Markup = &Keyboard{Rows: [][]Button{{{Text: "私聊下注", URL: base + "?start=bet"}, {Text: "个人中心", URL: base + "?start=home"}}, {{Text: "历史开奖", URL: base + "?start=history"}, {Text: "我的注单", URL: base + "?start=bets"}}}}
		}
	} else if chat > 0 {
		p.Markup = menu()
		if s.Config.SupportUsername != "" {
			p.Markup.Rows = append(p.Markup.Rows, []Button{{Text: "联系客服", URL: "https://t.me/" + s.Config.SupportUsername}})
		}
	}
	_, e := tx.Exec("INSERT OR IGNORE INTO outbox(key,chat_id,card_key,payload,created_at) VALUES(?,?,?,?,?)", key, chat, cardKey, asJSON(p), now())
	return e
}
func (s *Service) roundCard(tx *sqlite.Tx, r Round, heading string) error {
	if s.Config.GroupID == 0 {
		return nil
	}
	text := fmt.Sprintf("峡谷账房｜%s期\n%s\n", r.Number, heading)
	for i, h := range r.Heroes {
		mark := "闲"
		if i+1 == r.Banker {
			mark = "庄"
		}
		text += fmt.Sprintf("%d号 %s [%s]", i+1, h.Name, mark)
		if r.Result != nil {
			p := r.Result.Positions[i]
			text += fmt.Sprintf("｜伤害%s → %s｜%s", p.Hand.Raw, p.Hand.Normalized, p.Hand.Label)
			if r.Result.WholeVoid {
				text += "｜整期流局"
			}
		}
		text += "\n"
	}
	if r.Result != nil {
		text += fmt.Sprintf("时长%d秒｜玩家游戏合计%+d", r.Result.DurationSeconds, r.Result.PlayerGameDelta)
	} else {
		text += fmt.Sprintf("单笔%d–%d；累计不超过余额1/4。玩家模式不收取费用。", r.Rules.MinStake, r.Rules.MaxStake)
	}
	return s.queue(tx, fmt.Sprintf("round-card:%s:%s", r.ID, r.State), s.Config.GroupID, "round:"+r.ID, text, true)
}

// Settlement results must appear as a new message, not only an edit to the
// earlier round card. The stable key also protects replayed settlement requests.
func (s *Service) queueRoundResult(tx *sqlite.Tx, r Round) error {
	if s.Config.GroupID == 0 || r.Result == nil {
		return nil
	}
	var text strings.Builder

	if r.Result.WholeVoid {
		fmt.Fprintf(&text, "整期流局：%s\n", r.Result.Reason)
	}
	for _, p := range r.Result.Positions {
		role := "闲家"
		if p.Banker {
			role = "庄家"
		}
		outcome := map[string]string{"WIN": "闲赢", "LOSS": "庄赢", "VOID": "流局", "BANKER": "庄家"}[p.Outcome]
		fmt.Fprintf(&text, "\n%d号 %s【%s】\n", p.Position, p.Hero.Name, role)
		if p.Hand.Raw != "" {
			fmt.Fprintf(&text, "伤害 %s", p.Hand.Raw)
			if p.Hand.Normalized != "" {
				fmt.Fprintf(&text, " → %s", p.Hand.Normalized)
			}
			fmt.Fprintf(&text, "｜%s\n", p.Hand.Label)
		}
		fmt.Fprintf(&text, "结果：%s\n", outcome)
	}
	return s.queueTemplate(tx, "round-result:"+r.ID, s.Config.GroupID, "round_result", map[string]string{"当前期数": r.Number + "期", "开奖结果": strings.TrimSpace(text.String()), "本期合计盈亏": fmt.Sprintf("%+d", r.Result.PlayerGameDelta)}, false)
}

// Queue a separate winners list after the round result announcement.
// Winning profit excludes returned stake; net change includes all of a player's bets.
func (s *Service) queueWinners(tx *sqlite.Tx, r Round) error {
	if s.Config.GroupID == 0 || r.Result == nil {
		return nil
	}
	type winner struct {
		count       int64
		profit, net Money
	}
	totals := map[string]winner{}
	for _, line := range r.Result.Lines {
		w := totals[line.AccountID]
		w.net += line.NetDelta
		if line.Outcome == "WIN" {
			w.count++
			w.profit += line.GameDelta
		}
		totals[line.AccountID] = w
	}
	ids := []string{}
	for id, w := range totals {
		if w.count > 0 {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)

	if len(ids) == 0 {
		message := "本期无人中奖。"
		if r.Result.WholeVoid {
			message = "本期整期流局，无中奖名单。"
		}
		return s.queueTemplate(tx, "winners:"+r.ID+":0", s.Config.GroupID, "winners", map[string]string{"当前期数": r.Number + "期", "中奖名单": message}, false)
	}
	// Bound each page well below Telegram's message limit, including long nicknames.
	for start := 0; start < len(ids); start += 15 {
		end := start + 15
		if end > len(ids) {
			end = len(ids)
		}
		text := fmt.Sprintf("中奖玩家%d人｜第%d/%d页\n中奖盈利不含本金；本期净变化包含全部注单。\n", len(ids), start/15+1, (len(ids)+14)/15)
		for i := start; i < end; i++ {
			a, err := getAccount(tx, ids[i])
			if err != nil {
				return err
			}
			name := []rune(strings.Join(strings.Fields(a.Name), " "))
			if len(name) > 40 {
				name = name[:40]
			}
			w := totals[ids[i]]
			text += fmt.Sprintf("\n%d. %s（ID：%d）\n中奖%d笔｜中奖盈利%+d｜本期净变化%+d\n", i+1, string(name), a.TelegramID, w.count, w.profit, w.net)
		}
		if err := s.queueTemplate(tx, fmt.Sprintf("winners:%s:%d", r.ID, start/15), s.Config.GroupID, "winners", map[string]string{"当前期数": r.Number + "期", "中奖名单": text}, false); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) ClaimOutbox() (*OutboxItem, error) {
	var out *OutboxItem
	e := s.DB.Transaction(func(tx *sqlite.Tx) error {
		row, e := tx.One(`SELECT o.* FROM outbox o WHERE o.state='PENDING' AND o.next_at<=?
		 AND NOT EXISTS (SELECT 1 FROM outbox p WHERE p.chat_id=o.chat_id AND p.id<o.id AND p.state!='SENT'
		 AND (COALESCE(json_extract(CASE WHEN json_valid(p.payload) THEN p.payload ELSE '{}' END,'$.group_action'),'')='')=(COALESCE(json_extract(CASE WHEN json_valid(o.payload) THEN o.payload ELSE '{}' END,'$.group_action'),'')='')
		 AND NOT (COALESCE(json_extract(CASE WHEN json_valid(p.payload) THEN p.payload ELSE '{}' END,'$.group_action'),'')='mute' AND p.state IN ('FAILED','UNKNOWN')))
		 AND NOT EXISTS (SELECT 1 FROM outbox p WHERE p.chat_id=o.chat_id AND p.state='SENT' AND p.next_at>?)
		 ORDER BY CASE WHEN COALESCE(json_extract(CASE WHEN json_valid(o.payload) THEN o.payload ELSE '{}' END,'$.group_action'),'')!='' THEN 0 ELSE 1 END,o.id LIMIT 1`, now(), now())
		if e != nil || row == nil {
			return e
		}
		var p MessagePayload
		if e = json.Unmarshal([]byte(row["payload"]), &p); e != nil {
			_, e = tx.Exec("UPDATE outbox SET state='FAILED',last_error='消息内容损坏，请检查该任务' WHERE id=?", row.Int("id"))
			return e
		}
		item := &OutboxItem{ID: row.Int("id"), CardKey: row["card_key"], Payload: p, Attempts: row.Int("attempts") + 1}
		if item.CardKey != "" {
			card, e := tx.One("SELECT message_id FROM cards WHERE key=?", item.CardKey)
			if e != nil {
				return e
			}
			if card != nil {
				item.CardMessageID = card.Int("message_id")
			}
		}
		if _, e = tx.Exec("UPDATE outbox SET state='INFLIGHT',attempts=attempts+1 WHERE id=?", item.ID); e != nil {
			return e
		}
		out = item
		return nil
	})
	return out, e
}
func (s *Service) CompleteOutbox(item OutboxItem, state string, messageID int64, detail string, retryAfter int64) error {
	if len([]rune(detail)) > 300 {
		detail = string([]rune(detail)[:300])
	}
	return s.DB.Transaction(func(tx *sqlite.Tx) error {
		if state != "SENT" && state != "FAILED" && state != "UNKNOWN" && state != "PENDING" {
			return bad("非法消息状态")
		}
		if state == "SENT" && item.CardKey != "" && messageID > 0 {
			if _, e := tx.Exec("INSERT INTO cards(key,message_id) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET message_id=excluded.message_id", item.CardKey, messageID); e != nil {
				return e
			}
		}
		_, e := tx.Exec("UPDATE outbox SET state=?,message_id=?,last_error=?,next_at=? WHERE id=? AND state='INFLIGHT'", state, messageID, detail, now()+retryAfter, item.ID)
		return e
	})
}
func (s *Service) ResolveOutbox(tx *sqlite.Tx, id int64, action string, messageID int64) (any, error) {
	row, e := tx.One("SELECT * FROM outbox WHERE id=?", id)
	if e != nil {
		return nil, e
	}
	if row == nil {
		return nil, notfound("发送任务不存在")
	}
	if row["state"] != "FAILED" && row["state"] != "UNKNOWN" {
		return nil, conflict("只有失败或结果未知的任务需要人工处理")
	}
	switch action {
	case "retry":
		_, e = tx.Exec("UPDATE outbox SET state='PENDING',next_at=0,last_error='' WHERE id=?", id)
	case "ack":
		if row["card_key"] != "" && messageID <= 0 {
			return nil, bad("确认期次卡片已发送必须填写真实message_id，供后续编辑使用")
		}
		if row["card_key"] != "" {
			if _, e = tx.Exec("INSERT INTO cards(key,message_id) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET message_id=excluded.message_id", row["card_key"], messageID); e != nil {
				return nil, e
			}
		}
		_, e = tx.Exec("UPDATE outbox SET state='SENT',message_id=?,last_error='管理员已核实送达' WHERE id=?", messageID, id)
	case "skip":
		var payload MessagePayload
		_ = json.Unmarshal([]byte(row["payload"]), &payload)
		if payload.GroupAction == "restore" {
			return nil, bad("恢复群权限任务不能直接跳过；请重试，或手动恢复后选择已核实")
		}
		if strings.HasPrefix(row["card_key"], "round:") {
			return nil, bad("期次卡片不能直接跳过；请核实message_id或显式重试")
		}
		_, e = tx.Exec("UPDATE outbox SET state='SENT',last_error='管理员明确跳过未送达任务' WHERE id=?", id)
	default:
		return nil, bad("处理动作必须为retry、ack或skip")
	}
	return map[string]any{"id": id, "action": action}, e
}

func (s *Service) CompleteMedia(item OutboxItem, state string, id int64, detail, result string, retry int64) error {
	return s.DB.Transaction(func(tx *sqlite.Tx) error {
		_, e := tx.Exec("UPDATE outbox SET state=?,message_id=?,last_error=?,media_result=?,next_at=? WHERE id=? AND state='INFLIGHT'", state, id, detail, result, now()+retry, item.ID)
		return e
	})
}
func (s *Service) FallbackMedia(item OutboxItem, state, detail string) error {
	return s.DB.Transaction(func(tx *sqlite.Tx) error {
		// Mark the album handled so it cannot block later editable cards. Its actual
		// failure/unknown outcome stays visible in media_result and last_error.
		if _, e := tx.Exec("UPDATE outbox SET state='SENT',media_result=?,last_error=? WHERE id=? AND state='INFLIGHT'", state, detail, item.ID); e != nil {
			return e
		}
		p := item.Payload
		p.Media = nil
		_, e := tx.Exec("INSERT OR IGNORE INTO outbox(key,chat_id,payload,created_at) VALUES(?,?,?,?)", fmt.Sprintf("media-fallback:%d", item.ID), p.ChatID, asJSON(p), now())
		return e
	})
}
