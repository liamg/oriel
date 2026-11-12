package shell

import (
	"cmp"
	"context"
	"fmt"
	"image/color"
	"slices"
	"strings"
	"time"

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
	tTitleBg   = theme.Declare("titlebar.bg", "bg.surface")
	tTitleLine = theme.Declare("titlebar.line", "line")
	tTabOn     = theme.Declare("session.tab.active.bg", "bg.raised")
	tTabEdgeOn = theme.Declare("session.tab.active.edge", "accent")
	tTabName   = theme.Declare("session.tab.fg", "fg.muted")
	tTabNameOn = theme.Declare("session.tab.active.fg", "fg")
	tTabMini   = theme.Declare("session.tab.mini", "fg.faint")
	tTabKey    = theme.Declare("session.tab.key", "fg.ghost")
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
	// focus brings a session to the front: its kind's opening commands run first if the window shows nothing yet.
	focus := func(id core.SessionID, then ...kit.Call) error {
		kit.After(h, id, append([]kit.Call{{Command: "session.opens", Args: registry.Args{"id": string(id)}}}, then...))
		return h.Apply(core.SessionFocused{ID: id})
	}
	t.Add(h.Command(registry.Command{ID: "session.focus", Title: "Focus a session: {n} counting from 1, or {id}; {then} is a list of {command, args} run once the window is on it", Run: func(_ registry.Ctx, a registry.Args) error {
		if id, _ := a["id"].(string); id != "" {
			then, err := kit.CallsOf(a["then"])
			if err != nil {
				return err
			}
			return focus(core.SessionID(id), then...)
		}
		n, _ := a["n"].(int)
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

func (t *sessionTabs) build(c *ui.Ctx, h plugin.Host, vc uireg.ViewCtx) {
	th := c.Theme()
	st := h.State()
	c.Row("titlebar", ui.Style{W: ui.Flex(1), H: ui.Px(kit.TitleH), Cross: ui.Center, Fill: th.C(tTitleBg), Role: ui.RoleToolbar, Label: "Sessions"}, func(c *ui.Ctx) {
		c.Row("brand", ui.Style{H: ui.Flex(1), Pad: ui.PadXY(10, 0), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			slot(c, h, uireg.SlotTitleBrand, vc)
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
			if h.Available("session.new", nil) {
				c.Row("new", ui.Style{H: ui.Flex(1), Pad: ui.PadXY(8, 0), Cross: ui.Center}, func(c *ui.Ctx) {
					tip := "New session"
					if k := kit.BoundKey(h, "session.new", nil); k != "" {
						tip += " " + k
					}
					if widget.IconButton(c, "b", "+", widget.IconButtonOpts{Label: "New session", Tooltip: tip, Help: "shell/tabs", Size: 22}) {
						h.Run(context.Background(), "session.new", nil)
					}
				})
			}
		})
		c.Row("right", ui.Style{H: ui.Flex(1), Pad: ui.PadXY(14, 0), Gap: 14, Cross: ui.Center}, func(c *ui.Ctx) {
			slot(c, h, uireg.SlotTitleBar, vc)
		})
	})
}

func (t *sessionTabs) tab(c *ui.Ctx, h plugin.Host, i int, s core.Session, on bool) {
	th := c.Theme()
	fill, name := color.NRGBA{}, th.C(tTabName)
	if on {
		fill, name = th.C(tTabOn), th.C(tTabNameOn)
	}
	in := widget.Clickable(c, s.ID(), ui.Style{Dir: ui.Row, H: ui.Flex(1), Fill: fill, Help: "shell/tabs", Role: ui.RoleTab, Selected: on,
		Label: s.Name(), Subject: kit.SessionSubject + string(s.ID()),
		Drag: &ui.DragData{Kind: tabDragKind, Data: s.ID(), Label: s.Name()}, Drop: []string{tabDragKind}, NoFocus: true}, func(c *ui.Ctx) {
		hover := widget.PointerIn(c)
		c.Column("in", ui.Style{H: ui.Flex(1)}, func(c *ui.Ctx) {
			edge := color.NRGBA{}
			if on {
				edge = th.C(tTabEdgeOn)
			}
			c.Box("edge", ui.Style{W: ui.Flex(1), H: ui.Px(2), Fill: edge}, nil)
			// An 11px mark on 9px of padding keeps the tab the mockup's width.
			c.Row("body", ui.Style{H: ui.Flex(1), Pad: ui.Insets{L: 9, R: 12}, Gap: 7, Cross: ui.Center}, func(c *ui.Ctx) {
				kit.StatusMark(c, "mark", s.Status(), 9)
				if t.renaming == s.ID() {
					t.nameField(c, h, s)
				} else {
					c.Text("name", s.Name(), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12}, Color: name})
				}
				if w := statusWord(s); w != "" {
					c.Text("mini", w, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10, Weight: typeset.Medium}, Color: th.C(tTabMini)})
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
					c.Text("errs", fmt.Sprintf("✕%d", n), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10, Weight: typeset.SemiBold},
						Color: th.C(theme.Error)})
				}
				if hover {
					if widget.IconButton(c, "close", "✕", widget.IconButtonOpts{Label: "Close " + s.Name(), Help: "shell/tabs", Size: 16}) {
						closeSession(h, s.ID())
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
	if d := in.Dropped; d != nil {
		if id, ok := d.Data.(core.SessionID); ok && id != s.ID() {
			if err := h.Run(context.Background(), "session.tab.move", registry.Args{"id": string(id), "index": i + 1}); err != nil {
				h.Log().Error("sessiontabs", "move", id, "err", err)
			}
		}
	}
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
		h.Notify(core.Notice{ID: cmd, Severity: core.SevError, Source: "shell", Key: true, Text: err.Error(), Time: time.Now()})
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
