package registry

import (
	"cmp"
	"path"
	"slices"
	"strings"
)

// MenuItem is an entry in the right-click menu: the built-ins' and plugins' alike (PL-13). It shows where When
// matches, in its Group, ordered by Order then label; Help is always last.
type MenuItem struct {
	Name    string // unique among menu items: "editor.copy-path", "init.lua:12"
	Label   string
	Key     string // a shortcut shown beside it
	Group   string // items group by this, groups in the order of their first item's Order
	Order   int
	When    MenuWhen
	Command string // run this, with Args, or Run
	Args    Args
	Run     func(ctx Ctx, at MenuCtx) error
	// Trouble, with Command FixCommand, is what's wrong where it was clicked: the item shows only where there's
	// something, and runs with its Args.
	Trouble func(at MenuCtx) (Trouble, bool)
	Source  string
}

// MenuWhen says where an item applies; an empty field matches anything.
type MenuWhen struct {
	View      string // the view right-clicked: "editor", "problems"; "*" any view, not the window around them
	File      string // a glob over the file there (its base name or path): "*.go"
	Selection bool   // only with a selection
	Help      string // a prefix of the clicked node's help id: "shell/settings"
	Subject   string // a prefix of what the clicked node stands for: "session:"
}

// MenuCtx is what was right-clicked: its view and instance, the file and selection there, its help id and what it
// stands for (ui.Style.Subject).
type MenuCtx struct {
	View, Instance string
	Project        string // the project the view was opened for, if any
	File           string
	Selection      bool
	Help           string
	Subject        string // "session:fix-login"
}

func (MenuItem) ContributionKind() string   { return "menu.item" }
func (m MenuItem) ContributionName() string { return m.Name }

// Matches is whether the item applies at at.
func (m MenuItem) Matches(at MenuCtx) bool {
	w := m.When
	if w.View == "*" && at.View == "" || w.View != "" && w.View != "*" && w.View != at.View {
		return false
	}
	if w.File != "" {
		if at.File == "" {
			return false
		}
		ok, _ := path.Match(w.File, path.Base(at.File))
		if ok2, _ := path.Match(w.File, at.File); !ok && !ok2 {
			return false
		}
	}
	if w.Selection && !at.Selection {
		return false
	}
	if w.Subject != "" && !strings.HasPrefix(at.Subject, w.Subject) {
		return false
	}
	return w.Help == "" || strings.HasPrefix(at.Help, w.Help)
}

// MenuItems are r's menu items that apply at at, grouped and ordered, each fix with its explain twin after it.
func MenuItems(r Registry, at MenuCtx) []MenuItem {
	var out []MenuItem
	for _, c := range r.Contributions("menu.item") {
		m := c.(MenuItem)
		if !m.Matches(at) {
			continue
		}
		if m.Trouble != nil {
			var t Trouble
			var ok bool
			Call(func() error { t, ok = m.Trouble(at); return nil })
			if !ok {
				continue
			}
			m.Args = t.Args()
		}
		out = append(out, m)
	}
	first := map[string]int{}
	for _, m := range out {
		if o, ok := first[m.Group]; !ok || m.Order < o {
			first[m.Group] = m.Order
		}
	}
	slices.SortStableFunc(out, func(a, b MenuItem) int {
		return cmp.Or(cmp.Compare(first[a.Group], first[b.Group]), strings.Compare(a.Group, b.Group), cmp.Compare(a.Order, b.Order),
			strings.Compare(a.Label, b.Label))
	})
	return WithTwins(out)
}

// Located is a view that can say what's at an instance for the right-click menu: its file and whether there's a
// selection (the editor).
type Located interface {
	Location(instance string) (file string, selection bool)
}
