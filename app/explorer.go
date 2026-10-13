package app

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// ExplorerView is the Explorer (ui.md 01c): the work tree in three scopes, Changed, Uncommitted and All.
const ExplorerView = "explorer"

// ExplorerKeyMode is the keymap mode of the focused Explorer, for its fugitive-style keys.
const ExplorerKeyMode = "explorer"

var (
	tGitModified  = theme.Declare("git.modified", "amber")
	tGitAdded     = theme.Declare("git.added", "green")
	tGitDeleted   = theme.Declare("git.deleted", "red")
	tGitRenamed   = theme.Declare("git.renamed", "violet")
	tGitUntracked = theme.Declare("git.untracked", "fg.faint")
	tGitConflict  = theme.Declare("git.conflict", "red")
	tGitIgnored   = theme.Declare("git.ignored", "fg.ghost")
	tGitNameMod   = theme.Declare("git.name.modified", "accent")
	tGitDot       = theme.Declare("git.dot.uncommitted", "amber")
	tGitDotBoth   = theme.Declare("git.dot.both", "accent")
	tGitDotDone   = theme.Declare("git.dot.committed", "fg.faint")
	tExplorerBg   = theme.Declare("explorer.bg", "bg.surface")
)

// gitCodeTokens are the tokens a status letter is drawn in, and a file's name tinted with.
func gitCodeTokens(code byte) (letter, name theme.TokenID) {
	switch code {
	case 'M':
		return tGitModified, tGitNameMod
	case 'A':
		return tGitAdded, tGitAdded
	case 'D':
		return tGitDeleted, tGitDeleted
	case 'R', 'C':
		return tGitRenamed, tGitRenamed
	case '?':
		return tGitUntracked, tGitUntracked
	case 'U':
		return tGitConflict, tGitConflict
	}
	return 0, 0
}

var explorerScopes = []string{"changed", "uncommitted", "all"}

// explorer is the Explorer view and its keys.
type explorer struct {
	disposer
	sh *shell
	h  ext.Host

	scopes      map[core.SessionID]gitx.Scope // remembered per session
	cols        struct{ status, lines, diag bool }
	changedOnly bool
	flat        bool
	selected    string // the selected row's id: a repo path, a directory "dir/", or a section
}

func (e *explorer) Manifest() ext.Manifest { return ext.Manifest{ID: "explorer"} }

func (e *explorer) Activate(_ context.Context, h ext.Host) error {
	e.h = h
	e.scopes = map[core.SessionID]gitx.Scope{}
	e.cols.status, e.cols.lines, e.cols.diag = true, true, true
	open := registry.Args{"id": ExplorerView, "place": "dock", "edge": "left", "title": "Explorer", "size": 300}
	show := func(ctx registry.Ctx) error {
		for _, v := range h.Layout().Visible() {
			if v.ID == ExplorerView {
				return nil
			}
		}
		return h.Run(ctx, "view.open", open)
	}
	cmds := []registry.Command{
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
		{ID: "explorer.stage", Title: "Stage the selected file, or unstage it if it's staged", Run: func(ctx registry.Ctx, _ registry.Args) error {
			f, ok := e.selectedFile()
			if !ok {
				return nil
			}
			if f.Area == gitx.Staged {
				return h.Run(ctx, "git.unstage", registry.Args{"path": f.Path})
			}
			return h.Run(ctx, "git.stage", registry.Args{"path": f.Path})
		}},
		{ID: "explorer.unstage", Title: "Unstage the selected file", Run: func(ctx registry.Ctx, _ registry.Args) error {
			if f, ok := e.selectedFile(); ok {
				return h.Run(ctx, "git.unstage", registry.Args{"path": f.Path})
			}
			return nil
		}},
		{ID: "explorer.discard", Title: "Discard the selected file's unstaged changes", Run: func(ctx registry.Ctx, _ registry.Args) error {
			if f, ok := e.selectedFile(); ok && f.Area != gitx.Staged {
				return h.Run(ctx, "git.discard", registry.Args{"path": f.Path})
			}
			return nil
		}},
		{ID: "explorer.diff", Title: "Open the selected file's diff", Run: func(ctx registry.Ctx, _ registry.Args) error {
			if f, ok := e.selectedFile(); ok {
				return e.openDiff(ctx, f.Path)
			}
			return nil
		}},
	}
	for _, c := range cmds {
		e.add(h.Command(c))
	}
	for i, s := range explorerScopes {
		e.add(h.Keymap(registry.Keymap{Mode: "global", Keys: fmt.Sprintf("<M-%d>", i+1), Command: "explorer.scope", Args: registry.Args{"scope": s},
			Desc: "Explorer: " + s}))
	}
	e.add(h.Keymap(registry.Keymap{Mode: "global", Keys: "<M-c>", Command: "explorer.changed-only", Desc: "Explorer: changed only"}))
	for _, k := range []registry.Keymap{
		{Keys: "s", Command: "explorer.stage", Desc: "Stage or unstage"},
		{Keys: "u", Command: "explorer.unstage", Desc: "Unstage"},
		{Keys: "x", Command: "explorer.discard", Desc: "Discard"},
		{Keys: "=", Command: "explorer.diff", Desc: "Diff"},
		{Keys: "q", Command: "view.close", Desc: "Close"},
		{Keys: "<Esc>", Command: "view.close", Args: registry.Args{"docked": true}, Desc: "Close"},
	} {
		k.Mode = ExplorerKeyMode
		e.add(h.Keymap(k))
	}
	e.add(h.View(ExplorerView, explorerView{e}))
	return nil
}

