package gitui

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// ExplorerKeyMode is the keymap mode of the focused Explorer, for its fugitive-style keys.
const ExplorerKeyMode = "explorer"

var explorerScopes = []string{"changed", "uncommitted", "all"}

// Explorer is the Explorer view and its keys.
type Explorer struct {
	kit.Disposer
	Git *Git
	h   plugin.Host

	scopes      map[core.SessionID]gitx.Scope // remembered per session
	cols        struct{ status, lines, diag, agent bool }
	changedOnly bool
	hidden      bool // All shows dot files that haven't changed
	flat        bool
	byReview    bool            // Changed flat, the files with the most unread agent lines first
	ledger      registry.Ledger // the review ledger, as last drawn
	selected    string          // the selected row's id: a repo path, a directory "dir/", or a section
	hunks       explorerHunks
	work        *gitTree // the work tree of the project last drawn
	index       *explorerIndex
	rows        struct {
		key   explorerRowsKey
		items []widget.TreeItem
	}
}

// explorerRowsKey is what a scope's rows are built from, so they're built again only when it changes.
type explorerRowsKey struct {
	st                                   *gitx.State
	sc                                   gitx.Scope
	th                                   *theme.Resolved
	root                                 string
	changedOnly, hidden, flat, statusCol bool
	byReview                             bool
	ledger                               ledgerMark // the ledger Changed's files are sorted by, by review
	hunks                                hunksMark  // the hunks Uncommitted shows
}

// ledgerMark tells one ledger from another: a ledger made again has new Files.
type ledgerMark struct {
	files *registry.LedgerFile
	n     int
}

func markLedger(l registry.Ledger) ledgerMark {
	if len(l.Files) == 0 {
		return ledgerMark{}
	}
	return ledgerMark{&l.Files[0], len(l.Files)}
}

// hunksMark is the hunks' state, as Uncommitted's rows show it.
type hunksMark struct {
	path   string
	loaded bool
	gen    int
}

// explorerIndex is what rows look up in a git state, built once per state.
type explorerIndex struct {
	st          *gitx.State
	uncommitted int
	scopes      [3]*scopeIndex
}

// scopeIndex is a scope's files by row id, and how many changed files each directory ("dir/") holds.
type scopeIndex struct {
	count    int
	add, del int // lines, over its files
	byID     map[string]gitx.FileState
	changed  map[string]int
	areas    map[string]int // files per Uncommitted section id
}

// indexFor is st's index, built on first use.
func (e *Explorer) indexFor(st *gitx.State) *explorerIndex {
	if e.index == nil || e.index.st != st {
		e.index = &explorerIndex{st: st, uncommitted: kit.UncommittedCount(st)}
	}
	return e.index
}

// uncommittedHas says whether Uncommitted lists path, in any of its sections.
func (x *explorerIndex) uncommittedHas(path string) bool {
	s := x.scope(gitx.Uncommitted)
	for _, a := range []gitx.Area{gitx.Staged, gitx.Unstaged, gitx.Untracked} {
		if _, ok := s.byID[a.String()+"\x00"+path]; ok {
			return true
		}
	}
	return false
}

func (x *explorerIndex) scope(sc gitx.Scope) *scopeIndex {
	if s := x.scopes[sc]; s != nil {
		return s
	}
	s := &scopeIndex{count: x.st.Count(sc), byID: map[string]gitx.FileState{}, changed: map[string]int{}, areas: map[string]int{}}
	for _, f := range x.st.Files(sc) {
		id := "\x00" + f.Path
		if sc == gitx.Uncommitted {
			id = f.Area.String() + id
		}
		if _, ok := s.byID[id]; !ok {
			s.byID[id] = f
		}
		s.add, s.del = s.add+f.Add, s.del+f.Del
		s.areas["\x01"+f.Area.String()]++
		if f.Code == 0 {
			continue
		}
		for i := range len(f.Path) {
			if f.Path[i] == '/' {
				s.changed[f.Path[:i+1]]++
			}
		}
	}
	x.scopes[sc] = s
	return s
}

// git is the work tree the Explorer shows: its project's.
func (e *Explorer) git() *gitTree {
	if e.work != nil {
		return e.work
	}
	return &e.Git.gitTree
}

