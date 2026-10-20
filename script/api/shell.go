package api

import (
	"fmt"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
	"github.com/liamg/oriel/ui"
)

//oriel:api name=rail cap=ui
type Rail struct{ a *APIs }

//oriel:api doc="Add an entry to the project sidebar down the window's left edge: { name, icon, title, command, args, scope, order (50 by default), bottom, note, badge, badge_tone, shown }. scope is project (one of each project's panels: listed under the open project, its command run with args.project set to the project), session (below the projects; the default) or global (a page of every session's, below a second divider with Stats, Usage and Settings). note = fn(project) returns dim text for its right: a live figure. badge = fn() returns a count shown as a pill in its place (0 for none), in badge_tone, a theme token (rail.badge by default). icon is a built-in icon's name (files, search, git, pullrequest, tests, requests, ship, debug, settings, stats, usage, jobs) or a glyph, or one a plugin's [contributes] icons brings. It's dimmed while the command isn't available, and listed only while shown = fn(project) returns true, when there's a shown: oriel.secret.has('NAME') keeps a plugin's entry hidden until its secret is set."
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
	e.BadgeTone = str(spec, "badge_tone")
	if fn, ok := spec["badge"].(*script.Fn); ok {
		e.Badge = func() int {
			out, err := r.a.VM.Call(fn)
			if err != nil || len(out) == 0 {
				return 0
			}
			return int(num(out[0]))
		}
	}
	switch sc := str(spec, "scope"); sc {
	case "", registry.ScopeSessionEntry, registry.ScopeProjectEntry, registry.ScopeGlobalEntry:
		e.Scope = sc
	default:
		return fmt.Errorf("a rail entry's scope is project, session or global, not %q", sc)
	}
	if fn, ok := spec["shown"].(*script.Fn); ok {
		e.Shown = func(project string) bool { return r.a.truthy(fn, project) }
	}
	if fn, ok := spec["note"].(*script.Fn); ok {
		e.Note = func(project string) string {
			out, err := r.a.VM.Call(fn, project)
			if err != nil || len(out) == 0 || out[0] == nil {
				return ""
			}
			return fmt.Sprint(out[0])
		}
	}
	r.a.own(r.a.Host.Contribute(e))
	return nil
}

//oriel:api name=start cap=ui
type Start struct{ a *APIs }

//oriel:api doc="Add an action to the start view: { name, icon, title, desc, command, args, order (50 by default), link, note, shown }. It's dimmed while the command isn't available, and listed only while shown = fn() returns true, when there's a shown. link = true makes it a small link under the actions, with note = fn() its figure."
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
	e.Link, _ = spec["link"].(bool)
	if fn, ok := spec["shown"].(*script.Fn); ok {
		e.Shown = func() bool { return s.a.truthy(fn) }
	}
	if fn, ok := spec["note"].(*script.Fn); ok {
		e.Note = func() string {
			out, err := s.a.VM.Call(fn)
			if err != nil || len(out) == 0 || out[0] == nil {
				return ""
			}
			return fmt.Sprint(out[0])
		}
	}
	s.a.own(s.a.Host.Contribute(e))
	return nil
}

//oriel:api name=place cap=ui
type Place struct{ a *APIs }

//oriel:api doc="Add a full-window page to the window's history, which nav.back and nav.forward (and the mouse's side buttons) go through with the session in front: { name, title, command, args, shown = fn(), close = fn() }. shown says it's showing now, command with args opens it, and close puts it away."
func (p Place) Add(spec script.Table, where string) error {
	shown, _ := spec["shown"].(*script.Fn)
	closer, _ := spec["close"].(*script.Fn)
	pl := registry.Place{Name: str(spec, "name"), Title: str(spec, "title"), Command: str(spec, "command"), Source: where}
	if pl.Name == "" || pl.Command == "" || shown == nil || closer == nil {
		return fmt.Errorf("a place needs a name, a command, and shown and close functions")
	}
	if a, ok := spec["args"].(script.Table); ok {
		pl.Args = registry.Args(a)
	}
	pl.Shown = func() bool { return p.a.truthy(shown) }
	pl.Close = func() { _, _ = p.a.VM.Call(closer) }
	p.a.own(p.a.Host.Contribute(pl))
	return nil
}

//oriel:api name=overview cap=ui
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
