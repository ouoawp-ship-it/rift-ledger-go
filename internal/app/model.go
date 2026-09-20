package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"time"

	"riftledger/pkg/bull"
)

const Version = "0.2.0"
const MoneyLimit int64 = 1_000_000_000_000 // exact integers, well below JS's safe integer limit
const MaxBetsPerRound = 10000

type Fault struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (f *Fault) Error() string   { return f.Message }
func bad(msg string) error       { return &Fault{400, msg} }
func conflict(msg string) error  { return &Fault{409, msg} }
func forbidden(msg string) error { return &Fault{403, msg} }
func notfound(msg string) error  { return &Fault{404, msg} }
func PublicError(e error) (int, string) {
	var f *Fault
	if errors.As(e, &f) {
		return f.Code, f.Message
	}
	return 500, "服务内部错误；请查看服务器日志，不要反复新建请求"
}

type Rules struct {
	Confirmed    bool      `json:"confirmed"`
	Payout       []float64 `json:"payout"`
	MinStake     int64     `json:"min_stake"`
	MaxStake     int64     `json:"max_stake"`
	FeeTiming    string    `json:"fee_timing"`    // acceptance or settlement; explicit operator confirmation
	VoidFee      string    `json:"void_fee"`      // refund or charge
	FeeRecipient string    `json:"fee_recipient"` // fees or house
	ZeroTriple   bool      `json:"zero_triple"`
}

func DefaultRules() Rules {
	return Rules{Confirmed: false, Payout: []float64{1, 1, 1, 1, 1, 1, 1, 2, 2, 3, 4}, MinStake: 20, MaxStake: 300, ZeroTriple: true}
}
func (r Rules) Validate() error {
	if len(r.Payout) != 11 {
		return bad("赔率必须包含没牛至牛牛共11项")
	}
	for _, v := range r.Payout {
		if v < 0 || v > 100 || math.Round(v*100) != v*100 {
			return bad("每项赔率必须为0至100之间、最多两位小数的净盈利倍数")
		}
	}
	if r.MinStake < 1 || r.MaxStake < r.MinStake || r.MaxStake > 1_000_000 {
		return bad("下注限额必须为1至1000000之间的整数，且最低不超过最高")
	}
	return nil
}
func (r Rules) MaxPayout() float64 {
	m := float64(0)
	for _, v := range r.Payout {
		if v > m {
			m = v
		}
	}
	return m
}
func fee(stake int64) int64 {
	if stake <= 100 {
		return 1
	}
	return 2
}

type Hero struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type Round struct {
	ID           string   `json:"id"`
	Number       string   `json:"number"`
	State        string   `json:"state"`
	Banker       int      `json:"banker"`
	Heroes       []Hero   `json:"heroes"`
	Rules        Rules    `json:"rules"`
	RulesVersion int64    `json:"rules_version"`
	Revision     int64    `json:"revision"`
	CreatedAt    int64    `json:"created_at"`
	OpenedAt     int64    `json:"opened_at"`
	SettledAt    int64    `json:"settled_at"`
	NextRoundID  string   `json:"next_round_id"`
	Result       *Preview `json:"result,omitempty"`
}
type ConfigureRound struct {
	Number string `json:"number"`
	Banker int    `json:"banker"`
	Heroes []Hero `json:"heroes"`
}

var numberPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}-\d{4,8}$`)
var heroPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)

func validNumber(s string) bool {
	if !numberPattern.MatchString(s) {
		return false
	}
	_, e := time.Parse("2006-01-02", s[:10])
	return e == nil
}

type Account struct {
	Username     string `json:"username"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name"`
	FirstContact int64  `json:"first_contact"`
	LastContact  int64  `json:"last_contact"`
	ID           string `json:"id"`
	Name         string `json:"name"`
	Role         string `json:"role"`
	TelegramID   int64  `json:"telegram_id"`
	Enabled      bool   `json:"enabled"`
	Balance      int64  `json:"balance"`
	Locked       int64  `json:"locked"`
	Available    int64  `json:"available"`
	CreatedAt    int64  `json:"created_at"`
}
type Bet struct {
	ID        string `json:"id"`
	RoundID   string `json:"round_id"`
	AccountID string `json:"account_id"`
	Position  int    `json:"position"`
	Stake     int64  `json:"stake"`
	Fee       int64  `json:"fee"`
	State     string `json:"state"`
	GameDelta int64  `json:"game_delta"`
	NetDelta  int64  `json:"net_delta"`
	CreatedAt int64  `json:"created_at"`
	SettledAt int64  `json:"settled_at"`
}
type BetInput struct {
	AccountID string `json:"account_id"`
	RoundID   string `json:"round_id"`
	Position  int    `json:"position"`
	Stake     int64  `json:"stake"`
}
type SettleInput struct {
	CancelReason    string   `json:"-"`
	DurationSeconds int      `json:"duration_seconds"`
	Damages         []string `json:"damages"`
	PreviewToken    string   `json:"preview_token,omitempty"`
}
type Line struct {
	BetID      string  `json:"bet_id"`
	AccountID  string  `json:"account_id"`
	Position   int     `json:"position"`
	Stake      int64   `json:"stake"`
	Fee        int64   `json:"fee"`
	Outcome    string  `json:"outcome"`
	GameDelta  int64   `json:"game_delta"`
	NetDelta   int64   `json:"net_delta"`
	Multiplier float64 `json:"multiplier"`
}
type PositionResult struct {
	Position int       `json:"position"`
	Hero     Hero      `json:"hero"`
	Banker   bool      `json:"banker"`
	Hand     bull.Hand `json:"hand"`
	Outcome  string    `json:"outcome"`
	Reason   string    `json:"reason"`
}
type Preview struct {
	RoundID         string           `json:"round_id"`
	Number          string           `json:"number"`
	Revision        int64            `json:"revision"`
	DurationSeconds int              `json:"duration_seconds"`
	Damages         []string         `json:"damages"`
	WholeVoid       bool             `json:"whole_void"`
	Reason          string           `json:"reason"`
	Positions       []PositionResult `json:"positions"`
	Lines           []Line           `json:"lines"`
	PlayerGameDelta int64            `json:"player_game_delta"`
	HouseGameDelta  int64            `json:"house_game_delta"`
	FeeTotal        int64            `json:"fee_total"`
	Token           string           `json:"token"`
	NextRoundID     string           `json:"next_round_id,omitempty"`
}
type RuntimeConfig struct {
	GroupID         int64
	TopicID         int64
	NotifyAdminID   int64
	BotUsername     string
	SupportUsername string
}

func asJSON(v any) string {
	b, e := json.Marshal(v)
	if e != nil {
		panic(fmt.Sprintf("JSON编码失败: %v", e))
	}
	return string(b)
}
