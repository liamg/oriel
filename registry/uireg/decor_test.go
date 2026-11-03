package uireg_test

import (
	"slices"
	"testing"
	"time"

	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/text"
)

// Decorate asks a provider again only when what its decorations are made from changes: the buffer's text, the lines
// in view, its key, its word (DecorationsChanged), or a time it asked for.
func TestDecorateAsksAgainOnlyWhenSomethingChanged(t *testing.T) {
	r := uireg.New()
	buf := text.New([]byte("a\nb\nc\n"), text.Author{})
	asked, key := 0, 1
	var refresh time.Time
	r.Decorations("p", uireg.KeyedDecorations{Key: func(uireg.DecorationCtx) any { return key }, Make: func(ctx uireg.DecorationCtx) []uireg.Decoration {
		asked++
		if !refresh.IsZero() {
			ctx.RefreshAt(refresh)
		}
		return []uireg.Decoration{{Kind: uireg.Sign, Line: 1, Text: "x"}}
	}})
	now := time.Unix(1000, 0)
	var woken time.Time
	ctx := func() uireg.DecorationCtx {
		return uireg.DecorationCtx{Window: "1", Path: "/f", Buffer: buf, Snap: buf.Snapshot(), Top: 0, Bottom: 3, Now: now,
			Wake: func(t time.Time) { woken = t }}
	}
	step := func(what string, want int) {
		t.Helper()
		if ds := r.Decorate(ctx()); len(ds[1]) != 1 {
			t.Fatalf("%s: decorations %v", what, ds)
		}
		if asked != want {
			t.Errorf("%s: asked %d times, want %d", what, asked, want)
		}
	}
	step("first", 1)
	step("nothing changed", 1)
	buf.Apply(text.Edit{Ranges: []text.Replace{{Range: text.Range{Start: 0, End: 0}, Text: []byte("z")}}})
	step("an edit", 2)
	key = 2
	step("its key", 3)
	r.DecorationsChanged("p")
	step("its word", 4)
	refresh = now.Add(time.Second)
	r.DecorationsChanged("p")
	step("asking for a refresh", 5)
	if !woken.Equal(refresh) {
		t.Errorf("woken for %v, want %v", woken, refresh)
	}
	step("before the refresh", 5)
	now = now.Add(time.Second)
	step("at the refresh", 6)
}

// A line with room for one sign shows the one with the highest priority, and its menu offers each of the others'
// commands and menus.
func TestTopSignKeepsTheOthersInItsMenu(t *testing.T) {
	q := uireg.Decoration{Kind: uireg.Sign, Text: "?", Priority: 15, Command: "explain.unit", Tooltip: "Explain f"}
	run := uireg.Decoration{Kind: uireg.Sign, Text: "▶", Priority: 20, Command: "test.run", Menu: []uireg.SignItem{{Label: "Debug", Command: "test.debug"}}}
	bp := uireg.Decoration{Kind: uireg.Sign, Text: "●", Priority: 10, Menu: []uireg.SignItem{{Label: "Remove", Command: "debug.remove"}}}
	d, ok := uireg.TopSign([]uireg.Decoration{q, run, bp})
	if !ok || d.Text != "▶" || d.Command != "test.run" {
		t.Fatalf("top sign %q %q", d.Text, d.Command)
	}
	var got []string
	for _, it := range d.Menu {
		got = append(got, it.Label+"="+it.Command)
	}
	want := []string{"Debug=test.debug", "Remove=debug.remove", "Explain f=explain.unit"}
	if !slices.Equal(got, want) {
		t.Errorf("menu %v, want %v", got, want)
	}
	if len(run.Menu) != 1 {
		t.Errorf("the ▶'s own menu changed: %v", run.Menu)
	}
	if _, ok := uireg.TopSign(nil); ok {
		t.Error("a top sign of none")
	}
}
