package editor

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/app/kit/work"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/search"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// SearchView is project search: a PCRE2 query over the work dir's files, its
// matches streamed in by file, and a replace that shows its diff (ReplaceView) before it writes anything.
const (
	SearchView  = kit.SearchView
	ReplaceView = kit.ReplaceView
)

var (
	tSearchMatch   = theme.DeclareOn("search.match", "alpha(accent, 25%)", "bg.surface")
	tSearchRemoved = theme.Declare("search.removed", "error")
	tSearchAdded   = theme.Declare("search.added", "ok")
)

// Search is the search panel, the replace preview, and the grep picker on the same engine.
type Search struct {
	kit.Disposer
	Editor *Editor
	Tools  *search.Tools
	h      plugin.Host

	query, replace, globs string
	ignoreCase            bool
	allProjects           bool   // the session's every project, not only the panel's
	project               string // the project of the panel last drawn; "" for the window's own
	sel                   int

	mu      sync.Mutex
	ran     searchRun // the search last started
	runs    int       // how many have started
	matches []search.Match
	done    bool
	since   time.Time // when the search running began
	err     error
	cancel  context.CancelFunc
	busy    work.Busy

	plan *editPlan // the edits being previewed

	shown searchShown // on the loop only
}

// searchShown is the results as last drawn, made again only when a match comes in or the grouping changes.
type searchShown struct {
	key      searchShownKey
	rows     []searchRow
	n, files int
}

type searchShownKey struct {
	run      int
	matches  int
	projects string // the projects the rows are grouped under, by name and directory; "" ungrouped
}

// editPlan is a change to files shown as a diff before it's made: a replace, a rename.
type editPlan struct {
	title, detail string
	edits         []search.FileEdit
	dir           string // the edits' paths are relative to it, or absolute when it's ""
	after         func() // after it's applied
}

// forget stops the search running and drops what was searched, as the window moves onto another directory.
func (p *Search) forget() {
	p.mu.Lock()
	if p.cancel != nil {
		p.cancel()
	}
	p.ran, p.matches, p.done, p.err, p.cancel = searchRun{}, nil, false, nil, nil
	p.mu.Unlock()
	p.query, p.replace, p.globs, p.project, p.sel, p.plan, p.shown = "", "", "", "", 0, nil, searchShown{}
}

// searchRun is what a search was run with, so a change in any of it runs it again.
type searchRun struct {
	pattern, globs string
	all, icase     bool
	roots          string // the directories searched, joined by NULs
}

func (p *Search) Manifest() plugin.Manifest { return plugin.Manifest{ID: "search"} }