// explorerHunks are the staged and unstaged hunks of the file last selected in Uncommitted.
type explorerHunks struct {
	mu       sync.Mutex
	path     string
	loaded   bool
	loading  bool
	since    time.Time // when the loading began
	staged   []gitx.Hunk
	unstaged []gitx.Hunk
	gen      int // moves when the hunks or their path do
	busy     sync.WaitGroup
}

// show makes path the file whose hunks these are: another's are dropped, to be read again.
func (x *explorerHunks) show(path string) {
	if x.path != path {
		x.path, x.loaded, x.staged, x.unstaged = path, false, nil, nil
		x.gen++
	}
}

func (x *explorerHunks) mark() hunksMark {
	x.mu.Lock()
	defer x.mu.Unlock()
	return hunksMark{x.path, x.loaded, x.gen}
}

func (e *Explorer) Manifest() plugin.Manifest { return plugin.Manifest{ID: "explorer"} }

func (e *Explorer) Activate(_ context.Context, h plugin.Host) error {
	e.h = h
	e.scopes = map[core.SessionID]gitx.Scope{}
	e.cols.status, e.cols.lines, e.cols.diag, e.cols.agent = true, true, true, true
	for _, c := range e.commands() {
		e.Add(h.Command(c))
	}
	for i, s := range explorerScopes {
		e.Add(h.Keymap(registry.Keymap{Mode: "global", Keys: fmt.Sprintf("<M-%d>", i+1), Command: "explorer.scope", Args: registry.Args{"scope": s},
			Desc: "Explorer: " + s}))
	}
	e.Add(h.Keymap(registry.Keymap{Mode: "global", Keys: "<M-c>", Command: "explorer.changed-only", Desc: "Explorer: changed only"}))
	for _, k := range []registry.Keymap{
		{Keys: "s", Command: "explorer.stage", Desc: "Stage or unstage"},
		{Keys: "u", Command: "explorer.unstage", Desc: "Unstage"},
		{Keys: "x", Command: "explorer.discard", Desc: "Discard"},
		{Keys: "=", Command: "explorer.diff", Desc: "Diff"},
		{Keys: "c", Command: "explorer.commit", Desc: "Commit"},
		{Keys: "C", Command: "explorer.commit", Args: registry.Args{"all": true}, Desc: "Commit all"},
		{Keys: "q", Command: "view.close", Desc: "Close"},
		{Keys: "<Esc>", Command: "view.close", Args: registry.Args{"docked": true}, Desc: "Close"},
	} {
		k.Mode = ExplorerKeyMode
		e.Add(h.Keymap(k))
	}
	e.Add(h.View(kit.ExplorerView, explorerView{e}))
	if st := e.Git.status; st != nil {
		e.Add(kit.OnDispose(st.Watch(func([]string) {
			e.hunks.mu.Lock()
			e.hunks.loaded = false
			e.hunks.mu.Unlock()
		})))
	}
	return nil
}

