package lang

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/testrun"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// TestsView is the Tests panel.
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

// Tests runs tests: ▶ in the gutter on every test the adapters find, runs by scope, their results on
// the lines and in the Tests panel, failures inline at their assertion, watch mode and a coverage gutter.
type Tests struct {
	kit.Disposer
	HistoryFile func() string // where the runs are kept for the window's directory
	h           plugin.Host
	state       *kit.StateFile // the runs of the directory the window is on

	set     *testrun.Set
	history *testrun.History

	mu       sync.Mutex
	live     *testrun.Run
	exec     *testrun.Exec
	last     *testrun.Request
	watch    bool
	showCov  bool
	found    map[*text.Buffer]*discovery
	scanned  map[string][]testrun.Item // every test found in the workspace, by file
	scanFrom time.Time                 // when the workspace scan began
	scanGen  int                       // which scan is the window's directory's: one of a directory left is dropped
	byKey    map[string]testrun.Item   // scanned by key, made when asked for
	pkgs     map[string]bool
	work     kit.Work

	panel testsPanel
}

type discovery struct {
	version uint64
	adapter testrun.Adapter
	items   []testrun.Item
}

// testFiles are where the built-in adapters find tests: opening one loads the plugin, whose ▶ then show in it.
var testFiles = []string{"*_test.go", "test_*.py", "*_test.py", "*.test.*", "*.spec.*", "*.rs"}

// Manifest loads it on the first use of one of its commands, keys or views, or a test file opening.
func (x *Tests) Manifest() plugin.Manifest {
	lz := kit.LazyOn(append(x.commands(), registry.Command{ID: "test.run.show"}), x.keymaps())
	lz.Views = []string{TestsView, TestRunView}
	for _, f := range testFiles {
		lz.Events = append(lz.Events, "BufRead "+f)
	}
	return plugin.Manifest{ID: "tests", Lazy: lz}
}

// TestsEntries are the tests' from the start: the rail entry, without its counts until they load, and its key.
func TestsEntries() *kit.Entries {
	return &kit.Entries{ID: "tests.entries", Contribs: []registry.Contribution{testsRail(nil)},
		Keymaps: []registry.Keymap{{Mode: "global", Keys: "<D-T>", Command: "project.panel", Args: registry.Args{"name": "tests"}, Desc: "Tests"}}}
}

func testsRail(note func(string) string) uireg.RailEntry {
	return uireg.RailEntry{Name: "tests", Icon: "tests", Title: "Tests", Command: "view.toggle", Args: kit.ViewArgs(TestsView, "Tests", 0),
		Scope: uireg.ScopeProjectEntry, Order: 50, Note: note}
}

func (x *Tests) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	x.set = testrun.Builtin()
	x.Add(kit.OnDispose(testrun.Contribute(x.set, h.Contributions(testrun.ContributionKind))))
	x.found = map[*text.Buffer]*discovery{}
	x.state = kit.NewStateFile(h, kit.StateFileOpts{Name: "test runs", Path: x.HistoryFile, Load: x.loadHistory})
	x.Add(kit.OnDispose(x.state.Close))
	// The files git sees change are read again, in whichever work tree the window is on.
	x.Add(kit.OnDispose(kit.GitOf(h).WatchWindow(func(root string, paths []string) {
		for _, p := range paths {
			x.rescan(filepath.Join(root, filepath.FromSlash(p)))
		}
	})))
	x.Add(kit.OnDirChanged(h, "the tests of the directory", func(kit.DirChanged) {
		x.mu.Lock()
		x.scanGen++
		x.scanned, x.byKey, x.pkgs, x.last = nil, nil, nil, nil
		x.mu.Unlock()
		h.Do(x.scan) // off the loop: the hook only notes the move
	}))
	x.Add(h.Decorations("tests", uireg.DecorationFunc(x.decorations)))
	for _, c := range x.commands() {
		x.Add(h.Command(c))
	}
	for _, k := range x.keymaps() {
		x.Add(h.Keymap(k))
	}
	x.Add(h.Hook(registry.Hook{Event: "BufWritePost", Label: "re-run the tests the save affects", Fn: func(_ registry.Ctx, ev any) (registry.Verdict, error) {
		if w, ok := ev.(*kit.BufWritePost); ok && x.watch {
			x.watchSaved(w.File)
		}
		return registry.Verdict{}, nil
	}}))
	for _, a := range x.editorActions() {
		x.Add(h.Contribute(a))
	}
	x.Add(h.View(TestsView, testsView{x}))
	x.Add(h.Contribute(testsRail(func(string) string { return x.counts() })))
	x.scan()
	for _, d := range x.evidence() {
		x.Add(d)
	}
	x.Add(h.View("statusline.left.65-tests", uireg.ViewFunc(func(uireg.ViewCtx) ui.Node { return &ui.Func{Key: "tests", Fn: x.statusSegment} })))
	return nil
}

