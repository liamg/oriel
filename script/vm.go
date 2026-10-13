package script

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// The watchdog (scripting.md, "Execution model"): a call that runs past WarnAfter is reported as slow, and one past
// InterruptAfter is stopped with an error. Loading a config gets LoadBudget.
var (
	WarnAfter      = 50 * time.Millisecond
	InterruptAfter = 500 * time.Millisecond
	LoadBudget     = 2 * time.Second
)

// Chunk is Lua source and the name its errors and sources use: "init.lua".
type Chunk struct {
	Name   string
	Source []byte
}

// Fn is a Lua function held from Go: a hook, a keymap, a view.
type Fn struct {
	vm     *VM
	f      *lua.LFunction
	Source string // where it was defined: "init.lua:14"
}

// Table is a Lua table with string keys, as Go sees it.
type Table = map[string]any

// GoFunc is a Go function Lua calls: its arguments, and what it returns.
type GoFunc func(a *Args) ([]any, error)

// Stat is how long a Lua function took, for :Hooks and slow-call warnings.
type Stat struct {
	Source string
	Calls  int
	Total  time.Duration
	Max    time.Duration
	P99    time.Duration
}

// VM is one Lua state, on the UI loop: calls into it are synchronous and never overlap.
type VM struct {
	mu      sync.Mutex
	L       *lua.LState
	stats   map[string]*stat
	OnSlow  func(source string, took time.Duration) // a call past WarnAfter
	OnError func(err *Error)                        // a call's error: a hook's, a view's, a keymap's
	cancel  context.CancelFunc
	paths   []string
	raised  raised                // the API function that raised the error being unwound
	kept    map[string]lua.LValue // State's tables, by key
}

// raised is an oriel API function's error: its name, and the Go stack it came from.
type raised struct {
	name  string
	stack string
}

type stat struct {
	calls int
	total time.Duration
	max   time.Duration
	times []time.Duration
}

// New is a VM with Lua's standard libraries, and require searching paths: each a directory with ?.lua and
// ?/init.lua under it. A module's source name is its path from the directory holding the search path, as
// "lua/my/tickets.lua".
func New(paths ...string) *VM {
	L := lua.NewState(lua.Options{IncludeGoStackTrace: false})
	vm := &VM{L: L, stats: map[string]*stat{}, paths: paths}
	if len(paths) > 0 {
		var ps []string
		for _, p := range paths {
			ps = append(ps, p+"/?.lua", p+"/?/init.lua")
		}
		pkg := L.GetGlobal("package").(*lua.LTable)
		pkg.RawSetString("path", lua.LString(strings.Join(ps, ";")+";"+lua.LVAsString(pkg.RawGetString("path"))))
		if loaders, ok := pkg.RawGetString("loaders").(*lua.LTable); ok {
			loaders.RawSetInt(2, L.NewFunction(vm.loader))
		}
	}
	return vm
}

// loader is require's searcher for Lua files under the paths.
func (vm *VM) loader(L *lua.LState) int {
	name := strings.ReplaceAll(L.CheckString(1), ".", "/")
	var tried []string
	for _, p := range vm.paths {
		for _, f := range []string{filepath.Join(p, name+".lua"), filepath.Join(p, name, "init.lua")} {
			src, err := os.ReadFile(f)
			if err != nil {
				tried = append(tried, "\n\tno file '"+f+"'")
				continue
			}
			chunk := filepath.ToSlash(f)
			if rel, err := filepath.Rel(filepath.Dir(p), f); err == nil {
				chunk = filepath.ToSlash(rel)
			}
			fn, err := L.Load(strings.NewReader(string(src)), chunk)
			if err != nil {
				L.RaiseError("%s", atEOF(err.Error(), chunk, src))
			}
			L.Push(fn)
			return 1
		}
	}
	L.Push(lua.LString(strings.Join(tried, "")))
	return 1
}

// atEOF names the line of a syntax error at the end of a chunk, which the parser doesn't.
func atEOF(msg, chunk string, src []byte) string {
	if !strings.Contains(msg, chunk+" at EOF") {
		return msg
	}
	n := strings.Count(strings.TrimRight(string(src), "\n"), "\n") + 1
	return strings.Replace(msg, chunk+" at EOF", fmt.Sprintf("%s line:%d at EOF", chunk, n), 1)
}

