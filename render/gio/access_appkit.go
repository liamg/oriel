package gio

import (
	"sync"

	"github.com/liamg/oriel/render"
)

// The NSAccessibility side of the bridge that needs no AppKit, so it's tested everywhere. access_darwin.go maps
// these codes to AppKit's constants, in the same order.

type appkitRole int

const (
	axGroup appkitRole = iota
	axToolbar
	axStaticText
	axButton
	axTextField
	axList
	axRow
	axOutline
	axTabGroup
	axRadioButton
	axMenu
	axMenuItem
	axSlider
	axImage
	axLink
	axCheckBox
	axRoleCount
)

type appkitSubrole int

const (
	axNoSubrole appkitSubrole = iota
	axTabButton
	axDialog
	axSubroleCount
)

// appkitRoles is the NSAccessibilityRole and subrole for each of the tree's roles; a role not here is a group.
var appkitRoles = map[render.Role]struct {
	role appkitRole
	sub  appkitSubrole
}{
	render.RoleRegion:   {axGroup, axNoSubrole},
	render.RoleGroup:    {axGroup, axNoSubrole},
	render.RoleStatus:   {axGroup, axNoSubrole},
	render.RoleDialog:   {axGroup, axDialog},
	render.RoleToolbar:  {axToolbar, axNoSubrole},
	render.RoleText:     {axStaticText, axNoSubrole},
	render.RoleButton:   {axButton, axNoSubrole},
	render.RoleTextBox:  {axTextField, axNoSubrole},
	render.RoleList:     {axList, axNoSubrole},
	render.RoleListItem: {axRow, axNoSubrole},
	render.RoleTree:     {axOutline, axNoSubrole},
	render.RoleTreeItem: {axRow, axNoSubrole},
	render.RoleTabList:  {axTabGroup, axNoSubrole},
	render.RoleTab:      {axRadioButton, axTabButton},
	render.RoleMenu:     {axMenu, axNoSubrole},
	render.RoleMenuItem: {axMenuItem, axNoSubrole},
	render.RoleSlider:   {axSlider, axNoSubrole},
	render.RoleImage:    {axImage, axNoSubrole},
	render.RoleLink:     {axLink, axNoSubrole},
	render.RoleCheckBox: {axCheckBox, axNoSubrole},
}

func appkitRoleOf(r render.Role) (appkitRole, appkitSubrole) {
	if m, ok := appkitRoles[r]; ok {
		return m.role, m.sub
	}
	return axGroup, axNoSubrole
}

type appkitValueKind int

const (
	axNoValue appkitValueKind = iota
	axStringValue
	axNumberValue
)

// appkitValue is a node's accessibilityValue: a field's text, a static text's words, a check box's 0 or 1.
func appkitValue(n *render.AccessNode) (appkitValueKind, string, int) {
	switch n.Role {
	case render.RoleTextBox:
		return axStringValue, n.Value, 0
	case render.RoleText, render.RoleSlider:
		if n.Value != "" {
			return axStringValue, n.Value, 0
		}
		return axStringValue, n.Name, 0
	case render.RoleCheckBox, render.RoleTab:
		on := n.Selected
		if n.Checked != nil {
			on = *n.Checked
		}
		if on {
			return axNumberValue, "", 1
		}
		return axNumberValue, "", 0
	}
	if n.Value != "" {
		return axStringValue, n.Value, 0
	}
	return axNoValue, "", 0
}

// appkitLabel is a node's accessibilityLabel: a static text says its words as its value instead.
func appkitLabel(n *render.AccessNode) string {
	if n.Role == render.RoleText && n.Value == "" {
		return ""
	}
	return n.Name
}

// elementMap keeps one platform element per node, made the first time a screen reader reaches it and kept until the
// node goes.
type elementMap struct {
	mu   sync.Mutex
	byID map[uint64]uintptr
	make func(id uint64) uintptr
	free func(id uint64, h uintptr)
}

func (m *elementMap) element(id uint64) uintptr {
	m.mu.Lock()
	defer m.mu.Unlock()
	if h, ok := m.byID[id]; ok {
		return h
	}
	if m.byID == nil {
		m.byID = map[uint64]uintptr{}
	}
	h := m.make(id)
	m.byID[id] = h
	return h
}

func (m *elementMap) existing(id uint64) (uintptr, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.byID[id]
	return h, ok
}

// drop frees the elements of nodes that have gone.
func (m *elementMap) drop(ids []uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range ids {
		if h, ok := m.byID[id]; ok {
			delete(m.byID, id)
			m.free(id, h)
		}
	}
}

func (m *elementMap) clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, h := range m.byID {
		m.free(id, h)
	}
	m.byID = nil
}

func (m *elementMap) len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.byID)
}

type appkitNoteKind int

const (
	noteFocus appkitNoteKind = iota
	noteTitle
	noteValue
	noteSelectedText
	noteLayout
	noteAnnounce
	noteExpanded
	noteCollapsed
	noteCount
)

type appkitNote struct {
	Kind appkitNoteKind
	ID   uint64
	Text string
}

// appkitNotes are the notifications an update posts. A node that's gone posts its own (UIElementDestroyed) as its
// element is freed.
func appkitNotes(c accessChange, t *accessTree) []appkitNote {
	var out []appkitNote
	layout := len(c.Added) > 0 || len(c.Removed) > 0
	announce := func(id uint64) {
		if e, ok := t.get(id); ok && e.node.Role == render.RoleStatus {
			if s := statusText(&e.node); s != "" {
				out = append(out, appkitNote{Kind: noteAnnounce, ID: id, Text: s})
			}
		}
	}
	for _, id := range c.Added {
		announce(id)
	}
	for _, ch := range c.Changed {
		if ch.What&changeName != 0 {
			out = append(out, appkitNote{Kind: noteTitle, ID: ch.ID})
		}
		if ch.What&changeValue != 0 {
			out = append(out, appkitNote{Kind: noteValue, ID: ch.ID})
		}
		if ch.What&changeSelection != 0 {
			out = append(out, appkitNote{Kind: noteSelectedText, ID: ch.ID})
		}
		if ch.What&changeStates != 0 {
			if e, ok := t.get(ch.ID); ok && e.node.Expanded != nil && !sameBool(ch.Old.Expanded, e.node.Expanded) {
				k := noteCollapsed
				if *e.node.Expanded {
					k = noteExpanded
				}
				out = append(out, appkitNote{Kind: k, ID: ch.ID})
			}
		}
		if ch.What&(changeChildren|changeBounds) != 0 {
			layout = true
		}
		if ch.What&(changeName|changeValue) != 0 {
			announce(ch.ID)
		}
	}
	if layout {
		out = append(out, appkitNote{Kind: noteLayout})
	}
	if c.Moved && c.Focus != 0 {
		out = append(out, appkitNote{Kind: noteFocus, ID: c.Focus})
	}
	return out
}

func sameBool(a, b *bool) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }

func statusText(n *render.AccessNode) string {
	if n.Value != "" {
		return n.Value
	}
	return n.Name
}