func (x *Tests) commands() []registry.Command {
	return slices.Concat(x.runCommands(), x.controlCommands(), x.debugCommands(), x.outputCommands())
}

// runCommands run tests by scope.
func (x *Tests) runCommands() []registry.Command {
	scope := func(trigger string) func(registry.Ctx, registry.Args) error {
		return func(_ registry.Ctx, a registry.Args) error {
			return x.runScope(trigger, a.String("file"), testrun.ModeRun)
		}
	}
	return []registry.Command{
		{ID: "test.run", Title: "Run a test: {key}, as the gutter's ▶ does; with coverage if {coverage}", Run: func(_ registry.Ctx, a registry.Args) error {
			mode := testrun.ModeRun
			if a.Bool("coverage") {
				mode = testrun.ModeCoverage
			}
			return x.runKeys(a.String("file"), []string{a.String("key")}, "gutter", mode)
		}},
		{ID: "test.run.nearest", Title: "Run the test at the cursor", Run: scope("nearest")},
		{ID: "test.run.file", Title: "Run this file's tests, or {file}'s", Run: scope("file")},
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
	}
}

// controlCommands toggle coverage and watching, stop a run and step through failures.
func (x *Tests) controlCommands() []registry.Command {
	return []registry.Command{
		{ID: "test.coverage", Title: "Run this file's tests with coverage", Run: func(registry.Ctx, registry.Args) error {
			return x.runScope("file", "", testrun.ModeCoverage)
		}},
		{ID: "test.coverage.toggle", Title: "Show or hide the coverage gutter", Run: func(registry.Ctx, registry.Args) error {
			x.showCov = !x.showCov
			x.h.DecorationsChanged("tests")
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
	}
}

// debugCommands debug and explain tests, and show the panel.
func (x *Tests) debugCommands() []registry.Command {
	return []registry.Command{
		{ID: "debug.test", Title: "Debug a test: {key} as found in {file}, as ⇧ on the gutter's ▶ does; {line} sets a breakpoint to stop at",
			Available: x.debugAvailable, Run: x.debugTest},
		{ID: "test.debug.nearest", Title: "Debug the test at the cursor", Available: func(registry.Args) bool { return x.h.Available("debug.test", nil) },
			Run: func(ctx registry.Ctx, _ registry.Args) error {
				it, ok := x.nearest()
				if !ok {
					return fmt.Errorf("no test at the cursor")
				}
				return x.h.Run(ctx, "debug.test", registry.Args{"key": it.Key(), "file": it.File})
			}},
		{ID: "test.explain", Title: "Ask to explain a failure: {key}; the answer shows under it", Available: func(registry.Args) bool { return x.h.Available(registry.ExplainCommand, nil) },
			Run: func(ctx registry.Ctx, a registry.Args) error {
				res, _ := x.result(a.String("key"))
				if res == nil || res.Failure == nil {
					return fmt.Errorf("no failure to explain")
				}
				return plugin.RunSlow(ctx, x.h, plugin.Slow{ID: "test.explain." + res.Key(), Doing: "Reading " + res.Test},
					func(context.Context) ([]byte, error) {
						if res.Failure.Loc == nil {
							return nil, nil
						}
						src, _ := os.ReadFile(res.Failure.Loc.File)
						return src, nil
					},
					func(ctx registry.Ctx, src []byte) error {
						args := failureTrouble(res, src).Args()
						args["key"] = failureKey(res.Key())
						return x.h.Run(ctx, registry.ExplainCommand, args)
					})
			}},
		{ID: "tests.open", Title: "Show the Tests panel", Run: func(ctx registry.Ctx, _ registry.Args) error {
			return x.h.Run(ctx, "view.open", testsPanelArgs())
		}},
		{ID: "tests.toggle", Title: "Show or hide the Tests panel", Run: func(ctx registry.Ctx, _ registry.Args) error {
			return x.h.Run(ctx, "view.toggle", testsPanelArgs())
		}},
	}
}

func testsPanelArgs() registry.Args {
	return registry.Args{"id": TestsView, "place": "dock", "edge": "bottom", "size": 300, "title": "Tests"}
}

func (x *Tests) keymaps() []registry.Keymap {
	var ks []registry.Keymap
	for _, k := range []registry.Keymap{
		{Keys: "<Leader>t", Desc: "+tests"},
		{Keys: "<Leader>tt", Command: "test.run.file", Desc: "Run file"},
		{Keys: "<Leader>tr", Command: "test.run.nearest", Desc: "Run nearest"},
		{Keys: "<Leader>td", Command: "test.debug.nearest", Desc: "Debug nearest"},
		{Keys: "<Leader>tT", Command: "test.run.all", Desc: "Run all"},
		{Keys: "<Leader>tl", Command: "test.run.last", Desc: "Run last"},
		{Keys: "<Leader>ts", Command: "tests.toggle", Desc: "Toggle summary"},
		{Keys: "<Leader>to", Command: "test.output", Desc: "Show output"},
		{Keys: "<Leader>tO", Command: "tests.output", Desc: "Toggle output panel"},
		{Keys: "<Leader>tS", Command: "test.cancel", Desc: "Stop"},
		{Keys: "<Leader>tw", Command: "test.watch", Desc: "Toggle watch"},
		{Keys: "<Leader>ta", Command: "test.attach", Desc: "Attach to test"},
		{Keys: "<Leader>tf", Command: "test.run.file", Desc: "Run file"},
		{Keys: "<Leader>tF", Command: "test.run.failed", Desc: "Run failed"},
		{Keys: "<Leader>tp", Command: "test.run.package", Desc: "Run package"},
		{Keys: "<Leader>tc", Command: "test.coverage", Desc: "Run with coverage"},
		{Keys: "]t", Command: "test.next_failure", Desc: "Next failure"},
	} {
		k.Mode = "n"
		ks = append(ks, k)
	}
	return append(ks, registry.Keymap{Mode: "global", Keys: "<M-r>", Command: "test.run.file", Desc: "Run this file's tests"})
}

func (x *Tests) editorActions() []registry.EditorAction {
	isTest := func(path string) bool { return x.hasTests(path) }
	return []registry.EditorAction{
		{Name: "tests.file", Icon: "▶", Label: "file", Command: "test.run.file", When: isTest, Order: 10, Help: "editor/pane#tests"},
		{Name: "tests.failed", Label: "failed", Command: "test.run.failed", When: isTest, Order: 20, Help: "editor/pane#tests"},
		{Name: "tests.watch", Icon: "●", Label: "watch", Command: "test.watch", When: isTest, On: func(registry.EditorWindow) bool { return x.watch }, Order: 30,
			Help: "editor/pane#tests"},
		{Name: "tests.coverage", Label: "coverage", Command: "test.coverage", On: func(registry.EditorWindow) bool { return x.showCov }, Order: 40,
			When: func(path string) bool { return isTest(path) || len(x.h.CoverageFor(path)) > 0 },
			Help: "editor/pane#tests"},
	}
}

func (x *Tests) Deactivate(ctx context.Context) error {
	x.mu.Lock()
	if x.exec != nil {
		x.exec.Cancel()
	}
	x.mu.Unlock()
	x.work.Stop()
	return x.Disposer.Deactivate(ctx)
}

// Wait waits for the work it has running off the loop: discovery, a scan, a run's end.
func (x *Tests) Wait() { x.work.Wait() }

// Running says a test run is in progress.
func (x *Tests) Running() bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.exec != nil
}

// loadHistory takes the runs kept for the window's directory: none for nil.
func (x *Tests) loadHistory(b []byte) {
	x.history = testrun.NewHistory(20)
	if b == nil {
		return
	}
	if hs, err := testrun.LoadHistory(bytes.NewReader(b)); err == nil {
		x.history = hs
	}
	x.h.Redraw()
}

func (x *Tests) workspace() testrun.Workspace { return testrun.Workspace{Root: editorDir(x.h)} }

// discover is the tests in a buffer, found again when it changes.
func (x *Tests) discover(path string, buf *text.Buffer) *discovery {
	snap := buf.Snapshot()
	x.mu.Lock()
	d := x.found[buf]
	x.mu.Unlock()
	if d != nil && d.version == snap.Version()+1 {
		return d
	}
	src := testrun.Source{Path: path, Text: snap}
	if doc, ok := syntaxOf(x.h, buf); ok {
		src.Syntax = doc
	}
	a, items := x.set.Discover(x.workspace(), src)
	d = &discovery{version: snap.Version() + 1, adapter: a, items: items}
	x.mu.Lock()
	x.found[buf] = d
	x.mu.Unlock()
	if kit.Within(path, editorDir(x.h)) && x.set.TestFile(path) {
		x.setFound(path, items)
	}
	return d
}

func (x *Tests) hasTests(path string) bool {
	if f, ok := openBuffer(x.h, path); ok {
		return len(x.discover(path, f.Text).items) > 0
	}
	return false
}

// result is a test's latest outcome: the run in progress's, or the last finished run's that ran it.
func (x *Tests) result(key string) (*testrun.Result, *testrun.Run) {
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

func (x *Tests) decorations(ctx uireg.DecorationCtx) []uireg.Decoration {
	var out []uireg.Decoration
	d := x.discover(ctx.Path, ctx.Buffer)
	debug := len(d.items) > 0 && x.canDebug(d.adapter, d.items[0])
	for _, it := range d.items {
		if it.Line < ctx.Top || it.Line >= ctx.Bottom {
			continue
		}
		style, note := "tests.gutter", ""
		res, run := x.result(it.Key())
		if res != nil {
			took := ""
			if res.Duration > 0 {
				took = kit.Duration(res.Duration)
			}
			switch {
			case res.Status == testrun.StatusRunning:
				note = "running…"
			case res.Status == testrun.StatusPassed && x.history.Flaky(it.Key()):
				style, note = "tests.flaky", "◌ flaky · "+took
			case res.Status == testrun.StatusPassed:
				style, note = "tests.passed", strings.TrimSpace("✓ "+took)
			case res.Status.Failing():
				style = "tests.failed"
				note = "✕ " + took
				if p, f := subResults(run, it.Key()); f > 0 && p+f > 1 {
					note = strings.TrimSuffix(fmt.Sprintf("✕ %d of %d failed · %s", f, p+f, took), " · ")
				} else if it.Kind == testrun.KindRow && res.Failure != nil && res.Failure.Got != "" {
					note = "✕ got " + res.Failure.Got
				}
			case res.Status == testrun.StatusSkipped:
				style, note = "tests.skipped", "skipped"
			}
		}
		out = append(out, testSign(it, ctx.Path, style, debug))
		if note != "" {
			out = append(out, uireg.Decoration{Kind: uireg.VirtualText, Line: it.Line, Text: note, Style: style, Priority: 20})
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
			out = append(out, uireg.Decoration{Kind: uireg.LineBg, Line: l, Edge: edge, Priority: -5})
		}
	}
	return out
}

// testSign is a test's ▶: a click runs it, ⇧ debugs it where its adapter can, and a right-click offers more.
func testSign(it testrun.Item, file, style string, debug bool) uireg.Decoration {
	args := registry.Args{"key": it.Key(), "file": file}
	d := uireg.Decoration{Kind: uireg.Sign, Line: it.Line, Text: "▶", Style: style, Command: "test.run", Args: args,
		Tooltip: "Run · right-click for more", Priority: 20, Menu: []uireg.SignItem{
			{Label: "Run test", Command: "test.run", Args: args},
			{Label: "Debug test", Command: "debug.test", Args: args},
			{Label: "Run with coverage", Command: "test.run", Args: registry.Args{"key": it.Key(), "file": file, "coverage": true}},
			{Label: "Run file", Command: "test.run.file", Args: registry.Args{"file": file}},
		}}
	if debug {
		d.Modified, d.Tooltip = map[string]string{"shift": "debug.test"}, "Run · ⇧ debug · right-click for more"
	}
	return d
}

// nearest is the test at the cursor: the innermost one starting at or above it.
func (x *Tests) nearest() (testrun.Item, bool) {
	p := window(x.h)
	if p.Text == nil {
		return testrun.Item{}, false
	}
	path := p.Path
	d := x.discover(path, p.Text)
	line := p.Cursor.Line
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

// runScope runs the tests a scope names, from file, or the current file for "".
func (x *Tests) runScope(trigger, file string, mode testrun.Mode) error {
	ws := x.workspace()
	p := window(x.h)
	path, buf := p.Path, p.Text
	if file != "" {
		f, ok := openBuffer(x.h, file)
		if !ok {
			return fmt.Errorf("%s isn't open", file)
		}
		path, buf = file, f.Text
	}
	if buf == nil {
		return fmt.Errorf("no file at the cursor")
	}
	d := x.discover(path, buf)
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
func (x *Tests) runKeys(file string, keys []string, trigger string, mode testrun.Mode) error {
	f, ok := openBuffer(x.h, file)
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
func (x *Tests) watchSaved(file string) {
	ws := x.workspace()
	x.work.Go(func(context.Context) {
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
	})
}

// start runs req, the one before it stopping first; its events show as they come.
func (x *Tests) start(req testrun.Request) error {
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
	defer x.h.DecorationsChanged("tests")
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
		x.h.DecorationsChanged("tests")
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
	x.work.Go(func(context.Context) {
		run, err := exec.Wait()
		x.h.Do(func() {
			x.mu.Lock()
			if x.exec == exec {
				x.exec, x.live = nil, nil
			}
			x.mu.Unlock()
			x.h.DecorationsChanged("tests")
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
			x.h.DecorationsChanged("tests")
		})
	})
	return nil
}

// saveHistory keeps the runs for the window's directory, off the loop.
func (x *Tests) saveHistory() {
	var b bytes.Buffer
	if err := x.history.Save(&b); err == nil {
		x.state.Write(b.Bytes())
	}
}

// nextFailure goes to the next failing test's assertion, in any file.
func (x *Tests) nextFailure() error {
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
	p := window(x.h)
	path, line := p.Path, p.Cursor.Line
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
func (x *Tests) counts() string {
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
func (x *Tests) statusSegment(c *ui.Ctx) {
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
		c.Text("l", label, kit.StatusText(c, kit.TStatusFg))
		if failed > 0 {
			c.Text("f", fmt.Sprintf("%d ✕", failed), kit.StatusText(c, tTestFailed))
		}
		c.Text("p", fmt.Sprintf("%d ✓", passed), kit.StatusText(c, tTestPassed))
	})
}