func (p *Search) Activate(_ context.Context, h plugin.Host) error {
	p.Add(h.Contribute(registry.Busy{Name: "search", Busy: p.busy.Busy, Source: "builtin:search"}))
	p.h = h
	p.Add(kit.OnDirChanged(h, "search starts afresh in the directory", func(kit.DirChanged) { p.forget() }))
	for _, c := range []registry.Command{
		{ID: "search.open", Title: "Search the project for a PCRE2 {query}; \"@all \" first takes in generated files", Run: func(ctx registry.Ctx, a registry.Args) error {
			if q, ok := a["query"].(string); ok {
				p.query = q
			}
			for _, v := range h.Layout().Visible() {
				if v.ID == SearchView {
					return nil
				}
			}
			return h.Run(ctx, "project.panel", registry.Args{"name": "search"})
		}},
		{ID: "search.replace.preview", Title: "Show the diff the replace would make", Run: func(ctx registry.Ctx, _ registry.Args) error {
			return p.preview(ctx)
		}},
		{ID: "search.replace.apply", Title: "Make the replace being previewed", Run: func(ctx registry.Ctx, _ registry.Args) error {
			return p.apply(ctx)
		}},
		{ID: "edits.preview", Title: "Show {edits} to files (search.FileEdit, by absolute path) as a diff, which Apply makes; {title} and {detail} head it",
			Run: func(ctx registry.Ctx, a registry.Args) error {
				edits, _ := a["edits"].([]search.FileEdit)
				if len(edits) == 0 {
					return fmt.Errorf("no edits to preview")
				}
				return p.show(ctx, &editPlan{title: a.String("title"), detail: a.String("detail"), edits: edits})
			}},
		{ID: "search.quickfix", Title: "Send the search's matches to the quickfix list", Run: func(ctx registry.Ctx, _ registry.Args) error {
			var items []map[string]any
			for _, m := range p.results() {
				col := 1
				if len(m.Spans) > 0 {
					col = m.Spans[0][0] + 1
				}
				items = append(items, map[string]any{"path": m.Path, "line": m.Line, "col": col, "text": strings.TrimSpace(m.Text)})
			}
			if err := h.Run(ctx, "quickfix.set", registry.Args{"title": "search " + p.query, "items": items}); err != nil {
				return err
			}
			return h.Run(ctx, "quickfix.open", nil)
		}},
	} {
		p.Add(h.Command(c))
	}
	p.Add(h.View(SearchView, searchView{p}))
	p.Add(h.View(ReplaceView, replaceView{p}))
	p.Add(h.Keymap(registry.Keymap{Mode: "global", Keys: "<D-S-f>", Command: "project.panel", Args: registry.Args{"name": "search"}, Desc: "Search the project"}))
	return nil
}

func (p *Search) Deactivate(ctx context.Context) error {
	p.mu.Lock()
	if p.cancel != nil {
		p.cancel()
	}
	p.mu.Unlock()
	p.busy.Wait()
	return p.Disposer.Deactivate(ctx)
}

// Wait waits for the search running off the loop.
func (p *Search) Wait() { p.busy.Wait() }

func (p *Search) dir() string { return p.Editor.dir }

// shownPath is path as the preview names it: relative to its project, led by the project's name when there are
// several; relative to the work dir when it's in it.
func (p *Search) shownPath(path string) string {
	if !filepath.IsAbs(path) {
		return path
	}
	w := kit.ProjectsHere(p.h)
	if pr, ok := w.Of(path); ok && w.Many() {
		if r, err := filepath.Rel(pr.Dir, path); err == nil {
			return pr.Name + "/" + filepath.ToSlash(r)
		}
	}
	if r, err := filepath.Rel(p.dir(), path); err == nil && filepath.IsLocal(r) {
		return r
	}
	return path
}

// roots are the directories a search covers: the panel's project, or with All projects, every project's.
func (p *Search) roots() []string {
	w := kit.ProjectsHere(p.h)
	if p.allProjects && w.Many() {
		return kit.DirsOf(w.Projects)
	}
	if p.project != "" {
		if d := w.Dir(p.project); d != "" {
			return []string{d}
		}
	}
	return []string{p.dir()}
}

// queryFor is the engine's query for what's typed in root: "@all " first searches generated and vendored files too.
func (p *Search) queryFor(r searchRun, root string) search.Query {
	q := search.Query{Pattern: r.pattern, Dir: root, IgnoreCase: r.icase}
	if r.globs != "" {
		q.Globs = strings.Fields(strings.ReplaceAll(r.globs, ",", " "))
	}
	if !r.all && filepath.Clean(root) == filepath.Clean(p.dir()) {
		q.Exclude = p.Editor.unsearchable()
	}
	return q
}

