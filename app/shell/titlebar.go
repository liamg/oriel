package shell

import (
	"cmp"
	"context"
	"fmt"
	"image/color"
	"slices"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

var (
	// The band the tabs sit on is a step deeper than the surface under it, so the tab in front, filled with that
	// surface, reads as the top of the window below it.
	// A step from the window toward the text: lighter than a dark window, darker than a light one, so the band always
	// stands apart and the tab in front reads as cut from the window under it.
	tTitleBg   = theme.Declare("titlebar.bg", "mix(bg.surface, fg, 8%)")
	tTitleLine = theme.Declare("titlebar.line", "line.strong")
	tTabOn     = theme.Declare("session.tab.active.bg", "bg.surface")
	tTabHover  = theme.Declare("session.tab.hover.bg", "alpha(bg.surface, 55%)")
	tTabName   = theme.Declare("session.tab.fg", "fg.muted")
	tTabNameOn = theme.Declare("session.tab.active.fg", "fg")
	tTabMini   = theme.Declare("session.tab.mini", "fg.faint")
	tTabKey    = theme.Declare("session.tab.key", "fg.ghost")
	// tTabCalling is how strong a tab behind calling for you is washed at its pulses' peak; its colour is the prompt's.
	tTabCalling = theme.Declare("session.tab.calling.bg", "alpha(status.permission, 34%)")
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
	kit.Disposer
	sh       *shell
	renaming core.SessionID // the session whose tab is being renamed
	rename   widget.Rename
}

func (t *sessionTabs) Manifest() plugin.Manifest { return plugin.Manifest{ID: "sessiontabs"} }

func (t *sessionTabs) Activate(_ context.Context, h plugin.Host) error {
	focus := func(id core.SessionID, then ...kit.Call) error { return kit.Focus(h, id, then) }
	t.Add(h.Command(registry.Command{ID: "session.focus", Title: "Focus a session: {n} counting from 1, or {id}; {then} is a list of {command, args} run once the window is on it", Run: func(_ registry.Ctx, a registry.Args) error {
		if id, _ := a["id"].(string); id != "" {
			then, err := kit.CallsOf(a["then"])
			if err != nil {
				return err
			}
			return focus(core.SessionID(id), then...)
		}
		n := a.Int("n")
		ss := openTabs(h.State())
		if n < 1 || n > len(ss) {
			return fmt.Errorf("no session %d", n)
		}
		return focus(ss[n-1].ID())
	}}))
	t.Add(h.Command(registry.Command{ID: "session.close-tab", Title: "Close the tab of {id}, or of the session in front; the session stays",
		Available: func(a registry.Args) bool { return len(h.State().Tabs) > 0 },
		Run:       func(_ registry.Ctx, a registry.Args) error { return t.close(h, core.SessionID(a.String("id"))) }}))
	t.Add(h.Command(registry.Command{ID: "session.tab.move", Title: "Move the tab of {id}, or of the session in front, to the {index}th place",
		Available: func(a registry.Args) bool { return len(h.State().Tabs) > 1 },
		Run: func(_ registry.Ctx, a registry.Args) error {
			st := h.State()
			if i := a.Int("index"); i < 1 || i > len(st.Tabs) {
				return fmt.Errorf("no place %d among %d tabs", i, len(st.Tabs))
			}
			return h.Apply(core.TabMoved{ID: core.SessionID(cmp.Or(a.String("id"), string(st.Focus))), Index: a.Int("index") - 1})
		}}))
	for n := 1; n <= 9; n++ {
		t.Add(h.Keymap(registry.Keymap{Mode: "global", Keys: fmt.Sprintf("<D-%d>", n), Command: "session.focus", Args: registry.Args{"n": n},
			Desc: fmt.Sprintf("Session %d", n)}))
	}
	t.Add(h.Command(registry.Command{ID: "session.tab.rename", Title: "Rename session {id}, or the one in front, in its tab",
		Available: func(a registry.Args) bool { return len(h.State().Tabs) > 0 },
		Run: func(_ registry.Ctx, a registry.Args) error {
			id := core.SessionID(cmp.Or(a.String("id"), string(h.State().Focus)))
			s, ok := h.Session(id)
			if !ok {
				return fmt.Errorf("no session %q", id)
			}
			t.renaming = id
			t.rename.Start(s.Name())
			return nil
		}}))
	t.Add(h.Contribute(registry.MenuItem{Name: "session.tab.rename", Label: "Rename…", Group: "session", Order: 5,
		When: registry.MenuWhen{Subject: kit.SessionSubject, Help: "shell/tabs"}, Source: "builtin:sessiontabs",
		Run: func(ctx registry.Ctx, at registry.MenuCtx) error {
			return h.Run(ctx, "session.tab.rename", registry.Args{"id": strings.TrimPrefix(at.Subject, kit.SessionSubject)})
		}}))
	t.Add(h.Picker(sessionsSource(h)))
	t.Add(h.Contribute(uireg.Indicator{Name: "sessions", Slot: uireg.SlotHUD, Order: 30, Title: "Sessions", Help: kit.HUDHelp + "#sessions",
		Note: func() string { return kit.Counted(len(h.State().Sessions), "session") },
		Build: func(registry.Ctx) (ui.Node, error) {
			return &ui.Func{Key: "sessions", Fn: func(c *ui.Ctx) { t.sessionsPanel(c, h) }}, nil
		}}))
	t.Add(h.View(kit.TitleBarView, uireg.ViewFunc(func(vc uireg.ViewCtx) ui.Node {
		return &ui.Func{Key: "titlebar", Fn: func(c *ui.Ctx) { t.build(c, h, vc) }}
	})))
	return nil
}

// tabH is a session's tab: its name over its status line, standing on the bottom of the title bar.
const (
	tabH      = 42
	tabRadius = 9
)

func (t *sessionTabs) build(c *ui.Ctx, h plugin.Host, vc uireg.ViewCtx) {
	th := c.Theme()
	st := h.State()
	c.Row("titlebar", ui.Style{W: ui.Flex(1), H: ui.Px(kit.TitleH), Cross: ui.Center, Fill: th.C(tTitleBg), Role: ui.RoleToolbar, Label: "Sessions"}, func(c *ui.Ctx) {
		c.Row("brand", ui.Style{H: ui.Flex(1), Pad: ui.PadXY(10, 0), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			slot(c, h, uireg.SlotTitleBrand, vc)
		})
		// More sessions than fit scroll sideways, the one in front brought into view when it changes.
		open := openTabs(st)
		o := widget.StripOpts{Help: "shell/tabs"}
		for _, s := range open {
			o.Items = append(o.Items, s.ID())
		}
		if st.Focus != "" {
			o.Active = st.Focus
		}
		widget.Strip(c, "tabs", ui.Style{H: ui.Flex(1), W: ui.Flex(1), Cross: ui.End, Role: ui.RoleTabList, Label: "Sessions"}, o, func(c *ui.Ctx) {
			c.Box("lead", ui.Style{W: ui.Px(tabRadius + 1)}, nil) // room for the first tab's foot
			for i, s := range open {
				if i > 0 {
					// The tabs behind are split by a short rule, left out beside the one in front, which needs no edge.
					rule := th.C(tTitleLine)
					if s.ID() == st.Focus || open[i-1].ID() == st.Focus {
						rule = color.NRGBA{}
					}
					c.Row(fmt.Sprint("sep", i), ui.Style{W: ui.Px(2*tabRadius + 1), H: ui.Px(tabH), Main: ui.Center, Cross: ui.Center}, func(c *ui.Ctx) {
						c.Box("rule", ui.Style{W: ui.Px(1), H: ui.Px(18), Fill: rule}, nil)
					})
				}
				t.tab(c, h, i, s, s.ID() == st.Focus)
			}
			if h.Available("session.new", nil) {
				c.Row("new", ui.Style{H: ui.Px(tabH), Pad: ui.Insets{L: tabRadius + 4, R: 8}, Cross: ui.Center}, func(c *ui.Ctx) {
					tip := "New session"
					if k := kit.BoundKey(h, "session.new", nil); k != "" {
						tip += " " + k
					}
					if k := kit.BoundKey(h, "session.quick.start", nil); k != "" {
						tip += " · quick session " + k + ", or right-click"
					}
					if widget.IconButton(c, "b", "+", widget.IconButtonOpts{Label: "New session", Tooltip: tip, Help: kit.NewSessionHelp, Size: 22}) {
						h.Run(context.Background(), "session.new", nil)
					}
				})
			}
		})
		c.Row("right", ui.Style{Pad: ui.PadXY(14, 0), Gap: 14, Cross: ui.Center}, func(c *ui.Ctx) {
			slot(c, h, uireg.SlotTitleBar, vc)
		})
	})
}

// tab is s's tab: its name over a line of its state, the asks waiting in it and its key. The one in front is the
// surface under the title bar, joined to it; the others sit on the band.
func (t *sessionTabs) tab(c *ui.Ctx, h plugin.Host, i int, s core.Session, on bool) {
	th := c.Theme()
	name := th.C(tTabName)
	if on {
		name = th.C(tTabNameOn)
	}
	mono := func(size float32, w typeset.Weight) typeset.FontSpec {
		return typeset.FontSpec{Family: typeset.Mono, Size: size, Weight: w}
	}
	// Under the pointer, the tab keeps the width it had before, and its ✕ takes room from the end of its name.
	st := ui.Style{Dir: ui.Column, H: ui.Px(tabH), Pad: ui.Insets{L: 12, T: 5, R: 14, B: 5}, Gap: 4, Main: ui.Center,
		NoHover: true, Help: "shell/tabs", Role: ui.RoleTab, Selected: on, Label: s.Name(), Subject: kit.SessionSubject + string(s.ID()),
		Drag: &ui.DragData{Kind: tabDragKind, Data: s.ID(), Label: s.Name()}, Drop: []string{tabDragKind}, NoFocus: true}
	id := c.Next(s.ID())
	if c.PointerOver(id) {
		if r, ok := c.Rect(id); ok {
			st.W = ui.Px(r.Dx())
		}
	}
	in := widget.Clickable(c, s.ID(), st, func(c *ui.Ctx) {
		hover := widget.PointerIn(c)
		switch {
		case on:
			widget.Joined(c, "surface", th.C(tTabOn), tabRadius)
		case hover:
			widget.Joined(c, "surface", th.C(tTabHover), tabRadius)
		}
		if !on {
			callFrom(c, h, s)
		}
		top := ui.Style{H: ui.Px(16), Gap: 7, Cross: ui.Center}
		if hover {
			top.W = ui.Flex(1)
		}
		c.Row("top", top, func(c *ui.Ctx) {
			kit.StatusMark(c, "mark", s.Status(), 9)
			if t.renaming == s.ID() {
				t.nameField(c, h, s)
			} else if hover {
				c.Row("name", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
					c.Text("name", s.Name(), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12.5, Weight: weight(on)}, Color: name})
				})
				if widget.IconButton(c, "close", "✕", widget.IconButtonOpts{Label: "Close " + s.Name(), Help: "shell/tabs", Size: 16}) {
					closeSession(h, s.ID())
				}
			} else {
				c.Text("name", s.Name(), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12.5, Weight: weight(on)}, Color: name})
			}
		})
		c.Row("state", ui.Style{H: ui.Px(12), Gap: 7, Cross: ui.Center, Pad: ui.Insets{L: 16}}, func(c *ui.Ctx) {
			if w := statusWord(s); w != "" {
				col := th.C(tTabMini)
				if s.Status() == core.StatusNeedsYou || s.Status() == core.StatusPermission {
					col = th.C(kit.StatusTokens[s.Status()])
				}
				c.Text("mini", w, ui.TextStyle{Font: mono(10, typeset.Medium), Color: col})
			}
			if inbox, ok := kit.InboxOf(h); ok {
				if n, tone := inbox.Count(string(s.ID())); n > 0 {
					fill, ok := th.Color(tone)
					if !ok {
						fill = th.C(theme.StatusPermission)
					}
					kit.CountPill(c, "asks", n, fill)
				}
			}
			if n := s.Unseen(); n > 0 {
				c.Text("errs", fmt.Sprintf("✕%d", n), ui.TextStyle{Font: mono(10, typeset.SemiBold), Color: th.C(theme.Error)})
			}
			if i < 9 {
				c.Text("key", fmt.Sprintf("⌘%d", i+1), ui.TextStyle{Font: mono(9.5, typeset.Medium), Color: th.C(tTabKey)})
			}
		})
	})
	if in.Clicked {
		h.Run(context.Background(), "session.focus", registry.Args{"id": string(s.ID())})
	}
	if d := in.Dropped; d != nil {
		if id, ok := d.Data.(core.SessionID); ok && id != s.ID() {
			if err := h.Run(context.Background(), "session.tab.move", registry.Args{"id": string(id), "index": i + 1}); err != nil {
				h.Log().Error("sessiontabs", "move", id, "err", err)
			}
		}
	}
}

