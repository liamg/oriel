package lang_test

import (
	"testing"

	"github.com/liamg/oriel/dap"
	"github.com/liamg/oriel/lsp"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/lang"
)

// Language servers are contributions; a later one of a name shadows the earlier until disposed.
func TestLSPServers(t *testing.T) {
	r := registry.New()
	for _, s := range lsp.Builtins() {
		r.Contribute(lang.LSPServer(s))
	}
	mine := r.Contribute(lang.LSPServer{Name: "gopls", Language: []string{"go"}, Cmd: []string{"my-gopls"}, Source: "config"})
	var names []string
	var gopls lang.LSPServer
	for _, s := range lang.LSPServers(r) {
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
	for _, s := range lang.LSPServers(r) {
		if s.Name == "gopls" && s.Cmd[0] != "gopls" {
			t.Errorf("after dispose gopls = %+v", s)
		}
	}
}

// Debug adapters are contributions; a later one of a name shadows the earlier until disposed.
func TestDebugAdapters(t *testing.T) {
	r := registry.New()
	for _, s := range dap.Builtins() {
		r.Contribute(lang.DebugAdapter(s))
	}
	mine := r.Contribute(lang.DebugAdapter{Name: "dlv", Type: "go", Cmd: []string{"my-dlv"}, Source: "config"})
	var dlv lang.DebugAdapter
	for _, a := range lang.DebugAdapters(r) {
		if a.Name == "dlv" {
			dlv = a
		}
	}
	if len(lang.DebugAdapters(r)) != len(dap.Builtins()) || dlv.Cmd[0] != "my-dlv" || dap.AdapterSpec(dlv).Source != "config" {
		t.Errorf("dlv = %+v, want the shadowing one", dlv)
	}
	mine.Dispose()
	for _, a := range lang.DebugAdapters(r) {
		if a.Name == "dlv" && a.Cmd[0] != "dlv" {
			t.Errorf("after dispose dlv = %+v", a)
		}
	}
}