// run starts the search for what's typed, unless it's the one already running or run.
func (p *Search) run() {
	pat, all := strings.CutPrefix(p.query, "@all ")
	roots := p.roots()
	r := searchRun{pattern: pat, globs: p.globs, all: all, icase: p.ignoreCase, roots: strings.Join(roots, "\x00")}
	p.mu.Lock()
	if r == p.ran {
		p.mu.Unlock()
		return
	}
	if p.cancel != nil {
		p.cancel()
	}
	p.ran, p.matches, p.done, p.err, p.sel, p.since = r, nil, r.pattern == "", nil, 0, kit.WindowNow(p.h)
	p.runs++
	if r.pattern == "" {
		p.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.mu.Unlock()
	p.busy.Go(func() {
		var err error
		for _, root := range roots {
			// Matches keep absolute paths, so those of every root sit together.
			err = errors.Join(err, p.Tools.Run(ctx, p.queryFor(r, root), func(ms []search.Match) {
				for i := range ms {
					ms[i].Path = filepath.Join(root, filepath.FromSlash(ms[i].Path))
				}
				p.mu.Lock()
				if p.ran == r {
					p.matches = append(p.matches, ms...)
				}
				p.mu.Unlock()
				p.h.Redraw()
			}))
		}
		p.mu.Lock()
		if p.ran == r {
			p.done, p.err = true, err
		}
		p.mu.Unlock()
		p.h.Redraw()
	})
}

// results are the matches so far, by file then line.
func (p *Search) results() []search.Match {
	p.mu.Lock()
	ms := slices.Clone(p.matches)
	p.mu.Unlock()
	slices.SortStableFunc(ms, func(a, b search.Match) int {
		if a.Path != b.Path {
			return strings.Compare(a.Path, b.Path)
		}
		return a.Line - b.Line
	})
	return ms
}

// searchRow is a row of the results: a project's heading, a file's header, or a match in it.
type searchRow struct {
	project string
	file    string
	count   int
	match   *search.Match
}

// rowsOf are the matches' rows by file, under a heading per project when projectOf names them.
func rowsOf(ms []search.Match, projectOf func(path string) string) []searchRow {
	var rows []searchRow
	project := ""
	for i := range ms {
		m := &ms[i]
		if pr := projectOf(m.Path); pr != "" && pr != project {
			project = pr
			rows = append(rows, searchRow{project: pr})
		}
		if last := rows; len(last) == 0 || last[len(last)-1].file != m.Path && (last[len(last)-1].match == nil || last[len(last)-1].match.Path != m.Path) {
			rows = append(rows, searchRow{file: m.Path})
		}
		for j := len(rows) - 1; j >= 0; j-- {
			if rows[j].match == nil && rows[j].project == "" {
				rows[j].count += len(m.Spans)
				break
			}
		}
		rows = append(rows, searchRow{match: m})
	}
	return rows
}

type searchView struct{ p *Search }

func (v searchView) Title(string) string { return "Search" }
func (v searchView) EdgeToEdge()         {}
func (v searchView) Build(vc uireg.ViewCtx) ui.Node {
	return &ui.Func{Key: "search", Fn: func(c *ui.Ctx) {
		v.p.project = vc.Project
		v.p.build(c)
	}}
}

// rows are the results' rows, and how many matches and files they hold.
func (p *Search) rows(w kit.Here) ([]searchRow, int, int) {
	key := searchShownKey{}
	if p.allProjects && w.Many() {
		for _, pr := range w.Projects {
			key.projects += pr.Name + "\x00" + pr.Dir + "\x00"
		}
	}
	p.mu.Lock()
	key.run, key.matches = p.runs, len(p.matches)
	p.mu.Unlock()
	if p.shown.rows != nil && p.shown.key == key {
		return p.shown.rows, p.shown.n, p.shown.files
	}
	rows := rowsOf(p.results(), func(path string) string {
		if pr, ok := w.Of(path); ok && key.projects != "" {
			return pr.Name
		}
		return ""
	})
	n, files := 0, 0
	for _, r := range rows {
		if r.match == nil && r.project == "" {
			files++
			n += r.count
		}
	}
	if rows == nil {
		rows = []searchRow{}
	}
	p.shown = searchShown{key: key, rows: rows, n: n, files: files}
	return rows, n, files
}

func (p *Search) build(c *ui.Ctx) {
	th := c.Theme()
	p.run()
	w := kit.ProjectsHere(p.h)
	rows, n, files := p.rows(w)
	p.mu.Lock()
	done, err := p.done, p.err
	p.mu.Unlock()
	c.Column("search", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(kit.TExplorerBg), Help: "files/search", Role: ui.RoleRegion, Label: "Search"}, func(c *ui.Ctx) {
		c.Column("form", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 12, R: 12, T: 10, B: 8}, Gap: 6}, func(c *ui.Ctx) {
			c.Row("head", ui.Style{W: ui.Flex(1), Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("t", "SEARCH", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10, Weight: typeset.SemiBold, Tracking: .12},
					Color: th.C(theme.Accent)})
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				status := ""
				engine := "go"
				if _, ok := p.Tools.Ripgrep(); ok {
					engine = "rg"
				}
				switch {
				case err != nil:
					status = kit.FirstLine(err.Error())
				case p.ran.pattern == "":
				case !done:
					widget.Busy(c, "busy", widget.LoadState{Phase: widget.Pending, Since: p.since, Note: fmt.Sprintf("%d so far", n)},
						widget.BusyOpts{Label: "Searching with " + engine, Size: 10.5, Help: "files/search"})
					return
				default:
					status = fmt.Sprintf("%s · %d in %d %s", engine, n, files, kit.Plural(files, "file", "files"))
				}
				tok := theme.FgFaint
				if err != nil {
					tok = kit.TDiagError
				}
				c.Text("status", status, kit.MonoTok(c, 10.5, tok))
			})
			c.Row("find", ui.Style{W: ui.Flex(1), Gap: 6, Cross: ui.Center}, func(c *ui.Ctx) {
				widget.Input(c, "q", &p.query, widget.InputOpts{Label: "Find", Placeholder: "Find (PCRE2; @all for generated files)", Help: "files/search"})
				matchCase := widget.ToggleChip(c, "icase", "Aa", !p.ignoreCase, widget.ToggleChipOpts{Help: "files/search", Tooltip: "Match case"})
				p.ignoreCase = !matchCase
			})
			if w.Many() {
				c.Row("all", ui.Style{W: ui.Flex(1), Gap: 7, Cross: ui.Center}, func(c *ui.Ctx) {
					if widget.Toggle(c, "all", p.allProjects, widget.ToggleOpts{Label: "All projects", Help: "files/search#projects"}) {
						p.allProjects = !p.allProjects
					}
					c.Text("t", "All projects", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 11}, Color: th.C(theme.FgFaint)})
					c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
					if !p.allProjects && p.project != "" && done && p.ran.pattern != "" {
						c.Text("n", fmt.Sprintf("%d in %s", n, p.project), kit.MonoTok(c, 10, theme.FgGhost))
					}
				})
			}
			widget.Input(c, "r", &p.replace, widget.InputOpts{Label: "Replace", Placeholder: "Replace ($1, ${name})", Help: "files/search#replace"})
			widget.Input(c, "g", &p.globs, widget.InputOpts{Label: "Files", Placeholder: "files: *.go, *.ts", Help: "files/search"})
		})
		res := widget.List(c, "results", len(rows), &p.sel, widget.ListOpts{Help: "files/search", Label: "Results"}, func(c *ui.Ctx, i int, selected bool) {
			p.row(c, rows[i])
		})
		if res.Activated >= 0 && res.Activated < len(rows) {
			p.open(rows[res.Activated])
		}
		c.Row("actions", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(12, 8), Gap: 8, Cross: ui.Center, Stroke: th.C(theme.Line)}, func(c *ui.Ctx) {
			if widget.Button(c, "replace", "Replace all…", widget.ButtonOpts{Disabled: p.replace == "" || n == 0 || !done, Help: "files/search#replace"}) {
				if err := p.preview(context.Background()); err != nil {
					p.h.Notify(core.Notice{ID: "search.replace", Severity: core.SevError, Key: true, Text: err.Error()})
				}
			}
			if widget.Button(c, "qf", "→ quickfix", widget.ButtonOpts{Ghost: true, Disabled: n == 0, Help: "files/search"}) {
				p.h.Run(context.Background(), "search.quickfix", nil)
			}
		})
	})
}

