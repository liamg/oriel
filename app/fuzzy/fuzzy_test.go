package fuzzy

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
)

// Rank orders candidates by their score against a query, best first; ties keep the candidates' order.
func rank(query string, cands []string) []string {
	terms := Parse(query)
	type hit struct {
		s     string
		score int
		pos   []int
	}
	var hits []hit
	for _, c := range cands {
		if score, pos, ok := MatchQuery(terms, c); ok {
			hits = append(hits, hit{c, score, pos})
		}
	}
	slices.SortStableFunc(hits, func(a, b hit) int { return b.score - a.score })
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = fmt.Sprintf("%4d  %s", h.score, mark(h.s, h.pos))
	}
	return out
}

// mark brackets the matched characters: "[p]r[i]ce".
func mark(s string, pos []int) string {
	in := map[int]bool{}
	for _, p := range pos {
		in[p] = true
	}
	var b strings.Builder
	for i, r := range []rune(s) {
		if in[i] {
			fmt.Fprintf(&b, "[%c]", r)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

var candidates = []string{
	"pricing api usage", "pricing-rounding", "price-feed backfill", "New plan from price…", "Review PR by URL…",
	"Run job: pricing test sweep", "internal/pricing/round.go", "internal/pricing/tiers.go", "PriceForTier",
	"ENG-2240 Price cache invalidation on plan change", "#4412 fix: price lookup for spot instances",
	"cmd/oriel/main.go", "app/editor.go", "app/editorpane.go", "app/notifications.go", "vim/scroll.go",
	"layout.move", "layout.zoom", "editor.open", "view.open", "Log", "messages", "Events",
	"render/typeset/typeset.go", "ui/widget/notice.go", "_design/PLAN.md", "app/logpanel.go",
}

// "fuzzy ranking golden tests": each query's ranking over the candidates, in testdata/rank.golden.
func TestRankingGolden(t *testing.T) {
	var b strings.Builder
	for _, q := range []string{"pri", "pr", "round", "rg", "edp", "app/ed", "ap ed", "'price", "^app", ".go$", "!app .go$",
		"PFT", "pft", "layout", "lmv", "open", "notif", "msg", "tsg"} {
		fmt.Fprintf(&b, "== %s\n", q)
		for _, l := range rank(q, candidates) {
			b.WriteString(l + "\n")
		}
	}
	got := b.String()
	if os.Getenv("ORIEL_UPDATE") != "" {
		os.WriteFile("testdata/rank.golden", []byte(got), 0o644)
	}
	want, err := os.ReadFile("testdata/rank.golden")
	if err != nil {
		t.Fatalf("%v (ORIEL_UPDATE=1 writes it)", err)
	}
	if got != string(want) {
		t.Errorf("ranking changed:\n%s", got)
	}
}

// The scoring rules, each on its own.
func TestScoringRules(t *testing.T) {
	better := func(q, a, b string) {
		t.Helper()
		sa, _, oka := Match(q, a)
		sb, _, okb := Match(q, b)
		if !oka || !okb || sa <= sb {
			t.Errorf("%q: %q (%d) should beat %q (%d)", q, a, sa, b, sb)
		}
	}
	better("pri", "pricing", "spring")                  // the start of a word
	better("round", "round.go", "r-o-u-n-d")            // consecutive
	better("tiers", "pricing/tiers.go", "pricingtiers") // after a path separator
	better("pft", "PriceForTier", "pxfxxt")             // camel humps
	better("pa", "pricing api", "pxxxxa")               // a shorter gap
	if _, _, ok := Match("abc", "acb"); ok {
		t.Error("abc matched acb out of order")
	}
	if _, _, ok := Match("Ab", "ab"); ok {
		t.Error("an uppercase pattern character matched lowercase text")
	}
	if _, pos, ok := Match("rg", "pricing-rounding"); !ok || len(pos) != 2 {
		t.Errorf("rg: %v %v", pos, ok)
	}
}

func TestQuerySyntax(t *testing.T) {
	for q, want := range map[string][]Term{
		"foo bar": {{Text: "foo"}, {Text: "bar"}},
		"'ex":     {{Text: "ex", Exact: true}},
		"^pre":    {{Text: "pre", Prefix: true}},
		"suf$":    {{Text: "suf", Suffix: true}},
		"!no":     {{Text: "no", Not: true, Exact: true}},
		"^all$":   {{Text: "all", Prefix: true, Suffix: true}},
	} {
		if got := Parse(q); !slices.Equal(got, want) {
			t.Errorf("Parse(%q) = %+v, want %+v", q, got, want)
		}
	}
}
