package price

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The embedded snapshot is the vendored one, byte for byte.
//
// ref:   cost-dev/SOURCE
// quote: path: models.json
func TestSnapshotIsVendored(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("..", "..", "spec", "cost-dev", "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(Snapshot(), want) {
		t.Error("agent/price/models.json differs from spec/cost-dev/models.json")
	}
}

func approx(a, b Rate) bool {
	eq := func(x, y float64) bool { return math.Abs(x-y) < 1e-9 }
	return eq(a.In, b.In) && eq(a.Out, b.Out) && eq(a.CacheRead, b.CacheRead) && eq(a.CacheWrite5m, b.CacheWrite5m) &&
		eq(a.CacheWrite1h, b.CacheWrite1h)
}

// Each entry's list prices, input, output, a cache read and the two cache writes, as cost.dev lists them.
//
// ref:   cost-dev/models.json
// quote: pb_anthropic_claude-opus-5-5_direct_realtime_global_usd_20260922","verified_at":"2026-09-23T00:00:00Z","source_id":"source:anthropic-pricing"},"prices":{"cache_write_1h_tokens_per_1000000":"8","cache_write_5m_tokens_per_1000000":"5","cached_input_tokens_per_1000000":"0.2","input_tokens_per_1000000":"4","output_tokens_per_1000000":"20"}
// ref:   cost-dev/models.json
// quote: pb_anthropic_claude-haiku-5-5_direct_realtime_global_usd_20261007","verified_at":"2026-10-08T00:00:00Z","source_id":"source:anthropic-pricing","has_usage_based_tiers":true},"prices":{"cache_write_1h_tokens_per_1000000":"0.2","cache_write_5m_tokens_per_1000000":"0.125","cached_input_tokens_per_1000000":"0.01","input_tokens_per_1000000":"0.1","output_tokens_per_1000000":"0.5"}
// ref:   cost-dev/models.json
// quote: pb_anthropic_claude-sonnet-5-5_direct_realtime_global_usd_20261007","verified_at":"2026-10-08T00:00:00Z","source_id":"source:anthropic-pricing"},"prices":{"cache_write_1h_tokens_per_1000000":"4","cache_write_5m_tokens_per_1000000":"2.5","cached_input_tokens_per_1000000":"0.1","input_tokens_per_1000000":"2","output_tokens_per_1000000":"10"}
// ref:   cost-dev/models.json
// quote: pb_anthropic_claude-sonnet-4-6_direct_realtime_global_usd_2026","verified_at":"2026-05-16T00:00:00Z","source_id":"source:anthropic-pricing"},"prices":{"cache_write_1h_tokens_per_1000000":"6","cache_write_5m_tokens_per_1000000":"3.75","cached_input_tokens_per_1000000":"0.3","input_tokens_per_1000000":"3","output_tokens_per_1000000":"15"}
// ref:   cost-dev/models.json
// quote: pb_anthropic_claude-opus-4-7_direct_realtime_global_usd_2026","verified_at":"2026-05-16T00:00:00Z","source_id":"source:anthropic-pricing"},"prices":{"cache_write_1h_tokens_per_1000000":"10","cache_write_5m_tokens_per_1000000":"6.25","cached_input_tokens_per_1000000":"0.5","input_tokens_per_1000000":"5","output_tokens_per_1000000":"25"}
// ref:   cost-dev/models.json
// quote: pb_anthropic_claude-fable-5-1_direct_realtime_global_usd_20260901","verified_at":"2026-09-02T00:00:00Z","source_id":"source:anthropic-pricing"},"prices":{"cache_write_1h_tokens_per_1000000":"20","cache_write_5m_tokens_per_1000000":"12.5","cached_input_tokens_per_1000000":"0.25","input_tokens_per_1000000":"10","output_tokens_per_1000000":"50"}
// ref:   cost-dev/models.json
// quote: pb_openai_gpt-6.1-sol_direct_realtime_global_usd_20260929","verified_at":"2026-09-29T00:00:00Z","source_id":"source:openai-pricing","has_usage_based_tiers":true},"prices":{"cache_write_5m_tokens_per_1000000":"2.5","cached_input_tokens_per_1000000":"0.1","input_tokens_per_1000000":"2","output_tokens_per_1000000":"10"}
// ref:   cost-dev/models.json
// quote: pb_openai_gpt-6-luna_direct_realtime_global_usd_20260922","verified_at":"2026-09-23T00:00:00Z","source_id":"source:openai-pricing","has_usage_based_tiers":true},"prices":{"cache_write_5m_tokens_per_1000000":"0.125","cached_input_tokens_per_1000000":"0.01","input_tokens_per_1000000":"0.1","output_tokens_per_1000000":"0.5"}
// ref:   cost-dev/models.json
// quote: pb_openai_gpt-5.5_direct_realtime_global_usd_2026","verified_at":"2026-09-08T00:00:00Z","source_id":"source:openai-pricing","has_usage_based_tiers":true},"prices":{"cached_input_tokens_per_1000000":"0.5","input_tokens_per_1000000":"5","output_tokens_per_1000000":"30"}
func TestRateFor(t *testing.T) {
	opus55 := Rate{In: 4, Out: 20, CacheRead: 0.2, CacheWrite5m: 5, CacheWrite1h: 8}
	haiku55 := Rate{In: 0.1, Out: 0.5, CacheRead: 0.01, CacheWrite5m: 0.125, CacheWrite1h: 0.2}
	sonnet55 := Rate{In: 2, Out: 10, CacheRead: 0.1, CacheWrite5m: 2.5, CacheWrite1h: 4}
	sonnet46 := Rate{In: 3, Out: 15, CacheRead: 0.3, CacheWrite5m: 3.75, CacheWrite1h: 6}
	opus47 := Rate{In: 5, Out: 25, CacheRead: 0.5, CacheWrite5m: 6.25, CacheWrite1h: 10}
	fable51 := Rate{In: 10, Out: 50, CacheRead: 0.25, CacheWrite5m: 12.5, CacheWrite1h: 20}
	// No hour-long write is listed for OpenAI's: twice the input, as Anthropic's are.
	sol61 := Rate{In: 2, Out: 10, CacheRead: 0.1, CacheWrite5m: 2.5, CacheWrite1h: 4}
	luna6 := Rate{In: 0.1, Out: 0.5, CacheRead: 0.01, CacheWrite5m: 0.125, CacheWrite1h: 0.2}
	// No cache write at all is listed for gpt-5.5: a quarter more and twice the input.
	gpt55 := Rate{In: 5, Out: 30, CacheRead: 0.5, CacheWrite5m: 6.25, CacheWrite1h: 10}
	b := Default()
	for _, c := range []struct {
		model string
		want  Rate
	}{
		{"anthropic:claude-opus-5-5", opus55}, // the id
		{"claude-opus-5-5", opus55},           // the name, as Claude Code reports it
		{"claude-opus-5-5[1m]", opus55},
		{"CLAUDE-OPUS-5-5", opus55},
		{"opus", opus55}, // a bare line: its newest model
		{"opus[1m]", opus55},
		{"opusplan", opus55},
		{"sonnet", sonnet55},
		{"haiku", haiku55},
		{"fable", fable51},
		{"claude-sonnet-4-6-20260217", sonnet46}, // a dated id
		{"claude-sonnet-4-6-20260101", sonnet46}, // a dated id is its model
		{"claude-sonnet-4-5", Rate{}},            // no longer listed: no price, not a guess
		{"claude-opus-4-7", opus47},
		{"claude-opus-4-1", Rate{}},
		{"gpt-6.1-sol", sol61},
		{"openai:gpt-6.1-sol", sol61},
		{"gpt-6-luna", luna6},
		{"gpt-5.5", gpt55},
		{"gpt-5.5-chat-latest", gpt55}, // an alias
		{"gpt-9", Rate{}},
		{"", Rate{}},
	} {
		if r := b.RateFor(c.model, false, 0); !approx(r, c.want) {
			t.Errorf("%q = %+v, want %+v", c.model, r, c.want)
		}
	}
}

