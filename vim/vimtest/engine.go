package vimtest

import (
	"flag"
	"fmt"
	"os"
	"slices"
	"testing"
)

var oracleFlag = flag.Bool("oracle", false, "check every vimtest case against headless nvim")

// Oracle reports whether oracle mode is on: the -oracle test flag, or ORIEL_ORACLE=1 in the
// environment (which also works for test binaries that don't link vimtest).
func Oracle() bool {
	return *oracleFlag || os.Getenv("ORIEL_ORACLE") == "1"
}

// Engine is what a case runs against: our vim engine, or nvim in oracle mode.
type Engine interface {
	// Load replaces the buffer and cursor. A selection in b starts charwise visual mode.
	Load(b Buffer) error
	Feed(keys []Key) error
	Snapshot(q Query) (Result, error)
	Close() error
}

// Query says which optional state a Snapshot reads.
type Query struct {
	Regs  []rune
	Marks []rune
	Jumps bool
	Msg   bool
	Win   bool
	Top   bool
	Bufs  bool
}

// Result is an engine's state after a case's keys.
type Result struct {
	Source string // who produced it, for failure messages: "engine", "nvim v0.11.7"…

	Buffer // in charwise visual mode, Visual holds the selection and its anchor
	Mode   string
	Regs   map[rune]*string
	Marks  map[rune]*Pos
	Jumps  []Pos
	Msg    string
	Win    Win
	Top    int    // 1-based
	Bufs   string // the listed buffers, as a case's `bufs:` writes them

	Undo *Buffer // the buffer after the keys and `u`, when the case checks it
	Dot  *Buffer // the buffer after the keys and `.`, when the case checks it
}

// NewEngine starts a fresh engine for one run.
type NewEngine func() (Engine, error)

// Run plays the case's keys on fresh engines and reads back everything the case declares.
func Run(t testing.TB, newEngine NewEngine, c Case) Result {
	t.Helper()
	q := Query{Jumps: c.Jumps != nil, Msg: c.Msg != nil, Win: c.Win != nil, Top: c.Top != nil, Bufs: c.Bufs != nil}
	for r := range c.Regs {
		q.Regs = append(q.Regs, r)
	}
	for r := range c.Marks {
		q.Marks = append(q.Marks, r)
	}
	slices.Sort(q.Regs)
	slices.Sort(q.Marks)

	got, err := play(newEngine, c, nil, q)
	if err != nil {
		t.Fatalf("%s:%d: %v", c.File, c.Line, err)
	}
	after := func(extra string) *Buffer {
		r, err := play(newEngine, c, ParseKeys(extra), Query{})
		if err != nil {
			t.Fatalf("%s:%d: %v", c.File, c.Line, err)
		}
		return &r.Buffer
	}
	if c.Undo != nil {
		got.Undo = after("<Esc>u")
	}
	if c.Dot != nil {
		got.Dot = after("<Esc>.")
	}
	return got
}

func play(newEngine NewEngine, c Case, extra []Key, q Query) (Result, error) {
	e, err := newEngine()
	if err != nil {
		return Result{}, err
	}
	defer e.Close()
	if err := e.Load(c.Given); err != nil {
		return Result{}, fmt.Errorf("load: %w", err)
	}
	if err := e.Feed(c.Keys); err != nil {
		return Result{}, fmt.Errorf("keys %q: %w", c.RawKeys, err)
	}
	if extra != nil {
		if err := e.Feed(extra); err != nil {
			return Result{}, fmt.Errorf("keys %q: %w", Notation(extra), err)
		}
	}
	return e.Snapshot(q)
}

// Compare reports every declared expectation that got doesn't meet.
func (c Case) Compare(t testing.TB, got Result) {
	t.Helper()
	for _, d := range c.Diff(got) {
		t.Errorf("%s:%d: %s: %s", c.File, c.Line, got.Source, d)
	}
}

// Diff lists the declared expectations got doesn't meet.
func (c Case) Diff(got Result) []string {
	var diffs []string
	add := func(format string, args ...any) { diffs = append(diffs, fmt.Sprintf(format, args...)) }
	if want, have := c.Expect.String(), comparable(c.Expect, got.Buffer).String(); want != have {
		add("buffer:\n got: %s\nwant: %s", have, want)
	}
	if c.Mode != "" && c.Mode != got.Mode {
		add("mode: got %s, want %s", got.Mode, c.Mode)
	}
	for _, r := range sortedKeys(c.Regs) {
		if want, have := regString(c.Regs[r]), regString(got.Regs[r]); want != have {
			add("register %q: got %s, want %s", r, have, want)
		}
	}
	for _, r := range sortedKeys(c.Marks) {
		if want, have := posString(c.Marks[r]), posString(got.Marks[r]); want != have {
			add("mark %q: got %s, want %s", r, have, want)
		}
	}
	if c.Jumps != nil && !slices.Equal(c.Jumps, got.Jumps) {
		add("jumps: got %v, want %v", got.Jumps, c.Jumps)
	}
	if c.Msg != nil && *c.Msg != got.Msg {
		add("msg: got %q, want %q", got.Msg, *c.Msg)
	}
	if c.Win != nil && *c.Win != got.Win {
		add("win: got %s, want %s", got.Win, c.Win)
	}
	if c.Top != nil && *c.Top != got.Top {
		add("top: got %d, want %d", got.Top, *c.Top)
	}
	if c.Bufs != nil && *c.Bufs != got.Bufs {
		add("bufs: got %q, want %q", got.Bufs, *c.Bufs)
	}
	for _, b := range []struct {
		name      string
		want, got *Buffer
	}{{"undo", c.Undo, got.Undo}, {"dot", c.Dot, got.Dot}} {
		if b.want == nil {
			continue
		}
		if b.got == nil {
			add("%s: not run", b.name)
		} else if want, have := b.want.String(), comparable(*b.want, *b.got).String(); want != have {
			add("%s:\n got: %s\nwant: %s", b.name, have, want)
		}
	}
	return diffs
}

// comparable shapes got like want: a selection only when want has one, an anchor only when want gives it.
func comparable(want, got Buffer) Buffer {
	if want.Visual == nil || got.Visual == nil {
		got.Visual = nil
		return got
	}
	v := *got.Visual
	v.AnchorSet = want.Visual.AnchorSet
	got.Visual = &v
	return got
}

func regString(s *string) string {
	if s == nil {
		return unset
	}
	return fmt.Sprintf("%q", *s)
}

func posString(p *Pos) string {
	if p == nil {
		return unset
	}
	return p.String()
}

func sortedKeys[V any](m map[rune]V) []rune {
	keys := make([]rune, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