func (e *explorer) session() core.SessionID { return e.h.State().Focus }

func (e *explorer) scope() gitx.Scope {
	if s, ok := e.scopes[e.session()]; ok {
		return s
	}
	return gitx.All
}

// selectedFile is the selected row's file, in the scope showing.
func (e *explorer) selectedFile() (gitx.FileState, bool) {
	area, p, _ := strings.Cut(e.selected, "\x00")
	for _, f := range e.sh.git.state().Files(e.scope()) {
		if f.Path == p && (area == "" || area == f.Area.String()) {
			return f, true
		}
	}
	return gitx.FileState{}, false
}

// openDiff opens a file's branch diff, from a view that registers diff.open; until one does, the file.
func (e *explorer) openDiff(ctx registry.Ctx, p string) error {
	a := registry.Args{"path": e.sh.git.abs(p)}
	if e.h.Available("diff.open", a) {
		return e.h.Run(ctx, "diff.open", a)
	}
	return e.h.Run(ctx, "editor.open", a)
}

type explorerView struct{ e *explorer }

func (v explorerView) Title(string) string { return "Explorer" }
func (v explorerView) EdgeToEdge()         {}
func (v explorerView) Build(registry.ViewCtx) ui.Node {
	return &ui.Func{Key: "explorer", Fn: v.e.build}
}

func monoTok(c *ui.Ctx, size float32, tok theme.TokenID) ui.TextStyle {
	return ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: size}, Color: c.Theme().C(tok)}
}

func (e *explorer) build(c *ui.Ctx) {
	th := c.Theme()
	st := e.sh.git.state()
	sc := e.scope()
	c.Column("explorer", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(tExplorerBg), Help: "files/explorer", Role: ui.RoleRegion, Label: "Explorer"}, func(c *ui.Ctx) {
		if e.sh.git.root() == "" {
			c.Column("none", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(14, 12), Gap: 6}, func(c *ui.Ctx) {
				c.Text("t", "Not a git repository", monoTok(c, 11.5, theme.FgMuted))
				c.Text("d", "Open a directory inside a work tree to see its changes.", monoTok(c, 10.5, theme.FgFaint))
			})
			return
		}
		c.Row("scopes", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 10, R: 10, T: 10, B: 6}}, func(c *ui.Ctx) {
			labels := []string{fmt.Sprintf("CHANGED %d", st.Count(gitx.Changed)), fmt.Sprintf("UNCOMMITTED %d", uncommittedCount(st)),
				fmt.Sprintf("ALL %d", st.Count(gitx.All))}
			i := int(sc)
			if widget.Segmented(c, "seg", labels, &i, widget.SegmentedOpts{Help: "files/explorer#scopes", Label: "Explorer scope"}) {
				e.scopes[e.session()] = gitx.Scope(i)
				sc = gitx.Scope(i)
			}
		})
		e.subheader(c, st, sc)
		items := e.items(c, st, sc)
		res := widget.Tree(c, fmt.Sprint("tree.", sc), items, widget.TreeOpts{Help: "files/explorer", Label: "Files", KeyMode: ExplorerKeyMode,
			Trailing: func(c *ui.Ctx, it widget.TreeItem, _ bool) { e.trailing(c, st, sc, it) }})
		e.selected = res.Selected
		if res.Activated != "" {
			e.activate(res.Activated, sc)
		}
		e.footer(c, sc)
	})
}