// row draws a file's header, or a match: its line number and text, the matches marked, and with a replace typed, what
// each becomes.
func (p *Search) row(c *ui.Ctx, r searchRow) {
	th := c.Theme()
	code := ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11.5}, Color: th.C(theme.FgMuted)}
	if r.project != "" {
		c.Row("project", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.PadXY(12, 0), Gap: 7, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Box("sq", ui.Style{W: ui.Px(8), H: ui.Px(8), Radius: 2, Fill: kit.ProjectsHere(p.h).Colour(th, r.project)}, nil)
			c.Text("p", r.project, kit.MonoTok(c, 11, theme.FgMuted))
		})
		return
	}
	if r.match == nil {
		c.Row("file", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.PadXY(12, 0), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Box("sw", ui.Style{W: ui.Px(9), H: ui.Px(9), Radius: 2, Fill: th.C(theme.StatusWorking)}, nil)
			c.Text("p", p.rowPath(r.file), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12.5}, Color: th.C(theme.Fg)})
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			c.Text("n", fmt.Sprint(r.count), kit.MonoTok(c, 10.5, theme.FgFaint))
		})
		return
	}
	m := r.match
	c.Row("m", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.Insets{L: 30, R: 12}, Gap: 10, Cross: ui.Center, Clip: true}, func(c *ui.Ctx) {
		c.Text("ln", fmt.Sprint(m.Line), kit.MonoTok(c, 10.5, theme.FgGhost))
		lead := len(m.Text) - len(strings.TrimLeft(m.Text, " \t"))
		replaced, err := p.replaced(m)
		c.Row("text", ui.Style{Cross: ui.Center}, func(c *ui.Ctx) {
			if p.replace == "" || err != nil {
				segments(c, m.Text[lead:], shift(m.Spans, lead), code, func(c *ui.Ctx, k int, s string) {
					c.Box(k, ui.Style{Fill: th.C(tSearchMatch), Radius: 2}, func(c *ui.Ctx) { c.Text("t", s, code) })
				})
				return
			}
			c.Text("new", strings.TrimLeft(replaced, " \t"), ui.TextStyle{Font: code.Font, Color: th.C(tSearchAdded)})
		})
	})
}

