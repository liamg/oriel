package app

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
	"github.com/liamg/oriel/worker/search"
)

// SearchView is project search (ide.md, "Navigation and search"): a PCRE2 query over the work dir's files, its
// matches streamed in by file, and a replace that shows its diff (ReplaceView) before it writes anything.
const (
	SearchView  = "search"
	ReplaceView = "search.replace"
)

var (
	tSearchMatch   = theme.DeclareOn("search.match", "alpha(accent, 25%)", "bg.surface")
	tSearchRemoved = theme.Declare("search.removed", "error")
	tSearchAdded   = theme.Declare("search.added", "ok")
)

// projectSearch is the search panel, the replace preview, and the grep picker on the same engine.
type projectSearch struct {
	disposer
	sh *shell
	h  ext.Host

	query, replace, globs string
	ignoreCase            bool
	sel                   int

	mu      sync.Mutex
	ran     searchRun // the search last started
	matches []search.Match
	done    bool
	err     error
	cancel  context.CancelFunc
	busy    sync.WaitGroup

	plan *editPlan // the edits being previewed
}

// editPlan is a change to files shown as a diff before it's made: a replace, a rename.
type editPlan struct {
	title, detail string
	edits         []search.FileEdit
	dir           string // the edits' paths are relative to it, or absolute when it's ""
	after         func() // after it's applied
}

// searchRun is what a search was run with, so a change in any of it runs it again.
type searchRun struct {
	pattern, globs string
	all, icase     bool
}

func (p *projectSearch) Manifest() ext.Manifest { return ext.Manifest{ID: "search"} }

func (p *projectSearch) Activate(_ context.Context, h ext.Host) error {
	p.h = h
	open := registry.Args{"id": SearchView, "place": "dock", "edge": "left", "title": "Search", "size": 380}
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
			return h.Run(ctx, "view.open", open)
		}},
		{ID: "search.replace.preview", Title: "Show the diff the replace would make", Run: func(ctx registry.Ctx, _ registry.Args) error {
			return p.preview(ctx)
		}},
		{ID: "search.replace.apply", Title: "Make the replace being previewed", Run: func(ctx registry.Ctx, _ registry.Args) error {
			return p.apply(ctx)
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
		p.add(h.Command(c))
	}
	p.add(h.View(SearchView, searchView{p}))
	p.add(h.View(ReplaceView, replaceView{p}))
	p.sh.search = p
	p.add(h.Keymap(registry.Keymap{Mode: "global", Keys: "<D-S-f>", Command: "search.open", Desc: "Search the project"}))
	return nil
}

func (p *projectSearch) Deactivate(ctx context.Context) error {
	p.mu.Lock()
	if p.cancel != nil {
		p.cancel()
	}
	p.mu.Unlock()
	p.busy.Wait()
	return p.disposer.Deactivate(ctx)
}

func (p *projectSearch) wait() { p.busy.Wait() }

func (p *projectSearch) dir() string { return p.sh.editor.dir }

// shownPath is path as the preview names it: relative to the work dir when it's in it.
func (p *projectSearch) shownPath(path string) string {
	if !filepath.IsAbs(path) {
		return path
	}
	if r, err := filepath.Rel(p.dir(), path); err == nil && filepath.IsLocal(r) {
		return r
	}
	return path
}

// queryFor is the engine's query for what's typed: "@all " first searches generated and vendored files too.
func (p *projectSearch) queryFor(r searchRun) search.Query {
	q := search.Query{Pattern: r.pattern, Dir: p.dir(), IgnoreCase: r.icase}
	if r.globs != "" {
		q.Globs = strings.Fields(strings.ReplaceAll(r.globs, ",", " "))
	}
	if !r.all {
		q.Exclude = p.sh.editor.unsearchable()
	}
	return q
}

