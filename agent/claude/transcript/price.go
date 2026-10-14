package transcript

import "strings"

// Published list prices, US dollars per million tokens. Only the family in the model's name is matched: the API
// prices a tier, not a point release. Fast mode is its own tier.
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
	Input, CacheWrite5m, CacheWrite1h, CacheRead, Output int64
}

// Cost of the tokens at r, in dollars.
func (t Tokens) Cost(r Rate) float64 {
	const m = 1_000_000
	return (float64(t.Input)*r.In +
		float64(t.CacheWrite5m)*r.In*mulWrite5m +
		float64(t.CacheWrite1h)*r.In*mulWrite1h +
		float64(t.CacheRead)*r.In*mulRead +
		float64(t.Output)*r.Out) / m
}
