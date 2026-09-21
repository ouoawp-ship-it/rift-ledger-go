package app

import (
	"encoding/json"
	"fmt"
	"regexp"
	"riftledger/internal/sqlite"
	"strings"
)

type MessageBlock struct {
	Type    string `json:"type"`
	Text    string `json:"text,omitempty"`
	ImageID string `json:"image_id,omitempty"`
}
type TemplateVariable struct {
	Name     string `json:"name"`
	Example  string `json:"example"`
	Required bool   `json:"required"`
}
type MessageTemplate struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Destination string             `json:"destination"`
	Variables   []TemplateVariable `json:"variables"`
	Defaults    []MessageBlock     `json:"defaults"`
	Blocks      []MessageBlock     `json:"blocks"`
	Revision    int64              `json:"revision"`
}
type TemplatePatch struct {
	ID       string         `json:"id"`
	Revision int64          `json:"revision"`
	Blocks   []MessageBlock `json:"blocks"`
}

var placeholderPattern = regexp.MustCompile(`\{([^{}\n]+)\}`)

func (s *Service) queueOpenTemplate(tx *sqlite.Tx, r Round) error {
	var heroes strings.Builder
	for i, h := range r.Heroes {
		role := "闲"
		if i+1 == r.Banker {
			role = "庄"
		}
		fmt.Fprintf(&heroes, "%d号 %s [%s]\n", i+1, h.Name, role)
	}
	return s.queueTemplate(tx, fmt.Sprintf("round-card:%s:OPEN", r.ID), s.Config.GroupID, "round_open", map[string]string{"当前期数": r.Number + "期", "英雄列表": strings.TrimSpace(heroes.String()), "最低下注": fmt.Sprintf("%d", r.Rules.MinStake), "最高下注": fmt.Sprintf("%d", r.Rules.MaxStake)}, true)
}