// rowPath is a file as its header names it: relative to its project, or to the work dir.
func (p *Search) rowPath(path string) string {
	if pr, ok := kit.ProjectsHere(p.h).Of(path); ok {
		if r, err := filepath.Rel(pr.Dir, path); err == nil {
			return filepath.ToSlash(r)
		}
	}
	return p.shownPath(path)
}

// replaced is a match's line with the replace made.
func (p *Search) replaced(m *search.Match) (string, error) {
	plan, err := search.Plan(p.queryFor(p.ran, ""), p.replace, []search.Match{{Path: m.Path, Line: 1, Text: m.Text, Spans: m.Spans}},
		func(string) ([]byte, bool) { return []byte(m.Text + "\n"), true })
	if err != nil || len(plan) == 0 {
		return m.Text, err
	}
	return strings.TrimSuffix(string(plan[0].New), "\n"), nil
}

func shift(spans [][2]int, by int) [][2]int {
	out := make([][2]int, 0, len(spans))
	for _, s := range spans {
		if s[1] > by {
			out = append(out, [2]int{max(s[0]-by, 0), s[1] - by})
		}
	}
	return out
}

// segments draws s as text runs, spans of it by mark.
func segments(c *ui.Ctx, s string, spans [][2]int, ts ui.TextStyle, mark func(c *ui.Ctx, k int, s string)) {
	at := 0
	for i, sp := range spans {
		if sp[0] > len(s) {
			break
		}
		if sp[0] > at {
			c.Text(fmt.Sprint("t", i), s[at:sp[0]], ts)
		}
		mark(c, i, s[sp[0]:min(sp[1], len(s))])
		at = min(sp[1], len(s))
	}
	if at < len(s) {
		c.Text("rest", s[at:], ts)
	}
}

