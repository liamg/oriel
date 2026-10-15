package ui

import (
	"slices"
	"strings"

	"github.com/liamg/oriel/render"
)

// Roles and actions are render's, so a backend's bridge can read the tree without ui.
type (
	Role       = render.Role
	Action     = render.Action
	AccessNode = render.AccessNode
)

const (
	RoleNone     = render.RoleNone
	RoleWindow   = render.RoleWindow
	RoleRegion   = render.RoleRegion
	RoleGroup    = render.RoleGroup
	RoleToolbar  = render.RoleToolbar
	RoleText     = render.RoleText
	RoleButton   = render.RoleButton
	RoleTextBox  = render.RoleTextBox
	RoleList     = render.RoleList
	RoleListItem = render.RoleListItem
	RoleTree     = render.RoleTree
	RoleTreeItem = render.RoleTreeItem
	RoleTabList  = render.RoleTabList
	RoleTab      = render.RoleTab
	RoleMenu     = render.RoleMenu
	RoleMenuItem = render.RoleMenuItem
	RoleDialog   = render.RoleDialog
	RoleStatus   = render.RoleStatus
	RoleSlider   = render.RoleSlider
	RoleImage    = render.RoleImage

	ActionClick  = render.ActionClick
	ActionFocus  = render.ActionFocus
	ActionScroll = render.ActionScroll
)

// AccessTree is the latest frame's accessibility tree, rooted at a window.
func (u *UI) AccessTree() *AccessNode {
	root := &AccessNode{ID: 0, Role: RoleWindow, Name: u.Title}
	if u.root == nil {
		return root
	}
	var walk func(e *elem, into *AccessNode)
	walk = func(e *elem, into *AccessNode) {
		parent := into
		if e != u.root && e.style.Role != RoleNone {
			n := u.accessNode(e)
			into.Children = append(into.Children, n)
			parent = n
		}
		kids := e.kids
		// Floats are drawn over the flow; readers meet them after it, topmost last.
		flow := slices.DeleteFunc(slices.Clone(kids), func(k *elem) bool { return k.style.Float != nil })
		floats := slices.DeleteFunc(slices.Clone(kids), func(k *elem) bool { return k.style.Float == nil })
		slices.SortStableFunc(floats, func(a, b *elem) int { return a.style.Z - b.style.Z })
		for _, k := range append(flow, floats...) {
			walk(k, parent)
		}
	}
	walk(u.root, root)
	return root
}

func (u *UI) accessNode(e *elem) *AccessNode {
	s := e.style
	n := &AccessNode{
		ID: uint64(e.id), Role: s.Role, Name: s.Label, Value: s.Value, Description: s.Help,
		Bounds: e.rect(), Disabled: s.Disabled, Selected: s.Selected, Expanded: s.Expanded, Modal: s.Trap,
		Focused: u.in.focus == e.id && e.id != 0,
	}
	if n.Name == "" && s.Role != RoleTextBox {
		n.Name = textOf(e)
	}
	if s.Interactive || s.Focusable {
		n.Actions = append(n.Actions, ActionClick)
	}
	if s.Focusable {
		n.Actions = append(n.Actions, ActionFocus)
	}
	if s.ScrollX || s.ScrollY {
		n.Actions = append(n.Actions, ActionScroll)
	}
	return n
}

// textOf is the text inside e, in reading order, stopping at nodes that have roles of their own.
func textOf(e *elem) string {
	var parts []string
	var walk func(e *elem)
	walk = func(e *elem) {
		if e.kind == kText && strings.TrimSpace(e.text) != "" {
			parts = append(parts, strings.TrimSpace(e.text))
		}
		for _, k := range e.kids {
			if k.style.Role == RoleNone {
				walk(k)
			}
		}
	}
	walk(e)
	return strings.Join(parts, " ")
}

// Act does what assistive technology asks of id: a click (as if pressed and released on it), focus, or
// nothing for an action the node doesn't have. It takes effect on the next frame.
func (u *UI) Act(id ID, a Action) bool {
	f, ok := u.flags[id]
	if !ok || f.disabled {
		return false
	}
	switch a {
	case ActionClick:
		if !f.interactive {
			return false
		}
		if u.in.clicks == nil {
			u.in.clicks = map[ID]bool{}
		}
		u.in.clicks[id] = true
		if f.focusable {
			u.in.focus, u.in.visible = id, true
		}
	case ActionFocus:
		if !f.focusable {
			return false
		}
		u.Focus(id)
	default:
		return false
	}
	return true
}
