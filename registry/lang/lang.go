// Package lang holds the registry's language-tooling contributions: language servers and debug adapters, which
// carry the lsp and dap packages' own types.
package lang

import (
	"cmp"
	"encoding/json"
	"slices"
	"strings"

	"github.com/liamg/oriel/dap"
	"github.com/liamg/oriel/lsp"
	"github.com/liamg/oriel/registry"
)

// LSPServer is a language server: the languages it serves, its command, root markers, initialization
// options and settings. It has lsp.ServerSpec's fields, so each converts to the other; the lsp module stays free
// of the registry.
type LSPServer struct {
	Name     string   // unique among servers: "gopls"
	Language []string // language names, as Language contributions name them
	Cmd      []string
	Roots    []string // root markers, most important first
	Init     json.RawMessage
	Settings json.RawMessage
	Order    int // among the servers for a language, lower is tried first
	Source   string
	Connect  lsp.Launcher // reaches the server some other way than running Cmd: in process, over a socket
}

func (LSPServer) ContributionKind() string   { return "lsp.server" }
func (s LSPServer) ContributionName() string { return s.Name }

// LSPServers are the registered language servers by Order, then name.
func LSPServers(r registry.Contributions) []LSPServer {
	var out []LSPServer
	out = append(out, registry.ContributionsOf[LSPServer](r)...)
	slices.SortFunc(out, func(a, b LSPServer) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), strings.Compare(a.Name, b.Name))
	})
	return out
}

// DebugAdapter is a debug adapter: the languages it debugs, how to start and reach it, and the launch
// arguments its configs start from. It has dap.AdapterSpec's fields, so each converts to the other; the dap module
// stays free of the registry.
type DebugAdapter struct {
	Name      string // unique among adapters: "dlv"
	Type      string // what a launch config's type names: "go"
	Language  []string
	Cmd       []string // ${addr} and ${port} as dap.AdapterSpec says
	Transport string   // stdio, tcp or reverse
	Probe     []string
	Launch    map[string]any
	Attach    map[string]any
	Infer     func(file string) (dap.LaunchConfig, bool)
	Order     int
	Source    string
	Connect   dap.Launcher // reaches the adapter some other way than running Cmd: in process, over a socket
}

func (DebugAdapter) ContributionKind() string   { return "dap.adapter" }
func (a DebugAdapter) ContributionName() string { return a.Name }

// DebugAdapters are the registered debug adapters by Order, then name.
func DebugAdapters(r registry.Contributions) []DebugAdapter {
	var out []DebugAdapter
	out = append(out, registry.ContributionsOf[DebugAdapter](r)...)
	slices.SortFunc(out, func(a, b DebugAdapter) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), strings.Compare(a.Name, b.Name))
	})
	return out
}