func (e *Explorer) commands() []registry.Command {
	h := e.h
	show := func(ctx registry.Ctx) error {
		for _, v := range h.Layout().Visible() {
			if v.ID == kit.ExplorerView {
				return nil
			}
		}
		return h.Run(ctx, "project.panel", registry.Args{"name": "explorer"})
	}
	return []registry.Command{
		{ID: "explorer.scope", Title: "Show the Explorer in a {scope}: changed, uncommitted or all", Run: func(ctx registry.Ctx, a registry.Args) error {
			i := slices.Index(explorerScopes, a.String("scope"))
			if i < 0 {
				return fmt.Errorf("no scope %q: changed, uncommitted or all", a.String("scope"))
			}
			e.scopes[e.session()] = gitx.Scope(i)
			return show(ctx)
		}},
		{ID: "explorer.changed-only", Title: "Show only changed files in All (toggle)", Run: func(registry.Ctx, registry.Args) error {
			e.changedOnly = !e.changedOnly
			return nil
		}},
		{ID: "explorer.hidden", Title: "Show hidden files (dot files) in All (toggle)", Run: func(registry.Ctx, registry.Args) error {
			e.hidden = !e.hidden
			return nil
		}},
		{ID: "explorer.stage", Title: "Stage the selected file or hunk, or unstage it if it's staged", Run: func(ctx registry.Ctx, _ registry.Args) error {
			if h, staged, ok := e.selectedHunk(); ok {
				path, t := e.hunks.path, e.git()
				e.Git.async(func(ctx context.Context) error {
					repo := t.status.Repo()
					if staged {
						return repo.UnstageHunk(ctx, path, h)
					}
					work, err := os.ReadFile(t.abs(path))
					if err != nil {
						return err
					}
					return repo.StageHunk(ctx, path, work, h)
				})
				return nil
			}
			f, ok := e.selectedFile()
			if !ok {
				return nil
			}
			if f.Area == gitx.Staged {
				return h.Run(ctx, "git.unstage", registry.Args{"path": e.git().abs(f.Path)})
			}
			return h.Run(ctx, "git.stage", registry.Args{"path": e.git().abs(f.Path)})
		}},
		{ID: "explorer.unstage", Title: "Unstage the selected file", Run: func(ctx registry.Ctx, _ registry.Args) error {
			if f, ok := e.selectedFile(); ok {
				return h.Run(ctx, "git.unstage", registry.Args{"path": e.git().abs(f.Path)})
			}
			return nil
		}},
		{ID: "explorer.discard", Title: "Discard the selected file's unstaged changes", Run: func(ctx registry.Ctx, _ registry.Args) error {
			if f, ok := e.selectedFile(); ok && f.Area != gitx.Staged {
				return h.Run(ctx, "git.discard", registry.Args{"path": e.git().abs(f.Path)})
			}
			return nil
		}},
		{ID: "explorer.commit", Title: "Commit what's staged in the Explorer's repo ({all}: everything uncommitted), past the commit gates",
			Run: func(ctx registry.Ctx, a registry.Args) error {
				return h.Run(ctx, "git.commit", registry.Args{"path": e.git().root(), "all": a["all"] == true})
			}},
		{ID: "explorer.diff", Title: "Open the selected file's diff", Run: func(ctx registry.Ctx, _ registry.Args) error {
			if f, ok := e.selectedFile(); ok {
				return e.openDiff(ctx, f.Path)
			}
			return nil
		}},
	}
}

func (e *Explorer) Deactivate(ctx context.Context) error {
	e.hunks.busy.Wait()
	return e.Disposer.Deactivate(ctx)
}

func (e *Explorer) wait() { e.hunks.busy.Wait() }

// fileHunks are path's staged and unstaged hunks, read off the loop; nil until they're read.
func (e *Explorer) fileHunks(path string) (staged, unstaged []gitx.Hunk, ok bool) {
	x := &e.hunks
	x.mu.Lock()
	defer x.mu.Unlock()
	x.show(path)
	if !x.loaded && !x.loading {
		x.loading, x.since = true, kit.WindowNow(e.h)
		x.busy.Add(1)
		t := e.git()
		go func() {
			defer x.busy.Done()
			ctx, repo := context.Background(), t.status.Repo()
			st, _ := repo.StagedHunks(ctx, path)
			work, _ := os.ReadFile(t.abs(path))
			un, _ := repo.UnstagedHunks(ctx, path, work)
			x.mu.Lock()
			if x.path == path {
				x.staged, x.unstaged, x.loaded = st, un, true
				x.gen++
			}
			x.loading = false
			x.mu.Unlock()
			e.h.Redraw()
		}()
	}
	return x.staged, x.unstaged, x.loaded
}

// hunksLoading is the selected file's hunks as a load, while Uncommitted waits on them.
func (e *Explorer) hunksLoading(sc gitx.Scope) (widget.LoadState, bool) {
	x := &e.hunks
	x.mu.Lock()
	defer x.mu.Unlock()
	if sc != gitx.Uncommitted || x.path == "" || x.loaded || !x.loading {
		return widget.LoadState{}, false
	}
	return widget.LoadState{Phase: widget.Pending, Since: x.since}, true
}

// hunkRow is a hunk row's id: its side and index.
func hunkRow(side string, i int) string { return fmt.Sprintf("hunk\x00%s\x00%d", side, i) }

// selectedHunk is the selected hunk row's hunk, and whether it's staged.
func (e *Explorer) selectedHunk() (gitx.Hunk, bool, bool) {
	parts := strings.Split(e.selected, "\x00")
	if len(parts) != 3 || parts[0] != "hunk" {
		return gitx.Hunk{}, false, false
	}
	i, _ := strconv.Atoi(parts[2])
	st, un, ok := e.fileHunks(e.hunks.path)
	if !ok {
		return gitx.Hunk{}, false, false
	}
	hs := un
	if parts[1] == "staged" {
		hs = st
	}
	if i >= len(hs) {
		return gitx.Hunk{}, false, false
	}
	return hs[i], parts[1] == "staged", true
}

