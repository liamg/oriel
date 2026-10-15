package api

import (
	"encoding/json"
	"fmt"

	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
)

//oriel:api name=lsp cap=exec
type LSP struct{ a *APIs }

//oriel:api doc="Register a language server: { name, languages = { \"go\" }, cmd = { \"gopls\" }, roots = { \"go.mod\" }, settings = {…}, init = {…}, order }. One with a built-in's name replaces it; order picks among a language's servers, lowest first."
func (l LSP) Server(spec script.Table, where string) error {
	s := registry.LSPServer{Name: str(spec, "name"), Language: strs(spec["languages"]), Cmd: strs(spec["cmd"]), Roots: strs(spec["roots"]),
		Source: where}
	if s.Name == "" || len(s.Language) == 0 || len(s.Cmd) == 0 {
		return fmt.Errorf("a language server needs a name, languages and a cmd")
	}
	if !l.a.Plugin.Trusted() {
		if err := l.a.Check(plugin.CapExec, s.Cmd[0]); err != nil {
			return err
		}
	}
	if o, ok := spec["order"].(float64); ok {
		s.Order = int(o)
	}
	for k, dst := range map[string]*json.RawMessage{"settings": &s.Settings, "init": &s.Init} {
		if v, ok := spec[k]; ok {
			b, err := json.Marshal(v)
			if err != nil {
				return fmt.Errorf("%s: %v", k, err)
			}
			*dst = b
		}
	}
	l.a.own(l.a.Host.Contribute(s))
	return nil
}
