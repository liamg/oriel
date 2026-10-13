package testrun

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/syntax"
)

// Go runs `go test -json` and reads test2json's events.
type Go struct {
	// Binary is the go command; "go" when empty.
	Binary string
	// Flags go after "go test -json": -race, -timeout=…, -tags=….
	Flags []string

	mods  sync.Map // directory → goModule
	graph goGraphCache
}

func (*Go) Name() string { return "go" }

func (*Go) Detect(ws Workspace) bool { return ws.Exists("go.mod") || ws.Exists("go.work") }

func (g *Go) bin() string {
	if g.Binary != "" {
		return g.Binary
	}
	return "go"
}

type goModule struct {
	root, path string
	mod        time.Time
}

// module is the module a directory is in: its root and path, read from go.mod.
func (g *Go) module(dir string) (goModule, bool) {
	root, _, ok := findUp(dir, "go.mod")
	if !ok {
		return goModule{}, false
	}
	gomod := filepath.Join(root, "go.mod")
	st, err := os.Stat(gomod)
	if err != nil {
		return goModule{}, false
	}
	if v, ok := g.mods.Load(root); ok && v.(goModule).mod.Equal(st.ModTime()) {
		return v.(goModule), true
	}
	b, err := os.ReadFile(gomod)
	if err != nil {
		return goModule{}, false
	}
	m := goModule{root: root, mod: st.ModTime()}
	for _, l := range strings.Split(string(b), "\n") {
		if f := strings.Fields(l); len(f) >= 2 && f[0] == "module" {
			m.path = strings.Trim(f[1], `"`)
			break
		}
	}
	g.mods.Store(root, m)
	return m, true
}

// importPath is the package a directory holds, worked out from its module.
func (g *Go) importPath(dir string) string {
	m, ok := g.module(dir)
	if !ok {
		return ""
	}
	rel := relTo(m.root, dir)
	if rel == "." {
		return m.path
	}
	return m.path + "/" + rel
}

func (g *Go) Discover(src Source) []Item {
	if !strings.HasSuffix(src.Path, "_test.go") {
		return nil
	}
	ms := testMatches(src)
	pkg := g.importPath(filepath.Dir(src.Path))
	funcs := byCapture(ms, "test.func")
	runs := byCapture(ms, "test.run")
	tableRuns := byCapture(ms, "run.table")
	loops := byCapture(ms, "loop")
	tables := byCapture(ms, "table.literal")
	rows := byCapture(ms, "row")
	elems := byCapture(ms, "row.element")
	fields := byCapture(ms, "struct.field")
	keyed := byCapture(ms, "field.value")

	// Each test and static subtest's full name, by the range of its node; a dynamic t.Run's parent name too.
	type found struct {
		n    node
		name string
	}
	var named []found
	nameOf := func(n node) (string, bool) {
		for _, f := range named {
			if f.n.Range == n.Range {
				return f.name, true
			}
		}
		return "", false
	}
	var items []Item
	namer := goNamer{}
	add := func(n node, nameAt syntax.Capture, name string, path []string, kind Kind) {
		named = append(named, found{n, name})
		items = append(items, Item{Package: pkg, Name: name, Path: path, File: src.Path, Line: nameAt.Start.Line, Kind: kind})
	}
	pathOf := func(name string) []string {
		for _, it := range items {
			if it.Name == name {
				return it.Path
			}
		}
		return nil
	}
	for _, f := range funcs {
		c, _ := f.m.Get("test.name")
		add(f, c, c.Text, []string{c.Text}, KindTest)
	}
	// Subtests and table runs in source order, so parents are named before their children.
	type call struct {
		n     node
		table bool
	}
	var calls []call
	for _, r := range runs {
		calls = append(calls, call{r, false})
	}
	for _, r := range tableRuns {
		calls = append(calls, call{r, true})
	}
	sort.SliceStable(calls, func(i, j int) bool { return calls[i].n.Range.Start < calls[j].n.Range.Start })
	parents := append(append(append([]node(nil), funcs...), runs...), tableRuns...) // under a table run, a name isn't known
	for _, cl := range calls {
		parent, ok := innermost(parents, cl.n)
		if !ok {
			continue
		}
		pname, ok := nameOf(parent)
		if !ok {
			continue
		}
		if !cl.table {
			c, _ := cl.n.m.Get("test.name")
			sub, err := strconv.Unquote(c.Text)
			if err != nil {
				continue
			}
			name := namer.name(pname, sub)
			add(cl.n, c, name, append(append([]string(nil), pathOf(pname)...), sub), KindSubtest)
			continue
		}
		for _, row := range goTableRows(cl.n, loops, tables, rows, elems, fields, keyed) {
			name := namer.name(pname, row.name)
			items = append(items, Item{Package: pkg, Name: name, Path: append(append([]string(nil), pathOf(pname)...), row.name), File: src.Path, Line: row.line, Kind: KindRow})
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Line < items[j].Line })
	return items
}

