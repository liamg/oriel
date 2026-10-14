package app

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/lsp"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/testrun"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

func lspMarkdown(s string) lsp.Markup { return lsp.Markup{Kind: "markdown", Text: s} }

// testsPanel is the Tests panel's own state.
type testsPanel struct {
	filter int    // 0 every test, 1 failed only, 2 the slowest
	sel    string // the selected row's id: a result's key
	out    int    // 0 this case's output, 1 the whole run's, 2 the raw JSON
}

type testsView struct{ x *testsExt }

func (v testsView) Title(string) string {
	runs := v.x.history.Runs()
	if len(runs) > 0 {
		if _, f, _ := runs[len(runs)-1].Counts(); f > 0 {
			return fmt.Sprintf("Tests %d ✕", f)
		}
	}
	return "Tests"
}
func (v testsView) EdgeToEdge() {}
func (v testsView) Build(registry.ViewCtx) ui.Node {
	return &ui.Func{Key: "tests", Fn: v.x.panelView}
}

// latest is each test's latest result, by key, from the run in progress and the runs kept.
func (x *testsExt) latest() map[string]*testrun.Result {
	out := map[string]*testrun.Result{}
	for _, r := range x.history.Runs() {
		for _, res := range r.Tests {
			if res.Status != testrun.StatusCanceled {
				out[res.Key()] = res
			}
		}
	}
	x.mu.Lock()
	if x.live != nil {
		for _, res := range x.live.Tests {
			out[res.Key()] = res
		}
	}
	x.mu.Unlock()
	return out
}

// panelView is the tree of packages, tests and cases with their states and recent runs, filters, the selected
// case's output, and the last run (36-tests).
func (x *testsExt) panelView(c *ui.Ctx) {
	th := c.Theme()
	latest := x.latest()
	failed := 0
	for _, r := range latest {
		if r.Status.Failing() {
			failed++
		}
	}
	mono := func(size float32, tok theme.TokenID) ui.TextStyle {
		return ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: size}, Color: th.C(tok)}
	}
	p := &x.panel
	c.Column("tests", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(tExplorerBg), Help: "editor/pane#tests", Role: ui.RoleRegion, Label: "Tests"},
		func(c *ui.Ctx) {
			c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(34), Pad: ui.PadXY(12, 0), Gap: 8, Cross: ui.Center, Stroke: th.C(theme.Line)}, func(c *ui.Ctx) {
				c.Text("t", "TESTS", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10, Weight: typeset.SemiBold, Tracking: .12},
					Color: th.C(theme.Accent)})
				for i, f := range []struct{ label, name string }{{fmt.Sprintf("all %d", len(latest)), "All tests"},
					{fmt.Sprintf("✕ %d", failed), "Failed only"}, {"slowest", "Slowest"}} {
					if widget.ToggleChip(c, i, f.label, p.filter == i, widget.ToggleChipOpts{Label: f.name, Help: "editor/pane#tests"}) != (p.filter == i) {
						p.filter = i
					}
				}
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				x.agentRun(c)
				x.lastRunLine(c)
				var names []string
				for _, a := range x.set.Detect(x.workspace()) {
					names = append(names, a.Name())
				}
				c.Text("adapters", strings.Join(names, " · "), mono(10.5, theme.FgFaint))
			})
			c.Row("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
				c.Column("tree", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
					items := x.treeItems(latest, p.filter)
					if len(items) == 0 {
						c.Text("none", "No tests run yet: ␣tt runs the one at the cursor, ␣tf the file's.", mono(11, theme.FgGhost))
						return
					}
					res := widget.Tree(c, fmt.Sprint("tree", p.filter), items, widget.TreeOpts{Help: "editor/pane#tests", Label: "Tests", KeyMode: "tests",
						Trailing: func(c *ui.Ctx, it widget.TreeItem, _ bool) { x.rowTrailing(c, latest, it.ID) }})
					if res.Selected != "" {
						p.sel = res.Selected
					}
					if res.Activated != "" {
						x.openResult(latest[res.Activated])
					}
				})
				c.Box("edge", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
				c.Column("output", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.PadXY(12, 8), Gap: 6}, func(c *ui.Ctx) {
					c.Row("tabs", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
						name := ""
						if r := latest[p.sel]; r != nil {
							name = r.Test
						}
						c.Text("t", "OUTPUT · "+name, mono(10, theme.FgFaint))
						widget.Segmented(c, "which", []string{"this case", "whole run", "raw json"}, &p.out, widget.SegmentedOpts{Help: "editor/pane#tests",
							Label: "Output"})
					})
					c.Column("lines", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true, Clip: true}, func(c *ui.Ctx) {
						for i, l := range x.outputLines(latest[p.sel], p.out) {
							tok := theme.FgMuted
							if strings.Contains(l, "FAIL") || strings.Contains(l, "--- FAIL") {
								tok = tTestFailed
							}
							c.Text(i, l, mono(11.5, tok))
						}
					})
				})
			})
		})
}

