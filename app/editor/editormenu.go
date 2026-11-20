package editor

import (
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui/layout"
)

// menuItems are the editor's in the right-click menu: cut, copy and paste in the pane right-clicked, through the
// editor's own clipboard commands.
func (x *Editor) menuItems() []registry.MenuItem {
	item := func(name, label string, order int, command string, selection bool) registry.MenuItem {
		return registry.MenuItem{Name: name, Label: label, Group: "edit", Order: order, Source: "builtin:editor",
			When: registry.MenuWhen{View: kit.EditorView, Selection: selection},
			Run: func(ctx registry.Ctx, at registry.MenuCtx) error {
				x.h.Layout().Show(layout.ViewRef{ID: at.View, Instance: at.Instance, Project: at.Project}) // the pane right-clicked
				return x.h.Run(ctx, command, nil)
			}}
	}
	return []registry.MenuItem{
		item("editor.menu.cut", "Cut", 1, "editor.cut", true),
		item("editor.menu.copy", "Copy", 2, "editor.copy", true),
		item("editor.menu.paste", "Paste", 3, "editor.paste", false),
	}
}