type goRow struct {
	name string
	line int
}

// goTableRows are the rows of the table a t.Run(tc.field, …) or t.Run(key, …) call ranges over, with the names
// it gives them, for rows whose name is a string literal.
func goTableRows(run node, loops, tables, rows, elems, fields, keyed []node) []goRow {
	v, _ := run.m.Get("run.var")
	field, byField := run.m.Get("run.field")
	var loop node
	found := false
	for _, l := range loops {
		key, _ := l.m.Get("loop.key")
		val, hasVal := l.m.Get("loop.value")
		if l.contains(run) && (!found || loop.contains(l)) && (byField && hasVal && val.Text == v.Text || !byField && key.Text == v.Text) {
			loop, found = l, true
		}
	}
	if !found {
		return nil
	}
	source, _ := loop.m.Get("loop.source")
	var lit node
	ok := false
	for _, t := range tables {
		name, hasName := t.m.Get("table.name")
		if t.Range == source.Range || hasName && name.Text == source.Text && t.Range.End <= loop.Range.Start {
			lit, ok = t, true // the last such table before the loop wins
		}
	}
	if !ok {
		// Ranging straight over a literal: the source is the table.
		for _, r := range rows {
			if source.Range.Start <= r.Range.Start && r.Range.End <= source.Range.End {
				lit, ok = node{Capture: source}, true
				break
			}
		}
		if !ok {
			return nil
		}
	}
	var mine []node
	for _, r := range rows {
		if lit.Range.Start <= r.Range.Start && r.Range.End <= lit.Range.End {
			if len(mine) > 0 && mine[len(mine)-1].contains(r) {
				continue
			}
			mine = append(mine, r)
		}
	}
	var out []goRow
	for _, r := range mine {
		if !byField {
			if k, ok := r.m.Get("row.key"); ok {
				if s, err := strconv.Unquote(k.Text); err == nil {
					out = append(out, goRow{s, k.Start.Line})
				}
			}
			continue
		}
		if name, line, ok := goRowField(r, field.Text, lit, mine, elems, fields, keyed); ok {
			out = append(out, goRow{name, line})
		}
	}
	return out
}

// goRowField is the string literal a row gives a field: keyed (name: "x"), or by position against the struct's fields.
func goRowField(row node, field string, lit node, rows, elems, fields, keyed []node) (string, int, bool) {
	inRow := func(n node) bool {
		if !row.contains(n) {
			return false
		}
		for _, r := range rows {
			if row.contains(r) && r.contains(n) {
				return false
			}
		}
		return true
	}
	for _, kv := range keyed {
		k, _ := kv.m.Get("field.key")
		if k.Text == field && inRow(kv) {
			if s, err := strconv.Unquote(kv.Text); err == nil {
				return s, kv.Start.Line, true
			}
			return "", 0, false
		}
	}
	var mine []node
	for _, e := range elems {
		if inRow(e) && (len(mine) == 0 || !mine[len(mine)-1].contains(e)) {
			mine = append(mine, e)
		}
	}
	var names []string
	for _, f := range fields {
		if lit.contains(f) && (len(rows) == 0 || f.Range.End <= rows[0].Range.Start) {
			names = append(names, f.Text)
		}
	}
	for i, n := range names {
		if n == field && i < len(mine) {
			if s, err := strconv.Unquote(mine[i].Text); err == nil {
				return s, mine[i].Start.Line, true
			}
		}
	}
	return "", 0, false
}