// A bare line is its newest current model by release date, not the one cost.dev marks latest in its family.
//
// ref:   cost-dev/models.json
// quote: id":"anthropic:claude-opus-5-5","provider":{"id":"anthropic","display_name":"Anthropic","homepage_url":"https://www.anthropic.com/","origin_country":"US","status":"active"},"display_name":"Claude Opus 5.5","family":"claude-5","lifecycle_status":"current","latest":false,"release_date":"2026-09-22"
// ref:   cost-dev/models.json
// quote: id":"anthropic:claude-opus-4-8","provider":{"id":"anthropic","display_name":"Anthropic","homepage_url":"https://www.anthropic.com/","origin_country":"US","status":"active"},"display_name":"Claude Opus 4.8","family":"claude-4","lifecycle_status":"current","latest":true,"release_date":"2026-05-28"
func TestBareLineIsNewest(t *testing.T) {
	if m, _ := Default().Find("opus"); m.ID != "anthropic:claude-opus-5-5" {
		t.Errorf("opus = %s", m.ID)
	}
}

// Fast mode is its own price, where the list has one; where it lists no cache price, the multipliers apply to it.
//
// ref:   cost-dev/models.json
// quote: fast_prices":{"input_tokens_per_1000000":"8","output_tokens_per_1000000":"40"},"selected_offering":{"channel":"direct","service_mode":"realtime","region":"global","currency":"USD","price_book_id":"pb_anthropic_claude-opus-5-5_direct_realtime_global_usd_20260922"
func TestFastRate(t *testing.T) {
	b := Default()
	if r := b.RateFor("claude-opus-5-5", true, 0); !approx(r, Rate{In: 8, Out: 40, CacheRead: 0.8, CacheWrite5m: 10, CacheWrite1h: 16}) {
		t.Errorf("fast opus = %+v", r)
	}
	if r := b.RateFor("claude-sonnet-5-5", true, 0); r != b.RateFor("claude-sonnet-5-5", false, 0) {
		t.Errorf("sonnet has no fast price, but fast = %+v", r)
	}
}

