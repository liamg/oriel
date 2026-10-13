package app

import (
	"context"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/layout"
	"github.com/liamg/oriel/ui/widget"
)

// contextMenus is the window's right-click menu (PL-13): the menu items that apply where it was clicked, by group,
// then Help. Built-in entries are menu items like any plugin's.
type contextMenus struct {
	disposer
	sh     *shell
	h      ext.Host
	target ui.ID
	at     registry.MenuCtx
	items  []registry.MenuItem
}

func (m *contextMenus) Manifest() ext.Manifest { return ext.Manifest{ID: "menus"} }

func (m *contextMenus) Activate(_ context.Context, h ext.Host) error {
	m.h = h
	m.add(h.View(OverlayPrefix+"context-menu", registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "context-menu", Fn: m.build}
	})))
	for _, it := range []registry.MenuItem{
		{Name: "editor.copy-path", Label: "Copy path", Group: "edit", Order: 10, When: registry.MenuWhen{View: EditorView}, Source: "builtin:menus",
			Run: func(_ registry.Ctx, at registry.MenuCtx) error {
				if m.sh.desktop != nil {
					m.sh.desktop.SetClipboard(at.File)
				}
				return nil
			}},
		{Name: "pane.move", Label: "Move pane…", Key: "⌃W M", Group: "pane", Order: 90, When: registry.MenuWhen{View: "*"}, Source: "builtin:menus",
			Run: func(ctx registry.Ctx, at registry.MenuCtx) error {
				m.h.Layout().Show(layout.ViewRef{ID: at.View, Instance: at.Instance})
				return m.h.Run(ctx, "layout.menu", nil)
			}},
		{Name: "pane.close", Label: "Close", Group: "pane", Order: 91, When: registry.MenuWhen{View: "*"}, Source: "builtin:menus",
			Run: func(ctx registry.Ctx, at registry.MenuCtx) error {
				return m.h.Run(ctx, "view.close", registry.Args{"id": at.View, "instance": at.Instance})
			}},
	} {
		m.add(h.Contribute(it))
	}
	return nil
}

// where is what's at p: the pane's view and instance (the smallest pane holding p), and what that view says is
// there.
func (m *contextMenus) where(p render.Point, help string) registry.MenuCtx {
	at := registry.MenuCtx{Help: help}
	best := float32(-1)
	for _, v := range m.h.Layout().Visible() {
		r, ok := m.h.PaneRect(v)
		if !ok || p.X < r.Min.X || p.X >= r.Max.X || p.Y < r.Min.Y || p.Y >= r.Max.Y {
			continue
		}
		if area := r.Dx() * r.Dy(); best < 0 || area < best {
			best, at.View, at.Instance = area, v.ID, v.Instance
		}
	}
	if view, ok := m.h.ViewByID(at.View); ok {
		if l, ok := registry.As[registry.Located](view); ok {
			at.File, at.Selection = l.Location(at.Instance)
		}
	}
	return at
}

func (m *contextMenus) build(c *ui.Ctx) {
	u := m.h.UI()
	if u == nil {
		return
	}
	if t, p, ok := u.ContextClick(); ok {
		at := m.where(p, u.Help(t))
		if items := registry.MenuItems(m.h, at); len(items) > 0 || at.Help != "" {
			m.target, m.at, m.items = t, at, items
		}
	}
	if m.target == 0 {
		return
	}
	var entries []widget.MenuItem
	var index []int // entries' items, -1 for a separator
	for i, it := range m.items {
		if i > 0 && it.Group != m.items[i-1].Group {
			entries, index = append(entries, widget.MenuItem{Separator: true}), append(index, -1)
		}
		entries = append(entries, widget.MenuItem{Label: it.Label, Shortcut: it.Key, Help: widget.ContextMenuHelp})
		index = append(index, i)
	}
	res := widget.ContextMenu(c, "context-menu", m.target, entries)
	if res.Chosen >= 0 && res.Chosen < len(index) && index[res.Chosen] >= 0 {
		m.run(m.items[index[res.Chosen]])
	}
	if !res.Open {
		m.target = 0
	}
}

func (m *contextMenus) run(it registry.MenuItem) {
	at, ctx := m.at, context.Background()
	err := registry.Call(func() error {
		if it.Run != nil {
			return it.Run(ctx, at)
		}
		args := registry.Args{"view": at.View, "instance": at.Instance, "file": at.File}
		for k, v := range it.Args {
			args[k] = v
		}
		return m.h.Run(ctx, it.Command, args)
	})
	if err != nil {
		m.h.Notify(core.Notice{ID: "menu." + it.Name, Severity: core.SevError, Source: it.Source, Key: true, Text: it.Label + ": " + err.Error()})
	}
}
