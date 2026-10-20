package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

const (
	workspaceHelp    = "agent/workspace"
	WorkspaceKeyMode = "workspace"
	// wsEvery is how often an open Workspace page reads the session again.
	wsEvery = 3 * time.Second
	// wsLogTail is how much of a build log the page shows: its end.
	wsLogTail = 256 << 10
	// wsDirMax is how many entries of a directory the file list shows.
	wsDirMax = 500
)

// The Workspace page's tabs, by their name in workspace.open.
var wsTabs = []string{"overview", "files", "image", "build log", "permissions"}

const (
	wsOverview = iota
	wsFiles
	wsImage
	wsLog
	wsPermissions
)

// workspacePlugin is the Workspace page: where the session in front's agent works, the sandbox it runs in and its
// image, and what it may do (Permissions).
type workspacePlugin struct {
	disposer
	sh    *shell
	h     plugin.Host
	perms *permissionsPlugin
	open  bool
	tab   int
	by    map[core.SessionID]*wsState
}

// wsState is one session's page: its reads, and what's picked.
type wsState struct {
	load       *plugin.Load[host.Workspace]
	log        *plugin.Load[string]
	read       time.Time
	logRead    time.Time
	dir        int                      // the files tab's directory, of wsBrowsable
	entries    map[string][]os.DirEntry // the directories read so far
	rebuilding bool
	logSel     int
}

func (x *workspacePlugin) Manifest() plugin.Manifest { return plugin.Manifest{ID: "workspace"} }

func (x *workspacePlugin) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	x.by = map[core.SessionID]*wsState{}
	session := func(a registry.Args) core.SessionID {
		if id := a.String("id"); id != "" {
			return core.SessionID(id)
		}
		return h.State().Focus
	}
	inFront := func(registry.Args) bool { return x.sh.cfg.Host != nil && h.State().Focus != "" }
	for _, c := range []registry.Command{
		{ID: "workspace.open", Title: "Show where the session in front's agent works, its sandbox and image, and what it may do: {tab} overview, files, image, build log or permissions",
			Available: inFront,
			Run: func(_ registry.Ctx, a registry.Args) error {
				if t := a.String("tab"); t != "" {
					i := slices.Index(wsTabs, t)
					if i < 0 {
						return fmt.Errorf("no workspace tab %q: %s", t, strings.Join(wsTabs, ", "))
					}
					x.tab = i
				}
				x.open = true
				return nil
			}},
		{ID: "workspace.rebuild", Title: "Build the sandbox image of {id}, the session in front by default, again without the cache, then restart its agent on it",
			Available: inFront, Run: func(_ registry.Ctx, a registry.Args) error { return x.rebuild(session(a)) }},
		{ID: "workspace.restart", Title: "Restart the agent of {id}, the session in front by default, in a new sandbox once its turn ends",
			Available: inFront, Run: func(_ registry.Ctx, a registry.Args) error {
				id := session(a)
				if err := x.sh.cfg.Host.RestartSandbox(id); err != nil {
					return err
				}
				x.h.Do(func() { x.state(id).load.Reload() })
				return nil
			}},
	} {
		x.add(h.Command(c))
	}
	if x.sh.cfg.Host != nil {
		x.add(h.Contribute(registry.RailEntry{Name: "workspace", Icon: "workspace", Title: "Workspace", Command: "workspace.open", Order: 75,
			Active: func(string) bool { return x.open }}))
	}
	x.add(h.Contribute(pagePlace("workspace", "Workspace", "workspace.open", &x.open)))
	x.add(h.View(OverlayPrefix+"workspace", registry.ViewFunc(func(registry.ViewCtx) ui.Node { return &ui.Func{Key: "workspace", Fn: x.page} })))
	return nil
}