func (e *Explorer) session() core.SessionID { return e.h.State().Focus }

func (e *Explorer) scope() gitx.Scope {
	if s, ok := e.scopes[e.session()]; ok {
		return s
	}
	return gitx.All
}

// selectedFile is the selected row's file, in the scope showing.
func (e *Explorer) selectedFile() (gitx.FileState, bool) {
	st := e.git().state()
	if st == nil {
		return gitx.FileState{}, false
	}
	f, ok := e.indexFor(st).scope(e.scope()).byID[e.selected]
	return f, ok
}

// openDiff opens a file's branch diff, from a view that registers diff.open; until one does, the file.
func (e *Explorer) openDiff(ctx registry.Ctx, p string) error {
	a := registry.Args{"path": e.git().abs(p)}
	if e.h.Available("diff.open", a) {
		return e.h.Run(ctx, "diff.open", a)
	}
	return e.h.Run(ctx, "editor.open", a)
}

type explorerView struct{ e *Explorer }

func (v explorerView) Title(string) string { return "Explorer" }
func (v explorerView) EdgeToEdge()         {}
func (v explorerView) Build(vc uireg.ViewCtx) ui.Node {
	return &ui.Func{Key: "explorer", Fn: func(c *ui.Ctx) {
		v.e.work = nil
		if vc.Project != "" {
			v.e.work = v.e.Git.tree(kit.ProjectsHere(v.e.h).Dir(vc.Project))
		}
		v.e.build(c)
	}}
}

func (e *Explorer) build(c *ui.Ctx) {
	th := c.Theme()
	sc := e.scope()
	st := e.git().state()
	if e.cols.lines || sc != gitx.All {
		st = e.git().withCounts()
	}
	var x *explorerIndex
	if st != nil {
		x = e.indexFor(st)
		e.ledger, _ = registry.LedgerOf(e.h, e.git().root())
	}
	c.Column("explorer", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(kit.TExplorerBg), Help: "files/explorer", Role: ui.RoleRegion, Label: "Explorer"}, func(c *ui.Ctx) {
		if e.git().ready && e.git().root() == "" {
			c.Column("none", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(14, 12), Gap: 6}, func(c *ui.Ctx) {
				c.Text("t", "Not a git repository", kit.MonoTok(c, 11.5, theme.FgMuted))
				c.Text("d", "Open a directory inside a work tree to see its changes.", kit.MonoTok(c, 10.5, theme.FgFaint))
			})
			return
		}
		if st == nil {
			c.Column("loading", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(14, 12)}, func(c *ui.Ctx) {
				s := e.git().load()
				if s.Phase == widget.Ready {
					s = widget.LoadState{Phase: widget.Pending, Since: e.git().asked}
				}
				widget.Async(c, "tree", s, widget.AsyncOpts{Label: "Reading the work tree", Retry: e.Git.reload, Help: "files/explorer"}, nil)
			})
			return
		}
		c.Row("scopes", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 10, R: 8, T: 10, B: 6}, Gap: 6, Cross: ui.Center}, func(c *ui.Ctx) {
			chg, unc, all := x.scope(gitx.Changed).count, x.uncommitted, x.scope(gitx.All).count
			labels := []string{fmt.Sprintf("CHANGED %d", chg), fmt.Sprintf("UNCOMMITTED %d", unc), fmt.Sprintf("ALL %d", all)}
			i := int(sc)
			short := []string{fmt.Sprintf("CHG %d", chg), fmt.Sprintf("UNC %d", unc), fmt.Sprintf("ALL %d", all)}
			if widget.Segmented(c, "seg", labels, &i, widget.SegmentedOpts{Help: "files/explorer#scopes", Label: "Explorer scope", Short: short}) {
				e.scopes[e.session()] = gitx.Scope(i)
				sc = gitx.Scope(i)
			}
			e.options(c)
		})
		e.subheader(c, st, sc)
		items := e.items(c, st, sc)
		res := widget.Tree(c, fmt.Sprint("tree.", sc), items, widget.TreeOpts{Help: "files/explorer", Label: "Files", KeyMode: ExplorerKeyMode,
			Trailing: func(c *ui.Ctx, it widget.TreeItem, _ bool) { e.trailing(c, x.scope(sc), sc, it) }})
		e.selected = res.Selected
		if res.Activated != "" {
			e.activate(res.Activated, sc)
		}
		if s, ok := e.hunksLoading(sc); ok {
			c.Row("hunks", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(14, 6)}, func(c *ui.Ctx) {
				widget.Async(c, "wait", s, widget.AsyncOpts{Label: "Reading " + filepath.Base(e.hunks.path) + "'s hunks", Help: "files/explorer#keys"}, nil)
			})
		}
		e.footer(c, sc)
	})
}

