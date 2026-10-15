package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/testrun"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/vim"
)

// TestsView is the Tests panel (ide.md, "Running tests").
const TestsView = "tests"

var (
	tTestIdle      = theme.Declare("tests.gutter", "accent")
	tTestPassed    = theme.Declare("tests.passed", "ok")
	tTestFailed    = theme.Declare("tests.failed", "error")
	tTestFlaky     = theme.Declare("tests.flaky", "warn")
	tTestSkipped   = theme.Declare("tests.skipped", "fg.faint")
	tTestCovered   = theme.Declare("tests.coverage.covered", "alpha(ok, 70%)")
	tTestUncovered = theme.Declare("tests.coverage.uncovered", "alpha(error, 70%)")
)

// testsExt runs tests (TR-1..TR-4): ▶ in the gutter on every test the adapters find, runs by scope, their results on
// the lines and in the Tests panel, failures inline at their assertion, watch mode and a coverage gutter.
type testsExt struct {
	disposer
	sh *shell
	h  ext.Host

	set     *testrun.Set
	history *testrun.History

	mu      sync.Mutex
	live    *testrun.Run
	exec    *testrun.Exec
	last    *testrun.Request
	watch   bool
	showCov bool
	found   map[*text.Buffer]*discovery
	busy    sync.WaitGroup

	panel testsPanel
}

type discovery struct {
	version uint64
	adapter testrun.Adapter
	items   []testrun.Item
}

func (x *testsExt) Manifest() ext.Manifest { return ext.Manifest{ID: "tests"} }

