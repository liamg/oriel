package plugin

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/jsonrpc"
	"github.com/liamg/oriel/registry"
)

// Caller is a plugin as the process and WASM hosts see it.
type Caller struct {
	ID    string
	Dir   string // its own files: its binaries and .wasm start with no grant
	Guard Guard  // nil for your own config, which is trusted
	// Data is the plugin's own directory for what it keeps (a log, a cache): its processes may write there, and
	// find it in $ORIEL_PLUGIN_DATA. None if empty.
	Data string
	// Programs run its binaries as other programs, by the name it spawns them by: a bundled plugin's
	// bin/oriel-linear is oriel itself, run as `oriel plugin-process linear` (Bundled).
	Programs map[string][]string
}

// EnvData names the plugin's data directory in its processes' environment.
const EnvData = "ORIEL_PLUGIN_DATA"

// Trusted is your own config, which may do anything and runs unsandboxed.
func (p Caller) Trusted() bool { return p.Guard == nil }

// Check asks the guard whether p may use c on target.
func (p Caller) Check(c Capability, target string) error {
	if p.Guard == nil {
		return nil
	}
	return p.Guard.Check(p.ID, c, target)
}

// CheckProgram checks the plugin may run name (CapExec) and says what to run: a path (./tool, bin/server, ~/bin/x)
// is resolved against dir (the plugin's own directory if "") and run as the absolute path checked; a bare name is
// checked as named, and found on PATH where it's run, as the grant's name is.
func (p Caller) CheckProgram(name, dir string) (string, error) {
	prog := Expand(name)
	if strings.ContainsRune(prog, filepath.Separator) {
		if !filepath.IsAbs(prog) {
			prog = filepath.Join(cmp.Or(dir, p.Dir), prog)
		}
		prog = RealPath(filepath.Clean(prog))
	}
	return prog, p.Check(CapExec, prog)
}

// Path is a path the plugin named: under its directory when relative, ~ its home.
func (p Caller) Path(name string) string {
	name = Expand(name)
	if !filepath.IsAbs(name) && p.Dir != "" {
		name = filepath.Join(p.Dir, name)
	}
	return filepath.Clean(name)
}

// Own says whether path is one of the plugin's own files, symlinks followed, so a link out of its directory
// isn't.
func (p Caller) Own(path string) bool {
	if p.Dir == "" {
		return false
	}
	dir, err := filepath.EvalSymlinks(p.Dir)
	if err != nil {
		return false
	}
	real, err := filepath.EvalSymlinks(path)
	return err == nil && Within(dir, real)
}

// CodeNotDeclared is the JSON-RPC error code of a call the plugin's manifest doesn't allow.
const CodeNotDeclared = -32001

// Method is a host function a process or WASM plugin calls by name, with JSON params: one
// declaration, every binding. Cap is checked before Run, against Target's answer.
type Method struct {
	Name   string
	Cap    Capability
	Target func(params json.RawMessage) string // what the call touches: a path, a host; nil for the capability alone
	Run    func(ctx context.Context, p Caller, params json.RawMessage) (any, error)
}

// Methods are the host functions plugins can call.
type Methods struct {
	mu sync.Mutex
	n  int
	m  map[string][]method // a later one of a name shadows the one before until disposed
}

type method struct {
	Method
	n int
}

func NewMethods() *Methods { return &Methods{m: map[string][]method{}} }

// Add makes m callable; a method of the same name is shadowed until it's disposed.
func (ms *Methods) Add(m Method) registry.Disposable {
	if m.Cap == "" {
		panic("ext: method " + m.Name + " declares no capability")
	}
	ms.mu.Lock()
	ms.n++
	n := ms.n
	ms.m[m.Name] = append(ms.m[m.Name], method{m, n})
	ms.mu.Unlock()
	return registry.DisposeFunc(func() {
		ms.mu.Lock()
		defer ms.mu.Unlock()
		ms.m[m.Name] = slices.DeleteFunc(ms.m[m.Name], func(x method) bool { return x.n == n })
	})
}

