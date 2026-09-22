package app

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"riftledger/internal/sqlite"
	"riftledger/pkg/bull"
)

func playerGameDelta(tx *sqlite.Tx, accountID string, delta Money, batch, roundID, betID string) error {
	a, e := getAccount(tx, accountID)
	if e != nil {
		return e
	}
	if a.Balance+delta > MoneyLimit {
		return bad("玩家余额超出安全范围")
	}
	if delta < 0 && a.Balance+delta < 0 {
		return conflict("玩家余额不足以承担本期亏损")
	}
	if _, e = tx.Exec("UPDATE accounts SET balance=balance+? WHERE id=?", delta, accountID); e != nil {
		return e
	}
	_, e = tx.Exec("INSERT INTO entries(batch,account_id,round_id,bet_id,kind,delta,balance_after,note,created_at) VALUES(?,?,?,?,?,?,?,?,?)", batch, accountID, roundID, betID, "GAME", delta, a.Balance+delta, "玩家游戏盈亏", now())
	return e
}

func validateConfiguration(in ConfigureRound, complete bool) error {
	if !validNumber(in.Number) {
		return bad("期号格式应为有效日期-至少四位序号，例如2026-09-20-0001")
	}
	if !complete && len(in.Heroes) == 0 && in.Banker == 0 {
		return nil
	}
	if len(in.Heroes) != 5 {
		return bad("每期必须填写五个敌方英雄")
	}
	if in.Banker < 1 || in.Banker > 5 {
		return bad("必须手动选择1至5号中的庄家英雄，系统不会默认1号")
	}
	ids := map[string]bool{}
	names := map[string]bool{}
	for _, h := range in.Heroes {
		id := strings.ToLower(h.ID)
		name := strings.TrimSpace(h.Name)
		if !heroPattern.MatchString(h.ID) || name == "" || len([]rune(name)) > 40 {
			return bad("英雄ID只能包含字母、数字、下划线或横线；名称需1至40字")
		}
		if ids[id] || names[name] {
			return bad("本期英雄不能重复，请使用唯一且稳定的英雄ID")
		}
		ids[id] = true
		names[name] = true
	}
	return nil
}
func (s *Service) CreateDraft(tx *sqlite.Tx, in ConfigureRound) (any, error) {
	if e := s.canonicalHeroes(&in); e != nil {
		return nil, e
	}
	if e := validateConfiguration(in, false); e != nil {
		return nil, e
	}
	active, e := activeRound(tx)
	if e != nil {
		return nil, e
	}
	if active != nil {
		return nil, conflict("已有活动期次；请配置当前草稿或先完成当前局")
	}
	return s.createDraft(tx, in)
}
func (s *Service) createDraft(tx *sqlite.Tx, in ConfigureRound) (Round, error) {
	r, v, e := getRules(tx)
	if e != nil {
		return Round{}, e
	}
	id := newID()
	if in.Heroes == nil {
		in.Heroes = []Hero{}
	}
	existing, e := tx.One("SELECT id FROM rounds WHERE number=?", in.Number)
	if e != nil {
		return Round{}, e
	}
	if existing != nil {
		return Round{}, conflict("期号已存在")
	}
	if _, e = tx.Exec("INSERT INTO rounds(id,number,state,banker,heroes,rules,rules_version,created_at) VALUES(?,?,'DRAFT',?,?,?,?,?)", id, in.Number, in.Banker, asJSON(in.Heroes), asJSON(r), v, now()); e != nil {
		return Round{}, e
	}
	return getRound(tx, id)
}
func (s *Service) Configure(tx *sqlite.Tx, id string, in ConfigureRound) (any, error) {
	if e := s.canonicalHeroes(&in); e != nil {
		return nil, e
	}
	if e := validateConfiguration(in, true); e != nil {
		return nil, e
	}
	r, e := getRound(tx, id)
	if e != nil {
		return nil, e
	}
	if r.State != "DRAFT" {
		return nil, conflict("开始后不能更换英雄、庄家或期号")
	}
	old, e := tx.One("SELECT id FROM rounds WHERE number=? AND id!=?", in.Number, id)
	if e != nil {
		return nil, e
	}
	if old != nil {
		return nil, conflict("期号已经存在")
	}
	if _, e = tx.Exec("UPDATE rounds SET number=?,banker=?,heroes=?,revision=revision+1 WHERE id=?", in.Number, in.Banker, asJSON(in.Heroes), id); e != nil {
		return nil, e
	}
	return getRound(tx, id)
}
func (s *Service) OpenRound(tx *sqlite.Tx, id string) (any, error) {
	r, e := getRound(tx, id)
	if e != nil {
		return nil, e
	}
	if r.State != "DRAFT" {
		return nil, conflict("只有待配置期次可以开始")
	}
	if e = validateConfiguration(ConfigureRound{r.Number, r.Banker, r.Heroes}, true); e != nil {
		return nil, e
	}
	rules, v, e := getRules(tx)
	if e != nil {
		return nil, e
	}
	if !rules.Confirmed {
		return nil, conflict("请先在规则页保存并确认赔率和零组合口径，再开始")
	}
	if e = rules.Validate(); e != nil {
		return nil, e
	}
	if _, e = tx.Exec("UPDATE rounds SET state='OPEN',opened_at=?,rules=?,rules_version=?,revision=revision+1 WHERE id=?", now(), asJSON(rules), v, id); e != nil {
		return nil, e
	}
	r, e = getRound(tx, id)
	if e != nil {
		return nil, e
	}
	if e = s.queueOpenTemplate(tx, r); e != nil {
		return nil, e
	}
	if e = s.queueHeroes(tx, r); e != nil {
		return nil, e
	}
	return r, nil
}
func (s *Service) CloseRound(tx *sqlite.Tx, id string) (any, error) {
	r, e := getRound(tx, id)
	if e != nil {
		return nil, e
	}
	if r.State != "OPEN" {
		return nil, conflict("只有受理中的期次可以封盘")
	}
	if _, e = tx.Exec("UPDATE rounds SET state='CLOSED',revision=revision+1 WHERE id=?", id); e != nil {
		return nil, e
	}
	r, e = getRound(tx, id)
	if e != nil {
		return nil, e
	}
	if e = s.queueTemplate(tx, "round-close:"+r.ID, s.Config.GroupID, "round_close", map[string]string{"当前期数": r.Number + "期"}, false); e != nil {
		return nil, e
	}
	if e = s.queueGroupMute(tx, r); e != nil {
		return nil, e
	}
	return r, nil
}
func (s *Service) PlaceBet(tx *sqlite.Tx, in BetInput) (Bet, error) {
	empty := Bet{}
	r, e := getRound(tx, in.RoundID)
	if e != nil {
		return empty, e
	}
	if r.State != "OPEN" {
		return empty, conflict("本期尚未开始或已经封盘")
	}
	if in.Position < 1 || in.Position > 5 || in.Position == r.Banker {
		return empty, bad("只能选择本期非庄家的四个英雄位")
	}
	if in.Stake < r.Rules.MinStake || in.Stake > r.Rules.MaxStake {
		return empty, bad(fmt.Sprintf("单笔下注范围为%d至%d积分", r.Rules.MinStake, r.Rules.MaxStake))
	}
	a, e := getAccount(tx, in.AccountID)
	if e != nil {
		return empty, e
	}
	if a.Role != "player" || !a.Enabled {
		return empty, forbidden("您还没有加入战斗，或账户已停用")
	}
	sums, e := tx.One("SELECT COALESCE(SUM(stake),0) AS stakes,COALESCE(SUM(fee),0) AS fees FROM bets WHERE round_id=? AND account_id=?", r.ID, a.ID)
	if e != nil {
		return empty, e
	}
	riskBalance := a.Balance
	if !s.PlayerOnly && r.Rules.FeeTiming == "acceptance" {
		riskBalance += moneyRow(sums, "fees")
	}
	if moneyRow(sums, "stakes")+in.Stake > riskBalance/4 {
		return empty, bad("本期累计下注不能超过账面余额的四分之一；不会把冻结后可用额重复除以四")
	}
	count, e := tx.One("SELECT COUNT(*) AS n FROM bets WHERE round_id=?", r.ID)
	if e != nil {
		return empty, e
	}
	if count.Int("n") >= MaxBetsPerRound {
		return empty, bad("本期已达到10000笔安全上限")
	}
	charge := Money(0)
	if !s.PlayerOnly {
		charge = fee(in.Stake)
	}
	if !s.PlayerOnly {
		charge = fee(in.Stake)
	}
	if a.Available < riskFor(in.Stake, r.Rules.MaxLossMultiplier()) {
		return empty, bad("可用积分不足以覆盖本期可能的亏损")
	}
	if !s.PlayerOnly {
		house, e := getAccount(tx, "house")
		if e != nil {
			return empty, e
		}
		exposure := exposureFor(in.Stake, r.Rules.MaxPayout())
		if house.Available < exposure {
			return empty, bad("运营方可用承付积分不足，本笔未受理，也不会收费")
		}
	}
	b := Bet{ID: newID(), RoundID: r.ID, AccountID: a.ID, Position: in.Position, Stake: in.Stake, Fee: charge, State: "RESERVED", CreatedAt: now()}
	if _, e = tx.Exec("INSERT INTO bets(id,round_id,account_id,position,stake,fee,state,created_at) VALUES(?,?,?,?,?,?,'RESERVED',?)", b.ID, r.ID, a.ID, in.Position, in.Stake, charge, b.CreatedAt); e != nil {
		return empty, e
	}
	reserve := riskFor(in.Stake, r.Rules.MaxLossMultiplier())
	if !s.PlayerOnly && r.Rules.FeeTiming == "settlement" {
		reserve += charge
	} else if !s.PlayerOnly {
		if e = transfer(tx, a.ID, r.Rules.FeeRecipient, charge, "FEE", "fee:"+b.ID, r.ID, b.ID, "逐笔受理费用"); e != nil {
			return empty, e
		}
		// Preserve potential refund money until settlement; do not let an operator withdraw it.
		if r.Rules.VoidFee == "refund" {
			if e = lock(tx, r.Rules.FeeRecipient, charge); e != nil {
				return empty, e
			}
		}
	}
	if e = lock(tx, a.ID, reserve); e != nil {
		return empty, e
	}
	if !s.PlayerOnly {
		if e = lock(tx, "house", exposureFor(in.Stake, r.Rules.MaxPayout())); e != nil {
			return empty, e
		}
	}
	if _, e = tx.Exec("UPDATE rounds SET revision=revision+1 WHERE id=?", r.ID); e != nil {
		return empty, e
	}
	if s.Config.GroupID != 0 {
		if e = s.queueTemplate(tx, "bet:"+b.ID, s.Config.GroupID, "bet_group", map[string]string{"当前期数": r.Number + "期", "玩家ID": fmt.Sprint(a.TelegramID), "英雄位置": fmt.Sprint(b.Position), "英雄名称": r.Heroes[b.Position-1].Name, "下注金额": fmt.Sprintf("%d", b.Stake), "注单编号": b.ID}, false); e != nil {
			return empty, e
		}
	}
	return b, nil
}

