// Package api is the oriel.* Lua API (scripting.md), over the same host a Go plugin gets. Each namespace is a type
// annotated //oriel:api name=…, and each function a method annotated //oriel:api doc=…; tools/cmd/apigen generates
// the VM glue (zz_bind.go), the LuaLS types (script/types/oriel.d.lua) and the reference (docs/api.md) from them.
package api

//go:generate go run ../../tools/cmd/apigen

import (
	"fmt"
	"strings"
	"sync"

	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
)

// Version is oriel.api.version: a breaking change bumps its major (scripting.md, "Stability").
const Version = "0.4.0"

// futures are namespaces for features that aren't built yet: calling into one says which ticket brings it, so a
// config written for them loads (PL-1).
var futures = map[string]string{
	"thread":   "AI-5 (requests and threads)",
	"lsp":      "LS-1 (language servers)",
	"ledger":   "RV-2 (the review ledger)",
	"linear":   "PL-8 (Linear)",
	"coverage": "TR-4 (coverage)",
	"http":     "AI-7 (network approvals)",
	"async":    "a later ticket (timers, processes and RPC from Lua)",
}

// APIs is the API for one VM over a host. What a config registers is kept, so a reload drops it all first.
type APIs struct {
	Host ext.Host
	VM   *script.VM

	mu      sync.Mutex
	owned   []registry.Disposable
	n       int
	pending map[string]pendingOption // options set before anything registered them
	touched map[optKey]optPrev       // options the config set, and what they held before
}

// Install puts the API in vm.
func Install(vm *script.VM, h ext.Host) *APIs {
	a := &APIs{Host: h, VM: vm, pending: map[string]pendingOption{}, touched: map[optKey]optPrev{}}
	a.bind(vm)
	a.options(vm)
	vm.SetValue("api", "version", Version)
	for ns, ticket := range futures {
		vm.Future(ns, ticket)
	}
	return a
}

// Dispose drops everything the config registered: keymaps, commands, hooks, views and the rest.
func (a *APIs) Dispose() {
	a.mu.Lock()
	owned := a.owned
	a.owned = nil
	a.mu.Unlock()
	for _, d := range owned {
		d.Dispose()
	}
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

func strs(v any) []string {
	switch v := v.(type) {
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

func level(s string) string {
	switch strings.ToLower(s) {
	case "error", "warn", "warning", "debug":
		return strings.ToLower(s)
	}
	return "info"
}
