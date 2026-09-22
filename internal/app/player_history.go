package app

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"riftledger/internal/sqlite"
)

type PlayerHistoryQuery struct {
	AccountID, View, Filter, From, To, Cursor string
	Limit                                     int
}

type historyCursor struct {
	At int64  `json:"at"`
	ID string `json:"id"`
}

type PlayerHistoryPage struct {
	Account Account      `json:"account"`
	Rows    []sqlite.Row `json:"rows"`
	Next    string       `json:"next_cursor"`
}

// Search is deliberately bounded. User input is data, including LIKE wildcards.
func (s *Service) SearchPlayers(query string) (any, error) {
	query = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(query), "@"))
	if len([]rune(query)) > 80 {
		return nil, bad("玩家搜索最多80个字符")
	}
	pattern := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(query) + "%"
	rows, e := s.DB.Query(`SELECT a.*,COALESCE(u.username,'') AS username FROM accounts a LEFT JOIN telegram_users u ON u.telegram_user_id=a.telegram_id
		WHERE a.role='player' AND (?='' OR a.id=? OR CAST(a.telegram_id AS TEXT)=? OR a.name LIKE ? ESCAPE '\' OR u.username LIKE ? ESCAPE '\')
		ORDER BY a.created_at DESC,a.id LIMIT 21`, query, query, query, pattern, pattern)
	if e != nil {
		return nil, e
	}
	more := len(rows) > 20
	if more {
		rows = rows[:20]
	}
	players := []Account{}
	for _, row := range rows {
		players = append(players, account(row))
	}
	return map[string]any{"rows": players, "has_more": more}, nil
}

func historyDates(from, to string) (int64, int64, error) {
	zone := time.FixedZone("Asia/Shanghai", 8*3600)
	parse := func(v string) (time.Time, error) { return time.ParseInLocation("2006-01-02", v, zone) }
	var start, end int64
	if from != "" {
		t, e := parse(from)
		if e != nil || t.Year() < 1970 {
			return 0, 0, bad("开始日期无效")
		}
		start = t.Unix()
	}
	if to != "" {
		t, e := parse(to)
		if e != nil || t.Year() < 1970 {
			return 0, 0, bad("结束日期无效")
		}
		end = t.AddDate(0, 0, 1).Unix()
	}
	if from != "" && to != "" && start >= end {
		return 0, 0, bad("开始日期不能晚于结束日期")
	}
	return start, end, nil
}

func (s *Service) PlayerHistory(q PlayerHistoryQuery) (PlayerHistoryPage, error) {
	out := PlayerHistoryPage{Rows: []sqlite.Row{}}
	if q.AccountID == "" || len(q.AccountID) > 100 {
		return out, bad("请选择玩家")
	}
	if q.Limit < 1 || q.Limit > 100 {
		return out, bad("每页条数必须为1至100")
	}
	from, to, e := historyDates(q.From, q.To)
	if e != nil {
		return out, e
	}
	var selectSQL, filter string
	switch q.View {
	case "bets":
		// Outcomes and deltas are persisted at settlement; never recompute using
		// today's odds. Do not load the round's potentially huge result.lines JSON.
		selectSQL = `SELECT x.*,r.number AS round_number,r.state AS round_state,COALESCE(json_extract(r.heroes,'$['||(x.position-1)||'].name'),'') AS hero FROM bets x JOIN rounds r ON r.id=x.round_id`
		if q.Filter != "" {
			switch q.Filter {
			case "WIN", "LOSS", "VOID", "RESERVED":
				filter = " AND x.state=?"
			default:
				return out, bad("下注结果筛选无效")
			}
		}
	case "entries":
		selectSQL = `SELECT x.*,COALESCE(r.number,'') AS round_number,CASE WHEN x.batch GLOB 'adjust:balance-request:*' THEN 'REQUEST' ELSE 'MANUAL' END AS adjustment_source FROM entries x LEFT JOIN rounds r ON r.id=x.round_id`
		switch q.Filter {
		case "":
		case "CREDIT":
			filter = " AND x.kind='ADJUST' AND x.delta>0"
		case "DEBIT":
			filter = " AND x.kind='ADJUST' AND x.delta<0"
		case "GAME":
			filter = " AND x.kind='GAME'"
		default:
			return out, bad("流水类型筛选无效")
		}
	case "requests":
		selectSQL = "SELECT x.* FROM balance_requests x"
		if q.Filter != "" {
			switch q.Filter {
			case "PENDING", "APPROVED", "REJECTED":
				filter = " AND x.state=?"
			default:
				return out, bad("申请状态筛选无效")
			}
		}
	default:
		return out, bad("历史查询类型无效")
	}
	where := " WHERE x.account_id=?"
	args := []any{q.AccountID}
	if q.From != "" {
		where += " AND x.created_at>=?"
		args = append(args, from)
	}
	if q.To != "" {
		where += " AND x.created_at<?"
		args = append(args, to)
	}
	where += filter
	if filter == " AND x.state=?" {
		args = append(args, q.Filter)
	}
	if q.Cursor != "" {
		var cursor historyCursor
		raw, err := base64.RawURLEncoding.DecodeString(q.Cursor)
		if len(q.Cursor) > 256 || err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.At < 0 || cursor.ID == "" || len(cursor.ID) > 64 {
			return out, bad("分页游标无效，请重新查询")
		}
		var id any = cursor.ID
		if q.View != "bets" {
			v, err := strconv.ParseInt(cursor.ID, 10, 64)
			if err != nil || v < 1 {
				return out, bad("分页游标无效，请重新查询")
			}
			id = v
		}
		where += " AND (x.created_at,x.id)<(?,?)"
		args = append(args, cursor.At, id)
	}
	args = append(args, q.Limit+1)
	db := s.AuditDB
	if db == nil {
		db = s.DB
	}
	e = db.Snapshot(func(tx *sqlite.Tx) error {
		row, err := tx.One(`SELECT a.*,COALESCE(u.username,'') AS username FROM accounts a LEFT JOIN telegram_users u ON u.telegram_user_id=a.telegram_id WHERE a.id=? AND a.role='player'`, q.AccountID)
		if err != nil {
			return err
		}
		if row == nil {
			return notfound("玩家不存在")
		}
		out.Account = account(row)
		rows, err := tx.Query(selectSQL+where+" ORDER BY x.created_at DESC,x.id DESC LIMIT ?", args...)
		if err != nil {
			return err
		}
		if len(rows) > q.Limit {
			rows = rows[:q.Limit]
			last := rows[len(rows)-1]
			cursor, _ := json.Marshal(historyCursor{At: last.Int("created_at"), ID: last["id"]})
			out.Next = base64.RawURLEncoding.EncodeToString(cursor)
		}
		for _, row := range rows {
			displayMoneyRow(row, "stake", "fee", "game_delta", "net_delta", "delta", "balance_after", "amount", "balance_at_request")
		}
		out.Rows = rows
		return nil
	})
	return out, e
}