// A prompt past a tier's bound is priced at the tier's rates.
//
// ref:   cost-dev/models.json
// quote: input_tokens_per_1000000":{"basis_dimension":"input_tokens","ranges":[{"lte":100000,"price":"0.1"},{"gt":100000,"price":"0.5"}]}
func TestTieredRate(t *testing.T) {
	b := Default()
	if r := b.RateFor("claude-haiku-5-5", false, 100_000); r.In != 0.1 {
		t.Errorf("at 100k in = %v, want 0.1", r.In)
	}
	if r := b.RateFor("claude-haiku-5-5", false, 100_001); !approx(r, Rate{In: 0.5, Out: 2.5, CacheRead: 0.05, CacheWrite5m: 0.625, CacheWrite1h: 1}) {
		t.Errorf("past 100k = %+v", r)
	}
}

// Every kind is priced at its own listed rate.
//
// ref:   cost-dev/models.json
// quote: pb_anthropic_claude-opus-5-5_direct_realtime_global_usd_20260922","verified_at":"2026-09-23T00:00:00Z","source_id":"source:anthropic-pricing"},"prices":{"cache_write_1h_tokens_per_1000000":"8","cache_write_5m_tokens_per_1000000":"5","cached_input_tokens_per_1000000":"0.2","input_tokens_per_1000000":"4","output_tokens_per_1000000":"20"}
func TestCostByKind(t *testing.T) {
	tk := Tokens{Input: 110, Output: 220, CacheRead: 3_000_000, CacheWrite5m: 1000, CacheWrite1h: 50_000}
	c := tk.Of(Default().RateFor("claude-opus-5-5", false, 0))
	want := (110*4 + 1000*5 + 50_000*8 + 3_000_000*0.2 + 220*20) / 1e6
	if math.Abs(c.Total()-want) > 1e-9 {
		t.Errorf("cost = %v, want %v", c.Total(), want)
	}
	if math.Abs(c.CacheRead-0.6) > 1e-9 || math.Abs(c.CacheWrite1h-0.4) > 1e-9 {
		t.Errorf("costs = %+v", c)
	}
}

const tiny = `{"data_version":{"generated_at":"2026-10-01T00:00:00Z"},"items":[` +
	`{"id":"acme:m-1","release_date":"2026-01-01","selected_offering":{"verified_at":"2026-09-01T00:00:00Z"},` +
	`"prices":{"input_tokens_per_1000000":"2","output_tokens_per_1000000":"4"}}]}`

func TestParse(t *testing.T) {
	b, err := Parse([]byte(tiny))
	if err != nil {
		t.Fatal(err)
	}
	if r := b.RateFor("m-1", false, 0); !approx(r, Rate{In: 2, Out: 4, CacheRead: 0.2, CacheWrite5m: 2.5, CacheWrite1h: 4}) {
		t.Errorf("m-1 = %+v", r)
	}
	if !b.Generated().Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) || !b.Verified().Equal(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("generated %v, verified %v", b.Generated(), b.Verified())
	}
	same, _ := Parse([]byte(tiny))
	if same.Version() != b.Version() || Default().Version() == b.Version() {
		t.Error("a book's version names its prices")
	}
	for _, bad := range []string{
		`not json`,
		`{"items":[]}`,
		`{"items":[{"id":"acme:m","prices":{}}]}`,
		`{"items":[{"id":"acme:m","prices":{"input_tokens_per_1000000":"two","output_tokens_per_1000000":"4"}}]}`,
		`{"items":[{"id":"acme:m","prices":{"input_tokens_per_1000000":"-1","output_tokens_per_1000000":"4"}}]}`,
		`{"items":[{"id":"acme:m","prices":{"output_tokens_per_1000000":"4"}}]}`,
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("Parse(%s) took it", bad)
		}
	}
}

func TestLive(t *testing.T) {
	a, _ := Parse([]byte(tiny))
	l := NewLive(Default())
	l.Set(a)
	if l.Book() != a {
		t.Error("Set didn't swap the book in")
	}
	var none *Book
	if r := none.RateFor("opus", false, 0); r != (Rate{}) {
		t.Errorf("a nil book prices opus at %+v", r)
	}
}
