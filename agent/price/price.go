// Package price is the price table every cost estimate uses: dollars per million tokens, by model family.
package price

import "strings"

// Published list prices, US dollars per million tokens. Only the family in the model's name is matched: the APIs
// price a tier, not a point release. Fast mode is its own tier. A more specific family comes before one it contains.
var prices = []struct {
	family          string
	in, out         float64
	fastIn, fastOut float64
}{
	{family: "fable", in: 10, out: 50},
	{family: "mythos", in: 10, out: 50},
	{family: "opus", in: 5, out: 25, fastIn: 10, fastOut: 50},
	{family: "sonnet", in: 3, out: 15},
	{family: "haiku", in: 1, out: 5},
	// OpenAI's: unverified list prices, not yet checked against OpenAI's pricing page.
	{family: "gpt-5-nano", in: 0.05, out: 0.4},
	{family: "gpt-5-mini", in: 0.25, out: 2},
	{family: "gpt-5", in: 1.25, out: 10},
}

// Cache multipliers over the input price: a five-minute write, an hour-long one, and a read.
const (
	mulWrite5m = 1.25
	mulWrite1h = 2.0
	mulRead    = 0.1
)

// Rate is what a model costs, per million tokens.
type Rate struct {
	In, Out float64
}

// RateFor is model's price, or a zero Rate for a model with no known price.
func RateFor(model string, fast bool) Rate {
	m := strings.ToLower(model)
	for _, p := range prices {
		if !strings.Contains(m, p.family) {
			continue
		}
		if fast && p.fastIn > 0 {
			return Rate{In: p.fastIn, Out: p.fastOut}
		}
		return Rate{In: p.in, Out: p.out}
	}
	return Rate{}
}

// Tokens is one request's tokens, by how they're priced.
type Tokens struct {
	Input        int64 `json:"input,omitempty"`
	Output       int64 `json:"output,omitempty"`
	CacheRead    int64 `json:"cacheRead,omitempty"`
	CacheWrite5m int64 `json:"cacheWrite5m,omitempty"`
	CacheWrite1h int64 `json:"cacheWrite1h,omitempty"`
}

// Total is every token, cached or not.
func (t Tokens) Total() int64 {
	return t.Input + t.Output + t.CacheRead + t.CacheWrite5m + t.CacheWrite1h
}

// Prompt is the request's context: everything but the reply.
func (t Tokens) Prompt() int64 { return t.Input + t.CacheRead + t.CacheWrite5m + t.CacheWrite1h }

// Add is t and o summed, each scaled by k: -1 takes o back out.
func (t Tokens) Add(o Tokens, k int64) Tokens {
	return Tokens{t.Input + k*o.Input, t.Output + k*o.Output, t.CacheRead + k*o.CacheRead, t.CacheWrite5m + k*o.CacheWrite5m,
		t.CacheWrite1h + k*o.CacheWrite1h}
}

// Costs is what tokens cost, by kind, in dollars.
type Costs struct {
	Input        float64 `json:"input,omitempty"`
	Output       float64 `json:"output,omitempty"`
	CacheRead    float64 `json:"cacheRead,omitempty"`
	CacheWrite5m float64 `json:"cacheWrite5m,omitempty"`
	CacheWrite1h float64 `json:"cacheWrite1h,omitempty"`
}

func (c Costs) Total() float64 {
	return c.Input + c.Output + c.CacheRead + c.CacheWrite5m + c.CacheWrite1h
}

// Add is c and o summed, each of o's scaled by k.
func (c Costs) Add(o Costs, k float64) Costs {
	return Costs{c.Input + k*o.Input, c.Output + k*o.Output, c.CacheRead + k*o.CacheRead, c.CacheWrite5m + k*o.CacheWrite5m,
		c.CacheWrite1h + k*o.CacheWrite1h}
}

// Of is what t costs at r, by kind.
func (t Tokens) Of(r Rate) Costs {
	const m = 1_000_000
	return Costs{
		Input:        float64(t.Input) * r.In / m,
		Output:       float64(t.Output) * r.Out / m,
		CacheRead:    float64(t.CacheRead) * r.In * mulRead / m,
		CacheWrite5m: float64(t.CacheWrite5m) * r.In * mulWrite5m / m,
		CacheWrite1h: float64(t.CacheWrite1h) * r.In * mulWrite1h / m,
	}
}

// Cost of the tokens at r, in dollars.
func (t Tokens) Cost(r Rate) float64 { return t.Of(r).Total() }
