package testrun

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/liamg/oriel/syntax"
)

//go:embed pytest/oriel_pytest.py
var pytestPlugin []byte

// Pytest runs pytest with Oriel's bundled reporter plugin, which writes an event per line after a marker.
type Pytest struct {
	// Python is the interpreter; the workspace's .venv, then python3, when empty.
	Python string
	Args   []string // added to every run: -x, -k …
}

func (*Pytest) Name() string { return "pytest" }

func (*Pytest) Detect(ws Workspace) bool {
	for _, f := range []string{"pytest.ini", "pyproject.toml", "setup.cfg", "tox.ini", "conftest.py"} {
		if ws.Exists(f) {
			return true
		}
	}
	return false
}

func (p *Pytest) python(ws Workspace) string {
	if p.Python != "" {
		return p.Python
	}
	for _, v := range []string{".venv/bin/python", "venv/bin/python"} {
		if ws.Exists(v) {
			return filepath.Join(ws.Root, v)
		}
	}
	return "python3"
}

// pytestRoot is the directory pytest's node ids are relative to: the nearest one up with a config file, else
// the file's own (pytest's rootdir rules, simplified).
func pytestRoot(file string) string {
	if root, _, ok := findUp(filepath.Dir(file), "pytest.ini", ".pytest.ini", "pyproject.toml", "tox.ini", "setup.cfg"); ok {
		return root
	}
	return filepath.Dir(file)
}

func isPytestFile(path string) bool {
	b := filepath.Base(path)
	return strings.HasSuffix(b, ".py") && (strings.HasPrefix(b, "test_") || strings.HasSuffix(b, "_test.py"))
}

func (p *Pytest) Discover(src Source) []Item {
	if !isPytestFile(src.Path) {
		return nil
	}
	ms := testMatches(src)
	pkg := relTo(pytestRoot(src.Path), src.Path)
	funcs := byCapture(ms, "test.func")
	classes := byCapture(ms, "suite")
	inits := byCapture(ms, "init")
	defs := byCapture(ms, "def") // every function, to leave out ones nested in another
	params := byCapture(ms, "param")
	values := byCapture(ms, "param.value")
	ids := byCapture(ms, "param.id")
	idLists := byCapture(ms, "param.ids")
	var items []Item
	// A class is collected when it and every class around it is a test class with no __init__.
	classPath := func(n node) ([]string, bool) {
		var path []string
		for _, c := range classes {
			if !c.contains(n) && c.Range != n.Range {
				continue
			}
			name, _ := c.m.Get("suite.name")
			if !strings.HasPrefix(name.Text, "Test") {
				return nil, false
			}
			for _, in := range inits {
				if c.contains(in) {
					if owner, _ := innermost(classes, in); owner.Range == c.Range {
						return nil, false
					}
				}
			}
			path = append(path, name.Text)
		}
		return path, true
	}
	for _, c := range classes {
		path, ok := classPath(c)
		name, _ := c.m.Get("suite.name")
		if !ok {
			continue
		}
		items = append(items, Item{Package: pkg, Name: strings.Join(path, "::"), Path: path, File: src.Path, Line: name.Start.Line, Kind: KindSuite})
	}
	for _, f := range funcs {
		if outer, ok := innermost(defs, f); ok && outer.contains(f) {
			continue
		}
		path, ok := classPath(f)
		if !ok {
			continue
		}
		name, _ := f.m.Get("test.name")
		path = append(path, name.Text)
		full := strings.Join(path, "::")
		items = append(items, Item{Package: pkg, Name: full, Path: path, File: src.Path, Line: name.Start.Line, Kind: KindTest})
		var mine []node
		for _, pm := range params {
			if t, ok := pm.m.Get("param.target"); ok && t.Range == f.Range {
				mine = append(mine, pm)
			}
		}
		for _, row := range pytestRows(mine, values, idLists, ids) {
			items = append(items, Item{Package: pkg, Name: full + "[" + row.name + "]", Path: append(append([]string(nil), path...), row.name), File: src.Path, Line: row.line, Kind: KindRow})
		}
	}
	sortItems(items)
	return items
}

type pyRow struct {
	name string
	line int
}

// pytestRows are the ids parametrize gives a test's cases, when every decorator's ids can be worked out. The
// decorator nearest the function comes first in an id.
func pytestRows(decorators, values, idLists, ids []node) []pyRow {
	if len(decorators) == 0 {
		return nil
	}
	out := []pyRow{{}}
	for i := len(decorators) - 1; i >= 0; i-- {
		set, ok := pytestDecoratorRows(decorators[i], values, idLists, ids)
		if !ok {
			return nil
		}
		var next []pyRow
		for _, prefix := range out {
			for _, r := range set {
				if prefix.name != "" {
					r = pyRow{prefix.name + "-" + r.name, prefix.line}
				}
				next = append(next, r)
			}
		}
		out = next
	}
	return out
}