// Close frees the state.
func (vm *VM) Close() { vm.L.Close() }

// Interrupt stops whatever is running, with reason as its error.
func (vm *VM) Interrupt(reason string) {
	if vm.cancel != nil {
		vm.cancel()
	}
}

// guard runs fn with the watchdog's deadline on the state.
func (vm *VM) guard(budget time.Duration, fn func() error) error {
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	vm.cancel = cancel
	vm.L.SetContext(ctx)
	defer func() {
		cancel()
		vm.L.RemoveContext()
		vm.cancel = nil
	}()
	err := fn()
	if err != nil && ctx.Err() != nil {
		return &Error{Message: fmt.Sprintf("interrupted: it ran past %v", budget)}
	}
	return vm.luaError(err)
}

// Load runs a chunk: a config file, a line typed at :lua.
func (vm *VM) Load(c Chunk) error {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	fn, err := vm.L.Load(strings.NewReader(string(c.Source)), c.Name)
	if err != nil {
		return &Error{Message: strings.TrimSpace(atEOF(err.Error(), c.Name, c.Source))}
	}
	return vm.guard(LoadBudget, func() error {
		vm.L.Push(fn)
		return vm.L.PCall(0, lua.MultRet, nil)
	})
}

// Eval runs a chunk and returns what it returns: the :lua REPL tries "return <line>" first.
func (vm *VM) Eval(c Chunk) ([]any, error) {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	fn, err := vm.L.Load(strings.NewReader(string(c.Source)), c.Name)
	if err != nil {
		return nil, &Error{Message: strings.TrimSpace(atEOF(err.Error(), c.Name, c.Source))}
	}
	top := vm.L.GetTop()
	err = vm.guard(InterruptAfter, func() error {
		vm.L.Push(fn)
		return vm.L.PCall(0, lua.MultRet, nil)
	})
	if err != nil {
		return nil, err
	}
	var out []any
	for i := top + 1; i <= vm.L.GetTop(); i++ {
		out = append(out, vm.toGo(vm.L.Get(i), 0))
	}
	vm.L.SetTop(top)
	return out, nil
}

// Call calls a Lua function with Go values, and returns what it returns as Go values. Calls from inside a Lua
// call (a Go function calling back into Lua) run on the same state.
func (vm *VM) Call(fn *Fn, args ...any) ([]any, error) {
	nested := !vm.mu.TryLock()
	if !nested {
		defer vm.mu.Unlock()
	}
	start := time.Now()
	call := func() error {
		vm.L.Push(fn.f)
		for _, a := range args {
			vm.L.Push(vm.toLua(a))
		}
		return vm.L.PCall(len(args), lua.MultRet, nil)
	}
	top := vm.L.GetTop()
	var err error
	if nested {
		err = vm.luaError(call())
	} else {
		err = vm.guard(InterruptAfter, call)
	}
	took := time.Since(start)
	vm.record(fn.Source, took)
	if took > WarnAfter && vm.OnSlow != nil && !nested {
		vm.OnSlow(fn.Source, took)
	}
	if err != nil {
		vm.L.SetTop(top)
		if e, ok := err.(*Error); ok && !nested && vm.OnError != nil {
			if e.Where() == "" {
				e.At = fn.Source
			}
			vm.OnError(e)
		}
		return nil, err
	}
	var out []any
	for i := top + 1; i <= vm.L.GetTop(); i++ {
		out = append(out, vm.toGo(vm.L.Get(i), 0))
	}
	vm.L.SetTop(top)
	return out, nil
}

func (vm *VM) record(src string, took time.Duration) {
	s := vm.stats[src]
	if s == nil {
		s = &stat{}
		vm.stats[src] = s
	}
	s.calls++
	s.total += took
	s.max = max(s.max, took)
	s.times = append(s.times, took)
	if len(s.times) > 256 {
		s.times = s.times[1:]
	}
}

// Stats are every function's call times, by where it's defined.
func (vm *VM) Stats() []Stat {
	var out []Stat
	for src, s := range vm.stats {
		ts := slices.Clone(s.times)
		slices.Sort(ts)
		p := time.Duration(0)
		if len(ts) > 0 {
			p = ts[min(len(ts)-1, len(ts)*99/100)]
		}
		out = append(out, Stat{Source: src, Calls: s.calls, Total: s.total, Max: s.max, P99: p})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Source < out[j].Source })
	return out
}