// run starts the search for what's typed, unless it's the one already running or run.
func (p *projectSearch) run() {
	pat, all := strings.CutPrefix(p.query, "@all ")
	r := searchRun{pattern: pat, globs: p.globs, all: all, icase: p.ignoreCase}
	p.mu.Lock()
	if r == p.ran {
		p.mu.Unlock()
		return
	}
	if p.cancel != nil {
		p.cancel()
	}
	p.ran, p.matches, p.done, p.err, p.sel = r, nil, r.pattern == "", nil, 0
	if r.pattern == "" {
		p.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	q := p.queryFor(r)
	p.busy.Add(1)
	p.mu.Unlock()
	go func() {
		defer p.busy.Done()
		err := search.Run(ctx, q, func(ms []search.Match) {
			p.mu.Lock()
			if p.ran == r {
				p.matches = append(p.matches, ms...)
			}
			p.mu.Unlock()
			p.h.Redraw()
		})
		p.mu.Lock()
		if p.ran == r {
			p.done, p.err = true, err
		}
		p.mu.Unlock()
		p.h.Redraw()
	}()
}

// results are the matches so far, by file then line.
func (p *projectSearch) results() []search.Match {
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

// searchRow is a row of the results: a file's header, or a match in it.
type searchRow struct {
	file  string
	count int
	match *search.Match
}

func rowsOf(ms []search.Match) []searchRow {
	var rows []searchRow
	for i := range ms {
		m := &ms[i]
		if len(rows) == 0 || rows[len(rows)-1].file != m.Path && (rows[len(rows)-1].match == nil || rows[len(rows)-1].match.Path != m.Path) {
			rows = append(rows, searchRow{file: m.Path})
		}
		for j := len(rows) - 1; j >= 0; j-- {
			if rows[j].match == nil {
				rows[j].count += len(m.Spans)
				break
			}
		}
		rows = append(rows, searchRow{match: m})
	}
	return rows
}

type searchView struct{ p *projectSearch }

func (v searchView) Title(string) string { return "Search" }
func (v searchView) EdgeToEdge()         {}
func (v searchView) Build(registry.ViewCtx) ui.Node {
	return &ui.Func{Key: "search", Fn: v.p.build}
}

func (p *projectSearch) build(c *ui.Ctx) {
	th := c.Theme()
	p.run()
	ms := p.results()
	rows := rowsOf(ms)
	p.mu.Lock()
	done, err := p.done, p.err
	p.mu.Unlock()
	n, files := 0, 0
	for _, r := range rows {
		if r.match == nil {
			files++
			n += r.count
		}
	}
	c.Column("search", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(tExplorerBg), Help: "files/search", Role: ui.RoleRegion, Label: "Search"}, func(c *ui.Ctx) {
		c.Column("form", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 12, R: 12, T: 10, B: 8}, Gap: 6}, func(c *ui.Ctx) {
			c.Row("head", ui.Style{W: ui.Flex(1), Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("t", "SEARCH", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10, Weight: typeset.SemiBold, Tracking: .12},
					Color: th.C(theme.Accent)})
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				status := ""
				engine := "go"
				if _, ok := search.Ripgrep(); ok {
					engine = "rg"
				}
				switch {
				case err != nil:
					status = firstLine(err.Error())
				case p.ran.pattern == "":
				case !done:
					status = fmt.Sprintf("%s · %d…", engine, n)
				default:
					status = fmt.Sprintf("%s · %d in %d %s", engine, n, files, plural(files, "file", "files"))
				}
				tok := theme.FgFaint
				if err != nil {
					tok = tDiagError
				}
				c.Text("status", status, monoTok(c, 10.5, tok))
			})
			c.Row("find", ui.Style{W: ui.Flex(1), Gap: 6, Cross: ui.Center}, func(c *ui.Ctx) {
				widget.Input(c, "q", &p.query, widget.InputOpts{Label: "Find", Placeholder: "Find (PCRE2; @all for generated files)", Help: "files/search"})
				matchCase := widget.ToggleChip(c, "icase", "Aa", !p.ignoreCase, widget.ToggleChipOpts{Help: "files/search", Tooltip: "Match case"})
				p.ignoreCase = !matchCase
			})
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

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// row draws a file's header, or a match: its line number and text, the matches marked, and with a replace typed, what
// each becomes.
func (p *projectSearch) row(c *ui.Ctx, r searchRow) {
	th := c.Theme()
	code := ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11.5}, Color: th.C(theme.FgMuted)}
	if r.match == nil {
		c.Row("file", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.PadXY(12, 0), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Box("sw", ui.Style{W: ui.Px(9), H: ui.Px(9), Radius: 2, Fill: th.C(theme.StatusWorking)}, nil)
			c.Text("p", r.file, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12.5}, Color: th.C(theme.Fg)})
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			c.Text("n", fmt.Sprint(r.count), monoTok(c, 10.5, theme.FgFaint))
		})
		return
	}
	m := r.match
	c.Row("m", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.Insets{L: 30, R: 12}, Gap: 10, Cross: ui.Center, Clip: true}, func(c *ui.Ctx) {
		c.Text("ln", fmt.Sprint(m.Line), monoTok(c, 10.5, theme.FgGhost))
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

// replaced is a match's line with the replace made.
func (p *projectSearch) replaced(m *search.Match) (string, error) {
	plan, err := search.Plan(p.queryFor(p.ran), p.replace, []search.Match{{Path: m.Path, Line: 1, Text: m.Text, Spans: m.Spans}},
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
func (p *projectSearch) open(r searchRow) {
	path, line := r.file, 1
	if r.match != nil {
		path, line = r.match.Path, r.match.Line
	}
	if err := p.h.Run(context.Background(), "editor.open", registry.Args{"path": filepath.Join(p.dir(), path), "line": line}); err != nil {
		p.h.Notify(core.Notice{ID: "search.open", Severity: core.SevWarning, Key: true, Text: err.Error()})
	}
}

// preview plans the replace over the matches, from open buffers' text where a file is open, and shows its diff.
func (p *projectSearch) preview(ctx registry.Ctx) error {
	q := p.queryFor(p.ran)
	plan, err := search.Plan(q, p.replace, p.results(), p.sh.editor.openFileBytes)
	if err != nil {
		return err
	}
	if len(plan) == 0 {
		return fmt.Errorf("the replace changes nothing")
	}
	count := 0
	for _, f := range plan {
		count += f.Count
	}
	return p.show(ctx, &editPlan{
		title:  fmt.Sprintf("%d %s in %d %s", count, plural(count, "replacement", "replacements"), len(plan), plural(len(plan), "file", "files")),
		detail: fmt.Sprintf("/%s/ → %s", q.Pattern, p.replace), edits: plan, dir: q.Dir,
		after: func() {
			p.mu.Lock()
			p.ran = searchRun{} // search again: the matches moved
			p.mu.Unlock()
		}})
}

// show opens the diff of plan, which Apply makes.
func (p *projectSearch) show(ctx registry.Ctx, plan *editPlan) error {
	p.plan = plan
	return p.h.Run(ctx, "view.open", registry.Args{"id": ReplaceView, "place": "tab", "title": "Preview"})
}

// apply makes the previewed replace: open buffers are changed in the buffer, as one undoable step each; other files
// are written.
func (p *projectSearch) apply(ctx registry.Ctx) error {
	plan := p.plan
	if plan == nil || len(plan.edits) == 0 {
		return fmt.Errorf("nothing to apply")
	}
	n := 0
	var errs []string
	for _, f := range plan.edits {
		if buf, ok := p.sh.editor.openBuffer(filepath.Join(plan.dir, f.Path)); ok {
			cur := buf.Bytes()
			if string(cur) != string(f.Old) {
				errs = append(errs, f.Path+" changed since the preview")
				continue
			}
			if _, err := buf.Text.Apply(text.Edit{Ranges: hunkEdits(buf.Text.Snapshot(), f.Hunks), Author: text.Author{Kind: text.User}}); err != nil {
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

type replaceView struct{ p *projectSearch }

func (v replaceView) Title(string) string { return "Preview" }
func (v replaceView) Build(registry.ViewCtx) ui.Node {
	return &ui.Func{Key: "replace", Fn: v.p.buildPreview}
}

// buildPreview is the plan's diff, file by file, with Apply and Cancel.
func (p *projectSearch) buildPreview(c *ui.Ctx) {
	th := c.Theme()
	plan := p.plan
	if plan == nil {
		c.Text("none", "Nothing to preview.", monoTok(c, 11.5, theme.FgGhost))
		return
	}
	c.Column("replace", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Gap: 12, Help: "files/search#replace"}, func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("t", plan.title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 14, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
			c.Text("q", plan.detail, monoTok(c, 11, theme.FgFaint))
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
	code := ui.TextStyle{Font: codeFont}
	c.Column(key, ui.Style{W: ui.Flex(1), Radius: 4, Clip: true, Stroke: th.C(theme.Line), Border: 1}, func(c *ui.Ctx) {
		c.Row("h", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(8, 2), Fill: th.C(theme.BgRaised)}, func(c *ui.Ctx) {
			c.Text("t", h.Header(), monoTok(c, 10.5, theme.FgFaint))
		})
		line := func(k string, i int, sign, s string, fill, fg theme.TokenID) {
			c.Row(fmt.Sprint(k, i), ui.Style{W: ui.Flex(1), Pad: ui.PadXY(8, 1), Fill: th.C(fill)}, func(c *ui.Ctx) {
				code.Color = th.C(fg)
				c.Text("t", sign+" "+expandTabs(strings.TrimRight(s, "\r\n")), code)
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