func pytestDecoratorRows(d node, values, idLists, ids []node) ([]pyRow, bool) {
	names, _ := d.m.Get("param.names")
	list, _ := d.m.Get("param.values")
	argnames := pyArgNames(names.Text)
	if len(argnames) == 0 {
		return nil, false
	}
	var given []string
	if c, ok := innerOf(d, idLists); ok {
		for _, el := range directIn(c.Capture, ids) {
			s, ok := pyString(el.Text)
			if !ok {
				return nil, false
			}
			given = append(given, s)
		}
		if given == nil {
			return nil, false
		}
	}
	var rows []pyRow
	for _, v := range directIn(list, values) {
		id := pyParamID(v.Text, argnames, len(rows))
		if len(rows) < len(given) {
			id = given[len(rows)]
		}
		rows = append(rows, pyRow{id, v.Start.Line})
	}
	return rows, true
}

// innerOf is the first of ns inside n.
func innerOf(n node, ns []node) (node, bool) {
	for _, c := range ns {
		if n.contains(c) {
			return c, true
		}
	}
	return node{}, false
}

// directIn is the nodes of all inside c and not inside another of them.
func directIn(c syntax.Capture, all []node) []node {
	outer := node{Capture: c}
	var out []node
	for _, n := range all {
		if outer.contains(n) && (len(out) == 0 || !out[len(out)-1].contains(n)) {
			out = append(out, n)
		}
	}
	return out
}

var pyParamCall = regexp.MustCompile(`(?s)^pytest\.param\((.*)\)$`)
var pyIDKwarg = regexp.MustCompile(`(?s),\s*id\s*=\s*("[^"]*"|'[^']*')\s*\)?$`)

// pyParamID is the id pytest makes for one case: a pytest.param's id=, else each value's own, joined by "-".
func pyParamID(v string, argnames []string, index int) string {
	v = strings.TrimSpace(v)
	if m := pyParamCall.FindStringSubmatch(v); m != nil {
		if k := pyIDKwarg.FindStringSubmatch(v); k != nil {
			s, _ := pyString(k[1])
			return s
		}
		return pyValuesID(splitTopLevel(m[1]), argnames, index)
	}
	if len(argnames) > 1 {
		inner := strings.TrimSpace(v)
		if (strings.HasPrefix(inner, "(") && strings.HasSuffix(inner, ")")) || (strings.HasPrefix(inner, "[") && strings.HasSuffix(inner, "]")) {
			return pyValuesID(splitTopLevel(inner[1:len(inner)-1]), argnames, index)
		}
	}
	return pyValuesID([]string{v}, argnames, index)
}

func pyValuesID(vals []string, argnames []string, index int) string {
	var parts []string
	for i, val := range vals {
		if i >= len(argnames) {
			break
		}
		parts = append(parts, pyValueID(strings.TrimSpace(val), argnames[i], index))
	}
	return strings.Join(parts, "-")
}

// pyValueID is a value's part of an id: numbers, strings, booleans and None as they print; anything else is the
// argument's name and the case's index.
func pyValueID(v, argname string, index int) string {
	switch v {
	case "True", "False", "None":
		return v
	}
	if s, ok := pyString(v); ok {
		return s
	}
	n := strings.TrimPrefix(strings.TrimPrefix(v, "-"), "+")
	if _, err := strconv.ParseInt(n, 0, 64); err == nil {
		return v
	}
	if f, err := strconv.ParseFloat(n, 64); err == nil && !math.IsInf(f, 0) && strings.Contains(n, ".") && !strings.ContainsAny(n, "eE_") {
		return v
	}
	return argname + strconv.Itoa(index)
}

