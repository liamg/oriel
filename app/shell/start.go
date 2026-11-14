package shell

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry/uireg"

	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// StartView is what the main area shows with nothing open: no directory and no session.
// OverviewView is what it shows with a directory or session open but no view in it. OpeningView is what it
// shows while the window is about to open again on another directory or session.
const (
	StartView          = "app.start"
	OverviewView       = kit.OverviewView
	OpeningView        = "app.opening"
	OverviewPanelsView = kit.OverviewPanelsView
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
	kit.Disposer
	sh  *shell
	sel int
	// hovered is the row under the pointer last frame, or -1; the pointer moving onto a row selects it.
	hovered, hovering int
	w                 float32 // the view's width last frame
	dirs              startDirs
}

// startDirs are the recent directories as the recents file last read.
type startDirs struct {
	data   []byte // the file as read
	recent []recentDir
	forgot map[string]bool // forgotten since
}

// startDir is a recent directory, whether it's a git repo, and its branch.
type startDir struct {
	recentDir
	git    bool
	branch string
}

// recentDirs are the recent directories, with their repos' branches, read through the window's reads.
func (s *start) recentDirs(h plugin.Host) []startDir {
	rd, ok := kit.ReadsOf(h)
	if !ok || s.sh.recents == nil {
		return nil
	}
	d := &s.dirs
	if f, _ := rd.File(s.sh.recents.path); !kit.SameBytes(f.Data, d.data) {
		var rf recentFile
		json.Unmarshal(f.Data, &rf)
		d.data, d.recent, d.forgot = f.Data, rf.Directories, nil
	}
	var out []startDir
	for _, r := range d.recent {
		if d.forgot[r.Path] {
			continue
		}
		git, _ := rd.Stat(filepath.Join(r.Path, ".git"))
		head, _ := rd.File(filepath.Join(r.Path, ".git", "HEAD"))
		out = append(out, startDir{recentDir: r, git: git != nil, branch: headBranch(head.Data)})
	}
	return out
}

// The start view's widths: both columns at full size, as 39-start, down to startNarrow, where they become tabs.
const (
	startWide   = 1070
	startNarrow = 760
)

func (s *start) Manifest() plugin.Manifest { return plugin.Manifest{ID: "start"} }

