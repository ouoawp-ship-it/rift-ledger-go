package app

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"riftledger/internal/champion"
	"riftledger/internal/runtimeconfig"
	"riftledger/internal/sqlite"
)

type Service struct {
	DB         *sqlite.DB
	Config     RuntimeConfig
	Settings   *runtimeconfig.Store
	Champions  *champion.ChampionService
	PlayerOnly bool
}

// EnablePlayerOnly removes empty legacy operator/fee accounts and switches
// settlement to player-only accounting. Non-empty legacy accounts are refused
// rather than silently deleting funds or audit history.
func (s *Service) EnablePlayerOnly() error {
	e := s.DB.Transaction(func(tx *sqlite.Tx) error {
		rows, e := tx.Query("SELECT id,balance,locked FROM accounts WHERE id IN ('house','fees')")
		if e != nil {
			return e
		}
		for _, r := range rows {
			if r.Int("balance") != 0 || r.Int("locked") != 0 {
				return conflict("旧运营方或费用账户仍有余额，请先完成迁移后再启用玩家模式")
			}
		}
		refs, e := tx.One("SELECT COUNT(*) AS n FROM entries WHERE account_id IN ('house','fees')")
		if e != nil {
			return e
		}
		if refs.Int("n") > 0 {
			return conflict("旧运营方或费用账户已有历史流水，不能无损删除")
		}
		_, e = tx.Exec("DELETE FROM accounts WHERE id IN ('house','fees')")
		if e == nil {
			_, e = tx.Exec("INSERT INTO meta(key,value) VALUES('balance_mode','player_only') ON CONFLICT(key) DO UPDATE SET value='player_only'")
			if e == nil {
				r, v, ge := getRules(tx)
				if ge != nil {
					return ge
				}
				r.Confirmed = true
				_, e = tx.Exec("UPDATE settings SET version=?,rules=? WHERE id=1", v, asJSON(r))
			}
		}
		return e
	})
	if e != nil {
		return e
	}
	s.PlayerOnly = true
	return nil
}

func New(db *sqlite.DB, c RuntimeConfig) (*Service, error) {
	if e := initialize(db); e != nil {
		return nil, e
	}
	return &Service{DB: db, Config: c}, nil
}
func now() int64 { return time.Now().UTC().Unix() }
func newID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func fingerprint(v any) string {
	s := sha256.Sum256([]byte(asJSON(v)))
	return hex.EncodeToString(s[:])
}