// Bind puts Go functions in the oriel table: under ns ("keymap" → oriel.keymap.set), or at its top for "". The
// table is the global oriel.
func (vm *VM) Bind(ns string, fns map[string]GoFunc) {
	t := vm.namespace(ns)
	for name, fn := range fns {
		t.RawSetString(name, vm.L.NewFunction(vm.wrap(ns, name, fn)))
	}
}

// SetValue puts a plain value in a namespace: oriel.api.version.
func (vm *VM) SetValue(ns, name string, v any) { vm.namespace(ns).RawSetString(name, vm.toLua(v)) }

// namespace is oriel.<ns>, made if it isn't there; a dotted ns nests.
func (vm *VM) namespace(ns string) *lua.LTable {
	root, ok := vm.L.GetGlobal("oriel").(*lua.LTable)
	if !ok {
		root = vm.L.NewTable()
		vm.L.SetGlobal("oriel", root)
	}
	t := root
	if ns == "" {
		return t
	}
	for _, part := range strings.Split(ns, ".") {
		sub, ok := t.RawGetString(part).(*lua.LTable)
		if !ok {
			sub = vm.L.NewTable()
			t.RawSetString(part, sub)
		}
		t = sub
	}
	return t
}

// Future makes oriel.<ns> a namespace for a feature that isn't here yet: calling anything in it is an error that
// says which ticket brings it, rather than the config failing to load.
func (vm *VM) Future(ns, ticket string) {
	t := vm.namespace(ns)
	mt := vm.L.NewTable()
	mt.RawSetString("__index", vm.L.NewFunction(func(L *lua.LState) int {
		name := L.CheckString(2)
		L.Push(L.NewFunction(func(L *lua.LState) int {
			L.RaiseError("oriel.%s.%s isn't available until %s", ns, name, ticket)
			return 0
		}))
		return 1
	}))
	vm.L.SetMetatable(t, mt)
}

// Metatable gives oriel.<ns> __index and __newindex in Go: options tables (oriel.option) read and write the registry.
func (vm *VM) Metatable(ns string, get func(a *Args, key string) (any, error), set func(a *Args, key string, v any) error) {
	t := vm.namespace(ns)
	mt := vm.L.NewTable()
	mt.RawSetString("__index", vm.L.NewFunction(func(L *lua.LState) int {
		v, err := get(&Args{vm: vm, L: L, base: 1}, L.CheckString(2))
		if err != nil {
			L.RaiseError("%s", err.Error())
		}
		L.Push(vm.toLua(v))
		return 1
	}))
	mt.RawSetString("__newindex", vm.L.NewFunction(func(L *lua.LState) int {
		if err := set(&Args{vm: vm, L: L, base: 1}, L.CheckString(2), vm.toGo(L.Get(3), 0)); err != nil {
			L.RaiseError("%s", err.Error())
		}
		return 0
	}))
	vm.L.SetMetatable(t, mt)
}

func (vm *VM) wrap(ns, name string, fn GoFunc) lua.LGFunction {
	full := "oriel." + name
	if ns != "" {
		full = "oriel." + ns + "." + name
	}
	return func(L *lua.LState) int {
		a := &Args{vm: vm, L: L, name: full}
		out, err, stack := callGo(fn, a)
		if err != nil {
			vm.raised = raised{name: full, stack: stack}
			L.RaiseError("%s: %s", full, err.Error())
			return 0
		}
		for _, v := range out {
			L.Push(vm.toLua(v))
		}
		return len(out)
	}
}

// callGo runs a Go function for Lua, a panic in it as an error; a failure comes with the Go stack.
func callGo(fn GoFunc, a *Args) (out []any, err error, stack string) {
	defer func() {
		if v := recover(); v != nil {
			out, err, stack = nil, fmt.Errorf("panic: %v", v), string(debug.Stack())
		}
	}()
	out, err = fn(a)
	if err != nil {
		stack = string(debug.Stack())
	}
	return out, err, stack
}

// Args are a Go function's arguments from Lua, read by position (0 is the first).
type Args struct {
	vm   *VM
	L    *lua.LState
	name string
	base int // arguments start after this many (a metamethod's table)
}

func (a *Args) get(i int) lua.LValue { return a.L.Get(a.base + i + 1) }

