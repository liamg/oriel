package app

import (
	"context"
	"fmt"
	"image/color"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// TitleBarView is the bar across the top: the logo, the session tabs, and segments named titlebar.right.* (the
// usage and bus tats) at its right.
const (
	TitleBarView = "app.titlebar"
	TitleRight   = "titlebar.right."
)

const titleH = 38

var (
	tTitleBg     = theme.Declare("titlebar.bg", "bg.surface")
	tTitleLine   = theme.Declare("titlebar.line", "line")
	tTabOn       = theme.Declare("session.tab.active.bg", "bg.raised")
	tTabEdgeOn   = theme.Declare("session.tab.active.edge", "accent")
	tTabName     = theme.Declare("session.tab.fg", "fg.muted")
	tTabNameOn   = theme.Declare("session.tab.active.fg", "fg")
	tTabMini     = theme.Declare("session.tab.mini", "fg.faint")
	tTabKey      = theme.Declare("session.tab.key", "fg.ghost")
	tLogoRing    = theme.Declare("logo.ring", "accent")
	tLogoCursor  = theme.Declare("logo.cursor", "brand")
	tWordmark    = theme.Declare("logo.wordmark", "fg")
	statusTokens = map[core.Status]theme.TokenID{core.StatusNeedsYou: theme.StatusNeedsYou, core.StatusPermission: theme.StatusPermission,
		core.StatusWorking: theme.StatusWorking, core.StatusCI: theme.StatusCI, core.StatusReviewer: theme.StatusReviewer,
		core.StatusQuiet: theme.StatusQuiet, core.StatusOff: theme.StatusOff}
)

// statusWord is what a tab says beside its name: the status, unless it's working (then its kind) or quiet.
func statusWord(s core.Session) string {
	switch s.Status() {
	case core.StatusNeedsYou:
		return "needs you"
	case core.StatusPermission:
		return "permission"
	case core.StatusCI:
		return "ci"
	case core.StatusReviewer:
		return "in review"
	case core.StatusOff:
		return "off"
	case core.StatusWorking:
		return string(s.Kind())
	}
	return ""
}

// sessionTabs is the titlebar with a tab per session, in the order they opened; ⌘1–9 focus them.
type sessionTabs struct {
	disposer
	sh *shell
}

func (t *sessionTabs) Manifest() ext.Manifest { return ext.Manifest{ID: "sessiontabs"} }

func (t *sessionTabs) Activate(_ context.Context, h ext.Host) error {
	focus := func(id core.SessionID) error { return t.sh.cfg.Store.Apply(core.SessionFocused{ID: id}) }
	t.add(h.Command(registry.Command{ID: "session.focus", Title: "Focus a session: {n} counting from 1, or {id}", Run: func(_ registry.Ctx, a registry.Args) error {
		if id, _ := a["id"].(string); id != "" {
			return focus(core.SessionID(id))
		}
		n, _ := a["n"].(int)
		ss := h.Sessions()
		if n < 1 || n > len(ss) {
			return fmt.Errorf("no session %d", n)
		}
		return focus(ss[n-1].ID())
	}}))
	for n := 1; n <= 9; n++ {
		t.add(h.Keymap(registry.Keymap{Mode: "global", Keys: fmt.Sprintf("<D-%d>", n), Command: "session.focus", Args: registry.Args{"n": n},
			Desc: fmt.Sprintf("Session %d", n)}))
	}
	t.add(h.View(TitleBarView, registry.ViewFunc(func(vc registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "titlebar", Fn: func(c *ui.Ctx) { t.build(c, h, vc) }}
	})))
	return nil
}

