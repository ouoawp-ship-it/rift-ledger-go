package httpapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"riftledger/internal/app"
	"riftledger/internal/runtimeconfig"
	"riftledger/internal/sqlite"
	"riftledger/internal/telegram"
	"riftledger/pkg/bull"
)

//go:embed web/*
var assets embed.FS

type API struct {
	Service   *app.Service
	tokenHash [32]byte
}

func New(s *app.Service, token string) http.Handler {
	a := &API{s, sha256.Sum256([]byte(token))}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		_, e := s.DB.Query("SELECT 1")
		if e != nil {
			http.Error(w, "unhealthy", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.Handle("/api/", a.auth(http.HandlerFunc(a.route)))
	sub, _ := fs.Sub(assets, "web")
	mux.Handle("/", http.FileServer(http.FS(sub)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' https://ddragon.leagueoflegends.com; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		w.Header().Set("Cache-Control", "no-store")
		defer func() {
			if v := recover(); v != nil {
				slog.Error("HTTP处理异常")
				a.respond(w, nil, fmt.Errorf("internal panic"))
			}
		}()
		mux.ServeHTTP(w, r)
	})
}
func (a *API) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			a.respond(w, nil, &app.Fault{Code: 401, Message: "请先输入管理员访问密钥"})
			return
		}
		supplied := sha256.Sum256([]byte(strings.TrimPrefix(auth, "Bearer ")))
		if subtle.ConstantTimeCompare(supplied[:], a.tokenHash[:]) != 1 {
			a.respond(w, nil, &app.Fault{Code: 401, Message: "管理员密钥错误"})
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, e := url.Parse(origin)
			if e != nil || !strings.EqualFold(u.Host, r.Host) {
				a.respond(w, nil, &app.Fault{Code: 403, Message: "拒绝跨站管理请求"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
func (a *API) respond(w http.ResponseWriter, data any, e error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if e != nil {
		code, msg := app.PublicError(e)
		if code == 500 {
			slog.Error("API内部错误（详细参数已隐藏）")
		}
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": msg})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": data})
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	mediatype, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || mediatype != "application/json" {
		return &app.Fault{Code: 415, Message: "请求必须使用application/json"}
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e = d.Decode(v); e != nil {
		return &app.Fault{Code: 400, Message: "JSON格式、字段或整数类型错误"}
	}
	if e = d.Decode(new(any)); e != io.EOF {
		return &app.Fault{Code: 400, Message: "请求只能包含一个JSON对象"}
	}
	return nil
}
func (a *API) command(w http.ResponseWriter, r *http.Request, payload any, fn func(*sqlite.Tx) (any, error)) {
	result, e := a.Service.Command(r.Header.Get("Idempotency-Key"), r.Method+" "+r.URL.Path, payload, fn)
	a.respond(w, result, e)
}
func pagination(r *http.Request) (int, int) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 || offset > 1_000_000 {
		offset = 0
	}
	return limit, offset
}
func (a *API) route(w http.ResponseWriter, r *http.Request) {
	s := a.Service
	path := strings.Trim(r.URL.Path, "/")
	parts := strings.Split(path, "/")
	if r.Method == "GET" {
		limit, offset := pagination(r)
		switch path {
		case "api/balance-requests":
			v, e := s.BalanceRequests(limit, offset)
			a.respond(w, v, e)
			return
		case "api/bot-settings":
			if s.Settings == nil {
				a.respond(w, nil, &app.Fault{Code: 503, Message: "运行配置未初始化"})
				return
			}
			a.respond(w, s.Settings.View(), nil)
			return
		case "api/champions":
			if s.Champions == nil {
				a.respond(w, nil, &app.Fault{Code: 503, Message: "英雄服务未初始化"})
				return
			}
			a.respond(w, s.Champions.View(), nil)
			return
		case "api/state":
			v, e := s.State()
			a.respond(w, v, e)
			return
		case "api/rounds":
			v, e := s.History(limit, offset)
			a.respond(w, v, e)
			return
		case "api/reconcile":
			v, e := s.Reconcile()
			a.respond(w, v, e)
			return
		case "api/accounts", "api/entries", "api/audit", "api/outbox", "api/bets":
			v, e := s.Rows(parts[1], r.URL.Query().Get("account_id"), limit, offset)
			a.respond(w, v, e)
			return
		}
		if len(parts) == 3 && parts[1] == "rounds" {
			v, e := s.ReadRound(parts[2])
			a.respond(w, v, e)
			return
		}
	}
	if r.Method != "POST" {
		a.respond(w, nil, &app.Fault{Code: 404, Message: "接口不存在或请求方法不匹配"})
		return
	}
	switch path {
	case "api/bot-settings":
		var in runtimeconfig.Patch
		if e := decode(w, r, &in); e != nil {
			a.respond(w, nil, e)
			return
		}
		if s.Settings == nil {
			a.respond(w, nil, &app.Fault{Code: 503, Message: "运行配置未初始化"})
			return
		}
		if e := s.Settings.Save(in); e != nil {
			a.respond(w, nil, &app.Fault{Code: 400, Message: e.Error()})
			return
		}
		a.respond(w, s.Settings.View(), nil)
		return
	case "api/bot-settings/test", "api/bot-settings/test-group":
		var in struct{}
		if e := decode(w, r, &in); e != nil {
			a.respond(w, nil, e)
			return
		}
		if s.Settings == nil {
			a.respond(w, nil, &app.Fault{Code: 503, Message: "运行配置未初始化"})
			return
		}
		c := s.Settings.Current()
		if c.Token == "" {
			a.respond(w, nil, &app.Fault{Code: 400, Message: "请先保存机器人Token"})
			return
		}
		if path == "api/bot-settings/test" {
			var me struct {
				ID       int64  `json:"id"`
				Username string `json:"username"`
			}
			e := telegram.New(c.Token).Call(r.Context(), "getMe", map[string]any{}, &me)
			if e != nil {
				a.respond(w, nil, &app.Fault{Code: 400, Message: "机器人连接失败，请检查Token或网络"})
				return
			}
			if !strings.EqualFold(me.Username, c.BotUsername) {
				a.respond(w, nil, &app.Fault{Code: 400, Message: "机器人用户名与Token不匹配"})
				return
			}
			a.respond(w, me, nil)
			return
		}
		if s.Settings.View()["restart_required"] == true || !c.Enabled || c.GroupID == 0 {
			a.respond(w, nil, &app.Fault{Code: 400, Message: "请先启用机器人、填写群ID并重启使配置生效"})
			return
		}
		a.command(w, r, in, func(tx *sqlite.Tx) (any, error) { return s.QueueGroupTest(tx, r.Header.Get("Idempotency-Key")) })
		return
	case "api/champions/refresh":
		var in struct{}
		if e := decode(w, r, &in); e != nil {
			a.respond(w, nil, e)
			return
		}
		if s.Champions == nil {
			a.respond(w, nil, &app.Fault{Code: 503, Message: "英雄服务未初始化"})
			return
		}
		v, e := s.Champions.Refresh(r.Context())
		if e != nil {
			a.respond(w, nil, &app.Fault{Code: 502, Message: "刷新失败，原缓存保留，请检查Riot网络连接后重试"})
			return
		}
		a.respond(w, v, nil)
		return
	case "api/balance-requests/resolve":
		var in struct {
			ID     int64  `json:"id"`
			Action string `json:"action"`
			Note   string `json:"note"`
		}
		if e := decode(w, r, &in); e != nil {
			a.respond(w, nil, e)
			return
		}
		a.command(w, r, in, func(tx *sqlite.Tx) (any, error) { return s.ResolveBalanceRequest(tx, in.ID, in.Action, in.Note) })
		return
	case "api/calculate":
		var in struct {
			Damage       string `json:"damage"`
			BankerDamage string `json:"banker_damage,omitempty"`
		}
		if e := decode(w, r, &in); e != nil {
			a.respond(w, nil, e)
			return
		}
		rules, e := s.CurrentRules()
		if e != nil {
			a.respond(w, nil, e)
			return
		}
		h, e := bull.Evaluate(in.Damage, rules.ZeroTriple)
		if e != nil {
			a.respond(w, nil, &app.Fault{Code: 400, Message: e.Error()})
			return
		}
		out := map[string]any{"hand": h}
		if in.BankerDamage != "" {
			banker, e := bull.Evaluate(in.BankerDamage, rules.ZeroTriple)
			if e != nil {
				a.respond(w, nil, &app.Fault{Code: 400, Message: e.Error()})
				return
			}
			out["banker"] = banker
			out["comparison"] = bull.Compare(h, banker)
			if h.Rank >= 0 {
				out["player_win_multiplier"] = rules.Payout[h.Rank]
			}
		}
		a.respond(w, out, nil)
		return
	case "api/rules":
		var in struct {
			ExpectedVersion int64     `json:"expected_version"`
			Rules           app.Rules `json:"rules"`
		}
		if e := decode(w, r, &in); e != nil {
			a.respond(w, nil, e)
			return
		}
		a.command(w, r, in, func(tx *sqlite.Tx) (any, error) { return s.SaveRules(tx, in.ExpectedVersion, in.Rules) })
		return
	case "api/rounds":
		var in app.ConfigureRound
		if e := decode(w, r, &in); e != nil {
			a.respond(w, nil, e)
			return
		}
		a.command(w, r, in, func(tx *sqlite.Tx) (any, error) { return s.CreateDraft(tx, in) })
		return
	case "api/accounts":
		var in struct {
			TelegramID int64  `json:"telegram_id"`
			Name       string `json:"name"`
			Enabled    bool   `json:"enabled"`
		}
		if e := decode(w, r, &in); e != nil {
			a.respond(w, nil, e)
			return
		}
		a.command(w, r, in, func(tx *sqlite.Tx) (any, error) { return s.SetPlayer(tx, in.TelegramID, in.Name, in.Enabled) })
		return
	case "api/adjustments":
		var in struct {
			AccountID string `json:"account_id"`
			Delta     int64  `json:"delta"`
			Note      string `json:"note"`
		}
		if e := decode(w, r, &in); e != nil {
			a.respond(w, nil, e)
			return
		}
		a.command(w, r, in, func(tx *sqlite.Tx) (any, error) {
			return s.Adjust(tx, in.AccountID, in.Delta, in.Note, r.Header.Get("Idempotency-Key"))
		})
		return
	case "api/bets":
		var in app.BetInput
		if e := decode(w, r, &in); e != nil {
			a.respond(w, nil, e)
			return
		}
		a.command(w, r, in, func(tx *sqlite.Tx) (any, error) { return s.PlaceBet(tx, in) })
		return
	}
	if len(parts) == 4 && parts[1] == "rounds" {
		id, action := parts[2], parts[3]
		switch action {
		case "configure":
			var in app.ConfigureRound
			if e := decode(w, r, &in); e != nil {
				a.respond(w, nil, e)
				return
			}
			a.command(w, r, in, func(tx *sqlite.Tx) (any, error) { return s.Configure(tx, id, in) })
			return
		case "open", "close":
			var in struct{}
			if e := decode(w, r, &in); e != nil {
				a.respond(w, nil, e)
				return
			}
			a.command(w, r, in, func(tx *sqlite.Tx) (any, error) {
				if action == "open" {
					return s.OpenRound(tx, id)
				}
				return s.CloseRound(tx, id)
			})
			return
		case "preview", "settle":
			var in app.SettleInput
			if e := decode(w, r, &in); e != nil {
				a.respond(w, nil, e)
				return
			}
			if action == "preview" {
				v, e := s.Preview(id, in)
				a.respond(w, v, e)
			} else {
				a.command(w, r, in, func(tx *sqlite.Tx) (any, error) { return s.Settle(tx, id, in) })
			}
			return
		}
	}
	if len(parts) == 4 && parts[1] == "outbox" && parts[3] == "resolve" {
		id, e := strconv.ParseInt(parts[2], 10, 64)
		if e != nil {
			a.respond(w, nil, &app.Fault{Code: 400, Message: "无效发送任务ID"})
			return
		}
		var in struct {
			Action    string `json:"action"`
			MessageID int64  `json:"message_id"`
		}
		if e = decode(w, r, &in); e != nil {
			a.respond(w, nil, e)
			return
		}
		a.command(w, r, in, func(tx *sqlite.Tx) (any, error) { return s.ResolveOutbox(tx, id, in.Action, in.MessageID) })
		return
	}
	a.respond(w, nil, &app.Fault{Code: 404, Message: "接口不存在"})
}