// agentRun is the latest run the session in front's agent made, as the evidence its claims link to (TR-5).
func (x *testsExt) agentRun(c *ui.Ctx) {
	st := x.h.State()
	se, ok := st.Session(st.Focus)
	if !ok {
		return
	}
	var last core.TestRun
	for _, r := range se.Snapshot().Runs {
		if r.By == "agent" && !r.Ended.Before(last.Ended) {
			last = r
		}
	}
	if last.ID == "" {
		return
	}
	c.Text("agent", "AGENT", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10.5}, Color: c.Theme().C(theme.FgFaint)})
	evidenceChip(c, "agentrun", x.h, se.ID(), last.ID, runLabel(last), last.OK)
}

// lastRunLine is the last run: its number, trigger, scope, counts and who started it.
func (x *testsExt) lastRunLine(c *ui.Ctx) {
	runs := x.history.Runs()
	if len(runs) == 0 {
		return
	}
	r := runs[len(runs)-1]
	pass, fail, _ := r.Counts()
	th := c.Theme()
	st := ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10.5}, Color: th.C(theme.FgFaint)}
	c.Text("last", fmt.Sprintf("LAST RUN #%d · %s · by %s ·", len(runs), r.Trigger, r.By), st)
	if fail > 0 {
		st.Color = th.C(tTestFailed)
		c.Text("f", fmt.Sprintf("%d ✕", fail), st)
	}
	st.Color = th.C(tTestPassed)
	c.Text("p", fmt.Sprintf("%d ✓", pass), st)
}

