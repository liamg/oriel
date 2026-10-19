package api

import (
	"context"
	"fmt"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

//oriel:api name=editor cap=ui
type Editor struct{ a *APIs }

// windowTable is an editor window as Lua sees it.
func windowTable(w registry.EditorWindow) script.Table {
	return script.Table{"id": w.ID, "path": w.Path, "presenter": w.Presenter}
}

//oriel:api doc="Add a button at the right of an editor's breadcrumb, for the files when(path) takes: { name, label, label_on, icon, icon_on, command, args, run = fn(window), when = fn(path), on = fn(window), note = fn(window), order (50 by default), help }. window is { id, path, presenter }. on lights it, and label_on and icon_on show while it's on; note is dim text after the label (a count). icon is a built-in icon's name or a glyph. command runs with args.window set to the window it was clicked in; run is a function in its place."
func (e Editor) Action(spec script.Table, where string) error {
	a := registry.EditorAction{Name: str(spec, "name"), Label: str(spec, "label"), LabelOn: str(spec, "label_on"), Icon: str(spec, "icon"),
		IconOn: str(spec, "icon_on"), Command: str(spec, "command"), Order: 50, Help: str(spec, "help"), Source: where}
	if run, ok := spec["run"].(*script.Fn); ok {
		a.Command = e.a.anon(run)
	}
	if a.Name == "" || a.Command == "" {
		return fmt.Errorf("an editor action needs a name and a command or run function")
	}
	if args, ok := spec["args"].(script.Table); ok {
		a.Args = registry.Args(args)
	}
	if o, ok := spec["order"].(float64); ok {
		a.Order = int(o)
	}
	if fn, ok := spec["when"].(*script.Fn); ok {
		a.When = func(path string) bool { return e.truthy(fn, path) }
	}
	if fn, ok := spec["on"].(*script.Fn); ok {
		a.On = func(w registry.EditorWindow) bool { return e.truthy(fn, windowTable(w)) }
	}
	if fn, ok := spec["note"].(*script.Fn); ok {
		a.Note = func(w registry.EditorWindow) string {
			out, err := e.a.VM.Call(fn, windowTable(w))
			if err != nil || len(out) == 0 || out[0] == nil {
				return ""
			}
			return fmt.Sprint(out[0])
		}
	}
	e.a.own(e.a.Host.Contribute(a))
	return nil
}

// truthy calls fn and reports whether it returned something other than nil or false.
func (e Editor) truthy(fn *script.Fn, args ...any) bool {
	out, err := e.a.VM.Call(fn, args...)
	if err != nil || len(out) == 0 {
		return false
	}
	b, isBool := out[0].(bool)
	return out[0] != nil && (!isBool || b)
}

//oriel:api doc="Add a way to draw a buffer other than as text: { name, label, when = fn(path), comments, order, build = fn(ctx) }. ctx has path, name, lines (the buffer's text, a string a line) and width; ctx.block(from_line, to_line, fn) adds a block made from those source lines (1-based), drawn by fn(width), which returns oriel.ui nodes (or pass the nodes in place of fn). The file's threads show in the margin beside the block holding their line, and with comments a click on a block (or c on the selected one) opens a thread on its lines. editor.present { name } shows the focused window with it, and again shows the text."
func (e Editor) Presenter(spec script.Table, where string) error {
	builder, _ := spec["build"].(*script.Fn)
	p := registry.BufferPresenter{Name: str(spec, "name"), Label: str(spec, "label"), Order: 50, Source: where}
	if p.Name == "" || builder == nil {
		return fmt.Errorf("a presenter needs a name and a build function")
	}
	p.Comments, _ = spec["comments"].(bool)
	if o, ok := spec["order"].(float64); ok {
		p.Order = int(o)
	}
	if fn, ok := spec["when"].(*script.Fn); ok {
		p.When = func(path string) bool { return e.truthy(fn, path) }
	}
	id := "presenter." + p.Name
	p.Build = func(ctx *registry.PresentCtx) {
		lines := make([]any, len(ctx.Lines))
		for i, l := range ctx.Lines {
			lines[i] = l
		}
		t := script.Table{"path": ctx.Path, "name": ctx.Name, "lines": lines, "width": ctx.Width,
			"block": script.GoFunc(func(a *script.Args) ([]any, error) {
				body := a.Any(2)
				b := ctx.Block(a.Int(0)-1, a.Int(1)-1, func(c *ui.Ctx, d registry.PresentDraw) {
					node := body
					if fn, ok := body.(*script.Fn); ok {
						out, err := e.a.VM.Call(fn, d.Width)
						e.a.Host.Report(registry.KindView, id, err)
						if err != nil {
							c.Text("error", err.Error(), ui.TextStyle{Color: c.Theme().C(theme.Error)})
							return
						}
						node = nil
						if len(out) > 0 {
							node = out[0]
						}
					}
					build(c, node, e.a)
				})
				b.Space = 6
				return nil, nil
			})}
		_, err := e.a.VM.Call(builder, t)
		e.a.Host.Report(registry.KindView, id, err)
		if err != nil {
			ctx.Block(0, len(ctx.Lines)-1, func(c *ui.Ctx, _ registry.PresentDraw) {
				c.Text("error", p.Name+": "+err.Error(), ui.TextStyle{Color: c.Theme().C(theme.Error)})
			})
		}
	}
	e.a.own(e.a.Host.Contribute(p))
	return nil
}

//oriel:api doc="Show the files when(path) takes in a view instead of a buffer: { name, view, when = fn(path), order (50 by default), help }. editor.open opens such a file as a tab of the view, its instance the file's absolute path; the first viewer by order, then name, that takes a file shows it."
func (e Editor) Viewer(spec script.Table, where string) error {
	v := registry.FileViewer{Name: str(spec, "name"), View: str(spec, "view"), Order: 50, Help: str(spec, "help"), Source: where}
	when, ok := spec["when"].(*script.Fn)
	if v.Name == "" || v.View == "" || !ok {
		return fmt.Errorf("a file viewer needs a name, a view and a when function")
	}
	v.When = func(path string) bool { return e.truthy(when, path) }
	if o, ok := spec["order"].(float64); ok {
		v.Order = int(o)
	}
	e.a.own(e.a.Host.Contribute(v))
	return nil
}

//oriel:api doc="Show the focused editor window with the presenter name, or as text again when it shows it already."
func (e Editor) Present(name string) error {
	return e.a.Host.Run(context.Background(), "editor.present", registry.Args{"name": name})
}
