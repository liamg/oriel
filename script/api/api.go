// Package api is the oriel.* Lua API, over the same host a Go plugin gets. Each namespace is a type
// annotated //oriel:api name=…, and each function a method annotated //oriel:api doc=…; tools/cmd/apigen generates
// the VM glue (zz_bind.go), the LuaLS types (script/types/oriel.d.lua) and the reference (docs/api.md) from them.
package api

//go:generate go run ../../tools/cmd/apigen

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"

	"github.com/liamg/oriel/core"

	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
	"github.com/liamg/oriel/ui"
)

// Version is oriel.api.version: a breaking change bumps its major.
const Version = "0.6.0"

// futures are namespaces, or the rest of one, for features that aren't built yet: calling into one says what
// it waits on, so a config written for them loads.
var futures = map[string]string{
	"http":  "network approvals", // what oriel.http lacks yet: request is here, a blocking get isn't
	"async": "a later release (timers, processes and RPC from Lua)",
}

// APIs is the API for one VM over a host. What a config registers is kept, so a reload drops it all first.
type APIs struct {
	Host   plugin.Host
	VM     *script.VM
	Plugin plugin.Caller // whose VM it is: every call is checked against its grants; your config's is trusted

	procs *plugin.Processes // its process plugins, started on first use
	wasm  *plugin.WASM
	hostm *plugin.Methods

	mu      sync.Mutex
	owned   []registry.Disposable
	keyed   map[string]bool // what's owned once however often it's set: diagnostics and coverage, by owner and file
	n       int
	pending map[string]pendingOption     // options set before anything registered them
	touched map[optKey]optPrev           // options the config set, and what they held before
	shown   map[string]any               // oriel.hud.set's nodes, by indicator
	inbox   *luaInbox                    // oriel.inbox's cards
	waiting map[int]chan error           // awaited calls not answered yet, failed by an error in a later callback
	loading *luaLoads                    // oriel.load's handles
	part    func(from, to int, id ui.ID) // the presenter block being drawn's PresentDraw.Part, while it's drawn
}

// Install puts the API in vm, for your own config: trusted, its files in dir.
func Install(vm *script.VM, h plugin.Host, dir string) *APIs {
	return InstallPlugin(vm, h, plugin.Caller{ID: "init.lua", Dir: dir})
}

// InstallPlugin puts the API in vm for a plugin: each call it makes is checked against p's grants.
func InstallPlugin(vm *script.VM, h plugin.Host, p plugin.Caller) *APIs {
	a := &APIs{Host: h, VM: vm, Plugin: p, pending: map[string]pendingOption{}, touched: map[optKey]optPrev{}}
	a.bind(vm)
	a.options(vm)
	vm.SetValue("api", "version", Version)
	vm.SetValue("", "platform", runtime.GOOS)
	for ns, ticket := range futures {
		vm.Future(ns, ticket)
	}
	return a
}

// Dispose drops everything the config registered: keymaps, commands, hooks, views and the rest, and stops its
// processes and WASM modules.
func (a *APIs) Dispose() {
	a.mu.Lock()
	owned := a.owned
	a.owned, a.inbox, a.keyed = nil, nil, nil
	procs, wasm := a.procs, a.wasm
	a.procs, a.wasm = nil, nil
	a.mu.Unlock()
	for _, d := range owned {
		d.Dispose()
	}
	if procs != nil {
		procs.Close()
	}
	if wasm != nil {
		_ = wasm.Close(context.Background())
	}
}

// check is the guard on a call: the generated glue asks it for each function's capability.
func (a *APIs) check(c plugin.Capability) error { return a.Plugin.Check(c, "") }

// callbackFailed tells the user a plugin's Lua callback failed: where, how it got there, and an action opening the
// line.
func (a *APIs) callbackFailed(fn *script.Fn, err error) {
	n := core.Notice{ID: "lua.callback." + fn.Source, Severity: core.SevError, Source: fn.Source, Title: "Lua", Text: err.Error()}
	a.Host.Notify(script.WithTrace(n, err, a.Plugin.Dir, "Open at line"))
}

// callOnce calls fn, a callback answering something that happened once (a reply, a process's exit), and says why it
// failed in a notice (callbackFailed). A plugin stopped since isn't called, and that's no failure.
func (a *APIs) callOnce(fn *script.Fn, args ...any) {
	if _, err := a.VM.Call(fn, args...); err != nil && !errors.Is(err, script.ErrClosed) {
		a.callbackFailed(fn, err)
	}
}

// run runs command id for a card's click, as a key would run it, and says why it failed in a notice from the plugin.
func (a *APIs) run(id string, args registry.Args) {
	if err := a.Host.Run(context.Background(), id, args); err != nil {
		a.Host.Notify(core.Notice{ID: "lua.run." + id, Severity: core.SevError, Source: a.Plugin.Dir, Title: "Lua",
			Text: id + ": " + err.Error(), AutoClear: true})
	}
}

// contributed calls fn, a callback of kind's contribution id that runs as Oriel needs it (a badge each frame, a
// gate's check), and reports how it went to the registry's fault guard: an error shows among the plugin's failures,
// and enough in a row turn the contribution off. ok is false on an error. A callback answering something the user did
// once (a click, a request's answer) says why it failed in a notice instead (callbackFailed).
func (a *APIs) contributed(kind, id string, fn *script.Fn, args ...any) (out []any, ok bool) {
	out, err := a.VM.Call(fn, args...)
	if errors.Is(err, script.ErrClosed) {
		return nil, false
	}
	a.Host.Report(kind, id, err)
	return out, err == nil
}