func (x *testsExt) Activate(_ context.Context, h ext.Host) error {
	x.h = h
	x.sh.tests = x
	x.set = testrun.Builtin()
	x.add(disposeFn(testrun.Contribute(x.set, h.Contributions(testrun.ContributionKind))))
	x.history = testrun.NewHistory(20)
	x.found = map[*text.Buffer]*discovery{}
	if f, err := os.Open(x.historyPath()); err == nil {
		if hs, err := testrun.LoadHistory(f); err == nil {
			x.history = hs
		}
		f.Close()
	}
	x.add(h.Decorations("tests", registry.DecorationFunc(x.decorations)))
	scope := func(trigger string) func(registry.Ctx, registry.Args) error {
		return func(registry.Ctx, registry.Args) error { return x.runScope(trigger, testrun.ModeRun) }
	}
	for _, c := range []registry.Command{
		{ID: "test.run", Title: "Run a test: {key}, as the gutter's ▶ does", Run: func(_ registry.Ctx, a registry.Args) error {
			return x.runKeys(a.String("file"), []string{a.String("key")}, "gutter", testrun.ModeRun)
		}},
		{ID: "test.run.nearest", Title: "Run the test at the cursor", Run: scope("nearest")},
		{ID: "test.run.file", Title: "Run this file's tests", Run: scope("file")},
		{ID: "test.run.package", Title: "Run this package's tests", Run: scope("package")},
		{ID: "test.run.all", Title: "Run every test", Run: scope("all")},
		{ID: "test.run.failed", Title: "Run the tests that failed last time", Run: scope("failed")},
		{ID: "test.run.last", Title: "Run the last run again", Run: func(registry.Ctx, registry.Args) error {
			x.mu.Lock()
			last := x.last
			x.mu.Unlock()
			if last == nil {
				return fmt.Errorf("no run yet")
			}
			req := *last
			req.ID, req.Trigger, req.Change = "", "last", testrun.Fingerprint(req.Workspace.Root)
			return x.start(req)
		}},
		{ID: "test.coverage", Title: "Run this file's tests with coverage", Run: func(registry.Ctx, registry.Args) error {
			return x.runScope("file", testrun.ModeCoverage)
		}},
		{ID: "test.coverage.toggle", Title: "Show or hide the coverage gutter", Run: func(registry.Ctx, registry.Args) error {
			x.showCov = !x.showCov
			return nil
		}},
		{ID: "test.watch", Title: "Re-run the tests a save affects (toggle)", Run: func(registry.Ctx, registry.Args) error {
			x.watch = !x.watch
			return nil
		}},
		{ID: "test.cancel", Title: "Stop the run", Run: func(registry.Ctx, registry.Args) error {
			x.mu.Lock()
			defer x.mu.Unlock()
			if x.exec != nil {
				x.exec.Cancel()
			}
			return nil
		}},
		{ID: "test.next_failure", Title: "Go to the next failure", Run: func(registry.Ctx, registry.Args) error { return x.nextFailure() }},
		{ID: "test.debug.nearest", Title: "Debug the test at the cursor", Available: func(registry.Args) bool { return h.Available("debug.test", nil) },
			Run: func(ctx registry.Ctx, _ registry.Args) error {
				it, ok := x.nearest()
				if !ok {
					return fmt.Errorf("no test at the cursor")
				}
				return h.Run(ctx, "debug.test", registry.Args{"key": it.Key(), "file": it.File})
			}},
		{ID: "test.explain", Title: "Ask to explain a failure: {key}; the answer shows under it", Available: func(registry.Args) bool { return h.Available(registry.ExplainCommand, nil) },
			Run: func(ctx registry.Ctx, a registry.Args) error {
				res, _ := x.result(a.String("key"))
				if res == nil || res.Failure == nil {
					return fmt.Errorf("no failure to explain")
				}
				args := x.failureTrouble(res).Args()
				args["key"] = failureKey(res.Key())
				return h.Run(ctx, registry.ExplainCommand, args)
			}},
		{ID: "tests.open", Title: "Show the Tests panel", Run: func(ctx registry.Ctx, _ registry.Args) error {
			return h.Run(ctx, "view.open", registry.Args{"id": TestsView, "place": "dock", "edge": "bottom", "size": 300, "title": "Tests"})
		}},
	} {
		x.add(h.Command(c))
	}
	for _, k := range []registry.Keymap{
		{Keys: "<Leader>t", Desc: "+tests"},
		{Keys: "<Leader>tt", Command: "test.run.nearest", Desc: "Run nearest"},
		{Keys: "<Leader>tf", Command: "test.run.file", Desc: "Run file"},
		{Keys: "<Leader>tl", Command: "test.run.last", Desc: "Run last"},
		{Keys: "<Leader>tF", Command: "test.run.failed", Desc: "Run failed"},
		{Keys: "<Leader>tp", Command: "test.run.package", Desc: "Run package"},
		{Keys: "<Leader>ta", Command: "test.run.all", Desc: "Run all"},
		{Keys: "<Leader>td", Command: "test.debug.nearest", Desc: "Debug nearest"},
		{Keys: "<Leader>tc", Command: "test.coverage", Desc: "Run with coverage"},
		{Keys: "<Leader>tw", Command: "test.watch", Desc: "Watch"},
		{Keys: "<Leader>to", Command: "tests.open", Desc: "Tests panel"},
		{Keys: "]t", Command: "test.next_failure", Desc: "Next failure"},
	} {
		k.Mode = "n"
		x.add(h.Keymap(k))
	}
	x.add(h.Keymap(registry.Keymap{Mode: "global", Keys: "<M-r>", Command: "test.run.file", Desc: "Run this file's tests"}))
	x.add(h.Hook(registry.Hook{Event: "BufWritePost", Label: "re-run the tests the save affects", Fn: func(_ registry.Ctx, ev any) (registry.Verdict, error) {
		if w, ok := ev.(*BufWritePost); ok && x.watch {
			x.watchSaved(w.File)
		}
		return registry.Verdict{}, nil
	}}))
	isTest := func(path string) bool { return x.hasTests(path) }
	for _, a := range []registry.EditorAction{
		{Name: "tests.file", Icon: "▶", Label: "file", Command: "test.run.file", When: isTest, Order: 10, Help: "editor/pane#tests"},
		{Name: "tests.failed", Label: "failed", Command: "test.run.failed", When: isTest, Order: 20, Help: "editor/pane#tests"},
		{Name: "tests.watch", Icon: "●", Label: "watch", Command: "test.watch", When: isTest, On: func(registry.EditorWindow) bool { return x.watch }, Order: 30,
			Help: "editor/pane#tests"},
		{Name: "tests.coverage", Label: "coverage", Command: "test.coverage", On: func(registry.EditorWindow) bool { return x.showCov }, Order: 40,
			When: func(path string) bool { return isTest(path) || len(x.h.CoverageFor(path)) > 0 },
			Help: "editor/pane#tests"},
	} {
		x.add(h.Contribute(a))
	}
	x.add(h.View(TestsView, testsView{x}))
	for _, d := range x.evidence() {
		x.add(d)
	}
	x.add(h.View("statusline.left.65-tests", registry.ViewFunc(func(registry.ViewCtx) ui.Node { return &ui.Func{Key: "tests", Fn: x.statusSegment} })))
	return nil
}