// state is session id's page, its reads made on first use.
func (x *workspacePlugin) state(id core.SessionID) *wsState {
	st := x.by[id]
	if st != nil {
		return st
	}
	st = &wsState{entries: map[string][]os.DirEntry{}}
	st.load = plugin.NewLoad(x.h, func(ctx context.Context, _ func(string, float64)) (host.Workspace, error) {
		w := x.sh.cfg.Host
		if w == nil {
			return host.Workspace{}, errors.New("there are no sessions in this window")
		}
		return w.Workspace(ctx, id)
	}, plugin.LoadOpts{Timeout: 20 * time.Second})
	st.log = plugin.NewLoad(x.h, func(context.Context, func(string, float64)) (string, error) {
		ws, _ := st.load.Value()
		return readTail(ws.BuildLog, wsLogTail)
	}, plugin.LoadOpts{Timeout: 10 * time.Second})
	x.by[id] = st
	return st
}

// readTail is the last n bytes of the file at path, from a line's start; "" for no file.
func readTail(path string, n int64) (string, error) {
	if path == "" {
		return "", nil
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	cut := fi.Size() > n
	if cut {
		if _, err := f.Seek(-n, io.SeekEnd); err != nil {
			return "", err
		}
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}
	s := string(b)
	if cut {
		if _, rest, ok := strings.Cut(s, "\n"); ok {
			s = rest
		}
	}
	return s, nil
}

// rebuild builds session id's image again in the background, the build log following it.
func (x *workspacePlugin) rebuild(id core.SessionID) error {
	w := x.sh.cfg.Host
	if w == nil {
		return errors.New("there are no sessions in this window")
	}
	st := x.state(id)
	if st.rebuilding {
		return nil
	}
	st.rebuilding = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		err := w.RebuildSandbox(ctx, id)
		x.h.Do(func() {
			st.rebuilding = false
			st.load.Reload()
			st.log.Reload()
			if err != nil {
				x.h.Notify(core.Notice{ID: "workspace.rebuild", Severity: core.SevError, Source: "workspace", Text: err.Error(), Time: time.Now()})
			}
		})
	}()
	return nil
}

func (x *workspacePlugin) run(id string, a registry.Args) {
	if err := x.h.Run(context.Background(), id, a); err != nil {
		x.h.Notify(core.Notice{ID: "workspace", Severity: core.SevError, Source: "workspace", Text: err.Error(), Time: time.Now()})
	}
}

// The page.

func (x *workspacePlugin) page(c *ui.Ctx) {
	if !x.open || c.Theme().IsProbe() {
		return
	}
	widget.Page(c, "workspace", &x.open, widget.PageOpts{Over: mainArea(), KeyMode: WorkspaceKeyMode, Label: "Workspace page", Help: workspaceHelp},
		func(c *ui.Ctx) {
			th := c.Theme()
			se, ok := x.h.State().Session(x.h.State().Focus)
			if !ok {
				x.header(c, core.SessionState{}, nil)
				permsNote(c, "none", "No session in front: its workspace shows here.")
				return
			}
			s := se.Snapshot()
			st := x.state(s.ID)
			now := c.Now()
			every := wsEvery
			if st.rebuilding {
				every = time.Second
			}
			if st.load.State().Phase != widget.Pending && now.Sub(st.read) >= every {
				st.read = now
				st.load.Reload()
			}
			if x.tab == wsLog && st.log.State().Phase != widget.Pending && now.Sub(st.logRead) >= every {
				st.logRead = now
				st.log.Reload()
			}
			c.WakeAt(now.Add(every))
			x.header(c, s, st)
			c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			if x.tab == wsPermissions && x.perms != nil {
				x.perms.build(c)
				return
			}
			ws, _ := st.load.Value()
			widget.Async(c, "load", st.load.State(), widget.AsyncOpts{Label: "Reading the workspace", Retry: st.load.Reload, Height: ui.Flex(1),
				Help: workspaceHelp}, func(c *ui.Ctx) {
				c.Column("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.PadXY(18, 14), Gap: 8, ScrollY: x.tab != wsFiles && x.tab != wsLog},
					func(c *ui.Ctx) {
						switch x.tab {
						case wsOverview:
							x.overview(c, s, st, ws)
						case wsFiles:
							x.files(c, st, ws)
						case wsImage:
							x.image(c, s, st, ws)
						case wsLog:
							x.buildLog(c, st, ws)
						}
					})
			})
		})
}

