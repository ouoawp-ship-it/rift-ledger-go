package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"riftledger/internal/sqlite"
)

// Money stores thousandths of a point. SQL uses this integer representation;
// JSON retains the public point unit, including old integral request hashes.
type Money int64

const MoneyScale Money = 1000
const MoneyLimit Money = 1_000_000_000_000 * MoneyScale

func Points(n int64) Money         { return Money(n) * MoneyScale }
func (m Money) SQLiteInt64() int64 { return int64(m) }

var moneyPattern = regexp.MustCompile(`^(-?)([0-9]{1,13})(?:\.([0-9]{1,3}))?$`)

func ParseMoney(raw string) (Money, error) {
	parts := moneyPattern.FindStringSubmatch(raw)
	if parts == nil {
		return 0, bad("积分金额最多保留三位小数，请填写普通十进制数字")
	}
	whole, _ := strconv.ParseInt(parts[2], 10, 64)
	fraction, _ := strconv.ParseInt(parts[3]+strings.Repeat("0", 3-len(parts[3])), 10, 64)
	m := Points(whole) + Money(fraction)
	if m > MoneyLimit {
		return 0, bad("积分金额超出安全范围")
	}
	if parts[1] == "-" {
		m = -m
	}
	return m, nil
}

func (m Money) String() string {
	sign := ""
	if m < 0 {
		sign = "-"
		m = -m
	}
	return fmt.Sprintf("%s%d.%03d", sign, int64(m/MoneyScale), int64(m%MoneyScale))
}

func (m Money) Format(state fmt.State, verb rune) {
	text := m.String()
	if m >= 0 && state.Flag('+') {
		text = "+" + text
	}
	fmt.Fprint(state, text)
}

func (m Money) MarshalJSON() ([]byte, error) {
	// 1000 stays 1000 rather than 1000.000 so pre-upgrade idempotency keys
	// and preview hashes with integral amounts remain comparable.
	return []byte(strings.TrimRight(strings.TrimRight(m.String(), "0"), ".")), nil
}
func (m *Money) UnmarshalJSON(raw []byte) error {
	text := string(bytes.TrimSpace(raw))
	if strings.HasPrefix(text, `"`) {
		if err := json.Unmarshal(raw, &text); err != nil {
			return err
		}
	}
	value, err := ParseMoney(text)
	if err != nil {
		return err
	}
	*m = value
	return nil
}

// Rates are limited to two decimal places. Multiplication stays in integers.
func payoutHundredths(rate float64) (int64, error) {
	m, err := ParseMoney(strconv.FormatFloat(rate, 'f', -1, 64))
	if err != nil || m < 0 || m > 100*MoneyScale || m%10 != 0 {
		return 0, bad("每项赔率必须为0至100之间、最多两位小数的净盈利倍数")
	}
	return int64(m / 10), nil
}
func profitFor(stake Money, rate float64) Money {
	basis, err := payoutHundredths(rate)
	if err != nil {
		panic(err)
	} // persisted rules were validated before opening
	// Valid stakes <= 1,000,000 points and rates <= 100: product fits int64.
	return Money((int64(stake)*basis + 50) / 100)
}
func exposureFor(stake Money, rate float64) Money {
	basis, err := payoutHundredths(rate)
	if err != nil {
		panic(err)
	}
	// Preserve the old conservative whole-point reserve for legacy house mode.
	return Money((int64(stake)*basis+100000-1)/100000) * MoneyScale
}

func moneyRow(row sqlite.Row, key string) Money { return Money(row.Int(key)) }
func displayMoneyRow(row sqlite.Row, fields ...string) {
	for _, field := range fields {
		if _, ok := row[field]; ok {
			row[field] = moneyRow(row, field).String()
		}
	}
}
func displayMoneyRows(rows []sqlite.Row, err error, fields ...string) ([]sqlite.Row, error) {
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		displayMoneyRow(row, fields...)
	}
	return rows, nil
}
