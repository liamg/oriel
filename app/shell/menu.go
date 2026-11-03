package shell

import (
	"context"
	"slices"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/layout"
	"github.com/liamg/oriel/ui/layout/panes"
	"github.com/liamg/oriel/ui/widget"
)

// contextMenus is the window's right-click menu: the menu items that apply where it was clicked, by group,
// then Help. Built-in entries are menu items like any plugin's.
type contextMenus struct {
	kit.Disposer
	h      plugin.Host
	target ui.ID
	at     registry.MenuCtx
	items  []registry.MenuItem
}

func (m *contextMenus) Manifest() plugin.Manifest { return plugin.Manifest{ID: "menus"} }

func (m *contextMenus) Activate(_ context.Context, h plugin.Host) error {
	m.h = h
	m.Add(h.View(kit.OverlayPrefix+"context-menu", uireg.ViewFunc(func(uireg.ViewCtx) ui.Node {
		return &ui.Func{Key: "context-menu", Fn: m.build}
	})))
	for _, it := range []registry.MenuItem{
		{Name: "editor.copy-path", Label: "Copy path", Group: "edit", Order: 10, When: registry.MenuWhen{View: kit.EditorView}, Source: "builtin:menus",
			Run: func(_ registry.Ctx, at registry.MenuCtx) error {
				kit.CopyText(m.h, at.File)
				return nil
			}},
		{Name: "pane.move", Label: "Move pane…", Key: "⌃W M", Group: "pane", Order: 90, When: registry.MenuWhen{View: "*"}, Source: "builtin:menus",
			Run: func(ctx registry.Ctx, at registry.MenuCtx) error {
				m.h.Layout().Show(layout.ViewRef{ID: at.View, Instance: at.Instance})
				return m.h.Run(ctx, "layout.menu", nil)
			}},
		{Name: "pane.close", Label: "Close", Group: "pane", Order: 91, When: registry.MenuWhen{View: "*"}, Source: "builtin:menus",
			Run: func(ctx registry.Ctx, at registry.MenuCtx) error {
				return m.h.Run(ctx, "view.close", registry.Args{"id": at.View, "instance": at.Instance, "project": at.Project})
			}},
		m.closeTabs("tab.close-others", "Close Others", 92, func(i, at int) bool { return i != at }),
		m.closeTabs("tab.close-left", "Close to the Left", 93, func(i, at int) bool { return i < at }),
		m.closeTabs("tab.close-right", "Close to the Right", 94, func(i, at int) bool { return i > at }),
		m.closeTabs("tab.close-all", "Close All", 95, func(int, int) bool { return true }),
	} {
		m.Add(h.Contribute(it))
	}
	return nil
}

// closeTabs is a tab's menu item closing the tabs beside it in its pane that close says to, by place.
func (m *contextMenus) closeTabs(name, label string, order int, close func(i, at int) bool) registry.MenuItem {
	return registry.MenuItem{Name: name, Label: label, Group: "pane", Order: order, When: registry.MenuWhen{View: "*", Help: panes.HelpTab},
		Source: "builtin:menus", Run: func(ctx registry.Ctx, at registry.MenuCtx) error {
			v := layout.ViewRef{ID: at.View, Instance: at.Instance, Project: at.Project}
			var tabs []layout.ViewRef
			for _, t := range m.h.Layout().All() {
				if t.ID == v.ID && t.Instance == v.Instance && t.Project == v.Project {
					if w := m.h.Layout().Find(t); w != nil {
						tabs = slices.Clone(w.Leaf.Tabs)
					}
					break
				}
			}
			i := slices.IndexFunc(tabs, func(t layout.ViewRef) bool { return t.ID == v.ID && t.Instance == v.Instance && t.Project == v.Project })
			for j, t := range tabs {
				if i >= 0 && close(j, i) {
					if err := m.h.Run(ctx, "view.close", registry.Args{"id": t.ID, "instance": t.Instance, "project": t.Project}); err != nil {
						return err
					}
				}
			}
			return nil
		}}
}

// where is what node t at p is in: the view whose tab it is, or else the pane's view (the smallest pane holding
// p), and what that view says is there.
func (m *contextMenus) where(t ui.ID, p render.Point, help, subject string) registry.MenuCtx {
	at := registry.MenuCtx{Help: help, Subject: subject}
	if v, ok := m.h.PaneTab(t); ok {
		at.View, at.Instance, at.Project = v.ID, v.Instance, v.Project
	} else {
		best := float32(-1)
		for _, v := range m.h.Layout().Visible() {
			r, ok := m.h.PaneRect(v)
			if !ok || p.X < r.Min.X || p.X >= r.Max.X || p.Y < r.Min.Y || p.Y >= r.Max.Y {
				continue
			}
			if area := r.Dx() * r.Dy(); best < 0 || area < best {
				best, at.View, at.Instance, at.Project = area, v.ID, v.Instance, v.Project
			}
		}
	}
	if view, ok := m.h.ViewByID(at.View); ok {
		if l, ok := uireg.As[registry.Located](view); ok {
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
		at := m.where(t, p, u.Help(t), u.SubjectAt(t, p))
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
		e := widget.MenuItem{Label: it.Label, Shortcut: it.Key, Help: widget.ContextMenuHelp}
		if it.Disabled != nil {
			_ = registry.Call(func() error { e.Detail = it.Disabled(m.at); return nil })
			e.Disabled = e.Detail != ""
		}
		entries = append(entries, e)
		index = append(index, i)
	}
	res := widget.ContextMenu(c, "context-menu", m.target, entries)
	if res.Chosen >= 0 && res.Chosen < len(index) && index[res.Chosen] >= 0 && !entries[res.Chosen].Disabled {
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
		args := registry.Args{"view": at.View, "instance": at.Instance, "file": at.File, "subject": at.Subject}
		for k, v := range it.Args {
			args[k] = v
		}
		return m.h.Run(ctx, it.Command, args)
	})
	if err != nil {
		m.h.Notify(core.Notice{ID: "menu." + it.Name, Severity: core.SevError, Source: it.Source, Key: true, Text: it.Label + ": " + err.Error()})
	}
}
