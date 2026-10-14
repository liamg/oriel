package price

import (
	"math"
	"testing"
)

func TestRateFor(t *testing.T) {
	for _, c := range []struct {
		model string
		fast  bool
		want  Rate
	}{
		{"claude-opus-5-5", false, Rate{In: 5, Out: 25}},
		{"claude-opus-5-5", true, Rate{In: 10, Out: 50}},
		{"claude-sonnet-4-5", true, Rate{In: 3, Out: 15}},
		{"gpt-5-codex", false, Rate{In: 1.25, Out: 10}},
		{"gpt-5-mini", false, Rate{In: 0.25, Out: 2}},
		{"gpt-9", false, Rate{}},
	} {
		if r := RateFor(c.model, c.fast); r != c.want {
			t.Errorf("%s fast=%v = %+v, want %+v", c.model, c.fast, r, c.want)
		}
	}
}

// Opus: $5/Mtok in, $25 out; a cache read a tenth of input, a five-minute write a quarter more, an hour's twice it.
func TestCostByKind(t *testing.T) {
	tk := Tokens{Input: 110, Output: 220, CacheRead: 3_000_000, CacheWrite5m: 1000, CacheWrite1h: 50_000}
	c := tk.Of(RateFor("claude-opus-5", false))
	want := (110*5 + 1000*5*1.25 + 50_000*5*2 + 3_000_000*5*0.1 + 220*25) / 1e6
	if math.Abs(c.Total()-want) > 1e-9 || math.Abs(tk.Cost(RateFor("opus", false))-want) > 1e-9 {
		t.Errorf("cost = %v, want %v", c.Total(), want)
	}
	if math.Abs(c.CacheRead-1.5) > 1e-9 {
		t.Errorf("cache read = %v, want 1.5", c.CacheRead)
	}
}
