package api

import (
	"encoding/json"
	"fmt"

	"github.com/liamg/oriel/lsp"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry/lang"
	"github.com/liamg/oriel/script"
)

//oriel:api name=lsp cap=exec
type LSP struct{ a *APIs }

//oriel:api doc="Register a language server: { name, languages = { 'go' }, cmd = { 'gopls' }, roots = { 'go.mod' }, settings = {…}, init = {…}, order, install = { tool = 'npm' or 'go', packages = {…} } }. One with a built-in's name replaces it; order picks among a language's servers, lowest first. cmd is looked for in the project's node_modules/.bin, then on PATH, then where Oriel installed it; install lets Oriel offer to install it when it's found nowhere, which needs exec for the tool too."
func (l LSP) Server(spec script.Table, where string) error {
	s := lang.LSPServer{Name: str(spec, "name"), Language: plugin.Strings(spec["languages"]), Cmd: plugin.Strings(spec["cmd"]), Roots: plugin.Strings(spec["roots"]),
		Source: where}
	if s.Name == "" || len(s.Language) == 0 || len(s.Cmd) == 0 {
		return fmt.Errorf("a language server needs a name, languages and a cmd")
	}
	prog, err := l.a.Plugin.CheckProgram(s.Cmd[0], "")
	if err != nil {
		return err
	}
	s.Cmd[0] = prog // a path the plugin's, not the project's the server runs in
	if o, ok := spec["order"].(float64); ok {
		s.Order = int(o)
	}
	if in, ok := spec["install"].(script.Table); ok {
		tool := str(in, "tool")
		if tool != lsp.ToolNPM && tool != lsp.ToolGo {
			return fmt.Errorf("%s: install's tool is npm or go, not %q", s.Name, tool)
		}
		if _, err := l.a.Plugin.CheckProgram(tool, ""); err != nil {
			return err
		}
		s.Install = &lsp.Install{Tool: tool, Packages: plugin.Strings(in["packages"])}
		if len(s.Install.Packages) == 0 {
			return fmt.Errorf("%s: install needs its packages", s.Name)
		}
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