// commandGrant refuses to run command id with args for a plugin without the capability the command names
// (Command.Grant, checked against what its GrantOn says), and for any plugin when the command is the user's own
// (Command.UserOnly); and the same for each command its {then} runs after it. The user's config runs anything.
func (a *APIs) commandGrant(id string, args registry.Args) error {
	if c, ok := a.Host.CommandFor(id); ok {
		switch {
		case c.UserOnly && !a.Plugin.Trusted():
			return fmt.Errorf("%s: only you can run %s, not a plugin", a.Plugin.ID, id)
		case c.Grant != "" && c.Grant != registry.Open:
			target := ""
			if c.GrantOn != nil {
				target = c.GrantOn(args)
			}
			if err := a.Plugin.Check(plugin.Capability(c.Grant), target); err != nil {
				return fmt.Errorf("%s: %w", id, err)
			}
		}
	}
	for _, t := range thenCalls(args["then"]) {
		if err := a.commandGrant(t.id, t.args); err != nil {
			return err
		}
	}
	return nil
}

type thenCall struct {
	id   string
	args registry.Args
}

// thenCalls are the commands a command's {then} runs after it: a list of {command, args}.
func thenCalls(v any) []thenCall {
	list, _ := v.([]any)
	var out []thenCall
	for _, it := range list {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		id, _ := m["command"].(string)
		args, _ := m["args"].(map[string]any)
		out = append(out, thenCall{id: id, args: args})
	}
	return out
}

// Check asks the guard whether the VM's plugin may use c on target.
func (a *APIs) Check(c plugin.Capability, target string) error { return a.Plugin.Check(c, target) }

func (a *APIs) setIndicator(name string, v any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.shown == nil {
		a.shown = map[string]any{}
	}
	a.shown[name] = v
}

// takeIndicator is the nodes set for name, and whether there were any; once taken from one built by a function
// (once), they're gone.
func (a *APIs) takeIndicator(name string, once bool) (any, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	v, ok := a.shown[name]
	if ok && once {
		delete(a.shown, name)
	}
	return v, ok
}

// Registered is how many things the config has registered.
func (a *APIs) Registered() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.owned)
}

func (a *APIs) own(d registry.Disposable) {
	a.mu.Lock()
	a.owned = append(a.owned, d)
	a.mu.Unlock()
}

// ownOnce owns d unless something under key already is: what's set again and again needs one undoing.
func (a *APIs) ownOnce(key string, d registry.Disposable) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.keyed[key] {
		return
	}
	if a.keyed == nil {
		a.keyed = map[string]bool{}
	}
	a.keyed[key] = true
	a.owned = append(a.owned, d)
}

// scoped is a diagnostics or coverage owner a plugin names, under the plugin's id: it can't reach a built-in's owner
// of the same name (lua, tests, an LSP server's).
func (a *APIs) scoped(owner string) string { return a.Plugin.ID + ":" + owner }

// anon names a Lua function a command so keymaps can run it: "lua.init.lua:10".
func (a *APIs) anon(fn *script.Fn) string {
	a.mu.Lock()
	a.n++
	id := fmt.Sprintf("lua.%d", a.n)
	a.mu.Unlock()
	a.own(a.Host.Command(registry.Command{ID: id, Title: "Lua function at " + fn.Source, Source: fn.Source,
		Run: func(_ registry.Ctx, args registry.Args) error {
			_, err := a.VM.Call(fn, map[string]any(args))
			return err
		}}))
	return id
}

// optsAndFn reads a call's options and its function: options go last (a, fn, opts), and a call may give them before
// the function instead (a, opts, fn), as oriel.on and oriel.exec_async always have. Options are a table or nothing;
// anything else is a mistake in the call.
func optsAndFn(first, second any) (script.Table, *script.Fn, error) {
	opts, f := second, (*script.Fn)(nil)
	if fn, ok := first.(*script.Fn); ok {
		f = fn
	} else if fn, ok := second.(*script.Fn); ok {
		opts, f = first, fn
	}
	if f == nil {
		return nil, nil, errors.New("no function")
	}
	switch o := opts.(type) {
	case nil:
		return nil, f, nil
	case script.Table:
		return o, f, nil
	case []any:
		if len(o) == 0 {
			return nil, f, nil
		}
	}
	return nil, nil, fmt.Errorf("options are a table, not %T", opts)
}

// specOr reads a contribution given as its spec table, { <key> = name, run = fn }, the canonical shape, or as
// (name, fn). Either way it needs both.
func specOr(first any, fn *script.Fn, key string) (string, *script.Fn, error) {
	var name string
	switch v := first.(type) {
	case string:
		name = v
	case script.Table:
		name = str(v, key)
		if run, ok := v["run"].(*script.Fn); ok && fn == nil {
			fn = run
		}
	default:
		return "", nil, fmt.Errorf("a %s or a spec table { %s, run = fn }, not %T", key, key, first)
	}
	if name == "" || fn == nil {
		return "", nil, fmt.Errorf("it needs a %s and a function: { %s, run = fn }, or (%s, fn)", key, key, key)
	}
	return name, fn, nil
}

// str reads a string field of a table.
func str(t script.Table, k string) string {
	s, _ := t[k].(string)
	return s
}

func level(s string) string {
	switch strings.ToLower(s) {
	case "error", "warn", "warning", "debug":
		return strings.ToLower(s)
	}
	return "info"
}
