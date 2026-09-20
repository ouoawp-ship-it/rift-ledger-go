// Package bull is the sole damage normalization, NiuNiu and comparison engine.
// It contains no database, HTTP, Telegram, or wallet logic.
package bull

import (
	"errors"
	"fmt"
	"strings"
)

type Hand struct {
	Raw         string `json:"raw"`
	Normalized  string `json:"normalized"`
	Digits      []int  `json:"digits"`
	Rank        int    `json:"rank"` // 0=没牛, 1..9=牛1..9, 10=牛牛; -1=流局
	Label       string `json:"label"`
	MaxDigit    int    `json:"max_digit"`
	Triple      []int  `json:"triple"` // zero-based positions, not digit values
	Remainder   []int  `json:"remainder"`
	Void        bool   `json:"void"`
	Explanation string `json:"explanation"`
}

// Evaluate accepts the ORIGINAL decimal damage, not pre-padded input.
// Four digits get one extra 1. Its insertion position cannot change the rank
// or maximum digit because all three-position combinations are enumerated.
// Three-digit damage voids this hero. Six digits retain the last FIVE characters.
// zeroTriple explicitly decides whether 0+0+0 counts as a multiple of ten.
func Evaluate(raw string, zeroTriple bool) (Hand, error) {
	raw = strings.TrimSpace(raw)
	h := Hand{Raw: raw, Rank: -1, Digits: []int{}, Triple: []int{}, Remainder: []int{}}
	if len(raw) < 3 || len(raw) > 6 {
		return h, errors.New("原始伤害必须为3至6位整数；不足3位或超过6位尚无确认规则")
	}
	for _, c := range raw {
		if c < '0' || c > '9' {
			return h, errors.New("伤害只能包含半角数字，不接受负数、小数、逗号或科学计数法")
		}
	}
	if raw[0] == '0' {
		return h, errors.New("原始伤害不能带人为前导零；六位截取产生的零由系统保留")
	}
	switch len(raw) {
	case 3:
		h.Void = true
		h.Label = "流局"
		h.Explanation = "三位数伤害：该英雄流局，不等于没牛"
		return h, nil
	case 4:
		h.Normalized = "1" + raw
	case 5:
		h.Normalized = raw
	case 6:
		h.Normalized = raw[1:]
	}
	sum := 0
	for _, c := range h.Normalized {
		d := int(c - '0')
		h.Digits = append(h.Digits, d)
		sum += d
		if d > h.MaxDigit {
			h.MaxDigit = d
		}
	}
	h.Rank = 0
	h.Label = "没牛"
	for i := 0; i < 3; i++ {
		for j := i + 1; j < 4; j++ {
			for k := j + 1; k < 5; k++ {
				s := h.Digits[i] + h.Digits[j] + h.Digits[k]
				if s%10 != 0 || (s == 0 && !zeroTriple) {
					continue
				}
				h.Triple = []int{i, j, k}
				for n := 0; n < 5; n++ {
					if n != i && n != j && n != k {
						h.Remainder = append(h.Remainder, n)
					}
				}
				h.Rank = (sum - s) % 10
				if h.Rank == 0 {
					h.Rank = 10
					h.Label = "牛牛"
				} else {
					h.Label = fmt.Sprintf("牛%d", h.Rank)
				}
				h.Explanation = fmt.Sprintf("%d+%d+%d=%d；余下%d+%d=%d，得到%s", h.Digits[i], h.Digits[j], h.Digits[k], s, h.Digits[h.Remainder[0]], h.Digits[h.Remainder[1]], sum-s, h.Label)
				return h, nil
			}
		}
	}
	h.Explanation = "任取三个位置，均不能按当前零组合规则凑成10的倍数"
	return h, nil
}

type Comparison struct {
	Outcome string `json:"outcome"`
	Reason  string `json:"reason"`
}

// Compare returns the PLAYER's outcome. No comparison of the second-largest digit.
func Compare(player, banker Hand) Comparison {
	if player.Void || banker.Void {
		return Comparison{"VOID", "闲家或庄家英雄流局"}
	}
	if player.Rank > banker.Rank {
		return Comparison{"WIN", "闲家牛型更大"}
	}
	if player.Rank < banker.Rank {
		return Comparison{"LOSS", "庄家牛型更大"}
	}
	if player.MaxDigit > banker.MaxDigit {
		return Comparison{"WIN", "同牛，闲家最大数字更大"}
	}
	if player.MaxDigit < banker.MaxDigit {
		return Comparison{"LOSS", "同牛，庄家最大数字更大"}
	}
	return Comparison{"LOSS", "同牛且最大数字相同，庄家胜；不比较第二大数字"}
}

func Label(rank int) string {
	if rank == 0 {
		return "没牛"
	}
	if rank == 10 {
		return "牛牛"
	}
	return fmt.Sprintf("牛%d", rank)
}
