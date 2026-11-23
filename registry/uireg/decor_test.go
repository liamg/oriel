package uireg_test

import (
	"slices"
	"testing"
	"time"

	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/text"
)

// Decorate asks a provider again only when what its decorations are made from changes: the buffer's text, the lines
// in view, its key, its word (RefreshDecorations), or a time it asked for.
func TestDecorateAsksAgainOnlyWhenSomethingChanged(t *testing.T) {
	r := uireg.New()
	buf := text.New([]byte("a\nb\nc\n"), text.Author{})
	asked, key := 0, 1
	var refresh time.Time
	r.Decoration("p", uireg.KeyedDecorations{Key: func(uireg.DecorationCtx) any { return key }, Make: func(ctx uireg.DecorationCtx) []uireg.Decoration {
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
	if _, err := buf.Apply(text.Edit{Ranges: []text.Replace{{Range: text.Range{Start: 0, End: 0}, Text: []byte("z")}}}); err != nil {
		t.Fatal(err)
	}
	step("an edit", 2)
	key = 2
	step("its key", 3)
	r.RefreshDecorations("p")
	step("its word", 4)
	refresh = now.Add(time.Second)
	r.RefreshDecorations("p")
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
	q := uireg.Decoration{Kind: uireg.Sign, Text: "?", Priority: 15, Command: "explain.unit.ask", Tooltip: "Explain f"}
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
	want := []string{"Debug=test.debug", "Remove=debug.remove", "Explain f=explain.unit.ask"}
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

// A line's + (an OnHover sign) never takes the place of another sign, whatever its priority: it's in that one's menu.
func TestAHoverSignGoesInAnotherSignsMenu(t *testing.T) {
	plus := uireg.Decoration{Kind: uireg.Sign, Text: "+", Priority: 50, Command: "thread.new", Tooltip: "Comment on line 3", OnHover: true}
	bp := uireg.Decoration{Kind: uireg.Sign, Text: "●", Priority: 10, Command: "debug.toggle"}
	d, ok := uireg.TopSign([]uireg.Decoration{bp, plus})
	if !ok || d.Text != "●" {
		t.Fatalf("top sign %q, want the breakpoint's", d.Text)
	}
	if len(d.Menu) != 1 || d.Menu[0].Command != "thread.new" {
		t.Errorf("menu %v, want the + in it", d.Menu)
	}
	if d, _ := uireg.TopSign([]uireg.Decoration{plus}); d.Text != "+" {
		t.Errorf("alone, the top sign is %q", d.Text)
	}
}

// One EveryLine sign stands for one on each line, however long the file: Decorate keeps it under EveryLine, and on a
// line it runs with that line, the provider's own args left as they were.
func TestAnEveryLineSignIsOnEachLine(t *testing.T) {
	r := uireg.New()
	buf := text.New([]byte("a\nb\nc\n"), text.Author{})
	plus := uireg.Decoration{Kind: uireg.Sign, Line: uireg.EveryLine, Text: "+", Command: "thread.new", Args: map[string]any{"file": "/f"},
		Tooltip: "Comment on line {line}"}
	r.Decoration("p", uireg.DecorationFunc(func(uireg.DecorationCtx) []uireg.Decoration { return []uireg.Decoration{plus} }))
	ds := r.Decorate(uireg.DecorationCtx{Window: "1", Path: "/f", Buffer: buf, Snap: buf.Snapshot(), Top: 1, Bottom: 3})
	if len(ds) != 1 || len(ds[uireg.EveryLine]) != 1 {
		t.Fatalf("decorations %v, want the one sign under EveryLine", ds)
	}
	on := ds[uireg.EveryLine][0].OnLine(2)
	if on.Line != 2 || on.Args.Int("line") != 3 || on.Args.String("file") != "/f" || on.Tooltip != "Comment on line 3" {
		t.Errorf("on line 2 (0-based): %+v", on)
	}
	if _, ok := plus.Args["line"]; ok {
		t.Error("putting it on a line changed the provider's args")
	}
}

// An Old decoration is on a line of the file before the change: only a presenter that shows removed lines gets it,
// past the end of the new file too, and one that doesn't never sees it on a new line of the same number.
func TestAnOldDecorationGoesOnlyToAPresenterOfRemovedLines(t *testing.T) {
	r := uireg.New()
	buf := text.New([]byte("a\nb\n"), text.Author{})
	r.Decoration("p", uireg.DecorationFunc(func(uireg.DecorationCtx) []uireg.Decoration {
		return []uireg.Decoration{
			{Kind: uireg.Block, Line: 1, Text: "new"},
			{Kind: uireg.Block, Line: 1, Old: true, Text: "old"},
			{Kind: uireg.Block, Line: 5, Old: true, Text: "old, past the end"},
		}
	}))
	texts := func(ds []uireg.Decoration) (out []string) {
		for _, d := range ds {
			out = append(out, d.Text)
		}
		return out
	}
	ctx := uireg.DecorationCtx{Window: "editor", Path: "/f", Buffer: buf, Snap: buf.Snapshot(), Top: 0, Bottom: 2}
	ds := r.Decorate(ctx)
	if got := texts(ds[1]); !slices.Equal(got, []string{"new"}) || len(ds[5]) != 0 {
		t.Errorf("an editor got line 1 %v, line 5 %v; want only the new one", got, texts(ds[5]))
	}
	ctx.Window, ctx.Removed = "diff", true
	ds = r.Decorate(ctx)
	if got := texts(ds[1]); !slices.Equal(got, []string{"new", "old"}) {
		t.Errorf("a diff got line 1 %v, want both", got)
	}
	if got := texts(ds[5]); !slices.Equal(got, []string{"old, past the end"}) {
		t.Errorf("a diff got line 5 %v, want the old one past the new file's end", got)
	}
}
