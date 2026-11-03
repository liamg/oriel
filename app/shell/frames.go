package shell

import (
	"cmp"
	"context"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// The built-in frames: Home while no session is running, a workspace while one is.
const (
	HomeFrame      = "home"
	WorkspaceFrame = "workspace"
	// HomeView is Home's main area: the start view, and a view opened there as a page over it.
	HomeView = "app.home"
)

// frames contributes Home and Workspace.
type frames struct{ kit.Disposer }

func (x *frames) Manifest() plugin.Manifest { return plugin.Manifest{ID: "frames"} }

func (x *frames) Activate(_ context.Context, h plugin.Host) error {
	x.Add(h.Contribute(uireg.Frame{ID: WorkspaceFrame, Order: 50, When: sessionRunning, Regions: []uireg.Region{
		{View: kit.TitleBarView, Area: uireg.AreaTop},
		{View: RailView, Area: uireg.AreaLeft},
		{View: kit.RootView, Area: uireg.AreaMain},
		{View: kit.StatusLineView, Area: uireg.AreaBottom},
		{View: NotificationsView, Area: uireg.AreaOver},
		{View: ModalView, Area: uireg.AreaOver},
	}}))
	x.Add(h.Contribute(uireg.Frame{ID: HomeFrame, Order: 100, Global: true, Regions: []uireg.Region{
		{View: kit.TitleBarView, Area: uireg.AreaTop},
		{View: HomeView, Area: uireg.AreaMain},
		{View: kit.StatusLineView, Area: uireg.AreaBottom},
		{View: NotificationsView, Area: uireg.AreaOver},
		{View: ModalView, Area: uireg.AreaOver},
	}}))
	x.Add(h.View(HomeView, uireg.ViewFunc(func(vc uireg.ViewCtx) ui.Node {
		return &ui.Func{Key: "home", Fn: func(c *ui.Ctx) { home(c, h, vc) }}
	})))
	return nil
}

// home is the start view, with the view opened last (the log, the inbox) as a page over it: Home has no layout to
// dock it in. Closing the page closes the view.
func home(c *ui.Ctx, h plugin.Host, vc uireg.ViewCtx) {
	if v, ok := h.ViewByID(StartView); ok {
		c.Column(StartView, ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) { c.Node(v.Build(vc)) })
	}
	shown := h.Layout().Visible()
	if len(shown) == 0 {
		return
	}
	ref := shown[len(shown)-1]
	v, ok := h.ViewByID(ref.ID)
	if !ok {
		return
	}
	title := cmp.Or(ref.Title, ref.ID)
	if t, ok := uireg.As[uireg.Titled](v); ok {
		title = cmp.Or(t.Title(ref.Instance), title)
	}
	open := true
	widget.Page(c, "view", &open, widget.PageOpts{Label: title + " page", Help: "shell/start"}, func(c *ui.Ctx) {
		th := c.Theme()
		c.Row("top", ui.Style{W: ui.Flex(1), H: ui.Px(42), Pad: ui.PadXY(18, 0), Gap: 12, Cross: ui.Center, Fill: th.C(kit.TStatsHead)}, func(c *ui.Ctx) {
			c.Text("h", strings.ToUpper(title), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 14, Weight: typeset.SemiBold,
				Tracking: .04}, Color: th.C(theme.Fg)})
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if widget.Button(c, "close", "Close", widget.ButtonOpts{Key: "esc", Help: "shell/start"}) {
				open = false
			}
		})
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
		vc := vc
		vc.Instance, vc.Focused, vc.Project = ref.Instance, true, ref.Project
		c.Column("view", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.Pad(10)}, func(c *ui.Ctx) { c.Node(v.Build(vc)) })
	})
	if !open {
		h.Layout().Close(ref)
	}
}

// sessionRunning says a session is running in the window: one is in front, or the window is on a directory (its direct
// session, with a session host).
func sessionRunning(w uireg.Window) bool { return w.Session != nil || w.Dir != "" }

// frameWindow is what the window's frame is chosen by, with st the state and dir the directory the window is on.
func frameWindow(st core.State, dir string) uireg.Window {
	w := uireg.Window{Dir: dir}
	if se, ok := st.Session(st.Focus); ok {
		if s := se.Snapshot(); !s.Archived {
			w.Session = &s
		}
	}
	return w
}

// buildWindow is the window: win's frame, its top regions over its left ones beside the main area, its bottom ones
// under them, each a widget.Area, and its over regions and every app.overlay.* view floating over it all.
func buildWindow(c *ui.Ctx, reg uireg.Registry, size render.Point, win uireg.Window) {
	f, _ := uireg.FrameFor(reg, win)
	vc := uireg.ViewCtx{Width: size.X, Height: size.Y, Session: win.Session}
	if f.Global {
		vc.Session, vc.Global = nil, true
	}
	region := func(c *ui.Ctx, a uireg.Area, st ui.Style) {
		for _, r := range f.Regions {
			if v, ok := reg.ViewByID(r.View); ok && r.Area == a {
				c.Box(r.View, st, func(c *ui.Ctx) { c.Node(v.Build(vc)) })
			}
		}
	}
	c.Column("shell", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
		widget.Area(c, widget.AreaPage, "paged", ui.Style{Dir: ui.Column, W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
			widget.Area(c, widget.AreaTop, "top", ui.Style{Dir: ui.Column, W: ui.Flex(1)}, func(c *ui.Ctx) {
				region(c, uireg.AreaTop, ui.Style{W: ui.Flex(1)})
			})
			c.Row("main", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
				widget.Area(c, widget.AreaSide, "side", ui.Style{H: ui.Flex(1)}, func(c *ui.Ctx) {
					region(c, uireg.AreaLeft, ui.Style{H: ui.Flex(1)})
				})
				widget.Area(c, widget.AreaMain, "area", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
					region(c, uireg.AreaMain, ui.Style{W: ui.Flex(1), H: ui.Flex(1)})
				})
			})
		})
		widget.Area(c, widget.AreaBottom, "bottom", ui.Style{Dir: ui.Column, W: ui.Flex(1)}, func(c *ui.Ctx) {
			region(c, uireg.AreaBottom, ui.Style{W: ui.Flex(1)})
		})
	})
	over := ui.Style{W: ui.Px(0), H: ui.Px(0)} // its content floats
	region(c, uireg.AreaOver, over)
	for _, id := range reg.Views() {
		if v, ok := reg.ViewByID(id); ok && strings.HasPrefix(id, kit.OverlayPrefix) {
			c.Box(id, over, func(c *ui.Ctx) { c.Node(v.Build(vc)) })
		}
	}
}