func (x *workspacePlugin) header(c *ui.Ctx, s core.SessionState, st *wsState) {
	th := c.Theme()
	c.Row("top", ui.Style{W: ui.Flex(1), H: ui.Px(52), Pad: ui.PadXY(18, 0), Gap: 14, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Text("h", "Workspace", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 18, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
		c.Text("where", cmpOr(s.Name, string(s.ID)), monoFont(11, th.C(theme.FgFaint)))
		widget.Tabs(c, "tabs", []string{"OVERVIEW", "FILES", "IMAGE", "BUILD LOG", "PERMISSIONS"}, &x.tab, widget.TabsOpts{Help: workspaceHelp})
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if st != nil {
			ws, _ := st.load.Value()
			switch {
			case !ws.Sandboxed:
				widget.Chip(c, "where", "on your machine", th.C(theme.Warn), workspaceHelp)
			case ws.Running:
				widget.Chip(c, "where", ws.Backend+" · running", th.C(tPermAllow), workspaceHelp)
			default:
				widget.Chip(c, "where", ws.Backend+" · stopped", th.C(theme.FgFaint), workspaceHelp)
			}
		}
		if widget.Button(c, "close", "Close", widget.ButtonOpts{Key: "esc", Help: workspaceHelp}) {
			x.open = false
		}
	})
}

// wsRow is a label and its value, in mono, with a Copy for a path.
func wsRow(c *ui.Ctx, sh *shell, key, label, value string, copyable bool) {
	th := c.Theme()
	c.Row(key, ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center, Clip: true}, func(c *ui.Ctx) {
		c.Row("k", ui.Style{W: ui.Px(140), Clip: true}, func(c *ui.Ctx) { c.Text("t", label, uiFont(12, th.C(theme.FgMuted))) })
		c.Row("v", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("t", value, monoFont(11.5, th.C(theme.Fg))) })
		if copyable && value != "" && widget.Button(c, "copy", "Copy", widget.ButtonOpts{Ghost: true, Help: workspaceHelp}) {
			sh.desktop.SetClipboard(value)
		}
	})
}