// Command atomically records a successful mutation and its response. Repeating
// the same key and payload replays the response, not the mutation. Keys reused
// for different commands are rejected. Business validation is always in core.
func (s *Service) Command(key, action string, payload any, fn func(*sqlite.Tx) (any, error)) (json.RawMessage, error) {
	if len(key) < 8 || len(key) > 160 {
		return nil, bad("写操作需要8至160字符的Idempotency-Key业务号")
	}
	hash := fingerprint(struct {
		Action  string
		Payload any
	}{action, payload})
	var result json.RawMessage
	err := s.DB.Transaction(func(tx *sqlite.Tx) error {
		old, e := tx.One("SELECT fingerprint,response FROM idempotency WHERE key=?", key)
		if e != nil {
			return e
		}
		if old != nil {
			if old["fingerprint"] != hash {
				return conflict("此业务号已用于不同请求，请勿修改重试内容")
			}
			result = []byte(old["response"])
			return nil
		}
		obj, e := fn(tx)
		if e != nil {
			return e
		}
		result = []byte(asJSON(obj))
		if _, e = tx.Exec("INSERT INTO idempotency(key,fingerprint,response,created_at) VALUES(?,?,?,?)", key, hash, string(result), now()); e != nil {
			return e
		}
		_, e = tx.Exec("INSERT INTO audit(actor,action,request_key,detail,created_at) VALUES('admin-api',?,?,?,?)", action, key, asJSON(payload), now())
		return e
	})
	return result, err
}
func getRules(tx *sqlite.Tx) (Rules, int64, error) {
	row, e := tx.One("SELECT * FROM settings WHERE id=1")
	if e != nil {
		return Rules{}, 0, e
	}
	var r Rules
	if e = json.Unmarshal([]byte(row["rules"]), &r); e != nil {
		return r, 0, e
	}
	return r, row.Int("version"), nil
}
func (s *Service) SaveRules(tx *sqlite.Tx, expected int64, r Rules) (any, error) {
	if e := r.Validate(); e != nil {
		return nil, e
	}
	_, v, e := getRules(tx)
	if e != nil {
		return nil, e
	}
	if v != expected {
		return nil, conflict("配置已更新，请刷新后再保存")
	}
	if _, e = tx.Exec("UPDATE settings SET version=version+1,rules=? WHERE id=1", asJSON(r)); e != nil {
		return nil, e
	}
	return map[string]any{"version": v + 1, "rules": r}, nil
}
func account(row sqlite.Row) Account {
	return Account{Username: row["username"], FirstName: row["first_name"], LastName: row["last_name"], FirstContact: row.Int("first_contact"), LastContact: row.Int("last_contact"), ID: row["id"], Name: row["name"], Role: row["role"], TelegramID: row.Int("telegram_id"), Enabled: row.Int("enabled") == 1, Balance: row.Int("balance"), Locked: row.Int("locked"), Available: row.Int("balance") - row.Int("locked"), CreatedAt: row.Int("created_at")}
}
func getAccount(tx *sqlite.Tx, id string) (Account, error) {
	row, e := tx.One("SELECT * FROM accounts WHERE id=?", id)
	if e != nil {
		return Account{}, e
	}
	if row == nil {
		return Account{}, notfound("账户不存在")
	}
	return account(row), nil
}
func roundFrom(row sqlite.Row) (Round, error) {
	r := Round{ID: row["id"], Number: row["number"], State: row["state"], Banker: int(row.Int("banker")), RulesVersion: row.Int("rules_version"), Revision: row.Int("revision"), CreatedAt: row.Int("created_at"), OpenedAt: row.Int("opened_at"), SettledAt: row.Int("settled_at"), NextRoundID: row["next_round_id"]}
	if e := json.Unmarshal([]byte(row["heroes"]), &r.Heroes); e != nil {
		return r, e
	}
	if e := json.Unmarshal([]byte(row["rules"]), &r.Rules); e != nil {
		return r, e
	}
	if row["result"] != "" {
		var p Preview
		if e := json.Unmarshal([]byte(row["result"]), &p); e != nil {
			return r, e
		}
		r.Result = &p
	}
	return r, nil
}
func getRound(tx *sqlite.Tx, id string) (Round, error) {
	row, e := tx.One("SELECT * FROM rounds WHERE id=?", id)
	if e != nil {
		return Round{}, e
	}
	if row == nil {
		return Round{}, notfound("期次不存在")
	}
	return roundFrom(row)
}
func activeRound(tx *sqlite.Tx) (*Round, error) {
	row, e := tx.One("SELECT * FROM rounds WHERE state IN ('DRAFT','OPEN','CLOSED') LIMIT 1")
	if e != nil || row == nil {
		return nil, e
	}
	r, e := roundFrom(row)
	return &r, e
}
func betsFor(tx *sqlite.Tx, roundID string) ([]Bet, error) {
	rows, e := tx.Query("SELECT * FROM bets WHERE round_id=? ORDER BY created_at,id", roundID)
	if e != nil {
		return nil, e
	}
	out := []Bet{}
	for _, row := range rows {
		out = append(out, betFrom(row))
	}
	return out, nil
}
func betFrom(row sqlite.Row) Bet {
	return Bet{ID: row["id"], RoundID: row["round_id"], AccountID: row["account_id"], Position: int(row.Int("position")), Stake: row.Int("stake"), Fee: row.Int("fee"), State: row["state"], GameDelta: row.Int("game_delta"), NetDelta: row.Int("net_delta"), CreatedAt: row.Int("created_at"), SettledAt: row.Int("settled_at")}
}

