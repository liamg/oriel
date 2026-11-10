//go:build perf

package syntax

import (
	"context"
	"math"
	"os"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/liamg/oriel/tools/perf"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// A highlight query costs what its range holds, wherever it is: tree-sitter doesn't step over every
// top-level declaration before it, nor walk down from the root for each capture's parent. Each chunk the parser
// queries stays under 2 ms, so the wasm call never holds up the Go runtime for longer.
func TestHighlightQueryBudget(t *testing.T) {
	if raceEnabled || testing.Short() || testing.CoverMode() != "" {
		t.Skip("timing under -race, -short or -cover means nothing")
	}
	l, _ := Builtin().Language("go")
	// The 16 KB are read as the editor reads them, a chunk at a time, each chunk's time its best over the rounds,
	// the two files' chunks in turn: a short read gets a clean run more often than a long one, and a busy spell on
	// the machine falls on both.
	big := manyDecls()
	type read struct {
		p    *Parser
		at   int
		file int
	}
	var reads []read
	for file, c := range []struct {
		src []byte
		at  int
	}{{big, len(big) / 2}, {big[:20<<10], 0}} {
		p, err := NewParser(l)
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		p.Parse(c.src)
		if _, err := p.Highlights(c.at, c.at+16<<10); err != nil { // compiles the query
			t.Fatal(err)
		}
		for at := c.at; at < c.at+16<<10; at += highlightChunk {
			reads = append(reads, read{p, at, file})
		}
	}
	var took [2]time.Duration
	for i, d := range perf.Times(9, len(reads), nil, func(i int) { reads[i].p.Highlights(reads[i].at, reads[i].at+highlightChunk) }) {
		took[reads[i].file] += d
	}
	inBig, inSmall := took[0], took[1]
	t.Logf("16 KB: %v half way down 10,000 lines, %v at the top of 20 KB", inBig, inSmall)
	if inBig > inSmall*3/2+time.Millisecond {
		t.Errorf("16 KB half way down a 10,000-line file took %v, against %v in a 20 KB one", inBig, inSmall)
	}

	p, _ := NewParser(l)
	defer p.Close()
	p.Parse(big)
	mid := len(big) / 2
	p.Highlights(mid, mid+highlightChunk)
	// Each chunk's time is its best over three runs (perf.Times): a chunk that's slow every time is slow, one the
	// scheduler preempted, or that ran while every core was busy, isn't.
	type query struct {
		kind string
		at   int
	}
	var queries []query
	for at := mid; at < mid+64<<10; at += highlightChunk {
		for _, kind := range []string{"highlights", "injections"} {
			queries = append(queries, query{kind, at})
		}
	}
	worst := slices.Max(perf.Times(3, len(queries), nil, func(i int) {
		p.run(queries[i].kind, queries[i].at, queries[i].at+highlightChunk)
	}))
	t.Logf("a %d-byte chunk's query takes at worst %v", highlightChunk, worst)
	if worst > 2*time.Millisecond {
		t.Errorf("a chunk's query took %v; the budget is 2 ms", worst)
	}
}

// Opening a file in an already-compiled language is under 20 ms from cache: a runtime that finds the
// compiled grammar on disk compiles, instantiates and parses a fixture in that.
func TestCompiledFromCacheBudget(t *testing.T) {
	dir := t.TempDir()
	l, _ := Builtin().Language("go")
	wasm, _, err := l.load()
	if err != nil {
		t.Fatal(err)
	}
	src, _ := os.ReadFile("testdata/highlight/fixture.go")
	// open times a fresh runtime's compile, instantiate and parse, the best of rounds.
	open := func(rounds int) time.Duration {
		var rt wazero.Runtime
		defer func() { rt.Close(context.Background()) }()
		fresh := func() {
			if rt != nil {
				rt.Close(context.Background())
			}
			if rt, err = newRuntime(dir); err != nil {
				t.Fatal(err)
			}
		}
		return perf.Times(rounds, 1, fresh, func(int) {
			cm, err := rt.CompileModule(context.Background(), wasm)
			if err != nil {
				t.Fatal(err)
			}
			mod, err := rt.InstantiateModule(context.Background(), cm, wazero.NewModuleConfig().WithName("").WithStartFunctions("_initialize"))
			if err != nil {
				t.Fatal(err)
			}
			m := &module{m: mod, fns: map[string]api.Function{}}
			p := &Parser{lang: l, m: m, parser: m.call("parser_new"), queries: map[string]*query{}, symbols: map[string]uint32{}}
			p.Parse(src)
		})[0]
	}
	cold := open(1)
	warm := open(3)
	t.Logf("first open %v, from the cache %v", cold, warm)
	if warm > 20*time.Millisecond && !raceEnabled && testing.CoverMode() == "" {
		t.Errorf("opening from the cache took %v", warm)
	}
}

// TestIncrementalReparseBudget is the ticket's budget: typing a character into an identifier
// in a 600-line Go file reparses in under 1.5 ms (the median of 50 edits, the best of a few rounds). Bigger files parse off the
// keystroke path, in a Document (TestDocumentChangeBudget).
// reparseBudget: an edit in a 600-line file reparses within it.
const reparseBudget = 1500 * time.Microsecond

func TestIncrementalReparseBudget(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("timing budget: not under -short or -race")
	}
	p, buf := bigGo(t, 600)
	defer p.Close()
	lines := buf.Snapshot().Lines()
	// The best median of a few rounds of 50 edits: a round the scheduler preempted under a parallel test load
	// can't fail the budget, and a slow reparse still fails every round.
	best := time.Duration(math.MaxInt64)
	var worst time.Duration
	for round := range 5 {
		var reparses perf.Sampler
		for i := range 50 {
			ch := typeInIdentifier(t, buf, lines/4+i*(lines/100)+round)
			reparses.Time(func() { p.Update(ch) })
		}
		times := reparses.Times()
		sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
		best, worst = min(best, times[len(times)/2]), max(worst, times[len(times)-1])
		if best < reparseBudget {
			break
		}
	}
	t.Logf("%d lines: best median reparse %v, max %v", lines, best, worst)
	if best >= reparseBudget {
		t.Errorf("median incremental reparse %v, want under %v", best, reparseBudget)
	}
}

// Handing a Document a change while typing fast into a 2,000-line file returns at once: it never waits on a parse.
func TestDocumentChangeBudget(t *testing.T) {
	if raceEnabled {
		t.Skip("timing under -race means nothing")
	}
	p, buf := bigGo(t, 2000)
	p.Close()
	l, _ := Builtin().Language("go")
	d, err := NewDocument(l, buf.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	lines := buf.Snapshot().Lines()
	var slow []time.Duration // a parse waited on slows most changes; a preempted one, one or two
	for i := range 100 {
		ch := typeInIdentifier(t, buf, lines/4+i*(lines/200))
		start := time.Now()
		d.Change(ch)
		if took := time.Since(start); took > 200*time.Microsecond {
			slow = append(slow, took)
		}
	}
	if len(slow) > 2 {
		t.Errorf("handing a change to the document took over 200µs %d times of 100 (%v); it must not wait on a parse", len(slow), slow)
	}
}