// Command runs go test for items' packages, with -run selecting them unless a whole package is asked for.
func (g *Go) Command(ws Workspace, items []Item, mode Mode) (Cmd, error) {
	var pkgs, names []string
	seen := map[string]bool{}
	whole := false
	for _, it := range items {
		dir := it.File
		if it.Kind != KindPackage {
			dir = filepath.Dir(it.File)
		} else {
			whole = true
		}
		p := "./" + relTo(ws.Root, dir)
		if p == "./." {
			p = "."
		}
		if !seen[p] {
			seen[p] = true
			pkgs = append(pkgs, p)
		}
		if it.Name != "" {
			names = append(names, it.Name)
		}
	}
	if len(pkgs) == 0 {
		pkgs = []string{"./..."}
		whole = true
	}
	run := ""
	if !whole {
		run = goRunPattern(names)
	}
	cmd := Cmd{Dir: ws.Root, Env: ws.Env}
	switch mode {
	case ModeRun, ModeWatch, ModeCoverage:
		cmd.Args = append([]string{g.bin(), "test", "-json"}, g.Flags...)
		if run != "" {
			cmd.Args = append(cmd.Args, "-run", run)
		}
		if mode == ModeCoverage {
			cmd.CoverFile = "cover.out"
			cmd.Args = append(cmd.Args, "-coverprofile="+RunDir+"/cover.out")
		}
		cmd.Args = append(cmd.Args, pkgs...)
	case ModeDebug:
		if len(pkgs) != 1 {
			return Cmd{}, ErrUnsupported
		}
		cmd.Args = []string{"dlv", "test", pkgs[0], "--", "-test.v"}
		if run != "" {
			cmd.Args = append(cmd.Args, "-test.run", run)
		}
	}
	return cmd, nil
}

// test2json is one event of `go test -json` (go doc test2json), and go build -json's build output.
type test2json struct {
	Time        time.Time
	Action      string
	Package     string
	Test        string
	Elapsed     float64
	Output      string
	FailedBuild string
	ImportPath  string
}

type goPkgState struct {
	running  map[string]bool
	order    []string
	lines    map[string][]string // each test's output lines, for its failure
	partial  map[string]string   // output not yet ended by a newline
	timedOut bool
	panicked bool
}

// Parse reads test2json events. Output can come in pieces of a line; they're joined before they're emitted.
func (g *Go) Parse(r io.Reader, emit func(Event)) error {
	pkgs := map[string]*goPkgState{}
	build := map[string][]string{} // build output by package ID
	state := func(p string) *goPkgState {
		s := pkgs[p]
		if s == nil {
			s = &goPkgState{running: map[string]bool{}, lines: map[string][]string{}, partial: map[string]string{}}
			pkgs[p] = s
		}
		return s
	}
	line := func(e test2json, s *goPkgState, l string) {
		s.lines[e.Test] = append(s.lines[e.Test], l)
		if strings.HasPrefix(l, "panic: test timed out after ") {
			s.timedOut = true
		} else if strings.HasPrefix(l, "panic: ") {
			s.panicked = true
		}
		emit(Event{Kind: Output, Package: e.Package, Test: e.Test, Time: e.Time, Line: l})
	}
	flush := func(e test2json, s *goPkgState) {
		if p := s.partial[e.Test]; p != "" {
			delete(s.partial, e.Test)
			line(e, s, p)
		}
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var e test2json
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			emit(Event{Kind: Output, Line: sc.Text()})
			continue
		}
		elapsed := time.Duration(e.Elapsed * float64(time.Second))
		switch e.Action {
		case "build-output":
			id := e.ImportPath
			build[id] = append(build[id], strings.TrimSuffix(e.Output, "\n"))
			emit(Event{Kind: Output, Package: goPackageOf(id), Line: strings.TrimSuffix(e.Output, "\n")})
		case "start":
			state(e.Package)
			emit(Event{Kind: Started, Package: e.Package, Time: e.Time})
		case "run":
			s := state(e.Package)
			s.running[e.Test] = true
			s.order = append(s.order, e.Test)
			emit(Event{Kind: Started, Package: e.Package, Test: e.Test, Time: e.Time})
		case "output":
			s := state(e.Package)
			text := s.partial[e.Test] + e.Output
			for {
				i := strings.IndexByte(text, '\n')
				if i < 0 {
					break
				}
				line(e, s, text[:i])
				text = text[i+1:]
			}
			s.partial[e.Test] = text
		case "pass", "fail", "skip":
			s := state(e.Package)
			flush(e, s)
			if e.Test == "" {
				for _, t := range s.order {
					if s.partial[t] != "" {
						flush(test2json{Package: e.Package, Test: t, Time: e.Time}, s)
					}
				}
				g.finishPackage(e, s, build, elapsed, emit)
				delete(pkgs, e.Package)
				continue
			}
			delete(s.running, e.Test)
			ev := Event{Package: e.Package, Test: e.Test, Time: e.Time, Elapsed: elapsed}
			switch e.Action {
			case "pass":
				ev.Kind = Passed
			case "skip":
				ev.Kind = Skipped
				_, ev.Message = goLoc(s.lines[e.Test])
			case "fail":
				ev.Kind = Failed
				ev.Failure = goFailure(e.Test, s.lines[e.Test])
			}
			emit(ev)
		}
	}
	return sc.Err()
}