func (s *Service) SetPlayer(tx *sqlite.Tx, tgid int64, name string, enabled bool) (any, error) {
	name = strings.TrimSpace(name)
	if tgid <= 0 || tgid > 4_503_599_627_370_495 {
		return nil, bad("请输入有效的Telegram用户ID")
	}
	if name == "" || len([]rune(name)) > 60 {
		return nil, bad("玩家称呼需要1至60个字符")
	}
	id := "tg:" + strconv.FormatInt(tgid, 10)
	if _, e := tx.Exec(`INSERT INTO accounts(id,name,role,telegram_id,enabled,created_at) VALUES(?,?,'player',?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,enabled=excluded.enabled`, id, name, tgid, enabled, now()); e != nil {
		return nil, e
	}
	return getAccount(tx, id)
}

// transfer creates a balanced, immutable pair. Every balance change must pass
// here, including administrator adjustments; external is a clearing account,
// NOT spendable house bankroll. Frozen balances cannot be spent.
func transfer(tx *sqlite.Tx, from, to string, amount int64, kind, batch, roundID, betID, note string) error {
	if amount == 0 {
		return nil
	}
	if amount < 0 || amount > MoneyLimit {
		return bad("调账金额超出安全范围")
	}
	if from == to {
		return bad("不能向同一账户转账")
	}
	a, e := getAccount(tx, from)
	if e != nil {
		return e
	}
	b, e := getAccount(tx, to)
	if e != nil {
		return e
	}
	if a.Role != "external" && a.Available < amount {
		return conflict("账户可用积分不足：" + a.Name)
	}
	if a.Balance-amount < -MoneyLimit || b.Balance+amount > MoneyLimit {
		return bad("账户余额超出安全范围")
	}
	for _, v := range []struct {
		a     Account
		delta int64
	}{{a, -amount}, {b, amount}} {
		if _, e = tx.Exec("UPDATE accounts SET balance=balance+? WHERE id=?", v.delta, v.a.ID); e != nil {
			return e
		}
		if _, e = tx.Exec("INSERT INTO entries(batch,account_id,round_id,bet_id,kind,delta,balance_after,note,created_at) VALUES(?,?,?,?,?,?,?,?,?)", batch, v.a.ID, roundID, betID, kind, v.delta, v.a.Balance+v.delta, note, now()); e != nil {
			return e
		}
	}
	return nil
}
func lock(tx *sqlite.Tx, id string, delta int64) error {
	a, e := getAccount(tx, id)
	if e != nil {
		return e
	}
	if a.Locked+delta < 0 || a.Locked+delta > a.Balance {
		return conflict("冻结额度不足或超过余额：" + a.Name)
	}
	_, e = tx.Exec("UPDATE accounts SET locked=locked+? WHERE id=?", delta, id)
	return e
}
func (s *Service) Adjust(tx *sqlite.Tx, id string, delta int64, note, operationID string) (any, error) {
	note = strings.TrimSpace(note)
	if id == "external" {
		return nil, forbidden("对手账户不能手动操作")
	}
	if note == "" || len([]rune(note)) > 200 {
		return nil, bad("调分必须填写1至200字备注")
	}
	if delta == 0 || delta > MoneyLimit || delta < -MoneyLimit {
		return nil, bad("调分金额必须是非零安全整数")
	}
	a, e := getAccount(tx, id)
	if e != nil {
		return nil, e
	}
	if s.PlayerOnly {
		if a.Role != "player" {
			return nil, forbidden("玩家模式只能调整玩家账户")
		}
		if delta < 0 && a.Balance+delta < 0 {
			return nil, conflict("玩家余额不足")
		}
		if _, e = tx.Exec("UPDATE accounts SET balance=balance+? WHERE id=?", delta, id); e != nil {
			return nil, e
		}
		if _, e = tx.Exec("INSERT INTO entries(batch,account_id,kind,delta,balance_after,note,created_at) VALUES(?,?,?,?,?,?,?)", "adjust:"+operationID, id, "ADJUST", delta, a.Balance+delta, note, now()); e != nil {
			return nil, e
		}
		return getAccount(tx, id)
	}
	if delta < 0 && a.Role == "player" {
		row, e := tx.One(`SELECT COALESCE(SUM(stake),0) AS stakes,COALESCE(SUM(CASE WHEN json_extract(r.rules,'$.fee_timing')='acceptance' THEN b.fee ELSE 0 END),0) AS paid FROM bets b JOIN rounds r ON b.round_id=r.id WHERE b.account_id=? AND b.state='RESERVED'`, id)
		if e != nil {
			return nil, e
		}
		if row.Int("stakes") > (a.Balance+delta+row.Int("paid"))/4 {
			return nil, conflict("减分后会违反本期累计下注不超过余额四分之一的限制")
		}
	}
	from, to, amount := "external", id, delta
	if delta < 0 {
		from, to, amount = id, "external", -delta
	}
	if e = transfer(tx, from, to, amount, "ADJUST", "adjust:"+operationID, "", "", note); e != nil {
		return nil, e
	}
	return getAccount(tx, id)
}

