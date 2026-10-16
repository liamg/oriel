package app

import (
	"context"
	"fmt"
	"image/color"
	"slices"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// TitleBarView is the bar across the top: the logo, the session tabs, and segments named titlebar.right.* (the
// usage and bus indicators) at its right.
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

// sessionTabs is the titlebar with a tab per session open in the window, in the order they opened; ⌘1–9 focus them.
type sessionTabs struct {
	disposer
	sh *shell
}

func (t *sessionTabs) Manifest() plugin.Manifest { return plugin.Manifest{ID: "sessiontabs"} }

func (t *sessionTabs) Activate(_ context.Context, h plugin.Host) error {
	focus := func(id core.SessionID) error { return t.sh.cfg.Store.Apply(core.SessionFocused{ID: id}) }
	t.add(h.Command(registry.Command{ID: "session.focus", Title: "Focus a session: {n} counting from 1, or {id}", Run: func(_ registry.Ctx, a registry.Args) error {
		if id, _ := a["id"].(string); id != "" {
			return focus(core.SessionID(id))
		}
		n, _ := a["n"].(int)
		ss := openTabs(h.State())
		if n < 1 || n > len(ss) {
			return fmt.Errorf("no session %d", n)
		}
		return focus(ss[n-1].ID())
	}}))
	t.add(h.Command(registry.Command{ID: "session.close-tab", Title: "Close the tab of {id}, or of the session in front; the session stays",
		Available: func(a registry.Args) bool { return len(h.State().Tabs) > 0 },
		Run:       func(_ registry.Ctx, a registry.Args) error { return t.close(h, core.SessionID(a.String("id"))) }}))
	for n := 1; n <= 9; n++ {
		t.add(h.Keymap(registry.Keymap{Mode: "global", Keys: fmt.Sprintf("<D-%d>", n), Command: "session.focus", Args: registry.Args{"n": n},
			Desc: fmt.Sprintf("Session %d", n)}))
	}
	t.add(h.Picker(sessionsSource(h)))
	t.add(h.Contribute(registry.Indicator{Name: "sessions", Slot: registry.SlotHUD, Order: 30, Title: "Sessions", Help: hudHelp + "#sessions",
		Note: func() string { return counted(len(h.State().Sessions), "session") },
		Build: func(registry.Ctx) (ui.Node, error) {
			return &ui.Func{Key: "sessions", Fn: func(c *ui.Ctx) { t.sessionsPanel(c, h) }}, nil
		}}))
	t.add(h.View(TitleBarView, registry.ViewFunc(func(vc registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "titlebar", Fn: func(c *ui.Ctx) { t.build(c, h, vc) }}
	})))
	return nil
}

func (t *sessionTabs) build(c *ui.Ctx, h plugin.Host, vc registry.ViewCtx) {
	th := c.Theme()
	st := h.State()
	c.Row("titlebar", ui.Style{W: ui.Flex(1), H: ui.Px(titleH), Cross: ui.Center, Fill: th.C(tTitleBg), Role: ui.RoleToolbar, Label: "Sessions"}, func(c *ui.Ctx) {
		c.Row("brand", ui.Style{H: ui.Flex(1), Pad: ui.PadXY(14, 0), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Paint("logo", ui.Px(22), ui.Px(22), func(f render.Frame, r render.Rect) { logo(f, r, th) })
			c.Text("word", "ORIEL", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Weight: typeset.Bold, Size: 12, Tracking: .2},
				Color: th.C(tWordmark)})
		})
		c.Box("sep", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(tTitleLine)}, nil)
		// More sessions than fit scroll sideways, the one in front brought into view when it changes.
		open := openTabs(st)
		o := widget.StripOpts{Help: "shell/tabs"}
		for _, s := range open {
			o.Items = append(o.Items, s.ID())
		}
		if st.Focus != "" {
			o.Active = st.Focus
		}
		widget.Strip(c, "tabs", ui.Style{H: ui.Flex(1), W: ui.Flex(1), Role: ui.RoleTabList, Label: "Sessions"}, o, func(c *ui.Ctx) {
			for i, s := range open {
				t.tab(c, h, i, s, s.ID() == st.Focus)
				c.Box(fmt.Sprint("sep", i), ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(tTitleLine)}, nil)
			}
		})
		c.Row("right", ui.Style{H: ui.Flex(1), Pad: ui.PadXY(14, 0), Gap: 14, Cross: ui.Center}, func(c *ui.Ctx) {
			t.sh.slot(c, h, registry.SlotTitleBar, vc)
		})
	})
}