func (s *start) Activate(_ context.Context, h plugin.Host) error {
	s.sel, s.hovered = -1, -1 // the first action that's available
	for _, e := range []uireg.StartEntry{
		{Name: "session.new", Icon: "plus", Title: "New Session", Desc: "A new session on a repository, with its own branch and worktree",
			Command: "session.new", Order: 10},
		{Name: "session.resume", Icon: "resume", Title: "Resume a Session", Desc: "Pick up an existing session where it was left",
			Command: "session.resume", Order: 20},
		{Name: "directory.open", Icon: "folder", Title: "Open Directory", Desc: "Open a directory as it is: no worktree, no new branch",
			Command: "directory.open", Order: 30},
		{Name: "settings", Icon: "settings", Title: "Settings", Desc: "Theme, keys, editor and everything else", Command: "settings.open", Order: 40},
	} {
		s.Add(h.Contribute(e))
	}
	s.Add(h.Command(registry.Command{ID: "directory.open", Title: "Open a directory as it is, with no worktree or branch: {path}, or pick one",
		Run: func(ctx registry.Ctx, a registry.Args) error {
			p := a.String("path")
			if p == "" {
				return h.Run(ctx, "picker.open", registry.Args{"source": "directories"})
			}
			return s.sh.openDir(ctx, h, p)
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
		s.Add(h.Command(c))
	}
	for _, k := range []registry.Keymap{
		{Keys: "j", Command: "start.next"}, {Keys: "<Down>", Command: "start.next"},
		{Keys: "k", Command: "start.prev"}, {Keys: "<Up>", Command: "start.prev"},
		{Keys: "<CR>", Command: "start.run"}, {Keys: "<BS>", Command: "start.forget"},
	} {
		k.Mode = EmptyKeyMode
		s.Add(h.Keymap(k))
	}
	for _, k := range []registry.Keymap{
		{Keys: "<D-n>", Command: "session.new", Desc: "New Session"},
		{Keys: "<D-o>", Command: "session.resume", Desc: "Resume a Session"},
		{Keys: "<D-O>", Command: "directory.open", Desc: "Open Directory"},
	} {
		k.Mode = "global"
		s.Add(h.Keymap(k))
	}
	s.Add(h.Contribute(registry.ExclusiveKeys{Mode: EmptyKeyMode, Active: func() bool { return s.sh.cfg.WorkDir == "" },
		Allow: func(k registry.Keymap) bool { return s.keeps(h, k) }, Source: "builtin:start"}))
	dirs, walks := directoriesSource(h, s.sh)
	s.Add(h.Picker(dirs))
	s.Add(walks)
	s.Add(h.View(StartView, uireg.ViewFunc(func(uireg.ViewCtx) ui.Node {
		return &ui.Func{Key: "start", Fn: func(c *ui.Ctx) { s.build(c, h) }}
	})))
	s.Add(h.View(OpeningView, uireg.ViewFunc(func(uireg.ViewCtx) ui.Node {
		return &ui.Func{Key: "opening", Fn: func(c *ui.Ctx) { s.opening(c, h) }}
	})))
	return nil
}

// opening is the logo and what the window is opening, while the loop starts every plugin again on it.
func (s *start) opening(c *ui.Ctx, h plugin.Host) {
	to := s.sh.heading.Load()
	if to == nil {
		return
	}
	what := filepath.Base(to.dir)
	if se, ok := h.State().Session(to.session); ok {
		what = se.Name()
	}
	th := c.Theme()
	c.Column("opening", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Main: ui.Center, Cross: ui.Center, Gap: 18, Role: ui.RoleRegion, Label: "Opening"},
		func(c *ui.Ctx) {
			c.Paint("logo", ui.Px(44), ui.Px(44), func(f render.Frame, r render.Rect) { kit.Logo(f, r, th) })
			widget.Busy(c, "busy", widget.LoadState{}, widget.BusyOpts{Label: "Opening " + what, Size: 13})
		})
}

// keeps says whether a key from beyond the start view works while it shows: only its own entries' and links'.
func (s *start) keeps(h plugin.Host, k registry.Keymap) bool {
	return slices.ContainsFunc(uireg.StartEntries(h), func(e uireg.StartEntry) bool { return e.Command == k.Command })
}

// openDir reopens the window on dir, found off the loop. A caller that waits is told once the directory's session is
// in front, not only found.
func (sh *shell) openDir(ctx registry.Ctx, h plugin.Host, dir string) error {
	done := func(error) {}
	if finish, waited := registry.Later(ctx); waited {
		done = finish
		ctx = registry.OnDone(context.WithoutCancel(ctx), func(err error) {
			if err != nil {
				finish(err)
			}
		})
	}
	return plugin.RunSlow(ctx, h, plugin.Slow{ID: "directory.open", Doing: "Opening " + kit.TildePath(dir)},
		func(context.Context) (string, error) {
			if strings.HasPrefix(dir, "~") {
				home, _ := os.UserHomeDir()
				dir = filepath.Join(home, dir[1:])
			}
			abs, err := filepath.Abs(dir)
			if err != nil {
				return "", err
			}
			if st, err := os.Stat(abs); err != nil || !st.IsDir() {
				return "", fmt.Errorf("%s isn't a directory", kit.TildePath(abs))
			}
			return abs, nil
		},
		func(ctx registry.Ctx, dir string) error {
			if kit.HasSessions(sh.cfg.Registry) && sh.cfg.Registry.Available("session.open-dir", nil) {
				return sh.cfg.Registry.Run(registry.OnDone(ctx, done), "session.open-dir", registry.Args{"dir": dir})
			}
			err := sh.reopenOn(reopenTo{dir: dir})
			done(err)
			return err
		})
}

// reopenTo is a directory for the window to start again on, and the session it belongs to ("" for none).
type reopenTo struct {
	dir     string
	session core.SessionID
	asked   time.Time // when it was asked for, the cover going up
}

func (sh *shell) reopenOn(to reopenTo) error {
	if sh.reopen == nil {
		return fmt.Errorf("can't open a directory here")
	}
	select {
	case <-sh.reopen:
	default:
	}
	to.asked = time.Now()
	sh.heading.Store(&to)
	sh.reopen <- to
	sh.poke()
	return nil
}

// startRow is one selectable row: an action or a link, or a recent session or directory.
type startRow struct {
	entry   *uireg.StartEntry
	link    bool
	session *core.SessionState
	dir     string
	recent  startDir // a directory's
	ok      bool
	run     func()
	forget  func()
}

// rows are the view's rows in the order the keys move through them: the action cards, the links under them, then
// the recent sessions and directories.
func (s *start) rows(h plugin.Host) (actions, recent []startRow) {
	var links []startRow
	for _, e := range uireg.StartEntries(h) {
		e := e
		ok := h.Available(e.Command, e.Args)
		if e.Unlisted || e.Link && !ok {
			continue
		}
		r := startRow{entry: &e, link: e.Link, ok: ok, run: func() {
			if err := h.Run(context.Background(), e.Command, e.Args); err != nil {
				h.Log().Error("start", "entry", e.Name, "err", err)
			}
		}}
		if e.Link {
			links = append(links, r)
		} else {
			actions = append(actions, r)
		}
	}
	actions = append(actions, links...)
	sessions := slices.Clone(h.State().Sessions)
	sessions = slices.DeleteFunc(sessions, func(se core.SessionState) bool { return se.Archived })
	slices.SortStableFunc(sessions, func(a, b core.SessionState) int { return b.Created.Compare(a.Created) })
	for _, se := range sessions {
		recent = append(recent, startRow{session: &se, ok: true, run: func() {
			if err := h.Run(context.Background(), "session.open", registry.Args{"id": string(se.ID)}); err != nil {
				h.Log().Error("start", "session", se.ID, "err", err)
			}
		}})
	}
	for _, d := range s.recentDirs(h) {
		if slices.ContainsFunc(sessions, func(se core.SessionState) bool {
			return se.InPlace() && filepath.Clean(se.Dir) == filepath.Clean(d.Path)
		}) {
			continue
		}
		recent = append(recent, startRow{dir: d.Path, recent: d, ok: true,
			run: func() {
				if err := s.sh.openDir(context.Background(), h, d.Path); err != nil {
					h.Log().Error("start", "dir", d.Path, "err", err)
				}
			},
			forget: func() {
				s.sh.recents.forget(d.Path)
				if s.dirs.forgot == nil {
					s.dirs.forgot = map[string]bool{}
				}
				s.dirs.forgot[d.Path] = true
			}})
	}
	return actions, recent
}

func (s *start) build(c *ui.Ctx, h plugin.Host) {
	actions, recent := s.rows(h)
	if s.sel < 0 {
		s.sel = slices.IndexFunc(actions, func(r startRow) bool { return r.ok })
	}
	s.sel = min(max(s.sel, 0), max(len(actions)+len(recent)-1, 0))
	id := c.Next("start")
	w, main := c.Size().X, ui.Center
	if r, ok := c.Rect(id); ok {
		w = r.Dx()
		if in, ok := c.Rect(ui.Child(id, "content")); ok && in.Dy() > r.Dy() {
			main = ui.Start // too tall for the window: it scrolls from the top
		}
	}
	if w != s.w {
		s.w = w
		c.WakeAt(c.Now()) // laid out at the new width next frame
	}
	narrow := w < startNarrow
	pad, gap := float32(32), float32(90)
	if w < startWide+2*pad {
		gap = 48
	}
	if narrow {
		pad = 16
	}
	s.hovering = -1
	c.Column("start", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Main: main, Cross: ui.Center, Pad: ui.PadXY(pad, 0), ScrollY: true, Help: "shell/start",
		Role: ui.RoleRegion, Label: "Start"}, func(c *ui.Ctx) {
		s.content(c, h, actions, recent, narrow, gap)
	})
	s.hovered = s.hovering
}

func (s *start) content(c *ui.Ctx, h plugin.Host, actions, recent []startRow, narrow bool, gap float32) {
	th := c.Theme()
	c.Column("content", ui.Style{W: ui.Flex(1), Cross: ui.Center, Pad: ui.Insets{T: 24, B: 24}}, func(c *ui.Ctx) {
		width := ui.Flex(1).Clamp(0, startWide)
		if narrow {
			width = ui.Flex(1).Clamp(0, 520)
		}
		if len(recent) > 0 {
			c.Row("hello", ui.Style{W: width, Cross: ui.Center, Gap: 16, Pad: ui.Insets{B: 26}}, func(c *ui.Ctx) {
				c.Paint("logo", ui.Px(44), ui.Px(44), func(f render.Frame, r render.Rect) { kit.Logo(f, r, th) })
				c.Text("word", "ORIEL", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Weight: typeset.Bold, Size: 26, Tracking: .32},
					Color: th.C(kit.TWordmark)})
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				c.Text("version", s.sh.cfg.Version, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11}, Color: th.C(theme.FgFaint)})
			})
		}
		actionsCol := func(c *ui.Ctx, st ui.Style) {
			c.Column("actions", st, func(c *ui.Ctx) {
				if !narrow {
					kit.Caption(c, "cap", "Start")
				}
				links := slices.IndexFunc(actions, func(r startRow) bool { return r.link })
				if links < 0 {
					links = len(actions)
				}
				for i, r := range actions[:links] {
					if s.action(c, h, i, r) && r.ok {
						s.sel = i
						r.run()
					}
				}
				s.links(c, links, actions[links:])
			})
		}
		recentCol := func(c *ui.Ctx, st ui.Style) {
			c.Column("recent", st, func(c *ui.Ctx) {
				if !narrow {
					c.Row("head", ui.Style{W: ui.Flex(1), Pad: ui.Insets{B: 10}}, func(c *ui.Ctx) {
						kit.Caption(c, "cap", "Recent")
						c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
						c.Text("keys", "↵ open · ⌫ forget", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10}, Color: th.C(theme.FgFaint)})
					})
				}
				group := ""
				for i, r := range recent {
					n := len(actions) + i
					g := "Directories"
					if r.session != nil {
						g = "Sessions"
					}
					if g != group {
						group = g
						c.Box("group"+g, ui.Style{Pad: ui.Insets{L: 12, T: 6, B: 6}}, func(c *ui.Ctx) { kit.Caption(c, "cap", g) })
					}
					if r.session != nil {
						if s.recentSession(c, n, r) {
							s.sel = n
							r.run()
						}
						continue
					}
					if s.recent(c, n, r) {
						s.sel = n
						r.run()
					}
				}
			})
		}
		switch {
		case narrow && len(recent) > 0:
			c.Column("tabbed", ui.Style{W: width, Gap: 14}, func(c *ui.Ctx) {
				tab := 0
				if s.sel >= len(actions) {
					tab = 1
				}
				if widget.Tabs(c, "tabs", []string{"START", "RECENT"}, &tab, widget.TabsOpts{Help: "shell/start"}) {
					s.sel = len(actions)
					if tab == 0 {
						s.sel = max(slices.IndexFunc(actions, func(r startRow) bool { return r.ok }), 0)
					}
				}
				if tab == 0 {
					actionsCol(c, ui.Style{W: ui.Flex(1), Gap: 10})
				} else {
					recentCol(c, ui.Style{W: ui.Flex(1), Gap: 2})
				}
			})
		case narrow:
			actionsCol(c, ui.Style{W: width, Gap: 10})
		default:
			c.Row("cols", ui.Style{W: width, Gap: gap}, func(c *ui.Ctx) {
				actionsCol(c, ui.Style{W: ui.Flex(52).Clamp(300, 520), Gap: 10})
				if len(recent) > 0 {
					recentCol(c, ui.Style{W: ui.Flex(46).Clamp(260, 460), Gap: 2})
					return
				}
				c.Column("recent", ui.Style{W: ui.Flex(46).Clamp(260, 460), H: ui.Px(330), Main: ui.Center, Cross: ui.Center}, func(c *ui.Ctx) {
					c.Paint("logo", ui.Px(300), ui.Px(300), func(f render.Frame, r render.Rect) { kit.Logo(f, r, th) })
				})
			})
		}
	})
}