func (x *workspacePlugin) overview(c *ui.Ctx, s core.SessionState, st *wsState, ws host.Workspace) {
	th := c.Theme()
	permsCap(c, "sessioncap", "SESSION")
	wsRow(c, x.sh, "id", "Session", string(s.ID), true)
	wsRow(c, x.sh, "kind", "Kind", string(s.Kind), false)
	wsRow(c, x.sh, "root", "Workspace", ws.Root, true)
	permsCap(c, "sandboxcap", "SANDBOX")
	if !ws.Sandboxed {
		widget.Wrap(c, "host", "This session's agent runs on your machine, with your access: there's no sandbox, image or permissions.", 620,
			uiFont(12, th.C(theme.Warn)))
	} else {
		wsRow(c, x.sh, "backend", "Backend", ws.Backend, false)
		if ws.Container != "" {
			state := "stopped"
			if ws.Running {
				state = "running"
			}
			wsRow(c, x.sh, "container", "Container", ws.Container+" · "+state, false)
		}
		if ws.Image != "" {
			wsRow(c, x.sh, "image", "Image", ws.Image+" · "+ws.ImageFrom, false)
		}
		x.actions(c, s, st, ws)
	}
	permsCap(c, "dirscap", "DIRECTORIES")
	for i, p := range ws.Paths {
		c.Row(i, ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center, Clip: true, Role: ui.RoleText, Label: p.Name + " " + p.Host}, func(c *ui.Ctx) {
			c.Row("k", ui.Style{W: ui.Px(140), Clip: true}, func(c *ui.Ctx) { c.Text("t", p.Name, uiFont(12, th.C(theme.FgMuted))) })
			c.Column("v", ui.Style{W: ui.Flex(1), Clip: true, Gap: 2}, func(c *ui.Ctx) {
				c.Text("host", tildePath(p.Host), monoFont(11.5, th.C(theme.Fg)))
				var sub []string
				if p.Agent != "" {
					sub = append(sub, "in the sandbox: "+p.Agent)
				}
				if p.Note != "" {
					sub = append(sub, p.Note)
				}
				if len(sub) > 0 {
					c.Text("sub", strings.Join(sub, " · "), monoFont(10.5, th.C(theme.FgFaint)))
				}
			})
			if i := wsBrowsableIndex(ws, p.Host); i >= 0 && widget.Button(c, "browse", "Browse", widget.ButtonOpts{Ghost: true, Help: workspaceHelp}) {
				st.dir, x.tab = i, wsFiles
			}
			if widget.Button(c, "copy", "Copy", widget.ButtonOpts{Ghost: true, Help: workspaceHelp}) {
				x.sh.desktop.SetClipboard(p.Host)
			}
		})
	}
	if len(ws.Mounts) > 0 {
		permsCap(c, "mountscap", fmt.Sprintf("MOUNTS · %d", len(ws.Mounts)))
		for i, m := range ws.Mounts {
			c.Row(fmt.Sprint("m", i), ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center, Clip: true}, func(c *ui.Ctx) {
				c.Row("agent", ui.Style{W: ui.Px(320), Clip: true}, func(c *ui.Ctx) { c.Text("t", m.Agent, monoFont(11.5, th.C(theme.Fg))) })
				if m.ReadOnly {
					widget.Chip(c, "ro", "read-only", th.C(theme.FgFaint), workspaceHelp)
				} else {
					widget.Chip(c, "rw", "read-write", th.C(theme.Warn), workspaceHelp)
				}
				c.Row("host", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
					c.Text("t", "← "+tildePath(m.Host), monoFont(10.5, th.C(theme.FgFaint)))
				})
			})
		}
	}
}

// actions are the sandbox's buttons: Rebuild image and Restart sandbox.
func (x *workspacePlugin) actions(c *ui.Ctx, s core.SessionState, st *wsState, ws host.Workspace) {
	c.Row("actions", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center, Pad: ui.Insets{T: 4}}, func(c *ui.Ctx) {
		if ws.Rebuild && ws.Dockerfile != "" && widget.Button(c, "rebuild", "Rebuild image", widget.ButtonOpts{Busy: st.rebuilding, BusyLabel: "Rebuilding",
			Help: workspaceHelp}) {
			x.tab = wsLog
			x.run("workspace.rebuild", registry.Args{"id": string(s.ID)})
		}
		if widget.Button(c, "restart", "Restart sandbox", widget.ButtonOpts{Disabled: !ws.Running, Help: workspaceHelp}) {
			x.run("workspace.restart", registry.Args{"id": string(s.ID)})
		}
		c.Box("gap", ui.Style{W: ui.Px(8)}, nil)
		if widget.Button(c, "term", "Sandbox terminal", widget.ButtonOpts{Disabled: !ws.Running, Help: workspaceHelp}) {
			x.open = false
			x.run("term.open", registry.Args{"dir": ws.Root})
		}
		if widget.Button(c, "hostterm", "Host terminal", widget.ButtonOpts{Help: workspaceHelp}) {
			x.open = false
			x.run("term.host", registry.Args{"dir": ws.Root})
		}
	})
}