func (x *testsExt) Deactivate(ctx context.Context) error {
	x.mu.Lock()
	if x.exec != nil {
		x.exec.Cancel()
	}
	x.mu.Unlock()
	x.busy.Wait()
	return x.disposer.Deactivate(ctx)
}

func (x *testsExt) wait() { x.busy.Wait() }

func (x *testsExt) running() bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.exec != nil
}

func (x *testsExt) historyPath() string { return x.sh.dirState("tests") }

func (x *testsExt) workspace() testrun.Workspace { return testrun.Workspace{Root: x.sh.editor.dir} }

// discover is the tests in a buffer, found again when it changes.
func (x *testsExt) discover(path string, buf *text.Buffer) *discovery {
	snap := buf.Snapshot()
	x.mu.Lock()
	d := x.found[buf]
	x.mu.Unlock()
	if d != nil && d.version == snap.Version()+1 {
		return d
	}
	src := testrun.Source{Path: path, Text: snap}
	if hl, ok := x.sh.editor.docs[buf]; ok && hl.doc != nil {
		src.Syntax = hl.doc
	}
	a, items := x.set.Discover(x.workspace(), src)
	d = &discovery{version: snap.Version() + 1, adapter: a, items: items}
	x.mu.Lock()
	x.found[buf] = d
	x.mu.Unlock()
	return d
}

func (x *testsExt) hasTests(path string) bool {
	if f, ok := x.sh.editor.openBuffer(path); ok {
		return len(x.discover(path, f.Text).items) > 0
	}
	return false
}

// result is a test's latest outcome: the run in progress's, or the last finished run's that ran it.
func (x *testsExt) result(key string) (*testrun.Result, *testrun.Run) {
	x.mu.Lock()
	live := x.live
	x.mu.Unlock()
	if live != nil {
		x.mu.Lock()
		r, ok := live.Result(key)
		x.mu.Unlock()
		if ok {
			return r, live
		}
	}
	runs := x.history.Runs()
	for i := len(runs) - 1; i >= 0; i-- {
		if r, ok := runs[i].Result(key); ok {
			return r, runs[i]
		}
	}
	return nil, nil
}

// subResults are the results of a test's subtests, from the same run as its own.
func subResults(run *testrun.Run, key string) (passed, failed int) {
	if run == nil {
		return
	}
	for _, r := range run.Tests {
		if strings.HasPrefix(r.Key(), key+"/") {
			switch {
			case r.Status == testrun.StatusPassed:
				passed++
			case r.Status.Failing():
				failed++
			}
		}
	}
	return
}

func durationText(d time.Duration) string {
	switch {
	case d == 0:
		return ""
	case d < time.Millisecond:
		return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000)
	case d < time.Second:
		return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000)
	}
	return fmt.Sprintf("%.2fs", d.Seconds())
}