// Len is how many arguments there are.
func (a *Args) Len() int { return a.L.GetTop() - a.base }

func (a *Args) String(i int) string {
	v := a.get(i)
	if s, ok := v.(lua.LString); ok {
		return string(s)
	}
	if n, ok := v.(lua.LNumber); ok {
		return n.String()
	}
	return ""
}

func (a *Args) Int(i int) int {
	if n, ok := a.get(i).(lua.LNumber); ok {
		return int(n)
	}
	return 0
}

func (a *Args) Float(i int) float64 {
	if n, ok := a.get(i).(lua.LNumber); ok {
		return float64(n)
	}
	return 0
}

func (a *Args) Bool(i int) bool { return lua.LVAsBool(a.get(i)) }

// Any is an argument as a Go value: nil, bool, float64, string, Table, []any or *Fn.
func (a *Args) Any(i int) any { return a.vm.toGo(a.get(i), 0) }

// Fn is a function argument, or nil.
func (a *Args) Fn(i int) *Fn {
	f, _ := a.vm.toGo(a.get(i), 0).(*Fn)
	return f
}

// Table is a table argument, or nil.
func (a *Args) Table(i int) Table {
	t, _ := a.vm.toGo(a.get(i), 0).(Table)
	return t
}

// Strings is a string or a list of them.
func (a *Args) Strings(i int) []string {
	switch v := a.vm.toGo(a.get(i), 0).(type) {
	case string:
		return []string{v}
	case []any:
		var out []string
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// Where is the Lua line calling: "init.lua:12".
func (a *Args) Where() string { return where(a.L, 1) }

func where(L *lua.LState, level int) string {
	w := strings.TrimSuffix(L.Where(level), ":")
	return strings.TrimPrefix(w, "@")
}

// VM is the VM the call is in.
func (a *Args) VM() *VM { return a.vm }

// toLua makes a Go value a Lua one.
func (vm *VM) toLua(v any) lua.LValue {
	switch v := v.(type) {
	case nil:
		return lua.LNil
	case lua.LValue:
		return v
	case bool:
		return lua.LBool(v)
	case string:
		return lua.LString(v)
	case int:
		return lua.LNumber(v)
	case int64:
		return lua.LNumber(v)
	case float32:
		return lua.LNumber(v)
	case float64:
		return lua.LNumber(v)
	case time.Duration:
		return lua.LNumber(v.Seconds())
	case *Fn:
		return v.f
	case GoFunc:
		return vm.L.NewFunction(vm.wrap("", "fn", v))
	case []string:
		t := vm.L.NewTable()
		for _, s := range v {
			t.Append(lua.LString(s))
		}
		return t
	case []any:
		t := vm.L.NewTable()
		for _, x := range v {
			t.Append(vm.toLua(x))
		}
		return t
	case map[string]any:
		t := vm.L.NewTable()
		for k, x := range v {
			t.RawSetString(k, vm.toLua(x))
		}
		return t
	case map[string]string:
		t := vm.L.NewTable()
		for k, x := range v {
			t.RawSetString(k, lua.LString(x))
		}
		return t
	case error:
		return lua.LString(v.Error())
	}
	return lua.LString(fmt.Sprint(v))
}

// toGo makes a Lua value a Go one: a table with only 1..n keys is a []any, any other a Table.
func (vm *VM) toGo(v lua.LValue, depth int) any {
	switch v := v.(type) {
	case *lua.LNilType:
		return nil
	case lua.LBool:
		return bool(v)
	case lua.LNumber:
		return float64(v)
	case lua.LString:
		return string(v)
	case *lua.LFunction:
		return &Fn{vm: vm, f: v, Source: fnSource(v)}
	case *lua.LTable:
		if depth > 32 {
			return nil
		}
		if n := v.Len(); n > 0 {
			list := make([]any, 0, n)
			for i := 1; i <= n; i++ {
				list = append(list, vm.toGo(v.RawGetInt(i), depth+1))
			}
			extra := false
			v.ForEach(func(k, _ lua.LValue) {
				if _, ok := k.(lua.LNumber); !ok {
					extra = true
				}
			})
			if !extra {
				return list
			}
		}
		t := Table{}
		v.ForEach(func(k, x lua.LValue) {
			t[lua.LVAsString(k)] = vm.toGo(x, depth+1)
		})
		return t
	}
	return nil
}

func fnSource(f *lua.LFunction) string {
	if f.Proto == nil {
		return "go"
	}
	return fmt.Sprintf("%s:%d", strings.TrimPrefix(f.Proto.SourceName, "@"), f.Proto.LineDefined)
}

// State is a table kept by key for as long as the VM lives: the same table each call, made from initial (a table,
// or empty) the first time. A view keeps what it shows across redraws in one.
func (vm *VM) State(key string, initial any) any {
	if v, ok := vm.kept[key]; ok {
		return v
	}
	t, ok := vm.toLua(initial).(*lua.LTable)
	if !ok {
		t = vm.L.NewTable()
	}
	if vm.kept == nil {
		vm.kept = map[string]lua.LValue{}
	}
	vm.kept[key] = t
	return t
}

// Keys are the names in oriel.<ns>, for the REPL's completion.
func (vm *VM) Keys(path string) []string {
	var t lua.LValue = vm.L.GetGlobal("oriel")
	if path != "" {
		for _, p := range strings.Split(path, ".") {
			tt, ok := t.(*lua.LTable)
			if !ok {
				return nil
			}
			t = tt.RawGetString(p)
		}
	}
	tt, ok := t.(*lua.LTable)
	if !ok {
		return nil
	}
	var out []string
	tt.ForEach(func(k, _ lua.LValue) {
		if s, ok := k.(lua.LString); ok {
			out = append(out, string(s))
		}
	})
	sort.Strings(out)
	return out
}

// Error is a Lua error: its message, "init.lua:56: oriel.ledger.frac isn't available…", and its traceback.
type Error struct {
	Message   string
	Traceback string // Lua's: the oriel API function that raised it named, gopher-lua's own frames left out
	GoStack   string // the Go stack under the API function that raised it
	At        string // where it is, when neither the message nor the traceback says: "init.lua:12"
}

func (e *Error) Error() string { return e.Message }

var (
	syntaxAt  = regexp.MustCompile(`([\w./-]+\.lua) line:(\d+)`)
	messageAt = regexp.MustCompile(`^([\w./-]+\.lua):(\d+):`)
)

// Where is the line the error is on: a syntax error's, the message's, or the innermost Lua frame's.
func (e *Error) Where() string {
	if m := syntaxAt.FindStringSubmatch(e.Message); m != nil {
		return m[1] + ":" + m[2]
	}
	if m := messageAt.FindStringSubmatch(e.Message); m != nil {
		return m[1] + ":" + m[2]
	}
	for _, f := range e.Frames() {
		if !f.Go && f.Line > 0 {
			return f.Loc()
		}
	}
	return e.At
}

// Trace is the traceback and, after it, the Go stack.
func (e *Error) Trace() string {
	if e.GoStack == "" {
		return e.Traceback
	}
	return e.Traceback + "\n" + e.GoStack
}

// Frames are the trace's frames, innermost first: Lua's and the API's, then Go's.
func (e *Error) Frames() []Frame { return ParseTrace(e.Trace()) }

// luaError makes gopher-lua's error an *Error, its message apart from its traceback.
func (vm *VM) luaError(err error) error {
	var ae *lua.ApiError
	if !errors.As(err, &ae) {
		return err
	}
	msg := lua.LVAsString(ae.Object)
	if ae.Object == nil || ae.Object == lua.LNil {
		msg = ae.Error()
	}
	if i := strings.Index(msg, "\nstack traceback:"); i >= 0 {
		msg = msg[:i]
	}
	e := &Error{Message: strings.TrimSpace(msg)}
	r := vm.raised
	vm.raised = raised{}
	var tb []string
	for _, ln := range strings.Split(ae.StackTrace, "\n") {
		if !strings.HasPrefix(ln, "\t[G]:") {
			tb = append(tb, ln)
			continue
		}
		short := r.name[strings.LastIndexByte(r.name, '.')+1:]
		if r.name != "" && strings.TrimSpace(ln) == "[G]: in function '"+short+"'" {
			tb = append(tb, "\t"+r.name+": in function '"+short+"'")
			e.GoStack = r.stack
			r = raised{}
		}
	}
	e.Traceback = strings.TrimRight(strings.Join(tb, "\n"), "\n")
	return e
}
