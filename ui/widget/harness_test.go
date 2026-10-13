package widget

import (
	"testing"
	"time"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

type harness struct {
	t     *testing.T
	u     *ui.UI
	now   time.Time
	size  render.Point
	build func(c *ui.Ctx)
	list  *render.List
}

func resolved(t *testing.T, v theme.Variant) *theme.Resolved {
	t.Helper()
	st, err := theme.Stack(theme.Builtins(), "oriel")
	if err != nil {
		t.Fatal(err)
	}
	r, err := theme.Resolve(st, v)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func newHarness(t *testing.T, v theme.Variant, size render.Point, build func(c *ui.Ctx)) *harness {
	t.Helper()
	ts, err := typeset.NewBundled()
	if err != nil {
		t.Fatal(err)
	}
	u := ui.New(ts, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12.5}})
	u.SetTheme(resolved(t, v))
	o := u.Options()
	o.ReducedMotion = true // snapshots and behaviour are of the settled UI; motion has its own tests
	u.SetOptions(o)
	h := &harness{t: t, u: u, now: time.Unix(1000, 0), size: size, build: build}
	u.SetClock(func() time.Time { return h.now })
	h.frame()
	return h
}

func (h *harness) frame() {
	h.list = new(render.List)
	h.u.Frame(h.list.Record(1), h.size, h.build)
}

func (h *harness) pointer(k render.PointerKind, b render.Buttons, x, y float32) {
	h.u.Pointer(render.PointerEvent{Kind: k, Pos: render.Point{X: x, Y: y}, Buttons: b})
}

// click moves to (x, y), presses and releases the primary button, with a frame between each.
func (h *harness) click(x, y float32) {
	h.pointer(render.PointerMove, 0, x, y)
	h.frame()
	h.pointer(render.PointerPress, render.ButtonPrimary, x, y)
	h.frame()
	h.pointer(render.PointerRelease, render.ButtonPrimary, x, y)
	h.frame()
}

func (h *harness) rightClick(x, y float32) {
	h.pointer(render.PointerMove, 0, x, y)
	h.frame()
	h.pointer(render.PointerPress, render.ButtonSecondary, x, y)
	h.frame()
}

// keys types each key at whatever has focus, a frame per key.
func (h *harness) keys(ks ...string) {
	h.t.Helper()
	for _, k := range ks {
		ev := render.KeyEvent{Key: k}
		if len(k) > 2 && k[0] == '^' {
			ev = render.KeyEvent{Key: k[1:], Mods: render.ModCtrl}
		}
		if len(k) > 1 && k[0] == '+' {
			ev = render.KeyEvent{Key: k[1:], Mods: render.ModShift}
		}
		if !h.u.Key(ev) {
			h.t.Fatalf("nothing focused takes %q", k)
		}
		h.frame()
	}
}

func (h *harness) rect(id ui.ID) render.Rect {
	h.t.Helper()
	r, ok := h.u.Rect(id)
	if !ok {
		h.t.Fatalf("no rect for %v", id)
	}
	return r
}

func mid(r render.Rect) (float32, float32) { return (r.Min.X + r.Max.X) / 2, (r.Min.Y + r.Max.Y) / 2 }
