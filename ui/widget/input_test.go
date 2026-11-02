package widget

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/internal/testhook"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// A message's field (a comment, a reply): ⏎ submits it and ⇧⏎ starts a new line; it grows with its lines, Up and
// Down move between them, and what's submitted keeps its newlines.
func TestInputEnterSubmits(t *testing.T) {
	text := ""
	var id ui.ID
	var res InputResult
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		c.Column("col", ui.Style{Pad: ui.Pad(10), W: ui.Px(300)}, func(c *ui.Ctx) {
			id = c.Next("reply")
			res = Input(c, "reply", &text, InputOpts{Label: "Reply", Lines: 6, EnterSubmits: true, Help: "test/input"})
		})
	})
	one := h.rect(id).Dy()
	h.click(mid(h.rect(id)))
	h.keys("a", "b", "c", "+CR")
	if res.Submitted || text != "abc\n" {
		t.Fatalf("⇧⏎: text %q, submitted %v; want a new line", text, res.Submitted)
	}
	h.keys("a", "b", "c")
	if two := h.rect(id).Dy(); two <= one {
		t.Errorf("two lines are %v high, one %v: it should grow", two, one)
	}
	h.keys("Up", "X")
	if text != "abcX\nabc" {
		t.Errorf("Up from the end of the second line, then X: %q", text)
	}
	h.keys("Down", "Y")
	if text != "abcX\nabcY" {
		t.Errorf("Down, then Y: %q", text)
	}
	h.keys("CR")
	if !res.Submitted || text != "abcX\nabcY" {
		t.Errorf("⏎: submitted %v, text %q; want it submitted with its newline", res.Submitted, text)
	}
}

// A selection in a text field is painted where the eye finds it: select all (⌘A on macOS, Ctrl+A elsewhere) paints
// a rect behind the whole text, distinct from the field (1.5:1 or more) with the text still readable on it (4.5:1),
// in every built-in theme, one line or several.
func TestInputSelectionVisible(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		for _, src := range theme.Builtins() {
			for _, v := range []theme.Variant{theme.Dark, theme.Light} {
				for _, lines := range []int{0, 4} {
					t.Run(fmt.Sprintf("%s/%s/%s/%d-lines", goos, src.Name(), v, lines), func(t *testing.T) {
						testhook.Set(t, &platform, platformMods(goos))
						st, err := theme.Stack(theme.Builtins(), src.Name())
						if err != nil {
							t.Fatal(err)
						}
						th, err := theme.Resolve(st, theme.Effective(st, v))
						if err != nil {
							t.Fatal(err)
						}
						text := "hello world"
						var id ui.ID
						h := newHarness(t, v, small, func(c *ui.Ctx) {
							c.Column("col", ui.Style{Pad: ui.Pad(10), W: ui.Px(300)}, func(c *ui.Ctx) {
								id = c.Next("q")
								Input(c, "q", &text, InputOpts{Label: "Q", Lines: lines, Help: "test/input"})
							})
						})
						h.u.SetTheme(th)
						h.click(mid(h.rect(id)))
						if !h.u.Key(render.KeyEvent{Key: "a", Mods: platform.primary}) {
							t.Fatal("nothing takes select all")
						}
						h.frame()
						field, fg := th.C(tInputBg), th.C(tInputFg)
						textAt, width := -1, float32(0)
						for i, op := range h.list.Ops {
							if op.Kind == render.OpGlyphs && op.Color == fg {
								textAt, width = i, op.Run.Advance
								break
							}
						}
						if textAt < 0 {
							t.Fatal("the text isn't drawn")
						}
						var sel *render.Op
						for i := textAt - 1; i >= 0 && sel == nil; i-- {
							if op := h.list.Ops[i]; op.Kind == render.OpRect && op.Rect.Dx() >= width-1 && op.Rect.Dy() < 30 && op.Style.Fill != field {
								sel = &h.list.Ops[i]
							}
						}
						if sel == nil {
							t.Fatal("select all painted nothing behind the text")
						}
						on := theme.Over(sel.Style.Fill, field)
						if r := theme.ContrastRatio(on, field); r < 1.5 {
							t.Errorf("the selection %s on the field %s is %.2f:1, under 1.5:1", theme.Hex(on), theme.Hex(field), r)
						}
						if r := theme.ContrastRatio(fg, on); r < 4.5 {
							t.Errorf("the text on the selection is %.2f:1, under 4.5:1", r)
						}
					})
				}
			}
		}
	}
}

// Every selection (the editor's, a document's text, a field's) stands out from what it's on in every built-in theme.
func TestSelectionsLint(t *testing.T) {
	for _, src := range theme.Builtins() {
		for _, v := range []theme.Variant{theme.Dark, theme.Light} {
			st, err := theme.Stack(theme.Builtins(), src.Name())
			if err != nil {
				t.Fatal(err)
			}
			th, err := theme.Resolve(st, theme.Effective(st, v))
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range theme.Lint(th) {
				if strings.Contains(f.Message, "selection") {
					t.Errorf("%s %s: %s", src.Name(), v, f.Message)
				}
			}
		}
	}
}

// A double click in a field selects the word under it, a triple click its line; the pointer may drift a little
// between the clicks, and a slow second click is a click of its own.
func TestInputMultiClickSelects(t *testing.T) {
	text := "round half even\nsecond line"
	var id ui.ID
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		c.Column("col", ui.Style{Pad: ui.Pad(10), W: ui.Px(300)}, func(c *ui.Ctx) {
			id = c.Next("q")
			Input(c, "q", &text, InputOpts{Label: "Q", Lines: 4, Help: "test/input"})
		})
	})
	selected := func() string {
		s := h.access(id).Selection
		lo, hi := min(s.Start, s.End), max(s.Start, s.End)
		return string([]rune(text)[lo:hi])
	}
	r := h.rect(id)
	ts, err := typeset.NewBundled()
	if err != nil {
		t.Fatal(err)
	}
	x, y := r.Min.X+10+ts.Shape("round ha", typeset.FontSpec{Family: typeset.UI, Size: 12.5}).Advance, r.Min.Y+14
	h.click(x, y)
	if got := selected(); got != "" {
		t.Fatalf("one click selected %q", got)
	}
	h.click(x+2, y+1)
	if got := selected(); got != "half" {
		t.Errorf("a double click selected %q, want \"half\"", got)
	}
	h.click(x+1, y)
	if got := selected(); got != "round half even\n" {
		t.Errorf("a triple click selected %q, want the line", got)
	}
	h.now = h.now.Add(time.Second)
	h.click(x, y)
	h.now = h.now.Add(time.Second)
	h.click(x, y)
	if got := selected(); got != "" {
		t.Errorf("two slow clicks selected %q", got)
	}
}