// options is the cog at the scopes' right: a menu of All's columns and filters.
func (e *Explorer) options(c *ui.Ctx) {
	const help = "files/explorer#columns"
	widget.ToggleMenu(c, "options", []widget.ToolItem{
		{Key: "status", Label: "Status", On: &e.cols.status, Help: help},
		{Key: "lines", Label: "± lines", On: &e.cols.lines, Help: help},
		{Key: "agent", Label: "Agent", On: &e.cols.agent, Help: help},
		{Key: "diag", Label: "Diagnostics", On: &e.cols.diag, Help: help},
		{Key: "only", Label: "Changed only", On: &e.changedOnly, Help: help, Shortcut: "⌥C"},
		{Key: "hidden", Label: "Hidden files", On: &e.hidden, Help: help},
	}, widget.MenuButtonOpts{Label: "Explorer options", Size: 22, Help: help,
		Icon: func(c *ui.Ctx, fg render.Color) { kit.Icon(c, e.h, "icon", "settings", 13, fg) }})
}

// subheader is the line under the scopes: what Changed and Uncommitted compare with.
func (e *Explorer) subheader(c *ui.Ctx, st *gitx.State, sc gitx.Scope) {
	if sc == gitx.All {
		return
	}
	c.Row("sub", ui.Style{W: ui.Flex(1), H: ui.Px(26), Pad: ui.PadXY(12, 0), Gap: 6, Cross: ui.Center}, func(c *ui.Ctx) {
		switch sc {
		case gitx.Changed:
			base := st.Base
			if base == "" {
				base = "HEAD"
			}
			c.Text("vs", "vs "+base+"@"+st.BaseHash, kit.MonoTok(c, 10.5, theme.FgFaint))
			view := 0
			switch {
			case e.byReview:
				view = 2
			case e.flat:
				view = 1
			}
			if widget.Segmented(c, "view", []string{"tree", "flat", "by review"}, &view, widget.SegmentedOpts{Help: "files/explorer#changed", Label: "Layout"}) {
				e.flat, e.byReview = view == 1, view == 2
			}
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			x := e.indexFor(st).scope(gitx.Changed)
			widget.DiffStat(c, "total", x.add, x.del, widget.DiffStatOpts{})
		case gitx.Uncommitted:
			c.Text("vs", "vs HEAD", kit.MonoTok(c, 10.5, theme.FgFaint))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			x := e.indexFor(st).scope(gitx.Uncommitted)
			widget.DiffStat(c, "total", x.add, x.del, widget.DiffStatOpts{})
			if widget.Button(c, "commit", "Commit…", widget.ButtonOpts{Ghost: true, Key: "c", Help: "files/commit"}) {
				if err := e.h.Run(context.Background(), "explorer.commit", nil); err != nil {
					e.h.Notify(core.Notice{ID: "git.commit", Severity: core.SevError, Key: true, Text: err.Error()})
				}
			}
		}
	})
}

func (e *Explorer) footer(c *ui.Ctx, sc gitx.Scope) {
	hint := "⌥1 changed · ⌥2 uncommitted · ⌥3 all"
	switch sc {
	case gitx.Uncommitted:
		hint = "s stage · u unstage · x discard · = diff · c commit"
	case gitx.Changed:
		hint = "⏎ opens the diff, not the file"
	}
	c.Row("footer", ui.Style{W: ui.Flex(1), H: ui.Px(28), Pad: ui.PadXY(12, 0), Cross: ui.Center, Stroke: c.Theme().C(theme.Line)}, func(c *ui.Ctx) {
		c.Text("keys", hint, kit.MonoTok(c, 10, theme.FgFaint))
	})
}