func (t *sessionTabs) build(c *ui.Ctx, h ext.Host, vc registry.ViewCtx) {
	th := c.Theme()
	st := h.State()
	c.Row("titlebar", ui.Style{W: ui.Flex(1), H: ui.Px(titleH), Cross: ui.Center, Fill: th.C(tTitleBg), Role: ui.RoleToolbar, Label: "Sessions"}, func(c *ui.Ctx) {
		c.Row("brand", ui.Style{H: ui.Flex(1), Pad: ui.PadXY(14, 0), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Paint("logo", ui.Px(22), ui.Px(22), func(f render.Frame, r render.Rect) { logo(f, r, th) })
			c.Text("word", "ORIEL", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Weight: typeset.Bold, Size: 12, Tracking: .2},
				Color: th.C(tWordmark)})
		})
		c.Box("sep", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(tTitleLine)}, nil)
		c.Row("tabs", ui.Style{H: ui.Flex(1), W: ui.Flex(1), Clip: true, Role: ui.RoleTabList, Label: "Sessions"}, func(c *ui.Ctx) {
			for i, s := range h.Sessions() {
				t.tab(c, h, i, s, s.ID() == st.Focus)
				c.Box(fmt.Sprint("sep", i), ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(tTitleLine)}, nil)
			}
		})
		ids := h.Views()
		c.Row("right", ui.Style{H: ui.Flex(1), Pad: ui.PadXY(14, 0), Gap: 14, Cross: ui.Center}, func(c *ui.Ctx) {
			for _, id := range ids {
				if v, ok := h.ViewByID(id); ok && strings.HasPrefix(id, TitleRight) {
					c.Row(id, ui.Style{H: ui.Flex(1), Cross: ui.Center}, func(c *ui.Ctx) { c.Node(v.Build(vc)) })
				}
			}
		})
	})
}

func (t *sessionTabs) tab(c *ui.Ctx, h ext.Host, i int, s core.Session, on bool) {
	th := c.Theme()
	id := c.Next(s.ID())
	if c.Interaction(id).Clicked {
		h.Run(context.Background(), "session.focus", registry.Args{"id": string(s.ID())})
	}
	fill, name := color.NRGBA{}, th.C(tTabName)
	if on {
		fill, name = th.C(tTabOn), th.C(tTabNameOn)
	}
	dot := th.C(statusTokens[s.Status()])
	c.Row(s.ID(), ui.Style{H: ui.Flex(1), Fill: fill, Interactive: true, Cursor: render.CursorPointer,
		Help: "shell/tabs", Role: ui.RoleTab, Selected: on, Label: s.Name()}, func(c *ui.Ctx) {
		c.Column("in", ui.Style{H: ui.Flex(1)}, func(c *ui.Ctx) {
			edge := color.NRGBA{}
			if on {
				edge = th.C(tTabEdgeOn)
			}
			c.Box("edge", ui.Style{W: ui.Flex(1), H: ui.Px(2), Fill: edge}, nil)
			c.Row("body", ui.Style{H: ui.Flex(1), Pad: ui.PadXY(12, 0), Gap: 7, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Box("dot", ui.Style{W: ui.Px(8), H: ui.Px(8), Radius: 4, Fill: dot, Role: ui.RoleText, Label: string(s.Status())}, nil)
				c.Text("name", s.Name(), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12}, Color: name})
				if w := statusWord(s); w != "" {
					c.Text("mini", w, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10, Weight: typeset.Medium}, Color: th.C(tTabMini)})
				}
				if n := s.Unseen(); n > 0 {
					c.Text("errs", fmt.Sprintf("✕%d", n), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10, Weight: typeset.SemiBold},
						Color: th.C(theme.Error)})
				}
				if i < 9 {
					c.Text("key", fmt.Sprintf("⌘%d", i+1), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9.5, Weight: typeset.Medium},
						Color: th.C(tTabKey)})
				}
			})
		})
	})
}

// logo is the o▌ mark: a ring in the accent and a tall cursor in the brand orange, each with a faint glow.
func logo(f render.Frame, r render.Rect, th *theme.Resolved) {
	s := r.Dx() / 100
	cx, cy := r.Min.X+38*s, r.Min.Y+r.Dy()/2+3*s
	ring, cursor := th.C(tLogoRing), th.C(tLogoCursor)
	f.Effect(render.GlowDisc, render.XYWH(cx-25*s, cy-25*s, 50*s, 50*s), 4, theme.WithAlpha(cursor, .22))
	bar := render.XYWH(r.Min.X+68*s, r.Min.Y+r.Dy()/2-22*s, 13*s, 50*s)
	f.Effect(render.Glow, bar, 4, theme.WithAlpha(cursor, .3))
	f.Path(render.Ellipse(render.XYWH(cx-21*s, cy-21*s, 42*s, 42*s)), render.Style{Stroke: ring, Width: 8 * s})
	var p render.Path // a path, not a rect, so the bar isn't snapped to pixels and stays level with the ring
	p.MoveTo(bar.Min).LineTo(render.Point{X: bar.Max.X, Y: bar.Min.Y}).LineTo(bar.Max).LineTo(render.Point{X: bar.Min.X, Y: bar.Max.Y}).Close()
	f.Path(&p, render.Style{Fill: cursor})
}
