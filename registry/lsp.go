package registry

import (
	"cmp"
	"encoding/json"
	"slices"
	"strings"

	"github.com/liamg/oriel/lsp"
)

// LSPServer is a language server (LS-1): the languages it serves, its command, root markers, initialization
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
func LSPServers(r Registry) []LSPServer {
	var out []LSPServer
	for _, c := range r.Contributions("lsp.server") {
		out = append(out, c.(LSPServer))
	}
	slices.SortFunc(out, func(a, b LSPServer) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), strings.Compare(a.Name, b.Name))
	})
	return out
}