// activate opens a row: in Changed its diff, elsewhere the file.
func (e *Explorer) activate(id string, sc gitx.Scope) {
	_, p, _ := strings.Cut(id, "\x00")
	ctx := context.Background()
	var err error
	if sc == gitx.Changed {
		err = e.openDiff(ctx, p)
	} else {
		err = e.h.Run(ctx, "editor.open", registry.Args{"path": e.git().abs(p)})
	}
	if err != nil {
		e.h.Notify(core.Notice{ID: "explorer.open", Severity: core.SevWarning, Key: true, Text: err.Error()})
	}
}

// items are a scope's rows: Uncommitted's in sections, Changed's flat or as a tree, All's as a tree under the
// project's root.
func (e *Explorer) items(c *ui.Ctx, st *gitx.State, sc gitx.Scope) []widget.TreeItem {
	key := explorerRowsKey{st: st, sc: sc, th: c.Theme(), root: e.git().root(), changedOnly: e.changedOnly, hidden: e.hidden, flat: e.flat,
		statusCol: e.cols.status}
	switch {
	case sc == gitx.Uncommitted:
		if f, ok := e.selectedFile(); ok && f.Area != gitx.Untracked && f.Code != 'D' {
			e.hunks.mu.Lock()
			e.hunks.show(f.Path)
			e.hunks.mu.Unlock()
		}
		if p := e.hunks.mark().path; p != "" && e.indexFor(st).uncommittedHas(p) {
			e.fileHunks(p) // read off the loop when they're due
		}
		key.hunks = e.hunks.mark()
	case sc == gitx.Changed && e.byReview:
		key.byReview, key.ledger = true, markLedger(e.ledger)
	}
	if e.rows.key != key || e.rows.items == nil {
		e.rows.key, e.rows.items = key, e.buildItems(c.Theme(), st, sc)
	}
	return e.rows.items
}

func (e *Explorer) buildItems(th *theme.Resolved, st *gitx.State, sc gitx.Scope) []widget.TreeItem {
	files := st.Files(sc)
	switch sc {
	case gitx.Uncommitted:
		var out []widget.TreeItem
		for _, area := range []gitx.Area{gitx.Staged, gitx.Unstaged, gitx.Untracked} {
			sec := widget.TreeItem{ID: "\x01" + area.String(), Label: area.String(), Section: true, Open: true}
			for _, f := range files {
				if f.Area == area {
					sec.Children = append(sec.Children, e.fileItem(th, f, f.Path, sc))
				}
			}
			if len(sec.Children) > 0 {
				out = append(out, sec)
			}
		}
		if p := e.hunks.mark().path; p != "" && e.indexFor(st).uncommittedHas(p) {
			if sec, ok := e.hunkSection(th, p); ok {
				out = append(out, sec)
			}
		}
		return out
	case gitx.Changed:
		if e.byReview {
			byPath := map[string]gitx.FileState{}
			for _, f := range files {
				byPath[f.Path] = f
			}
			var out []widget.TreeItem
			for _, p := range e.reviewOrder(st) {
				out = append(out, e.fileItem(th, byPath[p], p, sc))
			}
			return out
		}
		if e.flat {
			var out []widget.TreeItem
			for _, f := range files {
				out = append(out, e.fileItem(th, f, f.Path, sc))
			}
			return out
		}
		return e.tree(th, files, sc)
	}
	if e.changedOnly || !e.hidden {
		files = slices.DeleteFunc(slices.Clone(files), func(f gitx.FileState) bool { return f.Code == 0 && (e.changedOnly || isHidden(f.Path)) })
	}
	root := filepath.Base(e.git().root())
	note := st.Branch
	if st.Ahead > 0 {
		note += fmt.Sprintf(" · ↑%d", st.Ahead)
	}
	if st.Behind > 0 {
		note += fmt.Sprintf(" · ↓%d", st.Behind)
	}
	return []widget.TreeItem{{ID: "\x02", Label: root, Note: note, Swatch: th.C(theme.Accent), Open: true, Children: e.tree(th, files, sc),
		Help: "files/explorer"}}
}

// isHidden reports whether a repo path is a dot file or under a dot directory.
func isHidden(p string) bool {
	return strings.HasPrefix(p, ".") || strings.Contains(p, "/.")
}