// hover notes that row n is under the pointer, and selects it when the pointer has just moved onto it.
func (s *start) hover(c *ui.Ctx, n int, in ui.Interaction) {
	if !in.Hovered {
		return
	}
	s.hovering = n
	if n != s.hovered && n != s.sel {
		s.sel = n
		c.WakeAt(c.Now()) // rows already built this frame still show the old selection
	}
}

func (s *start) all(h plugin.Host) []startRow {
	actions, recent := s.rows(h)
	return append(actions, recent...)
}

// action is one action's card; it says whether it was clicked.
func (s *start) action(c *ui.Ctx, h plugin.Host, i int, r startRow) bool {
	th := c.Theme()
	e := r.entry
	on := i == s.sel
	fill, line := th.C(tStartCard), th.C(tStartCardLine)
	if on && r.ok {
		fill, line = th.C(tStartCardOn), th.C(tStartLineOn)
	}
	fade := float32(0)
	if !r.ok {
		fade = .58
	}
	in := widget.Clickable(c, e.Name, ui.Style{Dir: ui.Row, W: ui.Flex(1), Pad: ui.PadXY(16, 14), Gap: 16, Cross: ui.Center, Fill: fill, Stroke: line,
		Border: 1, Radius: 10, Disabled: !r.ok, NoHover: true, Fade: fade, Label: e.Title, Selected: on, Help: "shell/start"}, func(c *ui.Ctx) {
		col := th.C(theme.Accent)
		if !r.ok {
			col = th.C(theme.FgFaint)
		}
		c.Box("icon", ui.Style{W: ui.Px(38), H: ui.Px(38), Radius: 9, Fill: th.C(tStartIconBg), Main: ui.Center, Cross: ui.Center}, func(c *ui.Ctx) {
			icon(c, "glyph", e.Icon, 18, col)
		})
		c.Column("text", ui.Style{W: ui.Flex(1), Gap: 3, Clip: true}, func(c *ui.Ctx) {
			c.Row("title", ui.Style{Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("t", e.Title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 14, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
				if note := entryNote(e); note != "" && r.ok {
					c.Text("n", note, kit.MonoFont(10, th.C(theme.FgFaint)))
				}
				if !r.ok {
					c.Box("soon", ui.Style{Pad: ui.PadXY(6, 1), Radius: 8, Stroke: th.C(theme.LineStrong), Border: 1}, func(c *ui.Ctx) {
						c.Text("t", "SOON", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9, Weight: typeset.SemiBold, Tracking: .1},
							Color: th.C(theme.FgFaint)})
					})
				}
			})
			c.Text("desc", e.Desc, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12}, Color: th.C(theme.FgFaint)})
		})
		if k := kit.BoundKey(h, e.Command, e.Args); k != "" {
			kit.Keycap(c, "key", k)
		}
	})
	s.hover(c, i, in)
	return in.Clicked
}

