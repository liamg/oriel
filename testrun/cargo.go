package testrun

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Cargo runs Rust tests: with cargo nextest's libtest JSON when it's installed, otherwise cargo test, whose
// output is read as libtest prints it. A test's Package is its target: "lib", "test/<name>", "bin/<name>", or
// "doc".
type Cargo struct {
	// Nextest says whether to use cargo nextest; nil looks for it once.
	Nextest *bool
	Args    []string

	once sync.Once
	has  bool
}

func (*Cargo) Name() string { return "cargo" }

func (*Cargo) Detect(ws Workspace) bool { return ws.Exists("Cargo.toml") }

func (c *Cargo) nextest(ws Workspace) bool {
	if c.Nextest != nil {
		return *c.Nextest
	}
	c.once.Do(func() {
		cmd := exec.Command("cargo", "nextest", "--version")
		cmd.Dir = ws.Root
		cmd.Env = append(os.Environ(), ws.Env...)
		c.has = cmd.Run() == nil
	})
	return c.has
}

// cargoCrate is a crate's root and package name, read from its Cargo.toml.
func cargoCrate(dir string) (root, name string, ok bool) {
	root, _, ok = findUp(dir, "Cargo.toml")
	if !ok {
		return "", "", false
	}
	b, err := os.ReadFile(filepath.Join(root, "Cargo.toml"))
	if err != nil {
		return "", "", false
	}
	inPackage := false
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "[") {
			inPackage = l == "[package]"
			continue
		}
		if k, v, found := strings.Cut(l, "="); found && inPackage && strings.TrimSpace(k) == "name" {
			return root, strings.Trim(strings.TrimSpace(v), `"'`), true
		}
	}
	return root, "", true
}

// cargoTarget is the target a source file is compiled into and its module path there.
func cargoTarget(file string) (target string, mod []string) {
	root, name, ok := cargoCrate(filepath.Dir(file))
	if !ok {
		return "", nil
	}
	rel := relTo(root, file)
	parts := strings.Split(strings.TrimSuffix(rel, ".rs"), "/")
	switch {
	case len(parts) >= 2 && parts[0] == "tests":
		return "test/" + parts[1], cargoModPath(parts[2:])
	case len(parts) >= 3 && parts[0] == "src" && parts[1] == "bin":
		return "bin/" + parts[2], cargoModPath(parts[3:])
	case len(parts) >= 1 && parts[0] == "src":
		if parts[len(parts)-1] == "main" && len(parts) == 2 || !fileExists(filepath.Join(root, "src", "lib.rs")) && fileExists(filepath.Join(root, "src", "main.rs")) {
			return "bin/" + name, cargoModPath(parts[1:])
		}
		return "lib", cargoModPath(parts[1:])
	}
	return "", nil
}

func cargoModPath(parts []string) []string {
	var out []string
	for i, p := range parts {
		if i == len(parts)-1 && (p == "mod" || p == "lib" || p == "main") {
			break
		}
		out = append(out, p)
	}
	return out
}

func (c *Cargo) Discover(src Source) []Item {
	if !strings.HasSuffix(src.Path, ".rs") {
		return nil
	}
	ms := testMatches(src)
	target, mod := cargoTarget(src.Path)
	funcs := byCapture(ms, "test.func")
	mods := byCapture(ms, "suite")
	modPath := func(n node) []string {
		path := append([]string(nil), mod...)
		for _, m := range mods {
			if m.contains(n) {
				name, _ := m.m.Get("suite.name")
				path = append(path, name.Text)
			}
		}
		return path
	}
	var items []Item
	for _, m := range mods {
		has := false
		for _, f := range funcs {
			has = has || m.contains(f)
		}
		if !has {
			continue
		}
		name, _ := m.m.Get("suite.name")
		path := append(modPath(m), name.Text)
		items = append(items, Item{Package: target, Name: strings.Join(path, "::"), Path: path, File: src.Path, Line: name.Start.Line, Kind: KindSuite})
	}
	for _, f := range funcs {
		name, _ := f.m.Get("test.name")
		path := append(modPath(f), name.Text)
		items = append(items, Item{Package: target, Name: strings.Join(path, "::"), Path: path, File: src.Path, Line: name.Start.Line, Kind: KindTest})
	}
	sortItems(items)
	return items
}