func makePreview(tx *sqlite.Tx, r Round, in SettleInput) (Preview, error) {
	p := Preview{RoundID: r.ID, Number: r.Number, Revision: r.Revision, DurationSeconds: in.DurationSeconds, Damages: in.Damages, Positions: []PositionResult{}, Lines: []Line{}}
	if r.State != "CLOSED" {
		return p, conflict("必须先封盘，再预览或结算")
	}
	// DurationSeconds is retained for compatibility with older API clients. New
	// manual settlement requests omit it; settlement is based on damage only.
	if in.DurationSeconds < 0 || in.DurationSeconds > 86400 {
		return p, bad("实际对局时长无效")
	}
	if len(in.Damages) != 5 {
		return p, bad("必须按1至5号顺序提交五个原始伤害")
	}
	if in.DurationSeconds > 0 && in.DurationSeconds <= 300 {
		p.WholeVoid = true
		p.Reason = "对局时长不超过5分钟，整期流局"
	}
	if in.CancelReason != "" {
		p.WholeVoid = true
		p.Reason = "管理员取消本期并退款：" + in.CancelReason
	}
	hands := make([]bull.Hand, 5)
	for i, raw := range in.Damages {
		h, e := bull.Evaluate(raw, r.Rules.ZeroTriple)
		if e != nil && !p.WholeVoid {
			return p, bad(fmt.Sprintf("%d号英雄：%v", i+1, e))
		}
		if e != nil {
			h = bull.Hand{Raw: raw, Void: true, Rank: -1, Label: "流局", Explanation: p.Reason}
		}
		hands[i] = h
	}
	if hands[r.Banker-1].Void {
		p.WholeVoid = true
		if p.Reason == "" {
			p.Reason = "庄家英雄流局，整期无法比较，整期流局"
		}
	}
	for i, h := range hands {
		c := bull.Compare(h, hands[r.Banker-1])
		if p.WholeVoid {
			c = bull.Comparison{Outcome: "VOID", Reason: p.Reason}
		}
		if i+1 == r.Banker && !p.WholeVoid {
			c = bull.Comparison{Outcome: "BANKER", Reason: "本期庄家英雄"}
		}
		p.Positions = append(p.Positions, PositionResult{Position: i + 1, Hero: r.Heroes[i], Banker: i+1 == r.Banker, Hand: h, Outcome: c.Outcome, Reason: c.Reason})
	}
	bets, e := betsFor(tx, r.ID)
	if e != nil {
		return p, e
	}
	for _, b := range bets {
		if b.State != "RESERVED" {
			return p, conflict("期次存在非待结算注单，禁止重复记账")
		}
		pos := p.Positions[b.Position-1]
		line := Line{BetID: b.ID, AccountID: b.AccountID, Position: b.Position, Stake: b.Stake, Fee: b.Fee, Outcome: pos.Outcome}
		if sPlayerOnly(tx) {
			line.Fee = 0
		}
		switch pos.Outcome {
		case "WIN":
			line.Multiplier = r.Rules.Payout[pos.Hand.Rank]
			line.GameDelta = profitFor(b.Stake, line.Multiplier)
		case "LOSS":
			line.Multiplier = r.Rules.LossMultiplierFor(hands[r.Banker-1].Rank)
			line.GameDelta = -lossFor(b.Stake, line.Multiplier)
		case "VOID":
			if in.CancelReason != "" || r.Rules.VoidFee == "refund" || line.Fee != b.Fee {
				line.Fee = 0
			}
		default:
			return p, bad("注单位置不合法")
		}
		line.NetDelta = line.GameDelta - line.Fee
		p.PlayerGameDelta += line.GameDelta
		p.FeeTotal += line.Fee
		p.Lines = append(p.Lines, line)
	}
	p.HouseGameDelta = -p.PlayerGameDelta
	p.Token = fingerprint(struct {
		Result Preview
		Rules  Rules
	}{p, r.Rules})
	return p, nil
}