// links are the link entries under the actions, rows from..., each a page with its figure.
func (s *start) links(c *ui.Ctx, from int, links []startRow) {
	if len(links) == 0 {
		return
	}
	th := c.Theme()
	c.Row("links", ui.Style{W: ui.Flex(1), Gap: 8, Pad: ui.Insets{T: 2}, Wrap: true}, func(c *ui.Ctx) {
		for i, r := range links {
			e, n := r.entry, from+i
			note := entryNote(e)
			fill, line := th.C(tStartCard), th.C(tStartCardLine)
			if n == s.sel {
				fill, line = th.C(tStartCardOn), th.C(tStartLineOn)
			}
			in := widget.Clickable(c, e.Name, ui.Style{Dir: ui.Row, H: ui.Px(26), Pad: ui.PadXY(10, 0), Gap: 7, Cross: ui.Center, Radius: 13,
				Fill: fill, Stroke: line, Border: 1, NoHover: true, Label: e.Title, Tooltip: e.Desc, Selected: n == s.sel, Help: "shell/start#links"},
				func(c *ui.Ctx) {
					icon(c, "icon", e.Icon, 12, th.C(theme.Accent))
					c.Text("t", e.Title, kit.UIFont(12, th.C(theme.Fg)))
					if note != "" {
						c.Text("n", note, kit.MonoFont(10, th.C(theme.FgFaint)))
					}
				})
			s.hover(c, n, in)
			if in.Clicked {
				s.sel = n
				r.run()
			}
		}
	})
}