// Command runs nextest with a filterset naming the items, or cargo test with their names as exact filters.
func (c *Cargo) Command(ws Workspace, items []Item, mode Mode) (Cmd, error) {
	if mode == ModeDebug {
		return Cmd{}, ErrUnsupported
	}
	cmd := Cmd{Dir: ws.Root, Env: ws.Env, Stderr: true}
	if mode == ModeCoverage {
		cmd.CoverFile = "llvm-cov.json"
		cmd.Args = append([]string{"cargo", "llvm-cov", "--json", "--output-path", RunDir + "/llvm-cov.json", "--ignore-run-fail"}, cargoTestArgs(items)...)
		cmd.Args = append(cmd.Args, c.Args...)
		return cmd, nil
	}
	if c.nextest(ws) {
		cmd.Stderr = false // nextest prints its own report, and failing tests' output again, there
		_, crate, _ := cargoCrate(ws.Root)
		cmd.Env = append(cmd.Env, "NEXTEST_EXPERIMENTAL_LIBTEST_JSON=1")
		cmd.Args = []string{"cargo", "nextest", "run", "--message-format", "libtest-json-plus", "--no-fail-fast"}
		if f := nextestFilter(crate, items); f != "" {
			cmd.Args = append(cmd.Args, "-E", f)
		}
		cmd.Args = append(cmd.Args, c.Args...)
		return cmd, nil
	}
	cmd.Args = append(append([]string{"cargo", "test", "--no-fail-fast"}, cargoTestArgs(items)...), c.Args...)
	return cmd, nil
}

// cargoTestArgs picks targets and, after "--", the tests: names exactly, or a module's as a prefix.
func cargoTestArgs(items []Item) []string {
	var targets, filters []string
	seen := map[string]bool{}
	exact := true
	for _, it := range items {
		var t string
		switch {
		case it.Package == "lib":
			t = "--lib"
		case strings.HasPrefix(it.Package, "test/"):
			t = "--test=" + strings.TrimPrefix(it.Package, "test/")
		case strings.HasPrefix(it.Package, "bin/"):
			t = "--bin=" + strings.TrimPrefix(it.Package, "bin/")
		}
		if t != "" && !seen[t] {
			seen[t] = true
			targets = append(targets, t)
		}
		switch it.Kind {
		case KindPackage, KindFile:
		case KindSuite:
			exact = false
			filters = append(filters, it.Name+"::")
		default:
			filters = append(filters, it.Name)
		}
	}
	args := targets
	if len(filters) > 0 {
		args = append(args, "--")
		if exact {
			args = append(args, "--exact")
		}
		args = append(args, filters...)
	}
	return args
}

// nextestFilter is a filterset: each item's binary and its test, exactly, or every test in a module.
func nextestFilter(crate string, items []Item) string {
	var alts []string
	for _, it := range items {
		bin := ""
		switch {
		case it.Package == "lib":
			bin = crate
		case strings.HasPrefix(it.Package, "test/"):
			bin = crate + "::" + strings.TrimPrefix(it.Package, "test/")
		case strings.HasPrefix(it.Package, "bin/"):
			bin = crate + "::" + it.Package
		}
		var parts []string
		if bin != "" {
			parts = append(parts, "binary_id("+bin+")")
		}
		switch it.Kind {
		case KindPackage, KindFile:
		case KindSuite:
			parts = append(parts, "test(/^"+regexp.QuoteMeta(it.Name+"::")+"/)")
		default:
			parts = append(parts, "test(="+it.Name+")")
		}
		if len(parts) > 0 {
			alts = append(alts, "("+strings.Join(parts, " & ")+")")
		}
	}
	return strings.Join(alts, " | ")
}

