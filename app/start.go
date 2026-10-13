package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// StartView is what the main area shows with nothing open: no directory and no session (ui.md 01e).
// OverviewView is what it shows with a directory or session open but no view in it (01g).
const (
	StartView    = "app.start"
	OverviewView = "app.overview"
)

var (
	tStartCard     = theme.Declare("start.card", "bg.surface")
	tStartCardLine = theme.Declare("start.card.line", "line")
	tStartCardOn   = theme.DeclareOn("start.card.selected", "alpha(accent, 7%)", "bg.surface")
	tStartLineOn   = theme.Declare("start.card.selected.line", "alpha(accent, 45%)")
	tStartIconBg   = theme.DeclareOn("start.icon.bg", "alpha(accent, 8%)", "bg.surface")
	tStartRecentOn = theme.Declare("start.recent.selected", "bg.raised")
)

// start is the start view and its built-in actions, Open Directory and the directories picker.
type start struct {
	disposer
	sh  *shell
	sel int
}

func (s *start) Manifest() ext.Manifest { return ext.Manifest{ID: "start"} }

func (s *start) Activate(_ context.Context, h ext.Host) error {
	s.sel = -1 // the first action that's available
	for _, e := range []registry.StartEntry{
		{Name: "session.new", Icon: "plus", Title: "New Session", Desc: "A new session on a repository, with its own branch and worktree",
			Command: "session.new", Order: 10},
		{Name: "session.resume", Icon: "resume", Title: "Resume a Session", Desc: "Pick up an existing session where it was left",
			Command: "session.resume", Order: 20},
		{Name: "directory.open", Icon: "folder", Title: "Open Directory", Desc: "Open a directory as it is: no worktree, no new branch",
			Command: "directory.open", Order: 30},
		{Name: "settings", Icon: "settings", Title: "Settings", Desc: "Theme, keys, editor and everything else", Command: "settings.open", Order: 40},
	} {
		s.add(h.Contribute(e))
	}
	s.add(h.Command(registry.Command{ID: "directory.open", Title: "Open a directory as it is, with no worktree or branch: {path}, or pick one",
		Run: func(ctx registry.Ctx, a registry.Args) error {
			p, _ := a["path"].(string)
			if p == "" {
				return h.Run(ctx, "picker.open", registry.Args{"source": "directories"})
			}
			return s.sh.openDir(p)
		}}))
	showing := func(registry.Args) bool { return s.sh.cfg.WorkDir == "" }
	for _, c := range []registry.Command{
		{ID: "start.next", Title: "Select the next row of the start view", Available: showing, Run: func(registry.Ctx, registry.Args) error {
			s.sel++
			return nil
		}},
		{ID: "start.prev", Title: "Select the row before on the start view", Available: showing, Run: func(registry.Ctx, registry.Args) error {
			s.sel = max(s.sel-1, 0)
			return nil
		}},
		{ID: "start.run", Title: "Run the start view's selected row", Available: showing, Run: func(registry.Ctx, registry.Args) error {
			if rows := s.all(h); s.sel < len(rows) && rows[s.sel].ok {
				rows[s.sel].run()
			}
			return nil
		}},
		{ID: "start.forget", Title: "Forget the selected recent directory", Available: showing, Run: func(registry.Ctx, registry.Args) error {
			if rows := s.all(h); s.sel < len(rows) && rows[s.sel].forget != nil {
				rows[s.sel].forget()
			}
			return nil
		}},
	} {
		s.add(h.Command(c))
	}
	for _, k := range []registry.Keymap{
		{Keys: "j", Command: "start.next"}, {Keys: "<Down>", Command: "start.next"},
		{Keys: "k", Command: "start.prev"}, {Keys: "<Up>", Command: "start.prev"},
		{Keys: "<CR>", Command: "start.run"}, {Keys: "<BS>", Command: "start.forget"},
	} {
		k.Mode = EmptyKeyMode
		s.add(h.Keymap(k))
	}
	for _, k := range []registry.Keymap{
		{Keys: "<D-n>", Command: "session.new", Desc: "New Session"},
		{Keys: "<D-o>", Command: "session.resume", Desc: "Resume a Session"},
		{Keys: "<D-O>", Command: "directory.open", Desc: "Open Directory"},
	} {
		k.Mode = "global"
		s.add(h.Keymap(k))
	}
	s.add(h.Picker(directoriesSource(h, s.sh)))
	s.add(h.View(StartView, registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "start", Fn: func(c *ui.Ctx) { s.build(c, h) }}
	})))
	return nil
}

// openDir reopens the window on dir.
func (sh *shell) openDir(dir string) error {
	if strings.HasPrefix(dir, "~") {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, dir[1:])
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return fmt.Errorf("%s isn't a directory", tildePath(dir))
	}
	if sh.reopen == nil {
		return fmt.Errorf("can't open a directory here")
	}
	select {
	case <-sh.reopen:
	default:
	}
	sh.reopen <- dir
	sh.poke()
	return nil
}