// entryNote is an entry's figure, or "".
func entryNote(e *uireg.StartEntry) (note string) {
	if e.Note != nil {
		registry.Call(func() error { note = e.Note(); return nil })
	}
	return note
}

// recent is one recent directory's row, row n of the view; it says whether it was clicked.
func (s *start) recent(c *ui.Ctx, n int, r startRow) bool {
	th := c.Theme()
	on := n == s.sel
	fill := render.Color{}
	if on {
		fill = th.C(tStartRecentOn)
	}
	in := widget.Clickable(c, fmt.Sprint("dir", n), ui.Style{Dir: ui.Row, W: ui.Flex(1), Pad: ui.PadXY(12, 10), Gap: 12, Cross: ui.Center, Fill: fill,
		Radius: 8, NoHover: true, Label: filepath.Base(r.dir), Selected: on, Help: "shell/start"}, func(c *ui.Ctx) {
		git := r.recent.git
		bg, col := th.C(tStartIconBg), th.C(theme.Accent)
		if !git {
			bg, col = th.C(theme.BgOverlay), th.C(theme.FgFaint)
		}
		c.Box("icon", ui.Style{W: ui.Px(26), H: ui.Px(26), Radius: 6, Fill: bg, Main: ui.Center, Cross: ui.Center}, func(c *ui.Ctx) {
			icon(c, "glyph", "folder", 14, col)
		})
		c.Column("text", ui.Style{W: ui.Flex(1), Gap: 2, Clip: true}, func(c *ui.Ctx) {
			c.Text("name", filepath.Base(r.dir), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 13, Weight: typeset.Medium}, Color: th.C(theme.Fg)})
			sub := kit.TildePath(r.dir)
			if b := r.recent.branch; b != "" {
				sub += " · " + b
			} else if !git {
				sub += " · not a git repo"
			}
			c.Text("path", sub, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10.5}, Color: th.C(theme.FgFaint)})
		})
		if !widget.PointerIn(c) {
			widget.Ago(c, "at", r.recent.At, widget.AgoOpts{Text: ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10.5}, Color: th.C(theme.FgGhost)}, Now: time.Now()})
		} else if widget.IconButton(c, "forget", "✕", widget.IconButtonOpts{Label: "Forget " + filepath.Base(r.dir), Help: "shell/start"}) {
			r.forget()
		}
	})
	s.hover(c, n, in)
	return in.Clicked
}