// libtestJSON is one line of libtest's JSON, as nextest writes it (with libtest-json-plus's nextest field).
type libtestJSON struct {
	Type     string  `json:"type"`
	Event    string  `json:"event"`
	Name     string  `json:"name"`
	ExecTime float64 `json:"exec_time"`
	Stdout   string  `json:"stdout"`
	Reason   string  `json:"reason"`
	Nextest  *struct {
		Crate      string `json:"crate"`
		TestBinary string `json:"test_binary"`
		Kind       string `json:"kind"`
	} `json:"nextest"`
}

var (
	cargoRunning = regexp.MustCompile(`^\s*Running (?:unittests )?(\S+) \((?:.*[/\\])?([^/\\]+?)-[0-9a-f]+(?:\.exe)?\)$`)
	cargoDoc     = regexp.MustCompile(`^\s*Doc-tests \S+$`)
	cargoResult  = regexp.MustCompile(`^test (.+?)(?: - should panic)? \.\.\. (ok|FAILED|ignored(?:, .*)?)$`)
	cargoSlow    = regexp.MustCompile(`^test (.+) has been running for over \d+ seconds$`)
	cargoStdout  = regexp.MustCompile(`^---- (.+) stdout ----$`)
	cargoSummary = regexp.MustCompile(`^test result: (ok|FAILED)\.`)
	rustPanicked = regexp.MustCompile(`^thread '.*' panicked at (.+):$`)
)

// Parse reads nextest's libtest JSON lines and cargo test's own output, whichever comes.
func (c *Cargo) Parse(r io.Reader, emit func(Event)) error {
	pkg := ""
	failed := map[string][]string{} // cargo test: failed tests and their output, till the failures section ends
	var order []string
	var section string
	finish := func(status string) {
		for _, t := range order {
			emit(Event{Kind: Failed, Package: pkg, Test: t, Failure: rustFailure(failed[t])})
		}
		failed, order, section = map[string][]string{}, nil, ""
		kind := Passed
		var f *Failure
		if status == "FAILED" {
			kind, f = Failed, &Failure{Reason: ReasonAssert}
		}
		emit(Event{Kind: kind, Package: pkg, Failure: f})
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "{") {
			var e libtestJSON
			if json.Unmarshal([]byte(line), &e) == nil && e.Type != "" {
				pkg = c.jsonEvent(e, pkg, emit)
				continue
			}
		}
		if m := cargoRunning.FindStringSubmatch(line); m != nil {
			pkg = cargoPackage(m[1], m[2])
			emit(Event{Kind: Output, Package: pkg, Line: line})
			continue
		}
		if cargoDoc.MatchString(line) {
			pkg = "doc"
			emit(Event{Kind: Output, Package: pkg, Line: line})
			continue
		}
		if strings.HasPrefix(line, "running ") && strings.HasSuffix(line, " tests") || line == "running 1 test" {
			emit(Event{Kind: Started, Package: pkg})
			continue
		}
		if m := cargoSlow.FindStringSubmatch(line); m != nil {
			emit(Event{Kind: Started, Package: pkg, Test: m[1]})
			continue
		}
		if m := cargoResult.FindStringSubmatch(line); m != nil {
			name := m[1]
			emit(Event{Kind: Started, Package: pkg, Test: name})
			switch {
			case m[2] == "ok":
				emit(Event{Kind: Passed, Package: pkg, Test: name})
			case m[2] == "FAILED":
				if _, ok := failed[name]; !ok {
					order = append(order, name)
				}
				failed[name] = nil
			default:
				msg := strings.TrimPrefix(strings.TrimPrefix(m[2], "ignored"), ", ")
				emit(Event{Kind: Skipped, Package: pkg, Test: name, Message: msg})
			}
			continue
		}
		if m := cargoStdout.FindStringSubmatch(line); m != nil {
			section = m[1]
			continue
		}
		if line == "failures:" {
			section = ""
			continue
		}
		if m := cargoSummary.FindStringSubmatch(line); m != nil {
			finish(m[1])
			continue
		}
		if section != "" {
			failed[section] = append(failed[section], line)
			emit(Event{Kind: Output, Package: pkg, Test: section, Line: line})
			continue
		}
		emit(Event{Kind: Output, Package: pkg, Line: line})
	}
	// Cut off before the summary (the run was killed): the failures known so far.
	for _, t := range order {
		emit(Event{Kind: Failed, Package: pkg, Test: t, Failure: rustFailure(failed[t])})
	}
	return sc.Err()
}