// sPlayerOnly is encoded in the round snapshot for previews created by the
// player-only service; legacy snapshots continue to use the old policy.
func sPlayerOnly(tx *sqlite.Tx) bool {
	row, _ := tx.One("SELECT value FROM meta WHERE key='balance_mode'")
	return row != nil && row["value"] == "player_only"
}
func (s *Service) Preview(id string, in SettleInput) (Preview, error) {
	var out Preview
	e := s.DB.Read(func(tx *sqlite.Tx) error {
		r, e := getRound(tx, id)
		if e != nil {
			return e
		}
		out, e = makePreview(tx, r, in)
		return e
	})
	return out, e
}
func nextNumber(number string) string {
	parts := strings.Split(number, "-")
	n, _ := strconv.ParseInt(parts[3], 10, 64)
	return strings.Join(parts[:3], "-") + fmt.Sprintf("-%04d", n+1)
}
func (s *Service) CancelRound(tx *sqlite.Tx, id, reason string) (any, error) {
	reason = strings.TrimSpace(reason)
	if len([]rune(reason)) < 2 || len([]rune(reason)) > 200 {
		return nil, bad("请填写2至200字的退款原因")
	}
	r, e := getRound(tx, id)
	if e != nil {
		return nil, e
	}
	if r.State != "OPEN" && r.State != "CLOSED" {
		return nil, conflict("只能取消受理中或已封盘的未结算期次，已结算期次不可退款")
	}
	if r.State == "OPEN" {
		if _, e = tx.Exec("UPDATE rounds SET state='CLOSED',revision=revision+1 WHERE id=?", id); e != nil {
			return nil, e
		}
		r, e = getRound(tx, id)
		if e != nil {
			return nil, e
		}
	}
	in := SettleInput{CancelReason: reason, Damages: []string{"", "", "", "", ""}}
	p, e := makePreview(tx, r, in)
	if e != nil {
		return nil, e
	}
	in.PreviewToken = p.Token
	if e = s.queueTemplate(tx, "cancel:"+id, s.Config.GroupID, "round_cancel", map[string]string{"当前期数": r.Number + "期", "取消原因": reason}, false); e != nil {
		return nil, e
	}
	return s.Settle(tx, id, in)
}