// wsBrowsable are the directories the files tab can show: the scratchpad first, then the rest in the overview's order.
func wsBrowsable(ws host.Workspace) []host.WorkspacePath {
	var out []host.WorkspacePath
	for _, p := range ws.Paths {
		if p.Name == "Go cache" || p.Host == "" {
			continue
		}
		out = append(out, p)
	}
	slices.SortStableFunc(out, func(a, b host.WorkspacePath) int {
		return boolOrder(b.Name == "Scratchpad") - boolOrder(a.Name == "Scratchpad")
	})
	return out
}

func boolOrder(b bool) int {
	if b {
		return 1
	}
	return 0
}

func wsBrowsableIndex(ws host.Workspace, path string) int {
	return slices.IndexFunc(wsBrowsable(ws), func(p host.WorkspacePath) bool { return p.Host == path })
}

func (x *workspacePlugin) files(c *ui.Ctx, st *wsState, ws host.Workspace) {
	th := c.Theme()
	dirs := wsBrowsable(ws)
	if len(dirs) == 0 {
		permsNote(c, "nodirs", "The session has no directories yet.")
		return
	}
	st.dir = min(st.dir, len(dirs)-1)
	names := make([]string, len(dirs))
	for i, d := range dirs {
		names[i] = d.Name
	}
	root := dirs[st.dir]
	c.Row("bar", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
		if v, ok := widget.Select(c, "dir", root.Name, names, widget.SelectOpts{Label: "Directory", Width: 220, Help: workspaceHelp}); ok {
			st.dir = slices.Index(names, v)
		}
		c.Row("path", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("t", tildePath(root.Host), monoFont(11, th.C(theme.FgFaint))) })
		if widget.Button(c, "reload", "Refresh", widget.ButtonOpts{Ghost: true, Help: workspaceHelp}) {
			clear(st.entries)
		}
	})
	items := x.dirItems(st, root.Host, 0)
	if len(items) == 0 {
		permsNote(c, "empty", "Nothing here yet.")
		return
	}
	res := widget.Tree(c, "tree", items, widget.TreeOpts{Height: ui.Flex(1), Label: root.Name, Help: workspaceHelp})
	if res.Toggled != "" {
		x.readDir(st, res.Toggled)
	}
	if res.Activated != "" && !strings.HasSuffix(res.Activated, wsMore) {
		x.open = false
		x.run("editor.open", registry.Args{"path": res.Activated})
	}
}

// wsMore marks the row standing for what a directory holds past wsDirMax, or before it's read.
const wsMore = "\x00more"

// dirItems are dir's entries as tree items, folders first; a folder not read yet holds one placeholder, so it shows
// its caret.
func (x *workspacePlugin) dirItems(st *wsState, dir string, depth int) []widget.TreeItem {
	es, ok := st.entries[dir]
	if !ok {
		if depth > 0 {
			return []widget.TreeItem{{ID: dir + wsMore, Label: "…"}}
		}
		es = x.readDir(st, dir)
	}
	th := x.h.CurrentTheme()
	var out []widget.TreeItem
	for i, e := range es {
		if i == wsDirMax {
			out = append(out, widget.TreeItem{ID: dir + wsMore, Label: fmt.Sprintf("%d more", len(es)-wsDirMax)})
			break
		}
		p := filepath.Join(dir, e.Name())
		it := widget.TreeItem{ID: p, Label: e.Name()}
		if e.IsDir() {
			it.Children = x.dirItems(st, p, depth+1)
			if len(it.Children) == 0 {
				it.Note = "empty"
			}
		} else if fi, err := e.Info(); err == nil {
			it.Note = humanBytes(fi.Size())
		}
		if th != nil && strings.HasPrefix(e.Name(), ".") {
			it.Tone = th.C(theme.FgFaint)
		}
		out = append(out, it)
	}
	return out
}