func (s *Service) State() (any, error) {
	var out any
	e := s.DB.Read(func(tx *sqlite.Tx) error {
		r, v, e := getRules(tx)
		if e != nil {
			return e
		}
		active, e := activeRound(tx)
		if e != nil {
			return e
		}
		ac, e := tx.Query("SELECT * FROM accounts WHERE role!='external' ORDER BY role,id LIMIT 200")
		if e != nil {
			return e
		}
		accounts := []Account{}
		for _, row := range ac {
			accounts = append(accounts, account(row))
		}
		bs := []Bet{}
		if active != nil {
			all, e := betsFor(tx, active.ID)
			if e != nil {
				return e
			}
			if len(all) > 200 {
				all = all[len(all)-200:]
			}
			bs = all
		}
		count, e := tx.One("SELECT COUNT(*) AS n FROM outbox WHERE state!='SENT'")
		if e != nil {
			return e
		}
		botRow, err := tx.One("SELECT value FROM meta WHERE key='bot_status'")
		if err != nil {
			return err
		}
		botStatus := "未启用Telegram"
		if botRow != nil {
			botStatus = botRow["value"]
		}
		out = map[string]any{"bot_status": botStatus, "suggested_number": TodayNumber(), "version": Version, "rules_version": v, "rules": r, "active_round": active, "accounts": accounts, "bets": bs, "outbox_unsent": count.Int("n"), "server_time": now(), "sqlite_version": sqlite.Version(), "telegram_configured": s.Config.BotUsername != "", "group_id": s.Config.GroupID, "topic_id": s.Config.TopicID}
		return nil
	})
	return out, e
}
func (s *Service) ReadRound(id string) (Round, error) {
	var out Round
	e := s.DB.Read(func(tx *sqlite.Tx) error { var e error; out, e = getRound(tx, id); return e })
	return out, e
}
func (s *Service) History(limit, offset int) ([]Round, error) {
	var out = []Round{}
	e := s.DB.Read(func(tx *sqlite.Tx) error {
		rows, e := tx.Query("SELECT * FROM rounds ORDER BY created_at DESC,rowid DESC LIMIT ? OFFSET ?", limit, offset)
		if e != nil {
			return e
		}
		for _, row := range rows {
			r, e := roundFrom(row)
			if e != nil {
				return e
			}
			out = append(out, r)
		}
		return nil
	})
	return out, e
}
func (s *Service) Rows(kind, id string, limit, offset int) (any, error) {
	switch kind {
	case "accounts":
		if s.PlayerOnly {
			return s.PlayerSummary(limit, offset)
		}
		rows, e := s.DB.Query("SELECT a.*,u.username,u.first_name,u.last_name,u.first_contact,u.last_contact FROM accounts a LEFT JOIN telegram_users u ON u.telegram_user_id=a.telegram_id WHERE role!='external' ORDER BY created_at DESC,id LIMIT ? OFFSET ?", limit, offset)
		a := []Account{}
		for _, r := range rows {
			a = append(a, account(r))
		}
		return a, e
	case "entries":
		if id != "" {
			return s.DB.Query("SELECT * FROM entries WHERE account_id=? ORDER BY id DESC LIMIT ? OFFSET ?", id, limit, offset)
		}
		return s.DB.Query("SELECT * FROM entries ORDER BY id DESC LIMIT ? OFFSET ?", limit, offset)
	case "audit":
		return s.DB.Query("SELECT * FROM audit ORDER BY id DESC LIMIT ? OFFSET ?", limit, offset)
	case "outbox":
		return s.DB.Query("SELECT * FROM outbox ORDER BY id DESC LIMIT ? OFFSET ?", limit, offset)
	case "bets":
		rows, e := s.DB.Query("SELECT * FROM bets WHERE account_id=? ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?", id, limit, offset)
		out := []Bet{}
		for _, r := range rows {
			out = append(out, betFrom(r))
		}
		return out, e
	}
	return nil, notfound(fmt.Sprintf("未知查询 %s", kind))
}