// open opens a match's file at its line, or a file at its first match.
func (p *Search) open(r searchRow) {
	path, line := r.file, 1
	if r.project != "" {
		return
	}
	if r.match != nil {
		path, line = r.match.Path, r.match.Line
	}
	if err := p.h.Run(context.Background(), "editor.open", registry.Args{"path": path, "line": line}); err != nil {
		p.h.Notify(core.Notice{ID: "search.open", Severity: core.SevWarning, Key: true, Text: err.Error()})
	}
}

// preview plans the replace over the matches, from open buffers' text where a file is open, and shows its diff.
// Files that aren't open are read off the loop.
func (p *Search) preview(ctx registry.Ctx) error {
	q, repl, matches := p.queryFor(p.ran, ""), p.replace, p.results()
	open := map[string][]byte{}
	for _, m := range matches {
		if _, seen := open[m.Path]; !seen {
			b, _ := p.Editor.openFileBytes(m.Path)
			open[m.Path] = b
		}
	}
	read := func(path string) ([]byte, bool) {
		b := open[path]
		return b, b != nil
	}
	return plugin.RunSlow(ctx, p.h, plugin.Slow{ID: "search.replace", Doing: "Planning the replace"},
		func(context.Context) ([]search.FileEdit, error) { return search.Plan(q, repl, matches, read) },
		func(ctx registry.Ctx, plan []search.FileEdit) error { return p.showPlan(ctx, q, repl, plan) })
}

// showPlan shows the diff of a planned replace.
func (p *Search) showPlan(ctx registry.Ctx, q search.Query, repl string, plan []search.FileEdit) error {
	if len(plan) == 0 {
		return fmt.Errorf("the replace changes nothing")
	}
	count := 0
	for _, f := range plan {
		count += f.Count
	}
	return p.show(ctx, &editPlan{
		title:  fmt.Sprintf("%d %s in %d %s", count, kit.Plural(count, "replacement", "replacements"), len(plan), kit.Plural(len(plan), "file", "files")),
		detail: fmt.Sprintf("/%s/ → %s", q.Pattern, repl), edits: plan, dir: q.Dir,
		after: func() {
			p.mu.Lock()
			p.ran = searchRun{} // search again: the matches moved
			p.mu.Unlock()
		}})
}

// show opens the diff of plan, which Apply makes.
func (p *Search) show(ctx registry.Ctx, plan *editPlan) error {
	p.plan = plan
	return p.h.Run(ctx, "view.open", registry.Args{"id": ReplaceView, "place": "tab", "title": "Preview"})
}