// finishPackage ends a package: tests still running when it failed timed out or died with it, and a build
// failure is reported as one.
func (g *Go) finishPackage(e test2json, s *goPkgState, build map[string][]string, elapsed time.Duration, emit func(Event)) {
	if e.FailedBuild != "" {
		out := build[e.FailedBuild]
		f := &Failure{Reason: ReasonBuild, Message: strings.Join(out, "\n")}
		for _, l := range out {
			if loc, ok := fileLine(strings.SplitN(l, ": ", 2)[0]); ok && !strings.HasPrefix(l, "#") {
				f.Loc = loc
				break
			}
		}
		emit(Event{Kind: BuildFailed, Package: e.Package, Time: e.Time, Failure: f})
		return
	}
	reason := ReasonAssert
	if s.timedOut {
		reason = ReasonTimeout
	} else if s.panicked {
		reason = ReasonPanic
	}
	for _, t := range s.order {
		if !s.running[t] {
			continue
		}
		var f *Failure
		if s.timedOut {
			f = &Failure{Reason: ReasonTimeout, Message: "test timed out", Loc: goFrameLoc(t, s.lines[t])}
			for _, l := range s.lines[t] {
				if strings.HasPrefix(l, "panic: test timed out") {
					f.Message = strings.TrimPrefix(l, "panic: ")
				}
			}
		} else {
			f = goFailure(t, s.lines[t])
		}
		emit(Event{Kind: Failed, Package: e.Package, Test: t, Time: e.Time, Failure: f})
	}
	ev := Event{Package: e.Package, Time: e.Time, Elapsed: elapsed}
	switch e.Action {
	case "pass":
		ev.Kind = Passed
	case "skip":
		ev.Kind = Skipped
	default:
		ev.Kind = Failed
		ev.Failure = &Failure{Reason: reason}
	}
	emit(ev)
}

// goFrameLoc is where in a stack trace the test function was: its frame "pkg.TestX(…)", then "\t/path/x_test.go:9 +0x28".
func goFrameLoc(test string, lines []string) *Loc {
	top, _, _ := strings.Cut(test, "/")
	for j := 0; j+1 < len(lines); j++ {
		if strings.Contains(lines[j], "."+top+"(") || strings.Contains(lines[j], "."+top+".func") {
			if m := goFrame.FindStringSubmatch(lines[j+1]); m != nil {
				n, _ := strconv.Atoi(m[2])
				return &Loc{File: m[1], Line: n - 1}
			}
		}
	}
	return nil
}

var goFrame = regexp.MustCompile(`^\t(.+\.go):(\d+)(?: \+0x[0-9a-f]+)?$`)

// goFailure reads a failed test's output: a panic and where in the test it happened, or the first t.Error.
func goFailure(test string, lines []string) *Failure {
	for i, l := range lines {
		if !strings.HasPrefix(l, "panic: ") {
			continue
		}
		f := &Failure{Reason: ReasonPanic, Message: strings.TrimSuffix(strings.TrimPrefix(l, "panic: "), " [recovered, repanicked]")}
		f.Loc = goFrameLoc(test, lines[i+1:])
		return f
	}
	f := &Failure{Reason: ReasonAssert}
	var msg []string
	loc, first := goLoc(lines)
	if loc != nil {
		f.Loc = loc
		msg = append(msg, first)
		// Continuation lines of the message are indented further.
		started := false
		for _, l := range lines {
			if m := goFileLine.FindStringSubmatch(l); m != nil {
				if started {
					break
				}
				started = true
				continue
			}
			if started && strings.HasPrefix(l, "        ") {
				msg = append(msg, strings.TrimSpace(l))
			} else if started {
				break
			}
		}
	}
	f.Message = strings.Join(msg, "\n")
	wantGot(f, msg)
	return f
}

// goPackageOf is the package of a build ID: "p [p.test]" is p.
func goPackageOf(id string) string {
	p, _, _ := strings.Cut(id, " ")
	return p
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
