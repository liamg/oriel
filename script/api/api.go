// Package api is the oriel.* Lua API, over the same host a Go plugin gets. Each namespace is a type
// annotated //oriel:api name=…, and each function a method annotated //oriel:api doc=…; tools/cmd/apigen generates
// the VM glue (zz_bind.go), the LuaLS types (script/types/oriel.d.lua) and the reference (docs/api.md) from them.
package api

//go:generate go run ../../tools/cmd/apigen

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"

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
	a.owned, a.inbox = nil, nil
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
		wasm.Close(context.Background())
	}
}

// check is the guard on a call: the generated glue asks it for each function's capability.
func (a *APIs) check(c string) error { return a.Plugin.Check(plugin.Capability(c), "") }

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