// recentSession is one session's row, row n of the view; it says whether it was clicked.
func (s *start) recentSession(c *ui.Ctx, n int, r startRow) bool {
	th := c.Theme()
	se := r.session
	fill := render.Color{}
	if n == s.sel {
		fill = th.C(tStartRecentOn)
	}
	name := kit.Label(*se)
	in := widget.Clickable(c, "session."+string(se.ID), ui.Style{Dir: ui.Row, W: ui.Flex(1), Pad: ui.PadXY(12, 10), Gap: 12, Cross: ui.Center,
		Fill: fill, Radius: 8, NoHover: true, Label: name, Selected: n == s.sel, Help: "shell/start#sessions"}, func(c *ui.Ctx) {
		tone := th.C(kit.StatusTokens[se.Status])
		c.Box("icon", ui.Style{W: ui.Px(26), H: ui.Px(26), Radius: 6, Fill: th.C(theme.BgOverlay), Main: ui.Center, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Paint("diamond", ui.Px(12), ui.Px(12), func(f render.Frame, r render.Rect) {
				cx, cy, h := (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, r.Dx()/2
				p := render.Path{}
				p.MoveTo(render.Point{X: cx, Y: cy - h})
				p.LineTo(render.Point{X: cx + h, Y: cy})
				p.LineTo(render.Point{X: cx, Y: cy + h})
				p.LineTo(render.Point{X: cx - h, Y: cy})
				p.Close()
				f.Path(&p, render.Style{Fill: tone})
			})
		})
		c.Column("text", ui.Style{W: ui.Flex(1), Gap: 2, Clip: true}, func(c *ui.Ctx) {
			c.Text("name", name, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 13, Weight: typeset.Medium}, Color: th.C(theme.Fg)})
			c.Text("path", sessionMeta(*se), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10.5}, Color: th.C(theme.FgFaint)})
		})
		if !se.Created.IsZero() {
			widget.Ago(c, "at", se.Created, widget.AgoOpts{Text: ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10.5}, Color: th.C(theme.FgGhost)}})
		}
	})
	s.hover(c, n, in)
	return in.Clicked
}

// sessionMeta is a session's kind, projects and branch: "plan · pricing · rethink/rounding".
func sessionMeta(se core.SessionState) string {
	parts := []string{string(se.Kind)}
	var names []string
	for _, p := range se.Projects {
		names = append(names, p.Name)
	}
	if len(names) > 0 {
		parts = append(parts, strings.Join(names, ", "))
	}
	if se.Branch != "" {
		parts = append(parts, se.Branch)
	}
	return strings.Join(parts, " · ")
}

// headBranch is the branch a repo's HEAD file names, or "".
func headBranch(head []byte) string {
	ref, ok := strings.CutPrefix(strings.TrimSpace(string(head)), "ref: refs/heads/")
	if !ok {
		return ""
	}
	return ref
}

func isRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}
