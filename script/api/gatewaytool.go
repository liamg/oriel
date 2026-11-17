package api

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
)

//oriel:api doc="Add a tool to a sandboxed agent's gateway, run on this machine with your credentials: { name (called oriel__ and name), desc, schema = its arguments' JSON Schema, read_only, run = fn(call, done) }. call is { session, project, dir = the project's checkout, args }: the project its project argument names, in a workspace with more than one. done(text) answers, a table as its JSON, or done(nil, err) refuses. The gateway's rules decide each call: asked by default, and a read_only tool allowed unless a rule says otherwise."
func (g Agent) GatewayTool(spec script.Table, where string) error {
	run, _ := spec["run"].(*script.Fn)
	name := str(spec, "name")
	if name == "" || run == nil {
		return errors.New("a gateway tool needs a name and a run function")
	}
	if err := g.a.Check(plugin.CapAgentTools, name); err != nil {
		return err
	}
	var sch json.RawMessage
	if s, ok := spec["schema"].(script.Table); ok {
		b, err := json.Marshal(plainOf(s))
		if err != nil {
			return err
		}
		sch = b
	}
	readOnly, _ := spec["read_only"].(bool)
	g.a.own(g.a.Host.Contribute(registry.GatewayTool{Name: name, Desc: str(spec, "desc"), InputSchema: sch, ReadOnly: readOnly, Source: where,
		Run: func(ctx context.Context, c registry.GatewayCall) (string, error) {
			var args any
			_ = json.Unmarshal(c.Args, &args)
			var out any
			call := map[string]any{"session": c.Session, "project": c.Project, "dir": c.Dir, "args": args}
			if err := g.a.callAs(ctx, run, &out, call); err != nil {
				return "", err
			}
			if s, ok := out.(string); ok {
				return s, nil
			}
			b, err := json.Marshal(out)
			return string(b), err
		}}))
	return nil
}
