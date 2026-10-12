package app

import (
	"context"
	"fmt"

	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui"
)

func builtins() []ext.Extension {
	return []ext.Extension{&layout{}, &introspect{}}
}

// disposer is the usual way for an extension to undo what it registered.
type disposer []registry.Disposable

func (d *disposer) add(x registry.Disposable) { *d = append(*d, x) }

func (d *disposer) Deactivate(context.Context) error {
	for _, x := range *d {
		x.Dispose()
	}
	*d = nil
	return nil
}

// layout fills the window with one view, which view.open picks. FW-9 replaces it with the layout tree.
type layout struct {
	disposer
	open string
}

func (l *layout) Manifest() ext.Manifest { return ext.Manifest{ID: "layout"} }

func (l *layout) Activate(_ context.Context, h ext.Host) error {
	l.add(h.Command(registry.Command{ID: "view.open", Title: "Open a view", Run: func(_ registry.Ctx, args registry.Args) error {
		id, _ := args["id"].(string)
		if _, ok := h.ViewByID(id); !ok {
			return fmt.Errorf("no view %q", id)
		}
		l.open = id
		return nil
	}}))
	l.add(h.View(RootView, registry.ViewFunc(func(vc registry.ViewCtx) ui.Node {
		if v, ok := h.ViewByID(l.open); ok {
			return v.Build(vc)
		}
		return ui.Column()
	})))
	return nil
}

// introspect is :Keys and :Commands: every keymap and command, with where it came from.
type introspect struct{ disposer }

func (i *introspect) Manifest() ext.Manifest { return ext.Manifest{ID: "introspect"} }

func (i *introspect) Activate(_ context.Context, h ext.Host) error {
	open := func(view string) func(registry.Ctx, registry.Args) error {
		return func(ctx registry.Ctx, _ registry.Args) error {
			return h.Run(ctx, "view.open", registry.Args{"id": view})
		}
	}
	i.add(h.Command(registry.Command{ID: "Keys", Title: "List keymaps", Run: open("keys")}))
	i.add(h.Command(registry.Command{ID: "Commands", Title: "List commands", Run: open("commands")}))
	i.add(h.View("keys", registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		rows := []ui.Node{ui.Text(fmt.Sprintf("%-10s %-14s %-24s %s", "MODE", "KEYS", "COMMAND", "SOURCE"))}
		for _, k := range h.Keymaps() {
			rows = append(rows, ui.Text(fmt.Sprintf("%-10s %-14s %-24s %s", k.Mode, k.Keys, k.Command, k.Source)))
		}
		return ui.Column(rows...)
	})))
	i.add(h.View("commands", registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		rows := []ui.Node{ui.Text(fmt.Sprintf("%-24s %-30s %s", "COMMAND", "TITLE", "SOURCE"))}
		for _, c := range h.Commands() {
			rows = append(rows, ui.Text(fmt.Sprintf("%-24s %-30s %s", c.ID, c.Title, c.Source)))
		}
		return ui.Column(rows...)
	})))
	return nil
}