func (x *testsExt) decorations(ctx registry.DecorationCtx) []registry.Decoration {
	var out []registry.Decoration
	d := x.discover(ctx.Path, ctx.Buffer)
	for _, it := range d.items {
		if it.Line < ctx.Top || it.Line >= ctx.Bottom {
			continue
		}
		style, note := "tests.gutter", ""
		res, run := x.result(it.Key())
		if res != nil {
			switch {
			case res.Status == testrun.StatusRunning:
				note = "running…"
			case res.Status == testrun.StatusPassed && x.history.Flaky(it.Key()):
				style, note = "tests.flaky", "◌ flaky · "+durationText(res.Duration)
			case res.Status == testrun.StatusPassed:
				style, note = "tests.passed", strings.TrimSpace("✓ "+durationText(res.Duration))
			case res.Status.Failing():
				style = "tests.failed"
				note = "✕ " + durationText(res.Duration)
				if p, f := subResults(run, it.Key()); f > 0 && p+f > 1 {
					note = strings.TrimSuffix(fmt.Sprintf("✕ %d of %d failed · %s", f, p+f, durationText(res.Duration)), " · ")
				} else if it.Kind == testrun.KindRow && res.Failure != nil && res.Failure.Got != "" {
					note = "✕ got " + res.Failure.Got
				}
			case res.Status == testrun.StatusSkipped:
				style, note = "tests.skipped", "skipped"
			}
		}
		out = append(out, registry.Decoration{Kind: registry.Sign, Line: it.Line, Text: "▶", Style: style, Command: "test.run",
			Args: registry.Args{"key": it.Key(), "file": ctx.Path}, Tooltip: "Run " + it.Name, Priority: 20})
		if note != "" {
			out = append(out, registry.Decoration{Kind: registry.VirtualText, Line: it.Line, Text: note, Style: style, Priority: 20})
		}
	}
	out = append(out, x.failureBlocks(ctx, d)...)
	if x.showCov {
		for l, hits := range x.h.CoverageFor(ctx.Path) {
			if l < ctx.Top || l >= ctx.Bottom {
				continue
			}
			edge := "tests.coverage.covered"
			if hits == 0 {
				edge = "tests.coverage.uncovered"
			}
			out = append(out, registry.Decoration{Kind: registry.LineBg, Line: l, Edge: edge, Priority: -5})
		}
	}
	return out
}

// nearest is the test at the cursor: the innermost one starting at or above it.
func (x *testsExt) nearest() (testrun.Item, bool) {
	p := x.sh.editor.current()
	path := x.panePath(p)
	d := x.discover(path, p.Buffer())
	line := p.Cursor().Line
	best, ok := testrun.Item{}, false
	for _, it := range d.items {
		if it.Line > line {
			continue
		}
		if it.Kind == testrun.KindRow && it.Line != line {
			continue
		}
		if !ok || it.Line >= best.Line {
			best, ok = it, true
		}
	}
	return best, ok
}

func (x *testsExt) panePath(p vim.Pane) string {
	path := p.Name()
	if path != "" && !filepath.IsAbs(path) {
		path = filepath.Join(x.sh.editor.dir, path)
	}
	return path
}

