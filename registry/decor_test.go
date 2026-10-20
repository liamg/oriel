package registry_test

import (
	"testing"
	"time"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/text"
)

// Decorate asks a provider again only when what its decorations are made from changes: the buffer's text, the lines
// in view, its key, its word (DecorationsChanged), or a time it asked for.
func TestDecorateAsksAgainOnlyWhenSomethingChanged(t *testing.T) {
	r := registry.New()
	buf := text.New([]byte("a\nb\nc\n"), text.Author{})
	asked, key := 0, 1
	var refresh time.Time
	r.Decorations("p", registry.KeyedDecorations{Key: func(registry.DecorationCtx) any { return key }, Make: func(ctx registry.DecorationCtx) []registry.Decoration {
		asked++
		if !refresh.IsZero() {
			ctx.RefreshAt(refresh)
		}
		return []registry.Decoration{{Kind: registry.Sign, Line: 1, Text: "x"}}
	}})
	now := time.Unix(1000, 0)
	var woken time.Time
	ctx := func() registry.DecorationCtx {
		return registry.DecorationCtx{Window: "1", Path: "/f", Buffer: buf, Snap: buf.Snapshot(), Top: 0, Bottom: 3, Now: now,
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
