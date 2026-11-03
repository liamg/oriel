package price

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// URL is cost.dev's model list, which a Book is read from.
const URL = "https://api.cost.dev/v1/models"

// Source names where the prices come from.
const Source = "cost.dev"

//go:embed models.json
var snapshot []byte

// Snapshot is the model list vendored with Oriel: spec/cost-dev/models.json.
func Snapshot() []byte { return bytes.Clone(snapshot) }

// Default is the snapshot's book. It parses the snapshot each call: keep the book.
func Default() *Book {
	b, err := Parse(snapshot)
	if err != nil {
		panic("price: the vendored snapshot: " + err.Error())
	}
	return b
}

// Books is where the current book is had.
type Books interface{ Book() *Book }

// Or is s, or the snapshot's book when s is nil.
func Or(s Books) Books {
	if s == nil {
		return Default()
	}
	return s
}

// Live is a book that's replaced as fresher prices come.
type Live struct{ p atomic.Pointer[Book] }

// NewLive starts with b.
func NewLive(b *Book) *Live {
	l := &Live{}
	l.p.Store(b)
	return l
}

func (l *Live) Book() *Book { return l.p.Load() }

// Set swaps b in.
func (l *Live) Set(b *Book) { l.p.Store(b) }

// Model is one model's list prices.
type Model struct {
	ID       string // "anthropic:claude-opus-5-5"
	Name     string // "claude-opus-5-5"
	Aliases  []string
	Released string // YYYY-MM-DD
	Current  bool
	Verified time.Time // when cost.dev last checked the prices against the provider's
	Rate     Rate
	Fast     Rate   // fast mode's; zero when it has none
	Tiers    []Tier // dearer prices for longer prompts, by Above
}

// Tier is the rate of a prompt longer than Above tokens.
type Tier struct {
	Above int64
	Rate  Rate
}

// Book is a model list's prices.
type Book struct {
	models    []Model
	names     map[string]int // id, name and aliases
	lines     map[string]int // a Claude line ("opus") to its newest model
	generated time.Time
	verified  time.Time
	version   string
}

func (b *Book) Book() *Book { return b }

// Generated is when cost.dev built the list.
func (b *Book) Generated() time.Time { return b.generated }

// Verified is the latest any of its prices was checked.
func (b *Book) Verified() time.Time { return b.verified }

// Version names the prices: two books with the same prices have the same one.
func (b *Book) Version() string {
	if b == nil {
		return ""
	}
	return b.version
}

// RateFor is what model's tokens cost in a request whose prompt is prompt tokens (0 when it isn't known), in fast
// mode if fast and the model has one. A model with no price is a zero Rate.
func (b *Book) RateFor(model string, fast bool, prompt int64) Rate {
	m, ok := b.Find(model)
	if !ok {
		return Rate{}
	}
	if fast && m.Fast != (Rate{}) {
		return m.Fast
	}
	r := m.Rate
	for _, t := range m.Tiers {
		if prompt > t.Above {
			r = t.Rate
		}
	}
	return r
}

var (
	dated  = regexp.MustCompile(`-(\d{8}|\d{4}-\d{2}-\d{2})$`)
	suffix = regexp.MustCompile(`\[[^\]]*\]$`)
)

// claudeAliases are Claude Code's model aliases that aren't a line's name.
var claudeAliases = map[string]string{"opusplan": "opus"}

// Find is the model a session's model name means: the list's id, name or alias; then without a date or the
// provider's prefix; a bare Claude line ("opus") is its newest model;
// then the name less its trailing parts ("gpt-5-codex" is gpt-5). A model it can't find has no price: a guess would
// show a cost as known that isn't.
func (b *Book) Find(model string) (Model, bool) {
	if b == nil {
		return Model{}, false
	}
	name := strings.ToLower(strings.TrimSpace(model))
	if i, ok := b.names[name]; ok {
		return b.models[i], true
	}
	name = suffix.ReplaceAllString(name, "")
	if i := strings.LastIndexAny(name, ":/"); i >= 0 {
		name = name[i+1:]
	}
	name = dated.ReplaceAllString(name, "")
	if a, ok := claudeAliases[name]; ok {
		name = a
	}
	if i, ok := b.lines[name]; ok {
		return b.models[i], true
	}
	for n := name; n != ""; {
		if i, ok := b.names[n]; ok {
			return b.models[i], true
		}
		cut := strings.LastIndexByte(n, '-')
		if cut < 0 {
			break
		}
		n = n[:cut]
	}
	return Model{}, false
}

// list is the part of cost.dev's /v1/models read here.
type list struct {
	DataVersion struct {
		GeneratedAt time.Time `json:"generated_at"`
	} `json:"data_version"`
	Items []struct {
		ID               string            `json:"id"`
		Aliases          []string          `json:"aliases"`
		LifecycleStatus  string            `json:"lifecycle_status"`
		Latest           bool              `json:"latest"`
		ReleaseDate      string            `json:"release_date"`
		Prices           map[string]string `json:"prices"`
		FastPrices       map[string]string `json:"fast_prices"`
		PriceTiers       map[string]tiered `json:"price_tiers"`
		SelectedOffering struct {
			VerifiedAt time.Time `json:"verified_at"`
		} `json:"selected_offering"`
	} `json:"items"`
}

type tiered struct {
	Basis  string `json:"basis_dimension"`
	Ranges []struct {
		Lte   *int64 `json:"lte"`
		Gt    *int64 `json:"gt"`
		Price string `json:"price"`
	} `json:"ranges"`
}

// The price fields, in Rate's order.
var fields = [...]string{"input_tokens_per_1000000", "output_tokens_per_1000000", "cached_input_tokens_per_1000000",
	"cache_write_5m_tokens_per_1000000", "cache_write_1h_tokens_per_1000000"}

