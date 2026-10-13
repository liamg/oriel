package api

import (
	"fmt"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
	"github.com/liamg/oriel/ui"
)

//oriel:api name=rail
type Rail struct{ a *APIs }

//oriel:api doc="Add an icon to the rail down the window's left edge: { name, icon, title, command, args, order (50 by default), bottom }. icon is a built-in icon's name (files, search, git, requests, debug, tests, settings) or a glyph. It's dimmed while the command isn't available."
func (r Rail) Add(spec script.Table, where string) error {
	e := registry.RailEntry{Name: str(spec, "name"), Icon: str(spec, "icon"), Title: str(spec, "title"), Command: str(spec, "command"),
		Order: 50, Source: where}
	if e.Name == "" || e.Command == "" {
		return fmt.Errorf("a rail entry needs a name and a command")
	}
	if a, ok := spec["args"].(script.Table); ok {
		e.Args = registry.Args(a)
	}
	if o, ok := spec["order"].(float64); ok {
		e.Order = int(o)
	}
	e.Bottom, _ = spec["bottom"].(bool)
	r.a.own(r.a.Host.Contribute(e))
	return nil
}

//oriel:api name=start
type Start struct{ a *APIs }

//oriel:api doc="Add an action to the start view: { name, icon, title, desc, command, args, order (50 by default) }. It's dimmed while the command isn't available."
func (s Start) Add(spec script.Table, where string) error {
	e := registry.StartEntry{Name: str(spec, "name"), Icon: str(spec, "icon"), Title: str(spec, "title"), Desc: str(spec, "desc"),
		Command: str(spec, "command"), Order: 50, Source: where}
	if e.Name == "" || e.Command == "" {
		return fmt.Errorf("a start entry needs a name and a command")
	}
	if a, ok := spec["args"].(script.Table); ok {
		e.Args = registry.Args(a)
	}
	if o, ok := spec["order"].(float64); ok {
		e.Order = int(o)
	}
	s.a.own(s.a.Host.Contribute(e))
	return nil
}

//oriel:api name=overview
type Overview struct{ a *APIs }

//oriel:api doc="Add a panel to the project overview's cards: { name, title, order (50 by default), repos = true, dirs = true, compute = fn(dir) }. compute returns rows, each { label, value }, and runs on the app's loop, so keep it quick."
func (o Overview) Add(spec script.Table, where string) error {
	fn, _ := spec["compute"].(*script.Fn)
	p := registry.OverviewPanel{Name: str(spec, "name"), Title: str(spec, "title"), Order: 50, Repos: true, Dirs: true, Source: where}
	if p.Name == "" || fn == nil {
		return fmt.Errorf("an overview panel needs a name and a compute function")
	}
	if o, ok := spec["order"].(float64); ok {
		p.Order = int(o)
	}
	if v, ok := spec["repos"].(bool); ok {
		p.Repos = v
	}
	if v, ok := spec["dirs"].(bool); ok {
		p.Dirs = v
	}
	p.Compute = func(ctx registry.Ctx, dir string) (ui.Node, error) {
		type result struct {
			v   any
			err error
		}
		got := make(chan result, 1)
		o.a.Host.Do(func() {
			out, err := o.a.VM.Call(fn, dir)
			var v any
			if len(out) > 0 {
				v = out[0]
			}
			got <- result{v, err}
		})
		select {
		case r := <-got:
			if r.err != nil {
				return nil, r.err
			}
			return rowsNode(r.v), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	o.a.own(o.a.Host.Contribute(p))
	return nil
}

// rowsNode draws a panel's rows: { { label, value }, … }.
func rowsNode(v any) ui.Node {
	var rows []ui.Node
	list, _ := v.([]any)
	for _, x := range list {
		if r, ok := x.(script.Table); ok {
			rows = append(rows, ui.RowNode(ui.TextNode(fmt.Sprint(r["label"])), ui.TextNode("  "+fmt.Sprint(r["value"]))))
		}
	}
	return ui.ColumnNode(rows...)
}
