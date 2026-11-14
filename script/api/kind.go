package api

import (
	"cmp"
	"encoding/json"
	"fmt"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
)

// luaKind is oriel.session.kind's spec as a SessionKind, its functions run in the VM.
func luaKind(a *APIs, name string, spec script.Table) (registry.Kind, error) {
	if name == "" {
		return registry.Kind{}, fmt.Errorf("a session kind needs a name")
	}
	k := registry.Kind{KindSpec: core.KindSpec{Kind: core.Kind(name), Title: cmp.Or(str(spec, "title"), name), Desc: str(spec, "desc"),
		About: str(spec, "about"), From: core.From(str(spec, "from")), Pack: cmp.Or(str(spec, "pack"), name),
		Worktrees:  core.Worktrees(cmp.Or(str(spec, "worktrees"), string(core.WorktreesNow))),
		PromptFile: str(spec, "prompt_file"), AgentGate: str(spec, "agent_gate"), Order: 100}, GateNames: plugin.Strings(spec["gates"])}
	k.NeedsRepo, _ = spec["needs_repo"].(bool)
	k.Launcher, _ = spec["launcher"].(bool)
	if o, ok := spec["order"].(float64); ok {
		k.Order = int(o)
	}
	for _, c := range plugin.Strings(spec["can"]) {
		k.Caps = append(k.Caps, core.Capability(c))
	}
	if opens, ok := spec["opens"].([]any); ok {
		for _, o := range opens {
			if t, ok := o.(script.Table); ok && str(t, "command") != "" {
				k.Opens = append(k.Opens, core.KindCall{Command: str(t, "command"), Args: tableOf(t["args"])})
			}
		}
	}
	if fn, ok := spec["label"].(*script.Fn); ok {
		k.LabelFunc = func(se core.SessionState) string {
			out, err := a.VM.Call(fn, sessionTable(se))
			a.Host.Report(registry.KindView, "session.kind."+name, err)
			if err != nil || len(out) == 0 {
				return name
			}
			s, _ := out[0].(string)
			return cmp.Or(s, name)
		}
	}
	if fn, ok := spec["chips"].(*script.Fn); ok {
		k.ChipsFunc = func(se core.SessionState) []registry.Chip {
			out, err := a.VM.Call(fn, sessionTable(se))
			a.Host.Report(registry.KindView, "session.kind."+name, err)
			if err != nil || len(out) == 0 {
				return nil
			}
			return chipsOf(out[0])
		}
	}
	if fn, ok := spec["prepare"].(*script.Fn); ok {
		k.PrepareFunc = func(ctx registry.Ctx, sp *core.SessionSpec) error {
			out, err := a.callOnLoop(ctx, fn, script.Table{"kind": string(sp.Kind), "name": sp.Name, "title": sp.Title, "prompt": sp.Prompt,
				"ticket": sp.Ticket, "context": sp.Context, "branch": sp.Branch, "base": sp.Base, "pr": sp.PR})
			if err != nil || len(out) == 0 {
				return err
			}
			t, _ := out[0].(script.Table)
			for f, dst := range map[string]*string{"name": &sp.Name, "title": &sp.Title, "prompt": &sp.Prompt, "context": &sp.Context,
				"branch": &sp.Branch, "base": &sp.Base} {
				if v, ok := t[f].(string); ok {
					*dst = v
				}
			}
			return nil
		}
	}
	return k, nil
}

// chipsOf are a Lua list of texts or { text, help, link } as chips.
func chipsOf(v any) []registry.Chip {
	list, _ := v.([]any)
	var chips []registry.Chip
	for _, c := range list {
		switch c := c.(type) {
		case string:
			chips = append(chips, registry.Chip{Text: c})
		case script.Table:
			chips = append(chips, registry.Chip{Text: str(c, "text"), Help: str(c, "help"), Link: str(c, "link")})
		}
	}
	return chips
}

// sessionTable is a session as Lua reads it: its fields, and its kind's own data under data.
func sessionTable(se core.SessionState) map[string]any {
	t := map[string]any{"id": string(se.ID), "name": se.Name, "kind": string(se.Kind), "status": string(se.Status), "prompt": se.Prompt,
		"ticket": se.Ticket, "branch": se.Branch, "dir": se.Dir, "archived": se.Archived, "started": se.Started}
	if d := se.Data(se.Kind); d != nil {
		var v any
		if b, err := json.Marshal(d); err == nil && json.Unmarshal(b, &v) == nil {
			t["data"] = v
		}
	}
	return t
}