func (x *workspacePlugin) readDir(st *wsState, dir string) []os.DirEntry {
	es, _ := os.ReadDir(dir)
	slices.SortStableFunc(es, func(a, b os.DirEntry) int {
		if a.IsDir() != b.IsDir() {
			if a.IsDir() {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name(), b.Name())
	})
	st.entries[dir] = es
	return es
}

func (x *workspacePlugin) image(c *ui.Ctx, s core.SessionState, st *wsState, ws host.Workspace) {
	th := c.Theme()
	switch {
	case !ws.Sandboxed:
		permsNote(c, "host", "This session's agent runs on your machine: it has no image.")
		return
	case ws.Image == "":
		permsNote(c, "noimage", "The "+ws.Backend+" sandbox runs the agent with your machine's own tools: it has no image.")
		return
	}
	wsRow(c, x.sh, "tag", "Image", ws.Image, true)
	wsRow(c, x.sh, "from", "From", ws.ImageFrom, false)
	built := "not built yet: the next start builds it"
	if ws.Built {
		built = "built"
	}
	wsRow(c, x.sh, "built", "State", built, false)
	x.actions(c, s, st, ws)
	if strings.Contains(ws.ImageFrom, ".oriel/") {
		widget.Wrap(c, "branch", "Oriel builds a repo's Dockerfile from its default branch, never from a session's: a change the agent makes here "+
			"takes effect once it's merged.", 620, uiFont(12, th.C(theme.FgFaint)))
	}
	if ws.Dockerfile == "" {
		widget.Wrap(c, "named", "This image is named in your settings: Oriel runs it as it is and doesn't build it.", 620, uiFont(12, th.C(theme.FgFaint)))
		return
	}
	permsCap(c, "srccap", "DOCKERFILE")
	codeLines(c, "src", ws.Dockerfile)
}

// codeLines is text in mono, a numbered row for each line.
func codeLines(c *ui.Ctx, key, text string) {
	th := c.Theme()
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	c.Column(key, ui.Style{W: ui.Flex(1), Pad: ui.PadXY(10, 8), Radius: 6, Fill: th.C(theme.BgBase), Stroke: th.C(theme.Line), Border: 1}, func(c *ui.Ctx) {
		for i, l := range lines {
			c.Row(i, ui.Style{W: ui.Flex(1), Gap: 12, Clip: true}, func(c *ui.Ctx) {
				c.Row("n", ui.Style{W: ui.Px(28), Main: ui.End}, func(c *ui.Ctx) { c.Text("t", fmt.Sprint(i+1), monoFont(11.5, th.C(theme.FgGhost))) })
				c.Text("l", l, monoFont(11.5, th.C(theme.Fg)))
			})
		}
	})
}

func (x *workspacePlugin) buildLog(c *ui.Ctx, st *wsState, ws host.Workspace) {
	th := c.Theme()
	if !ws.Sandboxed || ws.Image == "" {
		permsNote(c, "nolog", "There's no image, so there's no build log.")
		return
	}
	text, _ := st.log.Value()
	c.Row("bar", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Row("path", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
			c.Text("t", cmpOr(tildePath(ws.BuildLog), "no log yet"), monoFont(11, th.C(theme.FgFaint)))
		})
		if st.rebuilding {
			widget.Chip(c, "busy", "rebuilding", th.C(theme.Warn), workspaceHelp)
		}
		if text != "" && widget.Button(c, "copy", "Copy", widget.ButtonOpts{Ghost: true, Help: workspaceHelp}) {
			x.sh.desktop.SetClipboard(text)
		}
	})
	if text == "" {
		permsNote(c, "empty", "No build log: the image was built before Oriel kept one, or hasn't been built here. Rebuild image writes one.")
		return
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	widget.List(c, "log", len(lines), &st.logSel, widget.ListOpts{RowHeight: 18, Height: ui.Flex(1), Label: "Build log", Help: workspaceHelp},
		func(c *ui.Ctx, i int, _ bool) {
			c.Row("l", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.PadXY(10, 0), Cross: ui.Center, Clip: true}, func(c *ui.Ctx) {
				c.Text("t", lines[i], monoFont(11, th.C(theme.FgMuted)))
			})
		})
}