// Parse reads cost.dev's /v1/models response. It refuses one that isn't JSON, prices no model, or has a price that
// isn't a number.
func Parse(data []byte) (*Book, error) {
	var l list
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("cost.dev models: %w", err)
	}
	b := &Book{names: map[string]int{}, lines: map[string]int{}, generated: l.DataVersion.GeneratedAt}
	for _, it := range l.Items {
		if len(it.Prices) == 0 || it.ID == "" {
			continue
		}
		m := Model{ID: strings.ToLower(it.ID), Released: it.ReleaseDate, Current: it.LifecycleStatus == "" || it.LifecycleStatus == "current",
			Verified: it.SelectedOffering.VerifiedAt}
		_, m.Name, _ = strings.Cut(m.ID, ":")
		if m.Name == "" {
			m.Name = m.ID
		}
		raw, err := rawOf(it.Prices)
		if err != nil {
			return nil, fmt.Errorf("cost.dev models: %s: %w", it.ID, err)
		}
		if !raw.has[0] || !raw.has[1] {
			return nil, fmt.Errorf("cost.dev models: %s has no input or output price", it.ID)
		}
		m.Rate = raw.rate()
		if len(it.FastPrices) > 0 {
			fast, err := rawOf(it.FastPrices)
			if err != nil {
				return nil, fmt.Errorf("cost.dev models: %s fast: %w", it.ID, err)
			}
			if fast.has[0] && fast.has[1] {
				m.Fast = fast.rate()
			}
		}
		if m.Tiers, err = tiersOf(raw, it.PriceTiers); err != nil {
			return nil, fmt.Errorf("cost.dev models: %s tiers: %w", it.ID, err)
		}
		for _, a := range it.Aliases {
			m.Aliases = append(m.Aliases, strings.ToLower(a))
		}
		if m.Verified.After(b.verified) {
			b.verified = m.Verified
		}
		b.models = append(b.models, m)
	}
	if len(b.models) == 0 {
		return nil, errors.New("cost.dev models: no model has a price")
	}
	b.index()
	return b, nil
}

// index fills the book's lookups.
func (b *Book) index() {
	h := sha256.New()
	for i, m := range b.models {
		for _, n := range append([]string{m.ID, m.Name}, m.Aliases...) {
			if _, ok := b.names[n]; !ok {
				b.names[n] = i
			}
		}
		if line := claudeLine(m.ID); line != "" {
			if j, ok := b.lines[line]; !ok || newer(m, b.models[j]) {
				b.lines[line] = i
			}
		}
		fmt.Fprintf(h, "%s %v %v %v %v\n", m.ID, m.Aliases, m.Rate, m.Fast, m.Tiers)
	}
	b.version = hex.EncodeToString(h.Sum(nil))[:16]
}

// claudeLine is the line of an Anthropic model's id: "opus" for anthropic:claude-opus-5-5.
func claudeLine(id string) string {
	rest, ok := strings.CutPrefix(id, "anthropic:claude-")
	if !ok {
		return ""
	}
	line, _, _ := strings.Cut(rest, "-")
	if line == "" || strings.ContainsFunc(line, func(r rune) bool { return r < 'a' || r > 'z' }) {
		return ""
	}
	return line
}

// newer says whether a is the one to use over b: a current model, released later.
func newer(a, b Model) bool {
	if a.Current != b.Current {
		return a.Current
	}
	return cmp.Compare(a.Released, b.Released) > 0
}

// raw is a price list's fields as listed, in Rate's order.
type raw struct {
	v   [len(fields)]float64
	has [len(fields)]bool
}

func rawOf(prices map[string]string) (raw, error) {
	var r raw
	for i, f := range fields {
		s, ok := prices[f]
		if !ok {
			continue
		}
		v, err := dollars(s)
		if err != nil {
			return raw{}, fmt.Errorf("%s: %w", f, err)
		}
		r.v[i], r.has[i] = v, true
	}
	return r, nil
}

func dollars(s string) (float64, error) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0, fmt.Errorf("not a price: %q", s)
	}
	return v, nil
}

// rate is r, with a kind it doesn't list priced by its multiplier over input.
func (r raw) rate() Rate {
	or := func(i int, mul float64) float64 {
		if r.has[i] {
			return r.v[i]
		}
		return r.v[0] * mul
	}
	return Rate{In: r.v[0], Out: r.v[1], CacheRead: or(2, mulRead), CacheWrite5m: or(3, mulWrite5m), CacheWrite1h: or(4, mulWrite1h)}
}

// tiersOf are the rates past each of the tiers' lower bounds, over the prompt's tokens.
func tiersOf(base raw, tiers map[string]tiered) ([]Tier, error) {
	var bounds []int64
	for _, t := range tiers {
		if t.Basis != "input_tokens" {
			continue
		}
		for _, r := range t.Ranges {
			if r.Gt != nil {
				bounds = append(bounds, *r.Gt)
			}
		}
	}
	slices.Sort(bounds)
	bounds = slices.Compact(bounds)
	var out []Tier
	for _, above := range bounds {
		r := base
		for i, f := range fields {
			t, ok := tiers[f]
			if !ok || t.Basis != "input_tokens" {
				continue
			}
			for _, rg := range t.Ranges {
				if (rg.Gt == nil || above >= *rg.Gt) && (rg.Lte == nil || above < *rg.Lte) {
					v, err := dollars(rg.Price)
					if err != nil {
						return nil, fmt.Errorf("%s: %w", f, err)
					}
					r.v[i], r.has[i] = v, true
				}
			}
		}
		out = append(out, Tier{Above: above, Rate: r.rate()})
	}
	return out, nil
}