// callFrom washes the tab of s, behind, in the tone of a prompt waiting in it: a few pulses as each comes, then
// steady until it's seen.
func callFrom(c *ui.Ctx, h plugin.Host, s core.Session) {
	inbox, ok := kit.InboxOf(h)
	if !ok {
		return
	}
	key, tone, ok := inbox.Calling(string(s.ID()))
	if !ok {
		return
	}
	th := c.Theme()
	col, ok := th.Color(tone)
	if !ok {
		col = th.C(theme.StatusPermission)
	}
	wash := th.C(tTabCalling)
	strength := widget.Pulse(c, "call", key, widget.PulseOpts{})
	col.A = uint8(float32(wash.A) * strength)
	widget.Joined(c, "calling", col, tabRadius)
	c.Box("calls", ui.Style{Float: &ui.Float{Fill: true}, W: ui.Flex(1), H: ui.Flex(1), Z: ui.LayerUnder, Role: ui.RoleText, Label: "Calls for you"}, nil)
}

// weight is a tab name's: the one in front's is heavier.
func weight(on bool) typeset.Weight {
	if on {
		return typeset.SemiBold
	}
	return typeset.Normal
}

// tabDragKind is what a session's tab carries as it's dragged along the tabs.
const tabDragKind = "oriel.session-tab"

