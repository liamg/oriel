package testrun

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/liamg/oriel/syntax"
	"github.com/liamg/oriel/text"
)

// Adapter discovers, runs and reads the results of one kind of test runner. Built-ins: Go, pytest, vitest, jest
// and cargo; a plugin adds another by registering it (Set.Register, or a "testrun.adapter" contribution).
type Adapter interface {
	Name() string
	Detect(ws Workspace) bool                                   // go.mod, pyproject, package.json, Cargo.toml…
	Discover(src Source) []Item                                 // gutter ▶ positions; cheap, runs on every edit
	Command(ws Workspace, items []Item, mode Mode) (Cmd, error) // what to run for items
	Parse(r io.Reader, emit func(Event)) error                  // the runner's stdout, streamed
}

// Coverer is an Adapter that reads the coverage file a Coverage run leaves (Cmd.CoverFile).
type Coverer interface {
	Coverage(ws Workspace, r io.Reader) (*Coverage, error)
}

// Watcher is an Adapter that knows which tests a saved file affects.
type Watcher interface {
	Affected(ws Workspace, file string) ([]Item, error)
}

// TestFiler is an Adapter that tells its test files by their path alone.
type TestFiler interface {
	TestFile(path string) bool
}

// Workspace is a project the tests are in.
type Workspace struct {
	Root string
	Env  []string // added to every command's environment
}

// Exists reports whether a file under the root exists.
func (ws Workspace) Exists(rel string) bool {
	_, err := os.Stat(filepath.Join(ws.Root, rel))
	return err == nil
}

// ReadFile reads a file under the root.
func (ws Workspace) ReadFile(rel string) ([]byte, error) {
	return os.ReadFile(filepath.Join(ws.Root, rel))
}

// Rel is path relative to the root, slash-separated; path as it is when it's outside.
func (ws Workspace) Rel(path string) string {
	if !filepath.IsAbs(path) {
		return filepath.ToSlash(path)
	}
	r, err := filepath.Rel(ws.Root, path)
	if err != nil || r == ".." || len(r) > 2 && r[:3] == ".."+string(filepath.Separator) {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(r)
}

// Source is a buffer to find tests in.
type Source struct {
	Path string // absolute
	Text text.Snapshot
	// Syntax runs a named query over the buffer's parsed tree: its *syntax.Document, kept up to date as it's
	// edited. Nil parses Text afresh, in the language its path names in Languages (the built-ins if nil).
	Syntax    Querier
	Languages *syntax.Set
}

// Querier runs a named query; *syntax.Document and *syntax.Parser are both one.
type Querier interface {
	Query(kind string) ([]syntax.Match, error)
}

// Kind is what an item is.
type Kind string

const (
	KindTest    Kind = "test"    // a test function
	KindSubtest Kind = "subtest" // t.Run, a nested it
	KindRow     Kind = "row"     // a row of a table: a Go table case, a parametrize id, an .each row
	KindSuite   Kind = "suite"   // a group: describe, a test class, a Rust module
	KindPackage Kind = "package" // a whole package, for watch
	KindFile    Kind = "file"    // tests related to a file (vitest related), for watch
)

// Item is something that can be run: a test, a subtest, a table row, a suite or a package.
type Item struct {
	Package string   // the runner's unit: a Go import path, or a file relative to the workspace root
	Name    string   // the runner's full name for it inside Package; Key joins the two
	Path    []string // the name's parts, outermost first
	File    string   // absolute
	Line    int      // 0-based line of its name
	Kind    Kind
}

// Key identifies a test across discovery, events and history.
func (it Item) Key() string { return Key(it.Package, it.Name) }

// Key joins a package and a test name.
func Key(pkg, name string) string { return pkg + "\x00" + name }

// Mode is how to run.
type Mode int

const (
	ModeRun Mode = iota
	ModeDebug
	ModeCoverage
	ModeWatch
)

func (m Mode) String() string {
	switch m {
	case ModeDebug:
		return "debug"
	case ModeCoverage:
		return "coverage"
	case ModeWatch:
		return "watch"
	}
	return "run"
}

// Cmd is a process to start. ${ORIEL_TESTRUN_DIR} in Args and Env is a fresh directory the runner makes for the run,
// which Files are written into.
type Cmd struct {
	Args      []string
	Dir       string
	Env       []string
	Files     map[string][]byte // name in the run's directory → content
	CoverFile string            // the coverage file a Coverage run writes, in the run's directory
	Stderr    bool              // Parse reads stderr too, merged with stdout
	Launch    *Launch           // a ModeDebug run: how a debug adapter launches it, Args being its command-line equivalent
}

// Launch is a debug adapter's launch of a ModeDebug run: the fields of a launch config (.oriel/debug.toml's).
type Launch struct {
	Name    string // the session's name: "go test -run TestX/case"
	Type    string // the debug adapter's type: go, python
	Program string
	Args    []string
	Cwd     string
	Env     map[string]string
	Extra   map[string]any // the adapter's own arguments: delve's mode, debugpy's module
}

// ErrUnsupported is returned by Command for a mode the adapter can't run.
var ErrUnsupported = errors.New("testrun: mode not supported by this adapter")

// RunDir is how a Cmd names the directory the runner makes for it.
const RunDir = "${ORIEL_TESTRUN_DIR}"

// envMap is KEY=value entries as a map.
func envMap(env []string) map[string]string {
	out := map[string]string{}
	for _, e := range env {
		if k, v, ok := strings.Cut(e, "="); ok {
			out[k] = v
		}
	}
	return out
}