// Names are the methods there are, sorted.
func (ms *Methods) Names() []string {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	var out []string
	for n, l := range ms.m {
		if len(l) > 0 {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out
}

// Call runs a method for p, once the guard allows it. An unknown method is MethodNotFound, an undeclared call
// CodeNotDeclared, both as *jsonrpc.Error.
func (ms *Methods) Call(ctx context.Context, p Caller, name string, params json.RawMessage) (any, error) {
	ms.mu.Lock()
	l := ms.m[name]
	ms.mu.Unlock()
	if len(l) == 0 {
		return nil, &jsonrpc.Error{Code: jsonrpc.MethodNotFound, Message: "no host method " + name}
	}
	m := l[len(l)-1]
	target := ""
	if m.Target != nil {
		target = m.Target(params)
	}
	if err := p.Check(m.Cap, target); err != nil {
		return nil, &jsonrpc.Error{Code: CodeNotDeclared, Message: err.Error()}
	}
	return m.Run(ctx, p, params)
}

// Reporter is where host methods log and notify: a plugin.Host.
type Reporter interface {
	Log() *slog.Logger
	Notify(n core.Notice)
}

// Logger is the log for a plugin's own lines: its stderr, its log calls ("plugin:acme/jira").
func Logger(r Reporter, plugin string) *slog.Logger {
	return r.Log().With("source", "plugin:"+plugin)
}

// HostMethods are the built-in host functions: log and notify, fs.read and fs.write under their grants, and
// http.request when r is a Network.
func HostMethods(r Reporter) *Methods {
	ms := NewMethods()
	path := func(raw json.RawMessage) string {
		var p struct{ Path string }
		json.Unmarshal(raw, &p)
		return RealPath(p.Path)
	}
	ms.Add(Method{Name: "log", Cap: CapNotify, Run: func(_ context.Context, p Caller, raw json.RawMessage) (any, error) {
		var a struct{ Level, Message string }
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, err
		}
		l := Logger(r, p.ID)
		switch a.Level {
		case "debug":
			l.Debug(a.Message)
		case "warn", "warning":
			l.Warn(a.Message)
		case "error":
			l.Error(a.Message)
		default:
			l.Info(a.Message)
		}
		return nil, nil
	}})
	ms.Add(Method{Name: "notify", Cap: CapNotify, Run: func(_ context.Context, p Caller, raw json.RawMessage) (any, error) {
		var a struct{ Title, Text, Level string }
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, err
		}
		sev := core.SevInfo
		switch a.Level {
		case "warn", "warning":
			sev = core.SevWarning
		case "error":
			sev = core.SevError
		}
		r.Notify(core.Notice{ID: "plugin." + p.ID, Severity: sev, Source: p.ID, Title: a.Title, Text: a.Text})
		return nil, nil
	}})
	ms.Add(Method{Name: "fs.read", Cap: CapFSRead, Target: path, Run: func(_ context.Context, _ Caller, raw json.RawMessage) (any, error) {
		p := path(raw)
		if !filepath.IsAbs(p) {
			return nil, errors.New("fs.read: the path must be absolute")
		}
		b, err := os.ReadFile(RealPath(p)) // what the grant was checked against (Target)
		return string(b), err
	}})
	ms.Add(Method{Name: "fs.write", Cap: CapFSWrite, Target: path, Run: func(_ context.Context, _ Caller, raw json.RawMessage) (any, error) {
		var a struct{ Path, Data string }
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, err
		}
		if !filepath.IsAbs(a.Path) {
			return nil, errors.New("fs.write: the path must be absolute")
		}
		return nil, os.WriteFile(RealPath(a.Path), []byte(a.Data), 0o644)
	}})
	if n, ok := r.(Network); ok {
		ms.Add(HTTPMethod(n))
	}
	return ms
}

// RealPath is an absolute path with its symlinks followed, as far as it exists: a file not made yet keeps its
// name under its directory's real path. The guard checks paths this way, so a link can't lead out of a scope.
func RealPath(p string) string {
	if !filepath.IsAbs(p) {
		return p
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	dir, base := filepath.Split(filepath.Clean(p))
	if dir == p || base == "" {
		return filepath.Clean(p)
	}
	return filepath.Join(RealPath(filepath.Clean(dir)), base)
}