// nameField edits s's name in its tab: ⏎ renames it, Escape leaves it as it was.
func (t *sessionTabs) nameField(c *ui.Ctx, h plugin.Host, s core.Session) {
	name, done := widget.RenameField(c, "rename", &t.rename, widget.InputOpts{Label: "Rename " + s.Name(), Width: ui.Px(180), Help: "shell/tabs"})
	if !t.rename.On {
		t.renaming = ""
	}
	if done && name != "" && name != s.Name() {
		if err := h.Run(context.Background(), "session.rename", registry.Args{"id": string(s.ID()), "name": name}); err != nil {
			h.Notify(core.Notice{ID: "session.rename", Severity: core.SevError, Source: "sessiontabs", Text: err.Error()})
		}
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

// closeSession is a session tab's ✕: session.close, which ends the session, or with nothing contributing it, closing
// its tab.
func closeSession(h plugin.Host, id core.SessionID) {
	args := registry.Args{"id": string(id)}
	cmd := "session.close"
	if !h.Available(cmd, args) {
		cmd = "session.close-tab"
	}
	if err := h.Run(context.Background(), cmd, args); err != nil {
		h.Notify(core.Notice{ID: cmd, Severity: core.SevError, Source: "shell", Key: true, Text: err.Error()})
	}
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
	if err := h.Apply(core.TabClosed{ID: id}); err != nil {
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
