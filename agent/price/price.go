// Package price is what every cost estimate prices tokens with: a Book of models' list prices, in dollars per
// million tokens, read from cost.dev's model list. The snapshot vendored with Oriel (spec/cost-dev) is the default;
// the session host keeps a fresher one (host/pricing).
package price

// Rate is what a model's tokens cost, per million, by kind.
type Rate struct {
	In, Out      float64
	CacheRead    float64
	CacheWrite5m float64
	CacheWrite1h float64
}

// Cache multipliers over the input price, for a model whose list doesn't price that kind: a read, a five-minute
// write and an hour-long one.
const (
	mulRead    = 0.1
	mulWrite5m = 1.25
	mulWrite1h = 2.0
)

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
		CacheRead:    float64(t.CacheRead) * r.CacheRead / m,
		CacheWrite5m: float64(t.CacheWrite5m) * r.CacheWrite5m / m,
		CacheWrite1h: float64(t.CacheWrite1h) * r.CacheWrite1h / m,
	}
}

// Cost of the tokens at r, in dollars.
func (t Tokens) Cost(r Rate) float64 { return t.Of(r).Total() }