// runScope runs the tests a scope names, from the current file.
func (x *testsExt) runScope(trigger string, mode testrun.Mode) error {
	ws := x.workspace()
	p := x.sh.editor.current()
	path := x.panePath(p)
	d := x.discover(path, p.Buffer())
	a := d.adapter
	if a == nil {
		if as := x.set.Detect(ws); len(as) > 0 {
			a = as[0]
		}
	}
	if a == nil {
		return fmt.Errorf("no test runner for this project")
	}
	var items []testrun.Item
	switch trigger {
	case "nearest":
		it, ok := x.nearest()
		if !ok {
			return fmt.Errorf("no test at the cursor")
		}
		items = []testrun.Item{it}
	case "file":
		for _, it := range d.items {
			if it.Kind == testrun.KindTest || it.Kind == testrun.KindSuite && len(it.Path) == 1 {
				items = append(items, it)
			}
		}
		if len(items) == 0 {
			return fmt.Errorf("no tests in this file")
		}
	case "package":
		if len(d.items) == 0 {
			return fmt.Errorf("no tests in this file")
		}
		items = []testrun.Item{{Package: d.items[0].Package, Kind: testrun.KindPackage}}
	case "failed":
		runs := x.history.Runs()
		if len(runs) == 0 {
			return fmt.Errorf("no run yet")
		}
		for _, r := range runs[len(runs)-1].Failed() {
			if r.Test != "" {
				items = append(items, testrun.Item{Package: r.Package, Name: r.Test, Kind: testrun.KindTest})
			}
		}
		if len(items) == 0 {
			return fmt.Errorf("nothing failed last time")
		}
	}
	return x.start(testrun.Request{Adapter: a, Workspace: ws, Items: items, Mode: mode, Trigger: trigger, By: testrun.ByUser,
		Change: testrun.Fingerprint(ws.Root)})
}

// runKeys runs tests by key, as discovered in file.
func (x *testsExt) runKeys(file string, keys []string, trigger string, mode testrun.Mode) error {
	f, ok := x.sh.editor.openBuffer(file)
	if !ok {
		return fmt.Errorf("%s isn't open", file)
	}
	d := x.discover(file, f.Text)
	var items []testrun.Item
	for _, it := range d.items {
		if slices.Contains(keys, it.Key()) {
			items = append(items, it)
		}
	}
	if d.adapter == nil || len(items) == 0 {
		return fmt.Errorf("no such test")
	}
	ws := x.workspace()
	return x.start(testrun.Request{Adapter: d.adapter, Workspace: ws, Items: items, Mode: mode, Trigger: trigger, By: testrun.ByUser,
		Change: testrun.Fingerprint(ws.Root)})
}

// watchSaved runs the tests a saved file affects.
func (x *testsExt) watchSaved(file string) {
	ws := x.workspace()
	x.busy.Add(1)
	go func() {
		defer x.busy.Done()
		affected, err := x.set.Affected(ws, file)
		x.h.Do(func() {
			if err != nil {
				x.h.Notify(core.Notice{ID: "tests.watch", Severity: core.SevWarning, Text: err.Error()})
				return
			}
			for a, items := range affected {
				if err := x.start(testrun.Request{Adapter: a, Workspace: ws, Items: items, Mode: testrun.ModeWatch, Trigger: "watch",
					By: testrun.ByWatch, Change: testrun.Fingerprint(ws.Root)}); err != nil {
					x.h.Notify(core.Notice{ID: "tests.watch", Severity: core.SevWarning, Text: err.Error()})
				}
			}
		})
	}()
}