func templateDefinitions() []MessageTemplate {
	makeTemplate := func(id, name, destination, text string, vars ...TemplateVariable) MessageTemplate {
		return MessageTemplate{ID: id, Name: name, Destination: destination, Variables: vars, Defaults: []MessageBlock{{Type: "text", Text: text}}}
	}
	variable := func(name, example string) TemplateVariable {
		return TemplateVariable{Name: name, Example: example, Required: true}
	}
	period := variable("当前期数", "2026-09-21-0007期")
	return []MessageTemplate{
		makeTemplate("round_open", "开盘通知", "公示群", "峡谷账房｜{当前期数}\n开始答题：仅机器人私聊受理，封盘后仅可查询。\n{英雄列表}\n单笔{最低下注}–{最高下注}；累计不超过余额1/4。玩家模式不收取费用。", period, variable("英雄列表", "1号 剑魔 [庄]\n2号 阿狸 [闲]\n3号 阿卡丽 [闲]\n4号 艾希 [闲]\n5号 布隆 [闲]"), variable("最低下注", "20.000"), variable("最高下注", "300.000")),
		makeTemplate("round_close", "封盘通知", "公示群", "峡谷账房｜{当前期数}\n封盘通知\n已停止答题，本期不再受理下注。\n等待开奖结果，个人查询继续开放。", period),
		makeTemplate("round_result", "开奖结果", "公示群", "峡谷账房｜{当前期数}\n开奖结果\n{开奖结果}\n玩家本期游戏合计：{本期合计盈亏} 积分\n本期已结算。", period, variable("开奖结果", "1号 剑魔【庄家】\n伤害 12710 → 12710｜牛1\n结果：庄家\n\n2号 阿狸【闲家】\n伤害 12745 → 12745｜牛9\n结果：闲赢"), variable("本期合计盈亏", "+98.000")),
		makeTemplate("winners", "中奖名单", "公示群", "峡谷账房｜{当前期数}\n中奖名单\n{中奖名单}", period, variable("中奖名单", "中奖玩家1人｜第1/1页\n中奖盈利不含本金；本期净变化包含全部注单。\n\n1. 示例玩家（ID：123456789）\n中奖1笔｜中奖盈利+98.000｜本期净变化+98.000")),
		makeTemplate("round_next", "下一期通知", "公示群", "下一期：{下一期数}\n等待管理员配置敌方五英雄并手动选庄；尚未开放下注。", variable("下一期数", "2026-09-21-0008期")),
		makeTemplate("round_cancel", "取消本期", "公示群", "峡谷账房｜{当前期数}\n本期已取消，全部注单作废，本金冻结已解除，不计算输赢。\n原因：{取消原因}", period, variable("取消原因", "本场数据无法核实")),
		makeTemplate("bet_group", "下注公示", "公示群", "注单已受理\n{当前期数}\n玩家ID：{玩家ID}\n{英雄位置}号 {英雄名称}｜本金{下注金额}\n注单：{注单编号}", period, variable("玩家ID", "123456789"), variable("英雄位置", "2"), variable("英雄名称", "阿狸"), variable("下注金额", "100.000"), variable("注单编号", "bet-example-001")),
		makeTemplate("settle_private", "玩家结算通知", "玩家私聊", "{当前期数}结算\n注单{注单数量}笔｜游戏变化{游戏盈亏}｜费用{费用}\n本期净变化{净盈亏}\n当前余额{当前余额}｜可用{可用余额}", period, variable("注单数量", "1"), variable("游戏盈亏", "+98.000"), variable("费用", "0.000"), variable("净盈亏", "+98.000"), variable("当前余额", "1098.199"), variable("可用余额", "1098.199")),
		makeTemplate("balance_admin", "上下分申请提醒", "管理员私聊", "🔔 新的{申请类型}申请\n玩家：{玩家昵称}\nTelegram：{玩家用户名}\nTG ID：{玩家ID}\n申请金额：{申请金额}\n当前余额：{当前余额}\n申请时间：{申请时间}\n请进入后台处理。", variable("申请类型", "上分"), variable("玩家昵称", "示例玩家"), variable("玩家用户名", "@example_player"), variable("玩家ID", "123456789"), variable("申请金额", "100.199"), variable("当前余额", "1000.000"), variable("申请时间", "2026-09-21 20:30:00")),
		makeTemplate("balance_result", "上下分审批结果", "玩家私聊", "积分申请 #{申请编号} {审批结果}\n金额：{申请金额}\n当前余额：{当前余额}\n备注：{管理员备注}", variable("申请编号", "42"), variable("审批结果", "已批准"), variable("申请金额", "100.199"), variable("当前余额", "1100.199"), TemplateVariable{Name: "管理员备注", Example: "已核实", Required: false}),
	}
}
func templateDefinition(id string) (MessageTemplate, error) {
	for _, d := range templateDefinitions() {
		if d.ID == id {
			return d, nil
		}
	}
	return MessageTemplate{}, bad("未知消息类型")
}
func readTemplate(tx *sqlite.Tx, id string) (MessageTemplate, error) {
	d, err := templateDefinition(id)
	if err != nil {
		return d, err
	}
	d.Blocks = append([]MessageBlock{}, d.Defaults...)
	row, err := tx.One("SELECT revision,blocks FROM message_templates WHERE id=?", id)
	if err != nil {
		return d, err
	}
	if row != nil {
		d.Revision = row.Int("revision")
		if err = json.Unmarshal([]byte(row["blocks"]), &d.Blocks); err != nil {
			return d, err
		}
	}
	return d, nil
}
func validateBlocks(tx *sqlite.Tx, d MessageTemplate, blocks []MessageBlock) error {
	if len(blocks) < 1 || len(blocks) > 8 {
		return bad("每种消息需要1至8个内容段落")
	}
	allowed, seen := map[string]bool{}, map[string]bool{}
	for _, v := range d.Variables {
		allowed[v.Name] = true
	}
	images, texts := 0, 0
	for _, b := range blocks {
		switch b.Type {
		case "text":
			texts++
			if strings.TrimSpace(b.Text) == "" || len([]rune(b.Text)) > 2000 || b.ImageID != "" {
				return bad("文字段落不能为空，且最多2000个字符")
			}
			for _, match := range placeholderPattern.FindAllStringSubmatch(b.Text, -1) {
				if !allowed[match[1]] {
					return bad("该消息不支持变量：" + match[0])
				}
				seen[match[1]] = true
			}
			remaining := placeholderPattern.ReplaceAllString(b.Text, "")
			if strings.ContainsAny(remaining, "{}") {
				return bad("变量必须使用完整的 {变量名称}，请从变量列表插入")
			}
		case "image":
			images++
			if b.Text != "" || !imageIDPattern.MatchString(b.ImageID) {
				return bad("请上传有效图片")
			}
			row, err := tx.One("SELECT id FROM message_images WHERE id=?", b.ImageID)
			if err != nil {
				return err
			}
			if row == nil {
				return bad("图片不存在，请重新上传")
			}
		default:
			return bad("内容类型必须是文字或图片")
		}
	}
	if texts == 0 || images > 3 {
		return bad("至少保留一个文字段落，最多插入3张图片")
	}
	for _, v := range d.Variables {
		if v.Required && !seen[v.Name] {
			return bad("必须保留系统变量：{" + v.Name + "}")
		}
	}
	return nil
}
func (s *Service) MessageTemplates() (any, error) {
	var out []MessageTemplate
	err := s.DB.Read(func(tx *sqlite.Tx) error {
		for _, d := range templateDefinitions() {
			v, e := readTemplate(tx, d.ID)
			if e != nil {
				return e
			}
			out = append(out, v)
		}
		return nil
	})
	return out, err
}
func (s *Service) SaveMessageTemplate(tx *sqlite.Tx, p TemplatePatch) (any, error) {
	d, err := readTemplate(tx, p.ID)
	if err != nil {
		return nil, err
	}
	if d.Revision != p.Revision {
		return nil, conflict("这条消息设置已被修改，请重新载入后保存")
	}
	if err = validateBlocks(tx, d, p.Blocks); err != nil {
		return nil, err
	}
	if _, err = tx.Exec("INSERT INTO message_templates(id,revision,blocks) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET revision=excluded.revision,blocks=excluded.blocks", p.ID, p.Revision+1, asJSON(p.Blocks)); err != nil {
		return nil, err
	}
	return readTemplate(tx, p.ID)
}
func renderBlocks(blocks []MessageBlock, values map[string]string) []MessageBlock {
	out := make([]MessageBlock, 0, len(blocks))
	for _, b := range blocks {
		if b.Type == "text" {
			b.Text = placeholderPattern.ReplaceAllStringFunc(b.Text, func(token string) string { return values[token[1:len(token)-1]] })
		}
		out = append(out, b)
	}
	return out
}
func (s *Service) PreviewMessageTemplate(p TemplatePatch) (any, error) {
	var out []MessageBlock
	err := s.DB.Read(func(tx *sqlite.Tx) error {
		d, e := templateDefinition(p.ID)
		if e != nil {
			return e
		}
		if e = validateBlocks(tx, d, p.Blocks); e != nil {
			return e
		}
		values := map[string]string{}
		for _, v := range d.Variables {
			values[v.Name] = v.Example
		}
		out = renderBlocks(p.Blocks, values)
		return nil
	})
	return out, err
}

