package registry_test

import (
	"testing"

	"github.com/liamg/oriel/lsp"
	"github.com/liamg/oriel/registry"
)

// LS-1: language servers are contributions; a later one of a name shadows the earlier until disposed.
func TestLSPServers(t *testing.T) {
	r := registry.New()
	for _, s := range lsp.Builtins() {
		r.Contribute(registry.LSPServer(s))
	}
	mine := r.Contribute(registry.LSPServer{Name: "gopls", Language: []string{"go"}, Cmd: []string{"my-gopls"}, Source: "config"})
	var names []string
	var gopls registry.LSPServer
	for _, s := range registry.LSPServers(r) {
		names = append(names, s.Name)
		if s.Name == "gopls" {
			gopls = s
		}
	}
	if len(names) != len(lsp.Builtins()) || names[len(names)-1] != "vtsls" || names[len(names)-2] != "basedpyright" {
		t.Errorf("servers = %q, want the alternatives last", names)
	}
	if gopls.Cmd[0] != "my-gopls" || lsp.ServerSpec(gopls).Source != "config" {
		t.Errorf("gopls = %+v, want the shadowing one", gopls)
	}
	mine.Dispose()
	for _, s := range registry.LSPServers(r) {
		if s.Name == "gopls" && s.Cmd[0] != "gopls" {
			t.Errorf("after dispose gopls = %+v", s)
		}
	}
}