// reviewOrder is Changed's files by review: the most agent-written lines not read first, then by path.
func (e *Explorer) reviewOrder(st *gitx.State) []string {
	var ps []string
	for _, f := range st.Files(gitx.Changed) {
		ps = append(ps, f.Path)
	}
	notRead := make(map[string]int, len(e.ledger.Files))
	for _, f := range e.ledger.Files {
		if _, ok := notRead[f.Path]; !ok {
			notRead[f.Path] = f.All.NotRead()
		}
	}
	slices.SortStableFunc(ps, func(a, b string) int { return cmp.Or(cmp.Compare(notRead[b], notRead[a]), strings.Compare(a, b)) })
	return ps
}

// hunkSection is a file's hunks, staged ticked, each row its first line and size.
func (e *Explorer) hunkSection(th *theme.Resolved, path string) (widget.TreeItem, bool) {
	st, un, ok := e.fileHunks(path)
	if !ok || len(st)+len(un) == 0 {
		return widget.TreeItem{}, false
	}
	sec := widget.TreeItem{ID: "\x01hunks", Label: "hunks in " + filepath.Base(path), Section: true, Open: true, Help: "files/explorer#keys"}
	for _, side := range []struct {
		name string
		hs   []gitx.Hunk
	}{{"staged", st}, {"unstaged", un}} {
		for i, h := range side.hs {
			first := h.New
			if len(first) == 0 {
				first = h.Old
			}
			it := widget.TreeItem{ID: hunkRow(side.name, i), Label: fmt.Sprintf("@@ %d", h.NewStart), Glyph: "☐", GlyphTone: th.C(theme.FgFaint),
				Help: "files/explorer#keys"}
			if side.name == "staged" {
				it.Glyph, it.GlyphTone = "☑", th.C(kit.TGitAdded)
			}
			if len(first) > 0 {
				it.Note = strings.TrimSpace(first[0])
			}
			sec.Children = append(sec.Children, it)
		}
	}
	return sec, true
}

// tree nests files by directory, directories first.
func (e *Explorer) tree(th *theme.Resolved, files []gitx.FileState, sc gitx.Scope) []widget.TreeItem {
	changed := map[string]bool{}
	for _, f := range files {
		for i := range len(f.Path) {
			if f.Code != 0 && f.Path[i] == '/' {
				changed[f.Path[:i+1]] = true
			}
		}
	}
	type dir struct {
		dirs  map[string]*dir
		order []string
		files []widget.TreeItem
	}
	top := &dir{dirs: map[string]*dir{}}
	for _, f := range files {
		p := strings.TrimSuffix(f.Path, "/")
		d := top
		parts := strings.Split(p, "/")
		if f.Ignored && strings.HasSuffix(f.Path, "/") {
			parts = append(parts[:len(parts)-1], parts[len(parts)-1]+"/")
		}
		for _, part := range parts[:len(parts)-1] {
			sub, ok := d.dirs[part]
			if !ok {
				sub = &dir{dirs: map[string]*dir{}}
				d.dirs[part] = sub
				d.order = append(d.order, part)
			}
			d = sub
		}
		d.files = append(d.files, e.fileItem(th, f, strings.TrimSuffix(parts[len(parts)-1], "/"), sc))
	}
	var build func(d *dir, prefix string) []widget.TreeItem
	build = func(d *dir, prefix string) []widget.TreeItem {
		slices.Sort(d.order)
		var out []widget.TreeItem
		for _, name := range d.order {
			sub := d.dirs[name]
			// a chain of directories with one directory each shows as one row: internal/pricing
			label, p := name, prefix+name+"/"
			for len(sub.order) == 1 && len(sub.files) == 0 && sc == gitx.Changed {
				label += "/" + sub.order[0]
				p += sub.order[0] + "/"
				sub = sub.dirs[sub.order[0]]
			}
			out = append(out, widget.TreeItem{ID: "\x00" + p, Label: label, Children: build(sub, p), Open: sc == gitx.Changed || changed[p],
				Help: "files/explorer"})
		}
		return append(out, d.files...)
	}
	return build(top, "")
}