func (c *Cargo) jsonEvent(e libtestJSON, pkg string, emit func(Event)) string {
	switch e.Type {
	case "suite":
		if e.Nextest != nil {
			switch e.Nextest.Kind {
			case "lib", "proc-macro":
				pkg = "lib"
			case "test", "bench", "example":
				pkg = e.Nextest.Kind + "/" + e.Nextest.TestBinary
			default:
				pkg = "bin/" + e.Nextest.TestBinary
			}
		}
		switch e.Event {
		case "started":
			emit(Event{Kind: Started, Package: pkg})
		case "ok":
			emit(Event{Kind: Passed, Package: pkg, Elapsed: secs(e.ExecTime)})
		case "failed":
			emit(Event{Kind: Failed, Package: pkg, Elapsed: secs(e.ExecTime), Failure: &Failure{Reason: ReasonAssert}})
		}
		return pkg
	case "test":
		name := e.Name
		if _, n, ok := strings.Cut(name, "$"); ok {
			name = n
		}
		out := strings.Split(strings.TrimRight(e.Stdout, "\n"), "\n")
		if e.Stdout != "" {
			for _, l := range out {
				emit(Event{Kind: Output, Package: pkg, Test: name, Line: l})
			}
		}
		ev := Event{Package: pkg, Test: name, Elapsed: secs(e.ExecTime)}
		switch e.Event {
		case "started":
			ev.Kind = Started
		case "ok":
			ev.Kind = Passed
		case "ignored":
			ev.Kind = Skipped
		case "failed":
			ev.Kind = Failed
			ev.Failure = rustFailure(out)
			if strings.Contains(e.Reason, "time limit") {
				ev.Failure = &Failure{Reason: ReasonTimeout, Message: e.Reason}
			}
		default:
			return pkg
		}
		emit(ev)
	}
	return pkg
}

func secs(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }

// cargoPackage names a test binary cargo runs by its target: "Running unittests src/lib.rs (…/basic-1a2b)".
func cargoPackage(src, bin string) string {
	switch {
	case strings.HasPrefix(src, "tests/"):
		return "test/" + bin
	case src == "src/lib.rs":
		return "lib"
	case strings.HasPrefix(src, "benches/"):
		return "bench/" + bin
	case strings.HasPrefix(src, "examples/"):
		return "example/" + bin
	}
	return "bin/" + bin
}

// rustFailure reads a failed test's output: where it panicked, its message, and assert_eq's left and right.
func rustFailure(lines []string) *Failure {
	f := &Failure{Reason: ReasonPanic}
	for i, l := range lines {
		m := rustPanicked.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		if loc, ok := fileLine(m[1]); ok {
			f.Loc = loc
		}
		var msg []string
		for _, x := range lines[i+1:] {
			if strings.HasPrefix(x, "note: ") || strings.HasPrefix(x, "stack backtrace:") || x == "" {
				break
			}
			msg = append(msg, x)
		}
		f.Message = strings.Join(msg, "\n")
		if len(msg) > 0 && strings.HasPrefix(msg[0], "assertion") {
			f.Reason = ReasonAssert
		}
		wantGot(f, msg)
		break
	}
	return f
}

// Coverage reads cargo llvm-cov's JSON export.
func (*Cargo) Coverage(ws Workspace, r io.Reader) (*Coverage, error) { return parseLLVMCov(r, ws.Root) }

// Affected is the crate: Rust has no cheap way to know which tests a file reaches.
func (*Cargo) Affected(ws Workspace, file string) ([]Item, error) {
	if !strings.HasSuffix(file, ".rs") && filepath.Base(file) != "Cargo.toml" {
		return nil, nil
	}
	return []Item{{Package: "", File: ws.Root, Kind: KindPackage}}, nil
}