func (s *Service) PlayerSummary(limit, offset int) (any, error) {
	var out any
	e := s.DB.Read(func(tx *sqlite.Tx) error {
		active, e := activeRound(tx)
		if e != nil {
			return e
		}
		rows, e := tx.Query(`SELECT a.id,a.telegram_id,a.name,a.balance,a.locked,a.enabled,COALESCE(u.username,'') AS username,COALESCE(u.first_name,'') AS first_name,COALESCE(u.last_name,'') AS last_name,COALESCE((SELECT SUM(b.stake) FROM bets b WHERE b.account_id=a.id AND b.round_id=? AND b.state='RESERVED'),0) AS round_stake,COALESCE((SELECT group_concat(CAST(b.position AS TEXT)||':'||json_extract(r.heroes,'$['||(b.position-1)||'].name'),'、') FROM bets b JOIN rounds r ON r.id=b.round_id WHERE b.account_id=a.id AND b.round_id=? AND b.state='RESERVED'),'') AS round_content,COALESCE((SELECT SUM(b.game_delta) FROM bets b JOIN rounds r ON r.id=b.round_id WHERE b.account_id=a.id AND r.state IN ('SETTLED','VOID') AND b.settled_at=(SELECT MAX(b2.settled_at) FROM bets b2 JOIN rounds r2 ON r2.id=b2.round_id WHERE b2.account_id=a.id AND r2.state IN ('SETTLED','VOID'))),0) AS last_profit FROM accounts a LEFT JOIN telegram_users u ON u.telegram_user_id=a.telegram_id WHERE a.role='player' ORDER BY a.created_at DESC LIMIT ? OFFSET ?`, func() string {
			if active != nil {
				return active.ID
			}
			return ""
		}(), func() string {
			if active != nil {
				return active.ID
			}
			return ""
		}(), limit, offset)
		if e != nil {
			return e
		}
		zone := time.FixedZone("CST", 8*3600)
		t := time.Now().In(zone)
		start := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, zone).Unix()
		stats, e := tx.One("SELECT (SELECT COUNT(*) FROM accounts WHERE role='player') AS players,(SELECT COALESCE(SUM(balance),0) FROM accounts WHERE role='player') AS balance,(SELECT COALESCE(-SUM(e.delta),0) FROM entries e JOIN accounts a ON a.id=e.account_id WHERE a.role='player' AND e.kind='GAME' AND e.created_at>=?) AS profit", start)
		if e != nil {
			return e
		}
		out = map[string]any{"rows": rows, "stats": stats}
		return nil
	})
	return out, e
}
