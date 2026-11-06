package render

import (
	"fmt"
	"slices"
	"strings"
)

// Role is what a node is to assistive technology, after the ARIA and AccessKit roles of the same names.
type Role string

const (
	RoleNone     Role = ""
	RoleWindow   Role = "window"
	RoleRegion   Role = "region" // a landmark: the editor, a dock, the requests pane
	RoleGroup    Role = "group"
	RoleToolbar  Role = "toolbar"
	RoleText     Role = "text"
	RoleButton   Role = "button"
	RoleTextBox  Role = "textbox"
	RoleList     Role = "list"
	RoleListItem Role = "listitem"
	RoleTree     Role = "tree"
	RoleTreeItem Role = "treeitem"
	RoleTabList  Role = "tablist"
	RoleTab      Role = "tab"
	RoleMenu     Role = "menu"
	RoleMenuItem Role = "menuitem"
	RoleDialog   Role = "dialog"
	RoleStatus   Role = "status" // a polite live region: toasts, the statusline
	RoleSlider   Role = "slider" // its Label carries the value
	RoleImage    Role = "image"  // its Label says what it shows
	RoleLink     Role = "link"
	RoleCheckBox Role = "checkbox" // its state is Checked
)

// Action is something assistive technology asks a node to do.
type Action string

const (
	ActionClick     Action = "click"
	ActionFocus     Action = "focus"
	ActionScroll    Action = "scroll"
	ActionIncrement Action = "increment" // a slider steps up
	ActionDecrement Action = "decrement"
	ActionSetValue  Action = "setvalue" // a textbox's text becomes AccessAction.Value
	ActionSelect    Action = "select"   // a textbox's selection becomes AccessAction.Selection
)

// TextRange is a span of a textbox's Value, in runes from its start; an empty one is the caret.
type TextRange struct{ Start, End int }

// AccessNode is one node of the accessibility tree.
type AccessNode struct {
	ID          uint64
	Role        Role
	Name        string
	Value       string
	Description string // its help id, so a reader can say where help is
	Bounds      Rect
	Focused     bool
	Disabled    bool
	Selected    bool
	Modal       bool // a dialog that holds focus: readers stay inside it
	Expanded    *bool
	Checked     *bool     // a check box's state
	Selection   TextRange // a textbox's selection, or its caret
	Actions     []Action
	Children    []*AccessNode
}

// AccessUpdate is what changed in the accessibility tree since the last update: the nodes added or changed
// (children by id), those removed, and the focus. A platform bridge applies it to its own tree.
type AccessUpdate struct {
	Changed []*AccessNode
	Removed []uint64
	Focus   uint64
}

// AccessDiff compares the tree to prev, the tree from the last update (nil the first time).
func AccessDiff(prev, next *AccessNode) AccessUpdate {
	before, after := map[uint64]*AccessNode{}, map[uint64]*AccessNode{}
	var index func(n *AccessNode, into map[uint64]*AccessNode)
	index = func(n *AccessNode, into map[uint64]*AccessNode) {
		into[n.ID] = n
		for _, k := range n.Children {
			index(k, into)
		}
	}
	if prev != nil {
		index(prev, before)
	}
	index(next, after)
	var up AccessUpdate
	var order []uint64
	var walk func(n *AccessNode)
	walk = func(n *AccessNode) {
		order = append(order, n.ID)
		for _, k := range n.Children {
			walk(k)
		}
	}
	walk(next)
	for _, id := range order {
		n := after[id]
		if n.Focused {
			up.Focus = id
		}
		if old, ok := before[id]; !ok || !sameNode(old, n) {
			up.Changed = append(up.Changed, n)
		}
	}
	for id := range before {
		if _, ok := after[id]; !ok {
			up.Removed = append(up.Removed, id)
		}
	}
	slices.Sort(up.Removed)
	return up
}

func sameNode(a, b *AccessNode) bool {
	if a.Role != b.Role || a.Name != b.Name || a.Value != b.Value || a.Description != b.Description || a.Bounds != b.Bounds ||
		a.Focused != b.Focused || a.Disabled != b.Disabled || a.Selected != b.Selected || a.Modal != b.Modal || !slices.Equal(a.Actions, b.Actions) ||
		!sameFlag(a.Expanded, b.Expanded) || !sameFlag(a.Checked, b.Checked) || a.Selection != b.Selection || len(a.Children) != len(b.Children) {
		return false
	}
	for i := range a.Children {
		if a.Children[i].ID != b.Children[i].ID {
			return false
		}
	}
	return true
}

func sameFlag(a, b *bool) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }

// Dump writes the tree as a screen reader's outline: role, name, value and states, indented, for golden tests.
func (n *AccessNode) Dump() string {
	var b strings.Builder
	var walk func(n *AccessNode, depth int)
	walk = func(n *AccessNode, depth int) {
		fmt.Fprintf(&b, "%s%s", strings.Repeat("  ", depth), n.Role)
		if n.Name != "" {
			fmt.Fprintf(&b, " %q", n.Name)
		}
		if n.Value != "" {
			fmt.Fprintf(&b, " value=%q", n.Value)
		}
		for _, st := range []struct {
			on   bool
			name string
		}{{n.Focused, "focused"}, {n.Disabled, "disabled"}, {n.Selected, "selected"}, {n.Modal, "modal"}, {n.Expanded != nil && *n.Expanded, "expanded"},
			{n.Expanded != nil && !*n.Expanded, "collapsed"}, {n.Checked != nil && *n.Checked, "checked"},
			{n.Checked != nil && !*n.Checked, "unchecked"}} {
			if st.on {
				b.WriteString(" " + st.name)
			}
		}
		if n.Description != "" {
			fmt.Fprintf(&b, " help=%s", n.Description)
		}
		b.WriteByte('\n')
		for _, k := range n.Children {
			walk(k, depth+1)
		}
	}
	walk(n, 0)
	return b.String()
}

// AccessAction is assistive technology asking node ID to do something.
type AccessAction struct {
	ID        uint64
	Action    Action
	Value     string    // for ActionSetValue
	Selection TextRange // for ActionSelect
}

// Accessible is a Window a platform's screen reader can see: the app sends it the tree's changes after each
// frame, and runs the actions it reports.
type Accessible interface {
	UpdateAccess(u AccessUpdate)
	AccessActions() <-chan AccessAction
}

// AccessWanted is an Accessible that knows whether a screen reader is reading it: while none is, the app needn't
// build its tree, and once one starts the window asks for a frame, whose update brings the bridge up to date.
type AccessWanted interface {
	AccessWanted() bool
}