func uncommittedCount(st *gitx.State) int {
	seen := map[string]bool{}
	for _, f := range st.Files(gitx.Uncommitted) {
		seen[f.Path] = true
	}
	return len(seen)
}

// subheader is the line under the scopes: the columns All shows, or what Changed and Uncommitted compare with.
func (e *explorer) subheader(c *ui.Ctx, st *gitx.State, sc gitx.Scope) {
	c.Row("sub", ui.Style{W: ui.Flex(1), H: ui.Px(26), Pad: ui.PadXY(12, 0), Gap: 6, Cross: ui.Center}, func(c *ui.Ctx) {
		switch sc {
		case gitx.All:
			e.cols.status = widget.ToggleChip(c, "status", "status", e.cols.status, widget.ToggleChipOpts{Help: "files/explorer#columns"})
			e.cols.lines = widget.ToggleChip(c, "lines", "± lines", e.cols.lines, widget.ToggleChipOpts{Help: "files/explorer#columns"})
			e.cols.diag = widget.ToggleChip(c, "diag", "diag", e.cols.diag, widget.ToggleChipOpts{Help: "files/explorer#columns"})
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			e.changedOnly = widget.ToggleChip(c, "only", "⌥C changed-only", e.changedOnly, widget.ToggleChipOpts{Help: "files/explorer#columns"})
		case gitx.Changed:
			base := st.Base
			if base == "" {
				base = "HEAD"
			}
			c.Text("vs", "vs "+base+"@"+st.BaseHash, monoTok(c, 10.5, theme.FgFaint))
			flat := 0
			if e.flat {
				flat = 1
			}
			if widget.Segmented(c, "view", []string{"tree", "flat"}, &flat, widget.SegmentedOpts{Help: "files/explorer#changed", Label: "Layout"}) {
				e.flat = flat == 1
			}
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			add, del := 0, 0
			for _, f := range st.Files(gitx.Changed) {
				add, del = add+f.Add, del+f.Del
			}
			widget.DiffStat(c, "total", add, del, widget.DiffStatOpts{})
		case gitx.Uncommitted:
			c.Text("vs", "vs HEAD", monoTok(c, 10.5, theme.FgFaint))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			add, del := 0, 0
			for _, f := range st.Files(gitx.Uncommitted) {
				add, del = add+f.Add, del+f.Del
			}
			widget.DiffStat(c, "total", add, del, widget.DiffStatOpts{})
		}
	})
}

func (e *explorer) footer(c *ui.Ctx, sc gitx.Scope) {
	hint := "⌥1 changed · ⌥2 uncommitted · ⌥3 all"
	switch sc {
	case gitx.Uncommitted:
		hint = "s stage · u unstage · x discard · = diff"
	case gitx.Changed:
		hint = "⏎ opens the diff, not the file"
	}
	c.Row("footer", ui.Style{W: ui.Flex(1), H: ui.Px(28), Pad: ui.PadXY(12, 0), Cross: ui.Center, Stroke: c.Theme().C(theme.Line)}, func(c *ui.Ctx) {
		c.Text("keys", hint, monoTok(c, 10, theme.FgFaint))
	})
}

// activate opens a row: in Changed its diff, elsewhere the file.
func (e *explorer) activate(id string, sc gitx.Scope) {
	_, p, _ := strings.Cut(id, "\x00")
	ctx := context.Background()
	var err error
	if sc == gitx.Changed {
		err = e.openDiff(ctx, p)
	} else {
		err = e.h.Run(ctx, "editor.open", registry.Args{"path": e.sh.git.abs(p)})
	}
	if err != nil {
		e.h.Notify(core.Notice{ID: "explorer.open", Severity: core.SevWarning, Key: true, Text: err.Error()})
	}
}

// items are a scope's rows: Uncommitted's in sections, Changed's flat or as a tree, All's as a tree under the
// project's root.
func (e *explorer) items(c *ui.Ctx, st *gitx.State, sc gitx.Scope) []widget.TreeItem {
	th := c.Theme()
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
		return out
	case gitx.Changed:
		if e.flat {
			var out []widget.TreeItem
			for _, f := range files {
				out = append(out, e.fileItem(th, f, f.Path, sc))
			}
			return out
		}
		return e.tree(th, files, sc)
	}
	if e.changedOnly {
		files = slices.DeleteFunc(slices.Clone(files), func(f gitx.FileState) bool { return f.Code == 0 })
	}
	root := filepath.Base(e.sh.git.root())
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