// startRow is one selectable row: an action, or a recent session or directory.
type startRow struct {
	entry  *registry.StartEntry
	dir    string
	ok     bool
	run    func()
	forget func()
}

func (s *start) rows(h ext.Host) (actions, recent []startRow) {
	for _, e := range registry.StartEntries(h) {
		e := e
		ok := h.Available(e.Command, e.Args)
		actions = append(actions, startRow{entry: &e, ok: ok, run: func() {
			if err := h.Run(context.Background(), e.Command, e.Args); err != nil {
				h.Log().Error("start", "entry", e.Name, "err", err)
			}
		}})
	}
	for _, d := range s.sh.recents.list() {
		d := d
		recent = append(recent, startRow{dir: d.Path, ok: true,
			run:    func() { s.sh.openDir(d.Path) },
			forget: func() { s.sh.recents.forget(d.Path) }})
	}
	return actions, recent
}

func (s *start) build(c *ui.Ctx, h ext.Host) {
	th := c.Theme()
	actions, recent := s.rows(h)
	if s.sel < 0 {
		s.sel = slices.IndexFunc(actions, func(r startRow) bool { return r.ok })
	}
	s.sel = min(max(s.sel, 0), max(len(actions)+len(recent)-1, 0))
	c.Column("start", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Main: ui.Center, Cross: ui.Center, Clip: true, Help: "shell/start", Role: ui.RoleRegion,
		Label: "Start"}, func(c *ui.Ctx) {
		if len(recent) > 0 {
			c.Row("hello", ui.Style{W: ui.Px(1070), Cross: ui.Center, Gap: 16, Pad: ui.Insets{B: 26}}, func(c *ui.Ctx) {
				c.Paint("logo", ui.Px(44), ui.Px(44), func(f render.Frame, r render.Rect) { logo(f, r, th) })
				c.Text("word", "ORIEL", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Weight: typeset.Bold, Size: 26, Tracking: .32},
					Color: th.C(tWordmark)})
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				c.Text("version", s.sh.cfg.Version, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11}, Color: th.C(theme.FgFaint)})
			})
		}
		c.Row("cols", ui.Style{Gap: 90}, func(c *ui.Ctx) {
			c.Column("actions", ui.Style{W: ui.Px(520), Gap: 10}, func(c *ui.Ctx) {
				caption(c, "cap", "Start")
				for i, r := range actions {
					if s.action(c, h, i, r, i == s.sel) && r.ok {
						s.sel = i
						r.run()
					}
				}
			})
			c.Column("recent", ui.Style{W: ui.Px(460), Gap: 2}, func(c *ui.Ctx) {
				if len(recent) == 0 {
					c.Column("empty", ui.Style{W: ui.Flex(1), H: ui.Px(330), Main: ui.Center, Cross: ui.Center, Gap: 20}, func(c *ui.Ctx) {
						c.Paint("logo", ui.Px(300), ui.Px(300), func(f render.Frame, r render.Rect) { logo(f, r, th) })
						c.Text("none", "nothing recent yet", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11}, Color: th.C(theme.FgGhost)})
					})
					return
				}
				c.Row("head", ui.Style{W: ui.Flex(1), Pad: ui.Insets{B: 10}}, func(c *ui.Ctx) {
					caption(c, "cap", "Recent")
					c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
					c.Text("keys", "↵ open · ⌫ forget", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10}, Color: th.C(theme.FgFaint)})
				})
				c.Box("group", ui.Style{Pad: ui.Insets{L: 12, T: 6, B: 6}}, func(c *ui.Ctx) { caption(c, "cap", "Directories") })
				for i, r := range recent {
					n := len(actions) + i
					if s.recent(c, i, r, n == s.sel) {
						s.sel = n
						r.run()
					}
				}
			})
		})
		c.Row("foot", ui.Style{Pad: ui.Insets{T: 60}, Gap: 6, Cross: ui.Center}, func(c *ui.Ctx) {
			hint := ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11}, Color: th.C(theme.FgGhost)}
			c.Text("k", "⌘K search everything · ⌘? help · : command", hint)
		})
	})
}

func (s *start) all(h ext.Host) []startRow {
	actions, recent := s.rows(h)
	return append(actions, recent...)
}

func caption(c *ui.Ctx, key any, s string) {
	c.Text(key, strings.ToUpper(s), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9.5, Weight: typeset.SemiBold, Tracking: .14},
		Color: c.Theme().C(theme.FgFaint)})
}