// pyString reads a plain Python string literal, with no escapes it would have to decode.
func pyString(s string) (string, bool) {
	s = strings.TrimSpace(s)
	for _, q := range []string{`"""`, `'''`, `"`, `'`} {
		if len(s) >= 2*len(q) && strings.HasPrefix(s, q) && strings.HasSuffix(s, q) {
			body := s[len(q) : len(s)-len(q)]
			if strings.Contains(body, `\`) {
				return "", false
			}
			return body, true
		}
	}
	return "", false
}

// pyArgNames reads parametrize's first argument: "a,b", or a list or tuple of names.
func pyArgNames(s string) []string {
	s = strings.TrimSpace(s)
	if str, ok := pyString(s); ok {
		var out []string
		for _, n := range strings.Split(str, ",") {
			if n = strings.TrimSpace(n); n != "" {
				out = append(out, n)
			}
		}
		return out
	}
	if len(s) >= 2 && (s[0] == '[' || s[0] == '(') {
		var out []string
		for _, el := range splitTopLevel(s[1 : len(s)-1]) {
			if n, ok := pyString(el); ok {
				out = append(out, n)
			}
		}
		return out
	}
	return nil
}

// splitTopLevel splits on commas outside brackets and strings.
func splitTopLevel(s string) []string {
	var out []string
	depth, start := 0, 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
		case c == ',' && depth == 0:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if strings.TrimSpace(s[start:]) != "" {
		out = append(out, s[start:])
	}
	return out
}

func (p *Pytest) Command(ws Workspace, items []Item, mode Mode) (Cmd, error) {
	py := p.python(ws)
	args := []string{py, "-m", "pytest", "-p", "oriel_pytest"}
	if mode == ModeDebug {
		args = []string{py, "-m", "debugpy", "--listen", "127.0.0.1:5678", "--wait-for-client", "-m", "pytest", "-p", "oriel_pytest"}
	}
	args = append(args, p.Args...)
	cmd := Cmd{Dir: ws.Root, Files: map[string][]byte{"oriel_pytest.py": pytestPlugin}}
	pp := RunDir
	if old := os.Getenv("PYTHONPATH"); old != "" { // kept: the run's env goes after the inherited one
		pp += string(os.PathListSeparator) + old
	}
	cmd.Env = append(append([]string(nil), ws.Env...), "PYTHONPATH="+pp)
	if mode == ModeCoverage {
		cmd.CoverFile = "coverage.json"
		cmd.Env = append(cmd.Env, "COVERAGE_FILE="+RunDir+"/.coverage")
		args = append(args, "--cov="+ws.Root, "--cov-report=json:"+RunDir+"/coverage.json")
	}
	seen := map[string]bool{}
	var names []string
	for _, it := range items {
		var target string
		switch it.Kind {
		case KindPackage, KindFile:
			target = it.File
		default:
			target = it.File + "::" + it.Name
		}
		if !seen[target] {
			seen[target] = true
			args = append(args, target)
			names = append(names, ws.Rel(target))
		}
	}
	cmd.Args = args
	if mode == ModeDebug {
		targets := args[len(args)-len(seen):]
		l := &Launch{Name: "pytest " + strings.Join(names, " "), Type: "python", Cwd: ws.Root,
			Args: append(slices.Clone(p.Args), targets...), Extra: map[string]any{"module": "pytest"}}
		if py != "python3" {
			l.Extra["python"] = py
		}
		if len(ws.Env) > 0 {
			l.Env = envMap(ws.Env)
		}
		cmd.Launch = l
	}
	return cmd, nil
}

const pytestMarker = "@@oriel-event "

type pytestEvent struct {
	Event    string      `json:"event"`
	Time     float64     `json:"time"`
	NodeID   string      `json:"nodeid"`
	Duration float64     `json:"duration"`
	Outcome  string      `json:"outcome"`
	When     string      `json:"when"`
	Longrepr string      `json:"longrepr"`
	Message  string      `json:"message"`
	Xfail    any         `json:"xfail"`
	Sections [][2]string `json:"sections"`
	Crash    *struct {
		Path    string `json:"path"`
		Lineno  int    `json:"lineno"`
		Message string `json:"message"`
	} `json:"crash"`
}

// Parse reads the plugin's events from among pytest's own output, which becomes Output with no test.
func (p *Pytest) Parse(r io.Reader, emit func(Event)) error {
	root := ""
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		line := sc.Text()
		i := strings.Index(line, pytestMarker)
		if i < 0 {
			if line != "" {
				emit(Event{Kind: Output, Line: line})
			}
			continue
		}
		var e pytestEvent
		if err := json.Unmarshal([]byte(line[i+len(pytestMarker):]), &e); err != nil {
			emit(Event{Kind: Output, Line: line})
			continue
		}
		at := time.Unix(0, int64(e.Time*1e9)).UTC()
		pkg, test, _ := strings.Cut(e.NodeID, "::")
		switch e.Event {
		case "sessionstart":
			var s struct{ Rootdir string }
			json.Unmarshal([]byte(line[i+len(pytestMarker):]), &s)
			root = s.Rootdir
		case "collecterror":
			f := &Failure{Reason: ReasonBuild, Message: e.Longrepr}
			if e.Crash != nil {
				f.Loc = &Loc{File: e.Crash.Path, Line: e.Crash.Lineno - 1}
				f.Message = e.Crash.Message
			} else {
				// A traceback's last frame, and its error.
				for _, l := range strings.Split(e.Longrepr, "\n") {
					if m := pyFrame.FindStringSubmatch(l); m != nil {
						n, _ := strconv.Atoi(m[2])
						f.Loc = &Loc{File: m[1], Line: n - 1}
					} else if m := pyErrorLine.FindStringSubmatch(l); m != nil {
						f.Message = m[1]
					}
				}
			}
			emit(Event{Kind: BuildFailed, Package: pkg, Time: at, Failure: f})
		case "start":
			emit(Event{Kind: Started, Package: pkg, Test: test, Time: at})
		case "result":
			for _, s := range e.Sections {
				for _, l := range strings.Split(strings.TrimRight(s[1], "\n"), "\n") {
					emit(Event{Kind: Output, Package: pkg, Test: test, Time: at, Line: l})
				}
			}
			ev := Event{Package: pkg, Test: test, Time: at, Elapsed: time.Duration(e.Duration * float64(time.Second))}
			switch e.Outcome {
			case "passed":
				ev.Kind = Passed
			case "skipped":
				ev.Kind, ev.Message = Skipped, strings.TrimPrefix(e.Message, "Skipped: ")
				if e.Xfail != nil {
					ev.Message = "xfail"
					if s, ok := e.Xfail.(string); ok && s != "" {
						ev.Message = "xfail: " + s
					}
				}
			default:
				ev.Kind = Failed
				ev.Failure = pytestFailure(e, root)
			}
			emit(ev)
		}
	}
	return sc.Err()
}

var (
	pyErrorLine = regexp.MustCompile(`^E\s+(.*)$`)
	pyFrame     = regexp.MustCompile(`^(\S+\.py):(\d+): `)
)

func pytestFailure(e pytestEvent, root string) *Failure {
	f := &Failure{Reason: ReasonAssert, Message: e.Longrepr}
	if e.When != "call" {
		f.Reason = ReasonError
	}
	if e.Crash != nil {
		f.Loc = &Loc{File: e.Crash.Path, Line: e.Crash.Lineno - 1}
		f.Message = e.Crash.Message
		if f.Reason == ReasonAssert && !strings.HasPrefix(e.Crash.Message, "assert") && !strings.HasPrefix(e.Crash.Message, "AssertionError") {
			f.Reason = ReasonPanic
		}
	}
	var elines []string
	for _, l := range strings.Split(e.Longrepr, "\n") {
		if m := pyErrorLine.FindStringSubmatch(l); m != nil {
			elines = append(elines, m[1])
		}
	}
	wantGot(f, elines)
	return f
}

// Coverage reads coverage.py's JSON report.
func (p *Pytest) Coverage(ws Workspace, r io.Reader) (*Coverage, error) {
	return parseCoveragePy(r, ws.Root)
}

// Affected is the test files to run again after file was saved, by a heuristic: a test file itself; for
// conftest.py, every test under its directory; for another module, the test files that import it by its dotted
// name ("import pkg.mod", "from pkg.mod import x", "from pkg import mod") or, failing that, by its bare name.
func (p *Pytest) Affected(ws Workspace, file string) ([]Item, error) {
	if !strings.HasSuffix(file, ".py") {
		return nil, nil
	}
	if isPytestFile(file) {
		return []Item{{Package: relTo(pytestRoot(file), file), File: file, Kind: KindFile}}, nil
	}
	if filepath.Base(file) == "conftest.py" {
		dir := filepath.Dir(file)
		return []Item{{Package: relTo(ws.Root, dir), File: dir, Kind: KindPackage}}, nil
	}
	rel := strings.TrimSuffix(relTo(ws.Root, file), ".py")
	rel = strings.TrimSuffix(rel, "/__init__")
	for _, prefix := range []string{"src/", "lib/"} {
		rel = strings.TrimPrefix(rel, prefix)
	}
	dotted := strings.ReplaceAll(rel, "/", ".")
	parent, base := "", dotted
	if i := strings.LastIndexByte(dotted, '.'); i >= 0 {
		parent, base = dotted[:i], dotted[i+1:]
	}
	q := regexp.QuoteMeta
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?m)^\s*(?:import|from)\s+` + q(dotted) + `\b`),
	}
	if parent != "" {
		patterns = append(patterns, regexp.MustCompile(`(?m)^\s*from\s+`+q(parent)+`\s+import\s+[^\n]*\b`+q(base)+`\b`))
	}
	bare := regexp.MustCompile(`(?m)^\s*(?:import|from)\s+(?:\w+\.)*` + q(base) + `\b`)
	var exact, loose []Item
	err := filepath.WalkDir(ws.Root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			n := d.Name()
			if path != ws.Root && (strings.HasPrefix(n, ".") || n == "node_modules" || n == "venv" || n == "__pycache__" || n == "site-packages") {
				return filepath.SkipDir
			}
			return nil
		}
		if !isPytestFile(path) {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		it := Item{Package: relTo(pytestRoot(path), path), File: path, Kind: KindFile}
		for _, re := range patterns {
			if re.Match(b) {
				exact = append(exact, it)
				return nil
			}
		}
		if bare.Match(b) {
			loose = append(loose, it)
		}
		return nil
	})
	if len(exact) > 0 {
		return exact, err
	}
	return loose, err
}