// Public values are captured at the business event, never at send/retry time.
func (s *Service) queueTemplate(tx *sqlite.Tx, key string, chat int64, kind string, values map[string]string, buttons bool) error {
	if chat == 0 {
		return nil
	}
	// A second invocation must not append parts from a subsequently edited template.
	old, err := tx.One("SELECT id FROM outbox WHERE key=?", key)
	if err != nil || old != nil {
		return err
	}
	d, err := readTemplate(tx, kind)
	if err != nil {
		return err
	}
	for _, v := range d.Variables {
		if _, ok := values[v.Name]; !ok {
			return fmt.Errorf("消息 %s 缺少变量 %s", kind, v.Name)
		}
	}
	parts := renderBlocks(d.Blocks, values)
	index := 0
	for _, b := range parts {
		chunks := []string{b.Text}
		if b.Type == "text" {
			chunks = splitMessage(b.Text)
		}
		for _, chunk := range chunks {
			partKey := key
			if index > 0 {
				partKey = fmt.Sprintf("%s:part:%d", key, index)
			}
			index++
			if b.Type == "image" {
				p := MessagePayload{ChatID: chat, Text: "自定义消息图片", ImageID: b.ImageID}
				if _, err = tx.Exec("INSERT INTO outbox(key,chat_id,payload,created_at) VALUES(?,?,?,?)", partKey, chat, asJSON(p), now()); err != nil {
					return err
				}
			} else if err = s.queue(tx, partKey, chat, "", chunk, buttons); err != nil {
				return err
			}
		}
	}
	return nil
}
func splitMessage(text string) []string {
	var out []string
	var b strings.Builder
	units := 0
	for _, r := range text {
		size := 1
		if r > 0xffff {
			size = 2
		}
		if units+size > 4000 {
			out = append(out, b.String())
			b.Reset()
			units = 0
		}
		b.WriteRune(r)
		units += size
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}
