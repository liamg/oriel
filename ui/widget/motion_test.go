package widget

import (
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/snapshot"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// motionHarness is a harness whose springs run: each step moves the clock in 120 Hz frames.
func motionHarness(t *testing.T, reduced bool, build func(c *ui.Ctx)) *harness {
	h := newHarness(t, theme.Dark, render.Point{X: 640, Y: 400}, build)
	o := h.u.Options()
	o.ReducedMotion = reduced
	h.u.SetOptions(o)
	return h
}

func (h *harness) advance(d time.Duration) {
	for el := time.Duration(0); el < d; el += time.Second / 120 {
		h.now = h.now.Add(time.Second / 120)
		h.frame()
	}
}

// film records the frame now, at a midpoint and once settled, after change.
func film(h *harness, change func()) string {
	change()
	h.frame()
	var b strings.Builder
	b.WriteString("== start\n" + snapshot.List(h.list))
	h.advance(60 * time.Millisecond)
	b.WriteString("== mid\n" + snapshot.List(h.list))
	h.advance(250 * time.Millisecond)
	b.WriteString("== settled\n" + snapshot.List(h.list))
	return b.String()
}

// FW-13: each animation is a spring the test steps through: the start, a midpoint and the settled frame are display
// list snapshots; with reduced motion each starts where it settles; and nothing is still moving after 250 ms.
func TestWidgetMotion(t *testing.T) {
	type scene struct {
		name   string
		build  func(s *motionState) func(c *ui.Ctx)
		change func(s *motionState)
	}
	scenes := []scene{
		{"menu-open", menuScene, func(s *motionState) { s.menu = true }},
		{"menu-close", menuScene, func(s *motionState) { s.menu = false }},
		{"modal-open", modalScene, func(s *motionState) { s.modal = true }},
		{"modal-close", modalScene, func(s *motionState) { s.modal = false }},
		{"toast-in", toastScene, func(s *motionState) {
			s.toasts.Push(Toast{Title: "CI passed", Body: "12 checks", Tone: render.Color{R: 0x4f, G: 0xe0, B: 0xa0, A: 255}})
		}},
		{"toast-out", toastScene, func(s *motionState) { s.toasts.list = s.toasts.list[1:] }},
		{"tab-underline", tabsScene, func(s *motionState) { s.tab = 2 }},
		{"list-selection", listScene, func(s *motionState) { s.sel = 3 }},
		{"button-hover", buttonScene, func(s *motionState) { s.hover = true }},
	}
	for _, sc := range scenes {
		for _, reduced := range []bool{false, true} {
			name := sc.name
			if reduced {
				name += "-reduced"
			}
			s := &motionState{menu: strings.HasSuffix(sc.name, "close"), modal: strings.HasSuffix(sc.name, "close")}
			s.toasts.Push(Toast{Title: "git push", Body: "3 commits", Tone: render.Color{R: 0x4c, G: 0xc9, B: 0xf0, A: 255}})
			h := motionHarness(t, reduced, sc.build(s))
			s.h = h
			h.frame()
			got := film(h, func() { sc.change(s) })
			snapshot.Golden(t, "testdata/motion/"+name+".list", []byte(got))
			start := got[len("== start\n"):strings.Index(got, "== mid")]
			settled := got[strings.Index(got, "== settled\n")+len("== settled\n"):]
			if reduced && start != settled {
				t.Errorf("%s: with reduced motion, the first frame isn't the settled one", name)
			}
			if !reduced && start == settled {
				t.Errorf("%s doesn't move", name)
			}
			if !h.u.NextFrame().IsZero() && h.u.NextFrame().Equal(h.now) {
				t.Errorf("%s is still moving after 310 ms", name)
			}
		}
	}
}

type motionState struct {
	h      *harness
	menu   bool
	modal  bool
	toasts Toasts
	tab    int
	sel    int
	hover  bool
}

func menuScene(s *motionState) func(c *ui.Ctx) {
	return func(c *ui.Ctx) {
		if s.menu {
			at := render.Point{X: 40, Y: 40}
			c.Box("float", ui.Style{Z: 900, Float: &ui.Float{Point: &at}}, func(c *ui.Ctx) {
				Menu(c, "menu", []MenuItem{{Label: "Copy", Shortcut: "y"}, {Label: "Help", Shortcut: "K"}}, MenuOpts{Width: 200})
			})
		}
	}
}

func modalScene(s *motionState) func(c *ui.Ctx) {
	return func(c *ui.Ctx) {
		Modal(c, "modal", &s.modal, "Crash report", ModalOpts{Width: 300}, func(c *ui.Ctx) {
			c.Text("t", "Oriel stopped.", ui.TextStyle{})
		})
	}
}

func toastScene(s *motionState) func(c *ui.Ctx) {
	return func(c *ui.Ctx) { ShowToasts(c, "toasts", &s.toasts) }
}

func tabsScene(s *motionState) func(c *ui.Ctx) {
	return func(c *ui.Ctx) { Tabs(c, "tabs", []string{"AGENT", "TERMINAL", "PROBLEMS"}, &s.tab, TabsOpts{}) }
}

func listScene(s *motionState) func(c *ui.Ctx) {
	return func(c *ui.Ctx) {
		List(c, "list", 6, &s.sel, ListOpts{Height: ui.Px(200)}, func(c *ui.Ctx, i int, _ bool) {
			c.Text("t", []string{"one", "two", "three", "four", "five", "six"}[i], ui.TextStyle{})
		})
	}
}

func buttonScene(s *motionState) func(c *ui.Ctx) {
	return func(c *ui.Ctx) {
		if s.hover {
			s.h.u.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: 20, Y: 10}})
		}
		Button(c, "save", "Save", ButtonOpts{})
	}
}

// FW-13: "a key pressed mid-animation acts at once": the list's selection moves on the key, the highlight follows.
func TestKeysActMidAnimation(t *testing.T) {
	s := &motionState{}
	var id ui.ID
	h := motionHarness(t, false, func(c *ui.Ctx) {
		id = c.Next("list")
		listScene(s)(c)
	})
	h.u.Focus(id)
	h.frame()
	h.keys("j")
	h.advance(20 * time.Millisecond)
	h.keys("j")
	if s.sel != 2 {
		t.Errorf("two j's mid-glide: selection %d, want 2", s.sel)
	}
}