// apply makes the previewed replace: open buffers are changed in the buffer, as one undoable step each; other files
// are written.
func (p *Search) apply(ctx registry.Ctx) error {
	plan := p.plan
	if plan == nil || len(plan.edits) == 0 {
		return fmt.Errorf("nothing to apply")
	}
	n := 0
	var errs []string
	for _, f := range plan.edits {
		if buf, ok := p.Editor.openBuffer(filepath.Join(plan.dir, f.Path)); ok {
			cur := buf.Bytes()
			if string(cur) != string(f.Old) {
				errs = append(errs, f.Path+" changed since the preview")
				continue
			}
			if _, err := buf.Text.Apply(text.Edit{Ranges: kit.HunkEdits(buf.Text.Snapshot(), f.Hunks), Author: text.Author{Kind: text.User}}); err != nil {
				errs = append(errs, f.Path+": "+err.Error())
				continue
			}
		} else if err := f.Write(plan.dir); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		n += f.Count
	}
	p.plan = nil
	p.h.Layout().Close(layoutRef(ReplaceView))
	if plan.after != nil {
		plan.after()
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	p.h.Notify(core.Notice{ID: "edits.applied", Severity: core.SevInfo, Text: fmt.Sprintf("%s: done", plan.title)})
	return nil
}

type replaceView struct{ p *Search }

func (v replaceView) Title(string) string { return "Preview" }
func (v replaceView) Build(uireg.ViewCtx) ui.Node {
	return &ui.Func{Key: "replace", Fn: v.p.buildPreview}
}

// buildPreview is the plan's diff, file by file, with Apply and Cancel.
func (p *Search) buildPreview(c *ui.Ctx) {
	th := c.Theme()
	plan := p.plan
	if plan == nil {
		c.Text("none", "Nothing to preview.", kit.MonoTok(c, 11.5, theme.FgGhost))
		return
	}
	c.Column("replace", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Gap: 12, Help: "files/search#replace"}, func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("t", plan.title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 14, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
			c.Text("q", plan.detail, kit.MonoTok(c, 11, theme.FgFaint))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if widget.Button(c, "apply", "Apply", widget.ButtonOpts{Primary: true, Disabled: len(plan.edits) == 0, Help: "files/search#replace"}) {
				if err := p.apply(context.Background()); err != nil {
					p.h.Notify(core.Notice{ID: "search.replace", Severity: core.SevError, Key: true, Text: err.Error()})
				}
			}
			if widget.Button(c, "cancel", "Cancel", widget.ButtonOpts{Help: "files/search#replace"}) {
				p.plan = nil
				p.h.Layout().Close(layoutRef(ReplaceView))
			}
		})
		c.Column("files", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Gap: 14, ScrollY: true}, func(c *ui.Ctx) {
			for i, f := range plan.edits {
				c.Column(i, ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) {
					c.Row("name", ui.Style{Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
						c.Text("p", p.shownPath(f.Path), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 12, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
						add, del := 0, 0
						for _, h := range f.Hunks {
							add, del = add+h.NewLines, del+h.OldLines
						}
						widget.DiffStat(c, "stat", add, del, widget.DiffStatOpts{})
					})
					for j, h := range f.Hunks {
						diffBlock(c, j, h, true)
					}
				})
			}
		})
	})
}

// diffBlock draws a hunk as a unified diff: its header, the lines it removes, and with withNew, the lines it adds.
func diffBlock(c *ui.Ctx, key any, h gitx.Hunk, withNew bool) {
	th := c.Theme()
	code := ui.TextStyle{Font: kit.CodeFont}
	c.Column(key, ui.Style{W: ui.Flex(1), Radius: 4, Clip: true, Stroke: th.C(theme.Line), Border: 1}, func(c *ui.Ctx) {
		c.Row("h", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(8, 2), Fill: th.C(theme.BgRaised)}, func(c *ui.Ctx) {
			c.Text("t", h.Header(), kit.MonoTok(c, 10.5, theme.FgFaint))
		})
		line := func(k string, i int, sign, s string, fill, fg theme.TokenID) {
			c.Row(fmt.Sprint(k, i), ui.Style{W: ui.Flex(1), Pad: ui.PadXY(8, 1), Fill: th.C(fill)}, func(c *ui.Ctx) {
				code.Color = th.C(fg)
				c.Text("t", sign+" "+kit.ExpandTabs(strings.TrimRight(s, "\r\n")), code)
			})
		}
		for i, l := range h.Old {
			line("o", i, "-", l, theme.DiffDel, tSearchRemoved)
		}
		if withNew {
			for i, l := range h.New {
				line("n", i, "+", l, theme.DiffAdd, tSearchAdded)
			}
		}
	})
}