// treeItems are packages, then tests, then their cases; with a filter, the failing ones (and what holds them) or the
// slowest ten.
func (x *testsExt) treeItems(latest map[string]*testrun.Result, filter int) []widget.TreeItem {
	keys := make([]string, 0, len(latest))
	for k := range latest {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	th := x.h.CurrentTheme()
	item := func(r *testrun.Result, label string) widget.TreeItem {
		glyph, tok := "○", theme.FgFaint
		switch {
		case r.Status == testrun.StatusRunning:
			glyph, tok = "◌", theme.FgMuted
		case r.Status.Failing():
			glyph, tok = "✕", tTestFailed
		case r.Status == testrun.StatusPassed && x.history.Flaky(r.Key()):
			glyph, tok = "◌", tTestFlaky
		case r.Status == testrun.StatusPassed:
			glyph, tok = "✓", tTestPassed
		}
		return widget.TreeItem{ID: r.Key(), Label: label, Glyph: glyph, GlyphTone: th.C(tok), Open: r.Status.Failing(), Help: "editor/pane#tests"}
	}
	if filter == 2 {
		var rs []*testrun.Result
		for _, r := range latest {
			rs = append(rs, r)
		}
		slices.SortFunc(rs, func(a, b *testrun.Result) int { return int(b.Duration - a.Duration) })
		var out []widget.TreeItem
		for _, r := range rs[:min(10, len(rs))] {
			out = append(out, item(r, r.Test))
		}
		return out
	}
	failingUnder := func(prefix string) bool {
		for k, r := range latest {
			if r.Status.Failing() && (k == prefix || strings.HasPrefix(k, prefix+"/")) {
				return true
			}
		}
		return false
	}
	pkgs := map[string]*widget.TreeItem{}
	var order []string
	nodes := map[string]*widget.TreeItem{}
	for _, k := range keys {
		r := latest[k]
		if filter == 1 && !failingUnder(k) {
			continue
		}
		pkg := pkgs[r.Package]
		if pkg == nil {
			pkg = &widget.TreeItem{ID: "\x01" + r.Package, Label: r.Package, Open: true, Help: "editor/pane#tests"}
			pkgs[r.Package] = pkg
			order = append(order, r.Package)
		}
		parts := strings.Split(r.Test, "/")
		it := item(r, parts[len(parts)-1])
		nodes[k] = &it
	}
	// Each test under its parent (TestX/case under TestX), the top ones under their package; children first, so a
	// parent takes its children's final state.
	slices.SortFunc(keys, func(a, b string) int { return strings.Count(b, "/") - strings.Count(a, "/") })
	for _, k := range keys {
		n := nodes[k]
		if n == nil {
			continue
		}
		r := latest[k]
		parent := ""
		if i := strings.LastIndex(r.Test, "/"); i >= 0 {
			parent = testrun.Key(r.Package, r.Test[:i])
		}
		if p := nodes[parent]; parent != "" && p != nil {
			p.Children = append([]widget.TreeItem{*n}, p.Children...)
			continue
		}
		pkgs[r.Package].Children = append([]widget.TreeItem{*n}, pkgs[r.Package].Children...)
	}
	var out []widget.TreeItem
	for _, p := range order {
		slices.SortStableFunc(pkgs[p].Children, func(a, b widget.TreeItem) int { return strings.Compare(a.ID, b.ID) })
		out = append(out, *pkgs[p])
	}
	return out
}

// rowTrailing is a test's recent runs as a sparkline, flaky when it is, and its duration.
func (x *testsExt) rowTrailing(c *ui.Ctx, latest map[string]*testrun.Result, id string) {
	th := c.Theme()
	r := latest[id]
	if r == nil {
		return
	}
	st := ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10.5}, Color: th.C(theme.FgFaint)}
	es := x.history.Entries(id)
	if x.history.Flaky(id) {
		fails := 0
		for _, e := range es {
			if e.Status.Failing() {
				fails++
			}
		}
		st.Color = th.C(tTestFlaky)
		c.Text("flaky", fmt.Sprintf("flaky · %d/%d failed", fails, len(es)), st)
		st.Color = th.C(theme.FgFaint)
	}
	if len(es) > 1 {
		var bars []widget.SparkBar
		var most float32
		for _, e := range es {
			most = max(most, float32(e.Duration))
		}
		for _, e := range es[max(0, len(es)-8):] {
			tone := th.C(tTestPassed)
			if e.Status.Failing() {
				tone = th.C(tTestFailed)
			}
			h := float32(1)
			if most > 0 {
				h = .3 + .7*float32(e.Duration)/most
			}
			bars = append(bars, widget.SparkBar{H: h, Tone: theme.WithAlpha(tone, .8)})
		}
		widget.Spark(c, "spark", bars, fmt.Sprintf("%d runs", len(es)))
	}
	c.Text("d", durationText(r.Duration), st)
}

// outputLines are what the output pane shows: the case's output, the whole run's, or its raw JSON.
func (x *testsExt) outputLines(r *testrun.Result, which int) []string {
	runs := x.history.Runs()
	var run *testrun.Run
	if len(runs) > 0 {
		run = runs[len(runs)-1]
	}
	switch which {
	case 1:
		if run == nil {
			return nil
		}
		var out []string
		for _, t := range run.Tests {
			out = append(out, t.Output...)
		}
		return append(out, run.Output...)
	case 2:
		if run == nil {
			return nil
		}
		raw := run.Raw
		if raw == "" {
			b, _ := json.MarshalIndent(run, "", "  ")
			raw = string(b)
		}
		return strings.Split(strings.TrimRight(raw, "\n"), "\n")
	}
	if r == nil {
		return []string{"Pick a test to see its output."}
	}
	var out []string
	for _, l := range r.Output {
		out = append(out, strings.TrimRight(l, "\n"))
	}
	return out
}

// openResult goes to a test's failure, or its name.
func (x *testsExt) openResult(r *testrun.Result) {
	if r == nil || r.Failure == nil || r.Failure.Loc == nil {
		return
	}
	x.h.Run(context.Background(), "editor.open", registry.Args{"path": r.Failure.Loc.File, "line": r.Failure.Loc.Line + 1})
}