// tree nests files by directory, directories first.
func (e *explorer) tree(th *theme.Resolved, files []gitx.FileState, sc gitx.Scope) []widget.TreeItem {
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
			out = append(out, widget.TreeItem{ID: "\x00" + p, Label: label, Children: build(sub, p), Open: sc == gitx.Changed || changedUnder(files, p),
				Help: "files/explorer"})
		}
		return append(out, d.files...)
	}
	return build(top, "")
}

// changedUnder reports whether a file under dir has changes, so its folder starts open.
func changedUnder(files []gitx.FileState, dir string) bool {
	for _, f := range files {
		if f.Code != 0 && strings.HasPrefix(f.Path, dir) {
			return true
		}
	}
	return false
}

// fileItem is a file's row: its name tinted by its status, a renamed file's old name after it, a deleted one struck
// through, and in Changed a dot for whether it's committed.
func (e *explorer) fileItem(th *theme.Resolved, f gitx.FileState, label string, sc gitx.Scope) widget.TreeItem {
	id := "\x00" + f.Path
	if sc == gitx.Uncommitted {
		id = f.Area.String() + id
	}
	it := widget.TreeItem{ID: id, Label: label, Strike: f.Code == 'D', Help: "files/explorer"}
	if _, name := gitCodeTokens(f.Code); name != 0 && (sc != gitx.All || e.cols.status) {
		it.Tone = th.C(name)
	}
	if f.Ignored {
		it.Tone, it.Note = th.C(tGitIgnored), "ignored"
	}
	if f.Old != "" {
		it.Note = "← " + path.Base(f.Old)
	}
	if sc == gitx.Changed {
		switch f.Area {
		case gitx.Working:
			it.Glyph, it.GlyphTone = "●", th.C(tGitDot)
		case gitx.Both:
			it.Glyph, it.GlyphTone = "◉", th.C(tGitDotBoth)
		default:
			it.Glyph, it.GlyphTone = "○", th.C(tGitDotDone)
		}
	}
	return it
}

// trailing is a row's markers at the right: a directory's changed-file count, a file's diagnostics, ± lines and
// status letter, as the columns say.
func (e *explorer) trailing(c *ui.Ctx, st *gitx.State, sc gitx.Scope, it widget.TreeItem) {
	th := c.Theme()
	if it.Section {
		n := 0
		for _, f := range st.Files(sc) {
			if "\x01"+f.Area.String() == it.ID {
				n++
			}
		}
		c.Text("n", fmt.Sprint(n), monoTok(c, 10, theme.FgFaint))
		return
	}
	area, p, isFile := strings.Cut(it.ID, "\x00")
	if !isFile || p == "" {
		return
	}
	if len(it.Children) > 0 {
		n := 0
		for _, f := range st.Files(sc) {
			if strings.HasPrefix(f.Path, p) && f.Code != 0 {
				n++
			}
		}
		if n > 0 {
			c.Text("dot", "●", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 8}, Color: th.C(tGitDot)})
			c.Text("n", fmt.Sprint(n), monoTok(c, 10, theme.FgFaint))
		}
		return
	}
	var f gitx.FileState
	found := false
	for _, x := range st.Files(sc) {
		if x.Path == p && (area == "" || x.Area.String() == area) {
			f, found = x, true
			break
		}
	}
	if !found {
		return
	}
	if e.cols.diag || sc != gitx.All {
		e.diagCounts(c, e.sh.git.abs(p))
	}
	if (e.cols.lines || sc != gitx.All) && (f.Add > 0 || f.Del > 0) {
		widget.DiffStat(c, "stat", f.Add, f.Del, widget.DiffStatOpts{Bar: sc != gitx.Uncommitted, Scale: 80})
	}
	if letter, _ := gitCodeTokens(f.Code); letter != 0 && (e.cols.status || sc != gitx.All) {
		c.Text("code", string(f.Code), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10, Weight: typeset.SemiBold}, Color: th.C(letter)})
	}
}

// diagCounts are a file's error and warning counts: ✕2 ⚠1.
func (e *explorer) diagCounts(c *ui.Ctx, file string) {
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
	}{{errs, "✕", tDiagError}, {warns, "⚠", tDiagWarning}} {
		if x.n > 0 {
			c.Text(x.glyph, fmt.Sprintf("%s%d", x.glyph, x.n), monoTok(c, 10.5, x.tok))
		}
	}
}