func (t *sessionTabs) tab(c *ui.Ctx, h plugin.Host, i int, s core.Session, on bool) {
	th := c.Theme()
	fill, name := color.NRGBA{}, th.C(tTabName)
	if on {
		fill, name = th.C(tTabOn), th.C(tTabNameOn)
	}
	dot := th.C(statusTokens[s.Status()])
	in := widget.Clickable(c, s.ID(), ui.Style{Dir: ui.Row, H: ui.Flex(1), Fill: fill, Help: "shell/tabs", Role: ui.RoleTab, Selected: on,
		Label: s.Name()}, func(c *ui.Ctx) {
		hover := widget.PointerIn(c)
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
				if t.sh.inbox != nil {
					if n, tone := t.sh.inbox.Count(string(s.ID())); n > 0 {
						fill, ok := th.Color(tone)
						if !ok {
							fill = th.C(theme.StatusPermission)
						}
						countPill(c, "asks", n, fill)
					}
				}
				if n := s.Unseen(); n > 0 {
					c.Text("errs", fmt.Sprintf("✕%d", n), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10, Weight: typeset.SemiBold},
						Color: th.C(theme.Error)})
				}
				if hover {
					if widget.IconButton(c, "close", "✕", widget.IconButtonOpts{Label: "Close " + s.Name(), Help: "shell/tabs", Size: 16}) {
						h.Run(context.Background(), "session.close-tab", registry.Args{"id": string(s.ID())})
					}
				} else if i < 9 {
					c.Text("key", fmt.Sprintf("⌘%d", i+1), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9.5, Weight: typeset.Medium},
						Color: th.C(tTabKey)})
				}
			})
		})
	})
	if in.Clicked {
		h.Run(context.Background(), "session.focus", registry.Args{"id": string(s.ID())})
	}
}

// openTabs is the sessions with a tab, in the order they opened.
func openTabs(st core.State) []core.Session {
	var out []core.Session
	for _, id := range st.Tabs {
		if s, ok := st.Session(id); ok {
			out = append(out, s)
		}
	}
	return out
}

// close closes id's tab, or the front one's. The one in front hands the front to its neighbour, or to the start view.
func (t *sessionTabs) close(h plugin.Host, id core.SessionID) error {
	st := h.State()
	if id == "" {
		id = st.Focus
	}
	i := slices.Index(st.Tabs, id)
	if i < 0 {
		return fmt.Errorf("no tab for %q", id)
	}
	if err := t.sh.cfg.Store.Apply(core.TabClosed{ID: id}); err != nil {
		return err
	}
	if id != st.Focus {
		return nil
	}
	rest := slices.Delete(slices.Clone(st.Tabs), i, i+1)
	if len(rest) == 0 {
		return t.sh.reopenOn(reopenTo{})
	}
	return h.Run(context.Background(), "session.focus", registry.Args{"id": string(rest[min(i, len(rest)-1)])})
}

// logo is the o▌ mark: a ring in the accent and a tall cursor in the brand orange, each with a faint glow.
func logo(f render.Frame, r render.Rect, th *theme.Resolved) {
	s := r.Dx() / 100
	cx, cy := r.Min.X+38*s, r.Min.Y+r.Dy()/2+3*s
	ring, cursor := th.C(tLogoRing), th.C(tLogoCursor)
	if r.Dx() <= 64 {
		f.Effect(render.GlowDisc, render.XYWH(cx-25*s, cy-25*s, 50*s, 50*s), 4, theme.WithAlpha(cursor, .22)) // a big logo glows from its bar alone: this would fill the ring
	}
	bar := render.XYWH(r.Min.X+68*s, r.Min.Y+r.Dy()/2-22*s, 13*s, 50*s)
	f.Effect(render.Glow, bar, 4, theme.WithAlpha(cursor, .3))
	f.Path(render.Ellipse(render.XYWH(cx-21*s, cy-21*s, 42*s, 42*s)), render.Style{Stroke: ring, Width: 8 * s})
	var p render.Path // a path, not a rect, so the bar isn't snapped to pixels and stays level with the ring
	p.MoveTo(bar.Min).LineTo(render.Point{X: bar.Max.X, Y: bar.Min.Y}).LineTo(bar.Max).LineTo(render.Point{X: bar.Min.X, Y: bar.Max.Y}).Close()
	f.Path(&p, render.Style{Fill: cursor})
}
