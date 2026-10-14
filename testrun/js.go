package testrun

import (
	"encoding/json"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// jsFlavor is what differs between vitest and jest in naming .each cases.
type jsFlavor int

const (
	flavorVitest jsFlavor = iota
	flavorJest
)

// jsSep joins a test's describe blocks and title into its name.
const jsSep = " > "

func isJSTestFile(path string) bool {
	b := filepath.Base(path)
	for _, ext := range []string{".ts", ".tsx", ".js", ".jsx", ".mts", ".cts", ".mjs", ".cjs"} {
		if strings.HasSuffix(b, ".test"+ext) || strings.HasSuffix(b, ".spec"+ext) {
			return true
		}
	}
	return strings.Contains(filepath.ToSlash(path), "/__tests__/")
}

// jsDiscover finds describe/it/test calls and .each tables. A test's Package is its file's absolute path, as the
// runners' JSON reports name it.
func jsDiscover(src Source, flavor jsFlavor) []Item {
	if !isJSTestFile(src.Path) {
		return nil
	}
	ms := testMatches(src)
	calls := byCapture(ms, "test.call")
	eaches := byCapture(ms, "each.call")
	rows := byCapture(ms, "each.row")
	pairs := byCapture(ms, "each.value")

	// A block's names: one for a plain call, one per row for an .each; nil when they can't be worked out.
	type block struct {
		n     node
		suite bool
		names []string
		lines []int
	}
	var blocks []block
	for _, c := range calls {
		fn, _ := c.m.Get("test.fn")
		name, _ := c.m.Get("test.name")
		s, ok := jsString(name.Text)
		b := block{n: c, suite: jsSuite(fn.Text)}
		if ok {
			b.names, b.lines = []string{s}, []int{name.Start.Line}
		}
		blocks = append(blocks, b)
	}
	for _, e := range eaches {
		fn, _ := e.m.Get("each.fn")
		name, _ := e.m.Get("each.name")
		table, _ := e.m.Get("each.table")
		b := block{n: e, suite: jsSuite(fn.Text)}
		format, ok := jsString(name.Text)
		if ok {
			for i, row := range directIn(table, rows) {
				n, ok := jsEachName(format, row, i, rows, pairs, flavor)
				if !ok {
					b.names, b.lines = nil, nil
					break
				}
				b.names = append(b.names, n)
				b.lines = append(b.lines, row.Start.Line)
			}
		}
		blocks = append(blocks, b)
	}
	// Names nest: a block's full names are each of its parent's with each of its own.
	parentOf := func(b block) (block, bool) {
		var best block
		found := false
		for _, o := range blocks {
			if o.suite && o.n.contains(b.n) && (!found || best.n.contains(o.n)) {
				best, found = o, true
			}
		}
		return best, found
	}
	var paths func(b block) ([][]string, bool)
	paths = func(b block) ([][]string, bool) {
		if b.names == nil {
			return nil, false
		}
		var prefixes [][]string
		if p, ok := parentOf(b); ok {
			pp, ok := paths(p)
			if !ok {
				return nil, false
			}
			prefixes = pp
		} else {
			prefixes = [][]string{nil}
		}
		var out [][]string
		for _, pre := range prefixes {
			for _, n := range b.names {
				out = append(out, append(append([]string(nil), pre...), n))
			}
		}
		return out, true
	}
	var items []Item
	for _, b := range blocks {
		ps, ok := paths(b)
		if !ok {
			continue
		}
		kind := KindTest
		if b.suite {
			kind = KindSuite
		}
		_, each := b.n.m.Get("each.call")
		for i, p := range ps {
			line := b.lines[i%len(b.lines)]
			k := kind
			if each && !b.suite {
				k = KindRow
			}
			items = append(items, Item{Package: src.Path, Name: strings.Join(p, jsSep), Path: p, File: src.Path, Line: line, Kind: k})
		}
	}
	sortItems(items)
	return items
}

func jsSuite(fn string) bool {
	switch fn {
	case "describe", "suite", "context", "fdescribe", "xdescribe":
		return true
	}
	return false
}

// jsString reads a string literal, or a template string with nothing interpolated.
func jsString(s string) (string, bool) {
	if len(s) < 2 {
		return "", false
	}
	q := s[0]
	if (q != '\'' && q != '"' && q != '`') || s[len(s)-1] != q {
		return "", false
	}
	body := s[1 : len(s)-1]
	if q == '`' && strings.Contains(body, "${") {
		return "", false
	}
	if !strings.Contains(body, `\`) {
		return body, true
	}
	u, err := strconv.Unquote(`"` + strings.ReplaceAll(strings.ReplaceAll(body, `\'`, `'`), `"`, `\"`) + `"`)
	return u, err == nil
}

var jsPlaceholder = regexp.MustCompile(`%[sdifjo#%p]|\$[A-Za-z_]\w*`)

// jsEachName fills a .each title in for one row: printf placeholders take the row's values in turn; $name takes
// an object row's property. Values that aren't plain literals can't be worked out.
func jsEachName(format string, row node, index int, rows, pairs []node, flavor jsFlavor) (string, bool) {
	var args []string
	var props map[string]string
	text := strings.TrimSpace(row.Text)
	switch {
	case strings.HasPrefix(text, "["):
		for _, el := range directIn(row.Capture, rows) {
			args = append(args, el.Text)
		}
	case strings.HasPrefix(text, "{"):
		props = map[string]string{}
		for _, p := range pairs {
			if !row.contains(p) {
				continue
			}
			k, _ := p.m.Get("each.key")
			key := k.Text
			if s, ok := jsString(key); ok {
				key = s
			}
			if _, seen := props[key]; !seen {
				props[key] = p.Text
			}
		}
	default:
		args = []string{text}
	}
	ok := true
	next := 0
	out := jsPlaceholder.ReplaceAllStringFunc(format, func(ph string) string {
		if ph == "%%" {
			return "%"
		}
		if ph == "%#" {
			return strconv.Itoa(index)
		}
		if strings.HasPrefix(ph, "$") {
			v, found := props[ph[1:]]
			if !found {
				ok = false
				return ""
			}
			s, good := jsDisplay(v, flavor)
			ok = ok && good
			return s
		}
		if next >= len(args) {
			ok = false
			return ""
		}
		v := strings.TrimSpace(args[next])
		next++
		s, good := jsFormat(ph[1], v)
		ok = ok && good
		return s
	})
	return out, ok
}

// jsFormat is one printf placeholder filled with a literal, as util.format does.
func jsFormat(verb byte, v string) (string, bool) {
	if s, ok := jsString(v); ok {
		switch verb {
		case 's':
			return s, true
		case 'j':
			b, _ := json.Marshal(s)
			return string(b), true
		case 'p':
			return `"` + s + `"`, true
		}
		return "", false
	}
	n, err := strconv.ParseFloat(strings.ReplaceAll(v, "_", ""), 64)
	if err != nil {
		switch v {
		case "true", "false", "null", "undefined":
			return v, verb == 's' || verb == 'j' && v != "undefined"
		}
		return "", false
	}
	switch verb {
	case 'i':
		return strconv.FormatInt(int64(n), 10), true
	case 'd', 's', 'f', 'j', 'p':
		return strconv.FormatFloat(n, 'f', -1, 64), true
	}
	return "", false
}

// jsDisplay is how a $name value is shown: vitest formats with chai (loupe: strings in single quotes, zero as +0),
// jest with pretty-format (strings in double quotes).
func jsDisplay(v string, flavor jsFlavor) (string, bool) {
	v = strings.TrimSpace(v)
	if s, ok := jsString(v); ok {
		if flavor == flavorJest {
			return `"` + s + `"`, true
		}
		return "'" + s + "'", true
	}
	if n, err := strconv.ParseFloat(v, 64); err == nil {
		if n == 0 && flavor == flavorVitest {
			if strings.HasPrefix(v, "-") {
				return "-0", true
			}
			return "+0", true
		}
		return strconv.FormatFloat(n, 'f', -1, 64), true
	}
	switch v {
	case "true", "false", "null", "undefined":
		return v, true
	}
	return "", false
}

// jsReport is the JSON both vitest's json reporter and jest --json write (jest's format, which vitest follows).
type jsReport struct {
	StartTime   int64 `json:"startTime"`
	TestResults []struct {
		Name             string `json:"name"`
		Status           string `json:"status"`
		Message          string `json:"message"`
		StartTime        int64  `json:"startTime"`
		AssertionResults []struct {
			AncestorTitles  []string `json:"ancestorTitles"`
			Title           string   `json:"title"`
			Status          string   `json:"status"`
			Duration        *float64 `json:"duration"`
			FailureMessages []string `json:"failureMessages"`
			Location        *struct {
				Line   int `json:"line"`
				Column int `json:"column"`
			} `json:"location"`
		} `json:"assertionResults"`
	} `json:"testResults"`
}

// jsParse reads the report. Runners print it once, at the end, so its events come together; lines before it
// (a runner's own messages) are output.
func jsParse(r io.Reader, emit func(Event)) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	start := strings.Index(string(b), "{\"")
	if start < 0 {
		for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
			if l != "" {
				emit(Event{Kind: Output, Line: l})
			}
		}
		return nil
	}
	for _, l := range strings.Split(string(b[:start]), "\n") {
		if strings.TrimSpace(l) != "" {
			emit(Event{Kind: Output, Line: l})
		}
	}
	var rep jsReport
	if err := json.Unmarshal(b[start:], &rep); err != nil {
		return err
	}
	for _, f := range rep.TestResults {
		at := time.UnixMilli(f.StartTime).UTC()
		emit(Event{Kind: Started, Package: f.Name, Time: at})
		if len(f.AssertionResults) == 0 && f.Status == "failed" {
			emit(Event{Kind: BuildFailed, Package: f.Name, Time: at, Failure: jsFailure(f.Message, f.Name, ReasonBuild)})
			continue
		}
		failed := false
		for _, a := range f.AssertionResults {
			name := strings.Join(append(append([]string(nil), a.AncestorTitles...), a.Title), jsSep)
			emit(Event{Kind: Started, Package: f.Name, Test: name, Time: at})
			ev := Event{Package: f.Name, Test: name, Time: at}
			if a.Duration != nil {
				ev.Elapsed = time.Duration(*a.Duration * float64(time.Millisecond))
			}
			switch a.Status {
			case "passed":
				ev.Kind = Passed
			case "failed":
				failed = true
				ev.Kind = Failed
				ev.Failure = jsFailure(strings.Join(a.FailureMessages, "\n"), f.Name, "")
			default: // skipped, pending, todo, disabled
				ev.Kind, ev.Message = Skipped, a.Status
			}
			emit(ev)
		}
		ev := Event{Kind: Passed, Package: f.Name, Time: at}
		if failed || f.Status == "failed" {
			ev.Kind, ev.Failure = Failed, &Failure{Reason: ReasonAssert}
		}
		emit(ev)
	}
	return nil
}

var (
	jsStackPath = regexp.MustCompile(`^\s*at (?:.*?\()?(?:file://)?([^\s()]+):(\d+):(\d+)\)?$`)
	jsTimeout   = regexp.MustCompile(`(?i)timed out|exceeded timeout`)
)

// jsFailure reads a failure message: the first line says what failed, expected and received say want and got,
// and the stack's first frame in the test file says where.
func jsFailure(msg, file string, reason Reason) *Failure {
	lines := strings.Split(msg, "\n")
	f := &Failure{Reason: reason, Message: strings.TrimSpace(lines[0])}
	if reason == "" {
		switch first := lines[0]; {
		case jsTimeout.MatchString(first):
			f.Reason = ReasonTimeout
		case strings.HasPrefix(first, "AssertionError") || strings.Contains(first, "expect("):
			f.Reason = ReasonAssert
		default:
			f.Reason = ReasonPanic
		}
	}
	var fallback *Loc
	for _, l := range lines {
		m := jsStackPath.FindStringSubmatch(l)
		if m == nil || strings.Contains(m[1], "/node_modules/") || strings.HasPrefix(m[1], "node:") {
			continue
		}
		n, _ := strconv.Atoi(m[2])
		loc := &Loc{File: m[1], Line: n - 1}
		if m[1] == file {
			f.Loc = loc
			break
		}
		if fallback == nil {
			fallback = loc
		}
	}
	if f.Loc == nil {
		f.Loc = fallback
	}
	wantGot(f, lines)
	return f
}

// jsDeps is package.json's dependencies, and the words of its scripts.
func jsDeps(ws Workspace) map[string]bool {
	b, err := ws.ReadFile("package.json")
	if err != nil {
		return nil
	}
	var p struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
		Scripts         map[string]string `json:"scripts"`
	}
	if json.Unmarshal(b, &p) != nil {
		return nil
	}
	out := map[string]bool{}
	for d := range p.Dependencies {
		out[d] = true
	}
	for d := range p.DevDependencies {
		out[d] = true
	}
	for _, s := range p.Scripts {
		for _, f := range strings.Fields(s) {
			out[f] = true
		}
	}
	return out
}

// jsBin is a package's binary: the project's own in node_modules, else through npx.
func jsBin(ws Workspace, name string) []string {
	if rel := filepath.Join("node_modules", ".bin", name); ws.Exists(rel) {
		return []string{filepath.Join(ws.Root, rel)}
	}
	return []string{"npx", "--no-install", name}
}

// jsNamePattern is a regular expression for -t / --testNamePattern that matches items' full names: describe blocks
// and title joined by spaces, as both runners match them. A suite matches everything in it.
func jsNamePattern(items []Item) string {
	var alts []string
	for _, it := range items {
		if len(it.Path) == 0 {
			continue
		}
		p := regexp.QuoteMeta(strings.Join(it.Path, " "))
		if it.Kind == KindSuite {
			alts = append(alts, p+" ")
		} else {
			alts = append(alts, p+"$")
		}
	}
	if len(alts) == 0 {
		return ""
	}
	return "^(?:" + strings.Join(alts, "|") + ")"
}

// Vitest runs vitest with its json reporter.
type Vitest struct {
	Args []string
}

func (*Vitest) Name() string { return "vitest" }

func (*Vitest) Detect(ws Workspace) bool { return jsDeps(ws)["vitest"] }

func (*Vitest) Discover(src Source) []Item { return jsDiscover(src, flavorVitest) }

func (v *Vitest) Command(ws Workspace, items []Item, mode Mode) (Cmd, error) {
	if mode == ModeDebug {
		return Cmd{}, ErrUnsupported
	}
	args := append(jsBin(ws, "vitest"), "run")
	var related []string
	files, tests := jsSplit(ws, items)
	for _, it := range items {
		if it.Kind == KindFile && mode == ModeWatch {
			related = append(related, ws.Rel(it.File))
		}
	}
	if len(related) > 0 {
		args = append(jsBin(ws, "vitest"), append([]string{"related"}, related...)...)
		args = append(args, "--run")
		files, tests = nil, nil
	}
	args = append(args, "--reporter=json")
	cmd := Cmd{Dir: ws.Root, Env: ws.Env}
	if mode == ModeCoverage {
		cmd.CoverFile = "coverage/coverage-final.json"
		args = append(args, "--coverage.enabled", "--coverage.reporter=json", "--coverage.reportOnFailure", "--coverage.reportsDirectory="+RunDir+"/coverage")
	}
	if p := jsNamePattern(tests); p != "" {
		args = append(args, "-t", p)
	}
	args = append(args, v.Args...)
	cmd.Args = append(args, files...)
	return cmd, nil
}

// jsSplit is the files to run and the tests to pick out of them.
func jsSplit(ws Workspace, items []Item) (files []string, tests []Item) {
	seen := map[string]bool{}
	whole := false
	for _, it := range items {
		f := ws.Rel(it.File)
		if !seen[f] {
			seen[f] = true
			files = append(files, f)
		}
		if it.Kind == KindFile || it.Kind == KindPackage {
			whole = true
		} else {
			tests = append(tests, it)
		}
	}
	if whole {
		tests = nil
	}
	return files, tests
}

func (*Vitest) Parse(r io.Reader, emit func(Event)) error { return jsParse(r, emit) }

// Coverage reads istanbul's coverage-final.json.
func (*Vitest) Coverage(ws Workspace, r io.Reader) (*Coverage, error) {
	return parseIstanbul(r, ws.Root)
}

// Affected is the file itself, which a watch run hands to `vitest related`.
func (*Vitest) Affected(ws Workspace, file string) ([]Item, error) {
	return []Item{{Package: file, File: file, Kind: KindFile}}, nil
}

// Jest runs jest --json.
type Jest struct {
	Args []string
}

func (*Jest) Name() string { return "jest" }

func (*Jest) Detect(ws Workspace) bool { return jsDeps(ws)["jest"] }

func (*Jest) Discover(src Source) []Item { return jsDiscover(src, flavorJest) }

func (j *Jest) Command(ws Workspace, items []Item, mode Mode) (Cmd, error) {
	if mode == ModeDebug {
		return Cmd{}, ErrUnsupported
	}
	args := append(jsBin(ws, "jest"), "--json", "--testLocationInResults")
	cmd := Cmd{Dir: ws.Root, Env: ws.Env}
	files, tests := jsSplit(ws, items)
	if mode == ModeWatch {
		var changed []string
		for _, it := range items {
			if it.Kind == KindFile {
				changed = append(changed, ws.Rel(it.File))
			}
		}
		if len(changed) > 0 {
			args = append(args, "--findRelatedTests")
			args = append(args, changed...)
			files, tests = nil, nil
		}
	}
	if mode == ModeCoverage {
		cmd.CoverFile = "coverage/coverage-final.json"
		args = append(args, "--coverage", "--coverageReporters=json", "--coverageDirectory="+RunDir+"/coverage")
	}
	if p := jsNamePattern(tests); p != "" {
		args = append(args, "--testNamePattern", p)
	}
	args = append(args, j.Args...)
	if len(files) > 0 {
		args = append(args, "--runTestsByPath")
		args = append(args, files...)
	}
	cmd.Args = args
	return cmd, nil
}

func (*Jest) Parse(r io.Reader, emit func(Event)) error { return jsParse(r, emit) }

func (*Jest) Coverage(ws Workspace, r io.Reader) (*Coverage, error) { return parseIstanbul(r, ws.Root) }

// Affected is the file itself, which a watch run hands to jest --findRelatedTests.
func (*Jest) Affected(ws Workspace, file string) ([]Item, error) {
	return []Item{{Package: file, File: file, Kind: KindFile}}, nil
}
