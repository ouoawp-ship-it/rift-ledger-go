package bull

import (
	"fmt"
	"math/rand"
	"testing"
)

func TestDamageExamples(t *testing.T) {
	cases := []struct {
		raw, norm string
		rank      int
		void      bool
	}{{"12710", "12710", 1, false}, {"12745", "12745", 9, false}, {"12737", "12737", 10, false}, {"99999", "99999", 0, false}, {"999", "", -1, true}, {"1234", "11234", 0, false}, {"1237", "11237", 4, false}, {"100001", "00001", 1, false}, {"100000", "00000", 10, false}, {"100012", "00012", 3, false}}
	for _, c := range cases {
		t.Run(c.raw, func(t *testing.T) {
			h, e := Evaluate(c.raw, true)
			if e != nil || h.Normalized != c.norm || h.Rank != c.rank || h.Void != c.void {
				t.Fatalf("%+v %v", h, e)
			}
		})
	}
}
func TestInvalidDamage(t *testing.T) {
	for _, s := range []string{"", "0", "99", "01234", "00000", "-12345", "1.2345", "123,456", "１２３４５", "1e5", "1000000", "12a45"} {
		t.Run(s, func(t *testing.T) {
			if _, e := Evaluate(s, true); e == nil {
				t.Fatal("invalid damage accepted")
			}
		})
	}
}
func TestZeroPolicyExplicit(t *testing.T) {
	for _, s := range []string{"100000", "100001", "100012"} {
		h, e := Evaluate(s, false)
		if e != nil || h.Rank != 0 {
			t.Fatalf("%s %+v %v", s, h, e)
		}
	}
}
func TestAllOneHundredThousandFiveDigitVectors(t *testing.T) {
	for n := 0; n < 100000; n++ {
		s := fmt.Sprintf("%05d", n)
		digits := make([]int, 5)
		total := 0
		for i, c := range s {
			digits[i] = int(c - '0')
			total += digits[i]
		}
		for _, zero := range []bool{true, false} {
			// Independent oracle: choose the TWO leftover positions, not a triple.
			exists := false
			for a := 0; a < 4; a++ {
				for b := a + 1; b < 5; b++ {
					sum := total - digits[a] - digits[b]
					if sum%10 == 0 && (zero || sum != 0) {
						exists = true
					}
				}
			}
			want := 0
			if exists {
				want = total % 10
				if want == 0 {
					want = 10
				}
			}
			h, e := Evaluate("1"+s, zero)
			if e != nil || h.Rank != want {
				t.Fatalf("digits=%s zero=%v expected=%d got=%+v err=%v", s, zero, want, h, e)
			}
			if len(h.Normalized) != 5 {
				t.Fatal("lost leading zeros")
			}
		}
	}
}
func TestPermutationInvariant(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 500; i++ {
		s := fmt.Sprintf("%05d", rng.Intn(100000))
		h, _ := Evaluate("1"+s, true)
		d := []byte(s)
		rng.Shuffle(5, func(a, b int) { d[a], d[b] = d[b], d[a] })
		other, _ := Evaluate("1"+string(d), true)
		if h.Rank != other.Rank || h.MaxDigit != other.MaxDigit {
			t.Fatal("permutation changes comparison")
		}
	}
}
func TestCompareRules(t *testing.T) {
	cases := []struct {
		p, b Hand
		out  string
	}{{Hand{Rank: 9}, Hand{Rank: 1}, "WIN"}, {Hand{Rank: 9}, Hand{Rank: 10}, "LOSS"}, {Hand{Rank: 0, MaxDigit: 9}, Hand{Rank: 0, MaxDigit: 8}, "WIN"}, {Hand{Rank: 8, MaxDigit: 9}, Hand{Rank: 8, MaxDigit: 9}, "LOSS"}, {Hand{Rank: 8, MaxDigit: 8}, Hand{Rank: 8, MaxDigit: 9}, "LOSS"}, {Hand{Void: true}, Hand{Rank: 1}, "VOID"}, {Hand{Rank: 9}, Hand{Void: true}, "VOID"}}
	for _, c := range cases {
		if got := Compare(c.p, c.b); got.Outcome != c.out {
			t.Fatalf("%+v", got)
		}
	}
}
