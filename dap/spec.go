package dap

import (
	"bufio"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// AdapterSpec is a debug adapter: the languages it debugs, how to start it and reach it, and the launch arguments
// every config for it starts from. The app registers these as registry.DebugAdapter contributions; the two types
// have the same fields, so each converts to the other.
type AdapterSpec struct {
	Name     string   // "dlv"; also the log source
	Type     string   // what a launch config's type names: "go", "python"
	Language []string // Oriel language names, as registry.Language names them
	// Cmd starts the adapter. ${addr} is replaced by the address Oriel listens on (TransportReverse), ${port} by a
	// free port the adapter is to listen on (TransportTCP).
	Cmd       []string
	Transport string         // TransportStdio (the default), TransportTCP or TransportReverse
	Probe     []string       // a command that succeeds when the adapter can run; by default, Cmd[0] being on PATH
	Launch    map[string]any // launch arguments every launch config starts from
	Attach    map[string]any // the same, for attach
	// Infer is the config for debugging a file when nothing is configured: its package, its program, its tests.
	Infer  func(file string) (LaunchConfig, bool)
	Order  int // among the adapters for a type, lower is tried first
	Source string
	// Connect, when set, reaches the adapter some other way than running Cmd, in place of the client's launcher.
	Connect Launcher
}

// How the client talks to an adapter it starts.
const (
	TransportStdio   = "stdio"
	TransportTCP     = "tcp"     // the adapter listens on ${port}; Oriel connects
	TransportReverse = "reverse" // Oriel listens on ${addr}; the adapter connects
)

// Debugs reports whether the spec debugs language or launch configs of that type.
func (s AdapterSpec) Debugs(languageOrType string) bool {
	return s.Type == languageOrType || s.Name == languageOrType || slices.Contains(s.Language, languageOrType)
}

// LaunchConfig is one way to start debugging: a program to launch, or a process to attach to. Program, Args, Cwd,
// Env and PID are the arguments delve and debugpy share; anything an adapter has of its own goes in Extra.
type LaunchConfig struct {
	Name    string
	Type    string // an adapter's Type, or its Name
	Request string // launch (the default) or attach
	Program string
	Args    []string
	Cwd     string
	Env     map[string]string
	PID     int            // attach to this process
	Extra   map[string]any // adapter-specific arguments, over the rest: delve's mode, debugpy's justMyCode
}

// Requests.
const (
	RequestLaunch = "launch"
	RequestAttach = "attach"
)

// Arguments are the launch or attach request's arguments: the spec's defaults, the config's fields, then Extra.
func (c LaunchConfig) Arguments(spec AdapterSpec) map[string]any {
	out := map[string]any{}
	if c.request() == RequestAttach {
		maps.Copy(out, spec.Attach)
	} else {
		maps.Copy(out, spec.Launch)
	}
	if c.Name != "" {
		out["name"] = c.Name
	}
	if c.Program != "" {
		out["program"] = c.Program
	}
	if len(c.Args) > 0 {
		out["args"] = c.Args
	}
	if c.Cwd != "" {
		out["cwd"] = c.Cwd
	}
	if len(c.Env) > 0 {
		out["env"] = c.Env
	}
	if c.PID > 0 {
		out["processId"] = c.PID
	}
	maps.Copy(out, c.Extra)
	return out
}

func (c LaunchConfig) request() string {
	if c.Request == "" {
		return RequestLaunch
	}
	return c.Request
}

// ConfigFromMap reads a launch config from a table of .oriel/debug.toml: name, type, request, program, args, cwd,
// env and pid; any other key is the adapter's own (Extra). Relative program and cwd are taken from dir.
func ConfigFromMap(m map[string]any, dir string) (LaunchConfig, error) {
	var c LaunchConfig
	str := func(k string) (string, error) {
		v, ok := m[k]
		if !ok {
			return "", nil
		}
		s, ok := v.(string)
		if !ok {
			return "", fmt.Errorf("%s is %T, not a string", k, v)
		}
		return s, nil
	}
	var err error
	for _, f := range []struct {
		key string
		to  *string
	}{{"name", &c.Name}, {"type", &c.Type}, {"request", &c.Request}, {"program", &c.Program}, {"cwd", &c.Cwd}} {
		if *f.to, err = str(f.key); err != nil {
			return c, err
		}
	}
	if c.Type == "" {
		return c, fmt.Errorf("no type: name an adapter (go, python…)")
	}
	if c.Request != "" && c.Request != RequestLaunch && c.Request != RequestAttach {
		return c, fmt.Errorf("request is launch or attach, not %q", c.Request)
	}
	if v, ok := m["args"]; ok {
		l, ok := v.([]any)
		if !ok {
			return c, fmt.Errorf("args is %T, not a list", v)
		}
		for _, a := range l {
			c.Args = append(c.Args, fmt.Sprint(a))
		}
	}
	if v, ok := m["env"]; ok {
		t, ok := v.(map[string]any)
		if !ok {
			return c, fmt.Errorf("env is %T, not a table", v)
		}
		c.Env = map[string]string{}
		for k, e := range t {
			c.Env[k] = fmt.Sprint(e)
		}
	}
	if v, ok := m["pid"]; ok {
		n, ok := v.(int64)
		if !ok {
			return c, fmt.Errorf("pid is %T, not a number", v)
		}
		c.PID = int(n)
	}
	for _, p := range []*string{&c.Program, &c.Cwd} {
		if *p != "" && !filepath.IsAbs(*p) && dir != "" {
			*p = filepath.Join(dir, *p)
		}
	}
	known := []string{"name", "type", "request", "program", "args", "cwd", "env", "pid"}
	for k, v := range m {
		if !slices.Contains(known, k) {
			if c.Extra == nil {
				c.Extra = map[string]any{}
			}
			c.Extra[k] = v
		}
	}
	return c, nil
}

// Builtins are the adapters Oriel knows.
func Builtins() []AdapterSpec {
	const src = "builtin:dap"
	return []AdapterSpec{
		{Name: "dlv", Type: "go", Language: []string{"go"}, Cmd: []string{"dlv", "dap", "--client-addr=${addr}"}, Transport: TransportReverse,
			Launch: map[string]any{"mode": "debug", "outputMode": "remote"}, Attach: map[string]any{"mode": "local"}, Infer: inferGo, Source: src},
		{Name: "debugpy", Type: "python", Language: []string{"python"}, Cmd: []string{"python3", "-m", "debugpy.adapter"},
			Probe:  []string{"python3", "-c", "import debugpy"},
			Launch: map[string]any{"console": "internalConsole", "justMyCode": true}, Infer: inferPython, Source: src},
	}
}

// inferGo debugs a Go file's package: its program when it's package main, otherwise its tests.
func inferGo(file string) (LaunchConfig, bool) {
	if !strings.HasSuffix(file, ".go") {
		return LaunchConfig{}, false
	}
	dir := filepath.Dir(file)
	mode := "test"
	if !strings.HasSuffix(file, "_test.go") && goPackage(file) == "main" {
		mode = "debug"
	}
	name := "Debug " + filepath.Base(dir)
	if mode == "test" {
		name = "Debug the tests in " + filepath.Base(dir)
	}
	return LaunchConfig{Name: name, Type: "go", Program: dir, Cwd: dir, Extra: map[string]any{"mode": mode}}, true
}

// goPackage is a Go file's package name, from its package clause.
func goPackage(file string) string {
	f, err := os.Open(file)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if name, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "package "); ok {
			return strings.Fields(name)[0]
		}
	}
	return ""
}

func inferPython(file string) (LaunchConfig, bool) {
	if !strings.HasSuffix(file, ".py") {
		return LaunchConfig{}, false
	}
	return LaunchConfig{Name: "Debug " + filepath.Base(file), Type: "python", Program: file, Cwd: filepath.Dir(file)}, true
}

// SortSpecs orders adapters as they're tried: by Order, then name.
func SortSpecs(specs []AdapterSpec) {
	sort.SliceStable(specs, func(i, j int) bool {
		if specs[i].Order != specs[j].Order {
			return specs[i].Order < specs[j].Order
		}
		return specs[i].Name < specs[j].Name
	})
}