func (s *Service) Settle(tx *sqlite.Tx, id string, in SettleInput) (any, error) {
	r, e := getRound(tx, id)
	if e != nil {
		return nil, e
	}
	if r.State == "SETTLED" || r.State == "VOID" {
		if r.Result != nil && r.Result.Token == in.PreviewToken && r.Result.DurationSeconds == in.DurationSeconds && asJSON(r.Result.Damages) == asJSON(in.Damages) {
			return r.Result, nil
		}
		return nil, conflict("该期已经结算，结果不可覆盖；错误应另走人工审计冲正")
	}
	p, e := makePreview(tx, r, in)
	if e != nil {
		return nil, e
	}
	if in.PreviewToken == "" || in.PreviewToken != p.Token {
		return nil, conflict("预览已失效或内容改变，请重新预览后确认")
	}
	bets, e := betsFor(tx, r.ID)
	if e != nil {
		return nil, e
	}
	// Release reservations, apply transfers, save results and enqueue notices in ONE transaction.
	for _, b := range bets {
		reserve := riskFor(b.Stake, r.Rules.MaxLossMultiplier())
		if !s.PlayerOnly && r.Rules.FeeTiming == "settlement" {
			reserve += b.Fee
		}
		if e = lock(tx, b.AccountID, -reserve); e != nil {
			return nil, e
		}
		if !s.PlayerOnly {
			if e = lock(tx, "house", -exposureFor(b.Stake, r.Rules.MaxPayout())); e != nil {
				return nil, e
			}
		}
		if r.Rules.FeeTiming == "acceptance" && r.Rules.VoidFee == "refund" {
			if e = lock(tx, r.Rules.FeeRecipient, -b.Fee); e != nil {
				return nil, e
			}
		}
	}
	for i, b := range bets {
		l := p.Lines[i]
		if l.GameDelta > 0 && !s.PlayerOnly {
			e = transfer(tx, "house", b.AccountID, l.GameDelta, "GAME", "game:"+b.ID, r.ID, b.ID, "闲家按自身牛型净盈利")
		}
		if l.GameDelta < 0 && !s.PlayerOnly {
			e = transfer(tx, b.AccountID, "house", -l.GameDelta, "GAME", "game:"+b.ID, r.ID, b.ID, "闲家按庄家牛型失败净亏损倍数扣减")
		}
		if s.PlayerOnly {
			e = playerGameDelta(tx, b.AccountID, l.GameDelta, "game:"+b.ID, r.ID, b.ID)
		}
		if e != nil {
			return nil, e
		}
		if !s.PlayerOnly && r.Rules.FeeTiming == "settlement" && l.Fee > 0 {
			e = transfer(tx, b.AccountID, r.Rules.FeeRecipient, l.Fee, "FEE", "fee:"+b.ID, r.ID, b.ID, "结算收取逐笔费用")
		}
		if !s.PlayerOnly && r.Rules.FeeTiming == "acceptance" && l.Fee == 0 {
			e = transfer(tx, r.Rules.FeeRecipient, b.AccountID, b.Fee, "FEE_REFUND", "fee-refund:"+b.ID, r.ID, b.ID, "流局退回本笔已收费用")
		}
		if e != nil {
			return nil, e
		}
		if _, e = tx.Exec("UPDATE bets SET state=?,game_delta=?,net_delta=?,settled_at=? WHERE id=?", l.Outcome, l.GameDelta, l.NetDelta, now(), b.ID); e != nil {
			return nil, e
		}
	}
	state := "SETTLED"
	if p.WholeVoid {
		state = "VOID"
	}
	if _, e = tx.Exec("UPDATE rounds SET state=?,settled_at=?,revision=revision+1 WHERE id=?", state, now(), r.ID); e != nil {
		return nil, e
	}
	number := nextNumber(r.Number)
	// Custom numbers may collide with old periods. Skip occupied numbers, never overwrite.
	for n := 0; ; n++ {
		if n > 1000 || !validNumber(number) {
			return nil, conflict("下一期序号无法安全生成，请先检查历史期号")
		}
		row, e := tx.One("SELECT id FROM rounds WHERE number=?", number)
		if e != nil {
			return nil, e
		}
		if row == nil {
			break
		}
		number = nextNumber(number)
	}
	next, e := s.createDraft(tx, ConfigureRound{Number: number})
	if e != nil {
		return nil, e
	}
	p.NextRoundID = next.ID
	if _, e = tx.Exec("UPDATE rounds SET result=?,next_round_id=? WHERE id=?", asJSON(p), next.ID, r.ID); e != nil {
		return nil, e
	}
	r, e = getRound(tx, r.ID)
	if e != nil {
		return nil, e
	}
	if e = s.queueRoundResult(tx, r); e != nil {
		return nil, e
	}
	if e = s.queueGroupRestore(tx, r); e != nil {
		return nil, e
	}
	if s.Config.GroupID != 0 {
		if e = s.queueWinners(tx, r); e != nil {
			return nil, e
		}
		if e = s.queueTemplate(tx, "next:"+r.ID, s.Config.GroupID, "round_next", map[string]string{"下一期数": number + "期"}, true); e != nil {
			return nil, e
		}
	}
	// One personal settlement summary per participant, not one message per bet.
	totals := map[string][3]Money{}
	for _, l := range p.Lines {
		t := totals[l.AccountID]
		t[0] += l.GameDelta
		t[1] += l.Fee
		t[2]++
		totals[l.AccountID] = t
	}
	for id, t := range totals {
		a, e := getAccount(tx, id)
		if e != nil {
			return nil, e
		}
		if a.TelegramID == 0 {
			continue
		}
		if e = s.queueTemplate(tx, "settle:"+r.ID+":"+id, a.TelegramID, "settle_private", map[string]string{"当前期数": r.Number + "期", "注单数量": fmt.Sprint(int64(t[2])), "游戏盈亏": fmt.Sprintf("%+d", t[0]), "费用": fmt.Sprintf("%d", t[1]), "净盈亏": fmt.Sprintf("%+d", t[0]-t[1]), "当前余额": fmt.Sprintf("%d", a.Balance), "可用余额": fmt.Sprintf("%d", a.Available)}, false); e != nil {
			return nil, e
		}
	}
	return p, nil
}

// TodayNumber is just a suggested editable number, never a hidden banker/hero default.
func TodayNumber() string {
	return time.Now().In(time.FixedZone("CST", 8*3600)).Format("2006-01-02") + "-0001"
}

func (s *Service) canonicalHeroes(in *ConfigureRound) error {
	if s.Champions == nil {
		return nil
	}
	for i, h := range in.Heroes {
		c, ok := s.Champions.Lookup(h.ID)
		if !ok {
			return bad("请先刷新英雄数据，并从列表选择有效英雄")
		}
		in.Heroes[i] = Hero{ID: c.ID, Name: c.Name}
	}
	return nil
}