// start runs req, the one before it stopping first; its events show as they come.
func (x *testsExt) start(req testrun.Request) error {
	x.mu.Lock()
	if x.exec != nil {
		x.exec.Cancel()
	}
	if req.ID == "" {
		req.ID = testrun.NewRunID()
	}
	live := &testrun.Run{ID: req.ID, Trigger: req.Trigger, By: req.By, Change: req.Change, Mode: req.Mode.String(), Adapter: req.Adapter.Name(),
		Start: time.Now()}
	for _, it := range req.Items {
		live.Scope = append(live.Scope, it.Key())
	}
	x.live = live
	if req.By == testrun.ByUser {
		r := req
		x.last = &r
	}
	x.mu.Unlock()
	exec, err := testrun.Start(context.Background(), req, func(e testrun.Event) {
		x.mu.Lock()
		if x.live == live {
			live.Apply(e)
		}
		x.mu.Unlock()
		x.h.Redraw()
	})
	if err != nil {
		x.mu.Lock()
		x.live = nil
		x.mu.Unlock()
		return err
	}
	x.mu.Lock()
	x.exec = exec
	x.mu.Unlock()
	x.busy.Add(1)
	go func() {
		defer x.busy.Done()
		run, err := exec.Wait()
		x.h.Do(func() {
			x.mu.Lock()
			if x.exec == exec {
				x.exec, x.live = nil, nil
			}
			x.mu.Unlock()
			if run == nil {
				if err != nil {
					x.h.Notify(core.Notice{ID: "tests.run", Severity: core.SevError, Text: err.Error()})
				}
				return
			}
			if run.Status == testrun.StatusCanceled {
				return
			}
			x.history.Add(run)
			if req.Mode == testrun.ModeCoverage && run.Coverage != nil {
				x.h.ClearCoverage("tests")
				for file, hits := range run.Coverage.Files {
					x.h.SetCoverage("tests", file, hits)
				}
				x.showCov = true
			}
			x.saveHistory()
			x.h.Redraw()
		})
	}()
	return nil
}

func (x *testsExt) saveHistory() {
	if x.sh.cfg.StateDir == "" {
		return
	}
	os.MkdirAll(x.sh.cfg.StateDir, 0o755)
	f, err := os.Create(x.historyPath())
	if err != nil {
		return
	}
	defer f.Close()
	x.history.Save(f)
}

// nextFailure goes to the next failing test's assertion, in any file.
func (x *testsExt) nextFailure() error {
	runs := x.history.Runs()
	if len(runs) == 0 {
		return fmt.Errorf("no run yet")
	}
	var locs []*testrun.Loc
	for _, r := range runs[len(runs)-1].Failed() {
		if r.Failure != nil && r.Failure.Loc != nil {
			locs = append(locs, r.Failure.Loc)
		}
	}
	if len(locs) == 0 {
		return fmt.Errorf("no failure to go to")
	}
	p := x.sh.editor.current()
	path, line := x.panePath(p), p.Cursor().Line
	next := locs[0]
	for _, l := range locs {
		if l.File == path && l.Line > line {
			next = l
			break
		}
	}
	ctx := context.Background()
	return x.h.Run(ctx, "editor.open", registry.Args{"path": next.File, "line": next.Line + 1})
}

// counts is the last run's counts as the sidebar's Tests entry notes them: "48 ✓", "2 ✕"; "" before a run.
func (x *testsExt) counts() string {
	if x == nil {
		return ""
	}
	runs := x.history.Runs()
	if len(runs) == 0 {
		return ""
	}
	passed, failed, _ := runs[len(runs)-1].Counts()
	if failed > 0 {
		return fmt.Sprintf("%d ✕", failed)
	}
	return fmt.Sprintf("%d ✓", passed)
}

// statusSegment is TESTS with the last run's counts, or the one running.
func (x *testsExt) statusSegment(c *ui.Ctx) {
	x.mu.Lock()
	live := x.live
	x.mu.Unlock()
	var passed, failed int
	label := "TESTS"
	switch {
	case live != nil:
		x.mu.Lock()
		passed, failed, _ = live.Counts()
		x.mu.Unlock()
		label = "TESTS …"
	default:
		runs := x.history.Runs()
		if len(runs) == 0 {
			return
		}
		passed, failed, _ = runs[len(runs)-1].Counts()
	}
	c.Row("tests", ui.Style{H: ui.Flex(1), Gap: 6, Cross: ui.Center, Help: "editor/pane#tests", Role: ui.RoleStatus, Label: "Tests"}, func(c *ui.Ctx) {
		c.Text("l", label, statusText(c, tStatusFg))
		if failed > 0 {
			c.Text("f", fmt.Sprintf("%d ✕", failed), statusText(c, tTestFailed))
		}
		c.Text("p", fmt.Sprintf("%d ✓", passed), statusText(c, tTestPassed))
	})
}