// action is one action's card; it says whether it was clicked.
func (s *start) action(c *ui.Ctx, h ext.Host, i int, r startRow, on bool) bool {
	th := c.Theme()
	e := r.entry
	id := c.Next(e.Name)
	clicked := c.Interaction(id).Clicked
	fill, line := th.C(tStartCard), th.C(tStartCardLine)
	if on && r.ok {
		fill, line = th.C(tStartCardOn), th.C(tStartLineOn)
	}
	fade := float32(0)
	if !r.ok {
		fade = .58
	}
	c.Row(e.Name, ui.Style{W: ui.Flex(1), Pad: ui.PadXY(16, 14), Gap: 16, Cross: ui.Center, Fill: fill, Stroke: line, Border: 1, Radius: 10,
		Interactive: r.ok, Disabled: !r.ok, Cursor: render.CursorPointer, Fade: fade, Role: ui.RoleButton, Label: e.Title, Selected: on,
		Help: "shell/start"}, func(c *ui.Ctx) {
		col := th.C(theme.Accent)
		if !r.ok {
			col = th.C(theme.FgFaint)
		}
		c.Box("icon", ui.Style{W: ui.Px(38), H: ui.Px(38), Radius: 9, Fill: th.C(tStartIconBg), Main: ui.Center, Cross: ui.Center}, func(c *ui.Ctx) {
			icon(c, "glyph", e.Icon, 18, col)
		})
		c.Column("text", ui.Style{W: ui.Flex(1), Gap: 3}, func(c *ui.Ctx) {
			c.Row("title", ui.Style{Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("t", e.Title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 14, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
				if !r.ok {
					c.Box("soon", ui.Style{Pad: ui.PadXY(6, 1), Radius: 8, Stroke: th.C(theme.LineStrong), Border: 1}, func(c *ui.Ctx) {
						c.Text("t", "SOON", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9, Weight: typeset.SemiBold, Tracking: .1},
							Color: th.C(theme.FgFaint)})
					})
				}
			})
			c.Text("desc", e.Desc, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12}, Color: th.C(theme.FgFaint)})
		})
		if k := boundKey(h, e.Command, e.Args); k != "" {
			keycap(c, "key", k)
		}
	})
	return clicked
}

// keycap is a key drawn as a small key.
func keycap(c *ui.Ctx, key any, k string) {
	th := c.Theme()
	c.Box(key, ui.Style{Pad: ui.PadXY(7, 2), Radius: 4, Fill: th.C(theme.BgRaised), Stroke: th.C(theme.LineStrong), Border: 1}, func(c *ui.Ctx) {
		c.Text("k", k, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11, Weight: typeset.Medium}, Color: th.C(theme.FgMuted)})
	})
}

// recent is one recent directory's row; it says whether it was clicked.
func (s *start) recent(c *ui.Ctx, i int, r startRow, on bool) bool {
	th := c.Theme()
	id := c.Next(fmt.Sprint("dir", i))
	clicked := c.Interaction(id).Clicked
	fill := render.Color{}
	if on {
		fill = th.C(tStartRecentOn)
	}
	at := ""
	for _, d := range s.sh.recents.list() {
		if d.Path == r.dir {
			at = ago(time.Now(), d.At)
		}
	}
	c.Row(fmt.Sprint("dir", i), ui.Style{W: ui.Flex(1), Pad: ui.PadXY(12, 10), Gap: 12, Cross: ui.Center, Fill: fill, Radius: 8, Interactive: true,
		Cursor: render.CursorPointer, Role: ui.RoleButton, Label: filepath.Base(r.dir), Selected: on, Help: "shell/start"}, func(c *ui.Ctx) {
		git := isRepo(r.dir)
		bg, col := th.C(tStartIconBg), th.C(theme.Accent)
		if !git {
			bg, col = th.C(theme.BgOverlay), th.C(theme.FgFaint)
		}
		c.Box("icon", ui.Style{W: ui.Px(26), H: ui.Px(26), Radius: 6, Fill: bg, Main: ui.Center, Cross: ui.Center}, func(c *ui.Ctx) {
			icon(c, "glyph", "folder", 14, col)
		})
		c.Column("text", ui.Style{W: ui.Flex(1), Gap: 2}, func(c *ui.Ctx) {
			c.Text("name", filepath.Base(r.dir), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 13, Weight: typeset.Medium}, Color: th.C(theme.Fg)})
			sub := tildePath(r.dir)
			if b := branchOf(r.dir); b != "" {
				sub += " · " + b
			} else if !git {
				sub += " · not a git repo"
			}
			c.Text("path", sub, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10.5}, Color: th.C(theme.FgFaint)})
		})
		c.Text("at", at, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10.5}, Color: th.C(theme.FgGhost)})
	})
	return clicked
}

// branchOf is the branch checked out in the repo at dir, from its HEAD, or "".
func branchOf(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, ".git", "HEAD"))
	if err != nil {
		return ""
	}
	ref, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "ref: refs/heads/")
	if !ok {
		return ""
	}
	return ref
}

func isRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// ago is how long before now t was, shortly: 10m, 2h, 3d, 1w.
func ago(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
	return fmt.Sprintf("%dw", int(d.Hours()/24/7))
}
