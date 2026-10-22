package search

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

func fixture(t *testing.T) string {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"internal/pricing/round.go": "package pricing\n\nfunc Round(v int) int { return v * 100 }\n\nfunc RoundMinor(v int) int { return Round(v) / 100 }\n",
		"internal/pricing/tiers.go": "package pricing\n\n// ROUND is loud.\nconst ROUND = 1\n",
		"gen/api.pb.go":             "package gen\n\nfunc Round() {}\n",
		"notes.txt":                 "round and round\n",
		"bin.dat":                   "Round\x00\x01",
	} {
		p := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	return dir
}

func collect(t *testing.T, q Query, engine Engine) []string {
	t.Helper()
	var mu sync.Mutex
	var out []string
	err := NewTools().run(context.Background(), q, engine, func(ms []Match) {
		mu.Lock()
		defer mu.Unlock()
		for _, m := range ms {
			out = append(out, fmt.Sprintf("%s:%d:%v %s", m.Path, m.Line, m.Spans, m.Text))
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

// ripgrep and the Go matcher find the same lines and spans: both are PCRE2, binary files are
// skipped, and the excluded files are left out.
func TestEnginesAgree(t *testing.T) {
	if _, ok := NewTools().Ripgrep(); !ok {
		t.Skip("rg isn't installed")
	}
	dir := fixture(t)
	for _, q := range []Query{
		{Pattern: `Round\(`},
		{Pattern: `(?<=return )Round`},
		{Pattern: `round`, IgnoreCase: true},
		{Pattern: `\bROUND\b`},
		{Pattern: `Round`, Exclude: []string{"gen/api.pb.go"}},
		{Pattern: `Round`, Globs: []string{"*.txt"}, IgnoreCase: true},
	} {
		q.Dir = dir
		rg, gosearch := collect(t, q, Rg), collect(t, q, Go)
		if strings.Join(rg, "\n") != strings.Join(gosearch, "\n") {
			t.Errorf("%+v:\nrg:\n%s\ngo:\n%s", q, strings.Join(rg, "\n"), strings.Join(gosearch, "\n"))
		}
		if len(rg) == 0 {
			t.Errorf("%+v found nothing", q)
		}
	}
}

func TestExcludeAndSpans(t *testing.T) {
	dir := fixture(t)
	got := collect(t, Query{Dir: dir, Pattern: `Round`, Exclude: []string{"gen/api.pb.go"}}, Auto)
	want := []string{
		"internal/pricing/round.go:3:[[5 10]] func Round(v int) int { return v * 100 }",
		"internal/pricing/round.go:5:[[5 10] [36 41]] func RoundMinor(v int) int { return Round(v) / 100 }",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A bad pattern is an error, not an empty result.
func TestBadPattern(t *testing.T) {
	dir := fixture(t)
	for _, e := range []Engine{Go, Auto} {
		if err := NewTools().run(context.Background(), Query{Dir: dir, Pattern: `(unclosed`}, e, func([]Match) {}); err == nil {
			t.Errorf("engine %v: no error for a bad pattern", e)
		}
	}
}

// A replace plan is every matched line with its matches replaced as PCRE2's substitute does, per file, with the
// hunks that make it; nothing is written until it's applied.
func TestPlanAndApply(t *testing.T) {
	dir := fixture(t)
	var ms []Match
	NewTools().run(context.Background(), Query{Dir: dir, Pattern: `Round(\w*)\(`, Exclude: []string{"gen/api.pb.go"}}, Auto, func(b []Match) { ms = append(ms, b...) })
	plan, err := Plan(Query{Dir: dir, Pattern: `Round(\w*)\(`}, "Rounded$1(", ms, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 1 || plan[0].Path != "internal/pricing/round.go" || plan[0].Count != 3 || len(plan[0].Hunks) != 2 {
		t.Fatalf("plan: %+v", plan)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "internal/pricing/round.go")); strings.Contains(string(b), "Rounded") {
		t.Fatal("planning wrote the file")
	}
	if err := plan[0].Write(dir); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "internal/pricing/round.go"))
	want := "package pricing\n\nfunc Rounded(v int) int { return v * 100 }\n\nfunc RoundedMinor(v int) int { return Rounded(v) / 100 }\n"
	if string(b) != want {
		t.Errorf("after applying:\n%s", b)
	}
	// A file that changed since the search isn't overwritten.
	os.WriteFile(filepath.Join(dir, "internal/pricing/round.go"), []byte("changed\n"), 0o644)
	if err := plan[0].Write(dir); err == nil {
		t.Error("applying over a file that changed since the plan didn't fail")
	}
}