// fileItem is a file's row: its name tinted by its status, a renamed file's old name after it, a deleted one struck
// through, and in Changed a dot for whether it's committed.
func (e *Explorer) fileItem(th *theme.Resolved, f gitx.FileState, label string, sc gitx.Scope) widget.TreeItem {
	id := "\x00" + f.Path
	if sc == gitx.Uncommitted {
		id = f.Area.String() + id
	}
	it := widget.TreeItem{ID: id, Label: label, Strike: f.Code == 'D', Help: "files/explorer"}
	if _, name := kit.GitCodeTokens(f.Code); name != 0 && (sc != gitx.All || e.cols.status) {
		it.Tone = th.C(name)
	}
	if f.Ignored {
		it.Tone, it.Note = th.C(kit.TGitIgnored), "ignored"
	}
	if f.Old != "" {
		it.Note = "← " + path.Base(f.Old)
	}
	if sc == gitx.Changed {
		switch f.Area {
		case gitx.Working:
			it.Glyph, it.GlyphTone = "●", th.C(kit.TGitDot)
		case gitx.Both:
			it.Glyph, it.GlyphTone = "◉", th.C(kit.TGitDotBoth)
		default:
			it.Glyph, it.GlyphTone = "○", th.C(kit.TGitDotDone)
		}
	}
	return it
}

// trailing is a row's markers at the right: a directory's changed-file count, a file's diagnostics, ± lines and
// status letter, as the columns say.
func (e *Explorer) trailing(c *ui.Ctx, x *scopeIndex, sc gitx.Scope, it widget.TreeItem) {
	th := c.Theme()
	if it.Section {
		c.Text("n", fmt.Sprint(x.areas[it.ID]), kit.MonoTok(c, 10, theme.FgFaint))
		return
	}
	if strings.HasPrefix(it.ID, "hunk\x00") {
		parts := strings.Split(it.ID, "\x00")
		i, _ := strconv.Atoi(parts[2])
		st, un, _ := e.fileHunks(e.hunks.path)
		hs := un
		if parts[1] == "staged" {
			hs = st
		}
		if i < len(hs) {
			widget.DiffStat(c, "stat", hs[i].NewLines, hs[i].OldLines, widget.DiffStatOpts{})
		}
		return
	}
	_, p, isFile := strings.Cut(it.ID, "\x00")
	if !isFile || p == "" {
		return
	}
	if len(it.Children) > 0 {
		if n := x.changed[p]; n > 0 {
			c.Text("dot", "●", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 8}, Color: th.C(kit.TGitDot)})
			c.Text("n", fmt.Sprint(n), kit.MonoTok(c, 10, theme.FgFaint))
		}
		return
	}
	f, found := x.byID[it.ID]
	if !found {
		return
	}
	if e.cols.diag || sc != gitx.All {
		e.diagCounts(c, e.git().abs(p))
	}
	if lf, ok := e.ledger.File(p); ok {
		switch {
		case sc == gitx.Changed:
			kit.ReadBar(c, "read", lf.All)
		case sc == gitx.All && e.cols.agent:
			glyph := "◆"
			if lf.All.NotRead() == 0 {
				glyph = "◇"
			}
			c.Box("agent", ui.Style{Tooltip: fmt.Sprintf("%d of %d agent-written lines read", lf.All.Read, lf.All.Total())}, func(c *ui.Ctx) {
				c.Text("t", glyph, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10}, Color: th.C(tDiffAuthAgent)})
			})
		}
	}
	if (e.cols.lines || sc != gitx.All) && (f.Add > 0 || f.Del > 0 || f.Binary && f.Code != 0) {
		widget.DiffStat(c, "stat", f.Add, f.Del, widget.DiffStatOpts{Bar: sc != gitx.Uncommitted, Scale: 80, Binary: f.Binary})
	}
	if letter, _ := kit.GitCodeTokens(f.Code); letter != 0 && (e.cols.status || sc != gitx.All) {
		c.Text("code", string(f.Code), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10, Weight: typeset.SemiBold}, Color: th.C(letter)})
	}
}

// diagCounts are a file's error and warning counts: ✕2 ⚠1.
func (e *Explorer) diagCounts(c *ui.Ctx, file string) {
	errs, warns := 0, 0
	for _, d := range e.h.DiagnosticsFor(file) {
		switch d.Severity {
		case registry.SevError:
			errs++
		case registry.SevWarning:
			warns++
		}
	}
	for _, x := range []struct {
		n     int
		glyph string
		tok   theme.TokenID
	}{{errs, "✕", kit.TDiagError}, {warns, "⚠", kit.TDiagWarning}} {
		if x.n > 0 {
			c.Text(x.glyph, fmt.Sprintf("%s%d", x.glyph, x.n), kit.MonoTok(c, 10.5, x.tok))
		}
	}
}
