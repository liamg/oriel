package gio

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/liamg/oriel/render"
)

// The AT-SPI side of the bridge that needs no D-Bus, so it's tested everywhere. Numbers are libatspi's
// (testdata/atspi-enums.txt).

const (
	atspiRoleCheckBox    = 7
	atspiRoleDialog      = 16
	atspiRoleFrame       = 23
	atspiRoleImage       = 27
	atspiRoleLabel       = 29
	atspiRoleList        = 31
	atspiRoleListItem    = 32
	atspiRoleMenu        = 33
	atspiRoleMenuItem    = 35
	atspiRolePageTab     = 37
	atspiRolePageTabList = 38
	atspiRolePanel       = 39
	atspiRolePushButton  = 43
	atspiRoleSlider      = 51
	atspiRoleStatusBar   = 54
	atspiRoleToolBar     = 63
	atspiRoleTree        = 65
	atspiRoleApplication = 75
	atspiRoleEntry       = 79
	atspiRoleLink        = 88
	atspiRoleTreeItem    = 91
	atspiRoleLandmark    = 110
)

var atspiRoleNames = map[uint32]string{
	atspiRoleCheckBox: "check box", atspiRoleDialog: "dialog", atspiRoleFrame: "frame", atspiRoleImage: "image",
	atspiRoleLabel: "label", atspiRoleList: "list", atspiRoleListItem: "list item", atspiRoleMenu: "menu",
	atspiRoleMenuItem: "menu item", atspiRolePageTab: "page tab", atspiRolePageTabList: "page tab list",
	atspiRolePanel: "panel", atspiRolePushButton: "push button", atspiRoleSlider: "slider",
	atspiRoleStatusBar: "status bar", atspiRoleToolBar: "tool bar", atspiRoleTree: "tree",
	atspiRoleApplication: "application", atspiRoleEntry: "entry", atspiRoleLink: "link",
	atspiRoleTreeItem: "tree item", atspiRoleLandmark: "landmark",
}

var atspiRoles = map[render.Role]uint32{
	render.RoleWindow: atspiRoleFrame, render.RoleRegion: atspiRoleLandmark, render.RoleGroup: atspiRolePanel,
	render.RoleToolbar: atspiRoleToolBar, render.RoleText: atspiRoleLabel, render.RoleButton: atspiRolePushButton,
	render.RoleTextBox: atspiRoleEntry, render.RoleList: atspiRoleList, render.RoleListItem: atspiRoleListItem,
	render.RoleTree: atspiRoleTree, render.RoleTreeItem: atspiRoleTreeItem, render.RoleTabList: atspiRolePageTabList,
	render.RoleTab: atspiRolePageTab, render.RoleMenu: atspiRoleMenu, render.RoleMenuItem: atspiRoleMenuItem,
	render.RoleDialog: atspiRoleDialog, render.RoleStatus: atspiRoleStatusBar, render.RoleSlider: atspiRoleSlider,
	render.RoleImage: atspiRoleImage, render.RoleLink: atspiRoleLink, render.RoleCheckBox: atspiRoleCheckBox,
}

func atspiRole(r render.Role) uint32 {
	if n, ok := atspiRoles[r]; ok {
		return n
	}
	return atspiRolePanel
}

type atspiState uint32

const (
	stateActive         atspiState = 1
	stateChecked        atspiState = 4
	stateCollapsed      atspiState = 5
	stateEditable       atspiState = 7
	stateEnabled        atspiState = 8
	stateExpandable     atspiState = 9
	stateExpanded       atspiState = 10
	stateFocusable      atspiState = 11
	stateFocused        atspiState = 12
	stateModal          atspiState = 16
	stateSelectable     atspiState = 22
	stateSelected       atspiState = 23
	stateSensitive      atspiState = 24
	stateShowing        atspiState = 25
	stateVisible        atspiState = 30
	stateSelectableText atspiState = 38
	stateCheckable      atspiState = 41
)

var atspiStateNames = map[atspiState]string{
	stateActive: "active", stateChecked: "checked", stateCollapsed: "collapsed", stateEditable: "editable",
	stateEnabled: "enabled", stateExpandable: "expandable", stateExpanded: "expanded", stateFocusable: "focusable",
	stateFocused: "focused", stateModal: "modal", stateSelectable: "selectable", stateSelected: "selected",
	stateSensitive: "sensitive", stateShowing: "showing", stateVisible: "visible",
	stateSelectableText: "selectable-text", stateCheckable: "checkable",
}

// atspiStates is the node's state set; active is for the window's frame.
func atspiStates(n *render.AccessNode, active bool) []atspiState {
	var s []atspiState
	add := func(on bool, st ...atspiState) {
		if on {
			s = append(s, st...)
		}
	}
	add(!n.Disabled, stateEnabled, stateSensitive)
	add(true, stateVisible, stateShowing)
	add(n.Role == render.RoleWindow && active, stateActive)
	add(has(n, render.ActionFocus) || n.Role == render.RoleTextBox, stateFocusable)
	add(n.Focused, stateFocused)
	add(n.Role == render.RoleListItem || n.Role == render.RoleTreeItem || n.Role == render.RoleTab, stateSelectable)
	add(n.Selected, stateSelected)
	add(n.Expanded != nil, stateExpandable)
	add(n.Expanded != nil && *n.Expanded, stateExpanded)
	add(n.Expanded != nil && !*n.Expanded, stateCollapsed)
	add(n.Modal, stateModal)
	add(n.Role == render.RoleTextBox && !n.Disabled, stateEditable, stateSelectableText)
	add(n.Role == render.RoleCheckBox, stateCheckable)
	add(n.Checked != nil && *n.Checked, stateChecked)
	slices.Sort(s)
	return s
}

// atspiStateSet is states as GetState's two words of bits.
func atspiStateSet(states []atspiState) []uint32 {
	out := []uint32{0, 0}
	for _, st := range states {
		out[st/32] |= 1 << (st % 32)
	}
	return out
}

// atspiActions are the actions the Action interface offers for a node, by AT-SPI name.
func atspiActions(n *render.AccessNode) []render.Action {
	var out []render.Action
	for _, a := range []render.Action{render.ActionClick, render.ActionIncrement, render.ActionDecrement} {
		if has(n, a) {
			out = append(out, a)
		}
	}
	return out
}

// atspiInterfaces are the interfaces a node's object implements.
func atspiInterfaces(n *render.AccessNode) []string {
	out := []string{"org.a11y.atspi.Accessible", "org.a11y.atspi.Component"}
	if len(atspiActions(n)) > 0 {
		out = append(out, "org.a11y.atspi.Action")
	}
	if n.Role == render.RoleTextBox {
		out = append(out, "org.a11y.atspi.Text", "org.a11y.atspi.EditableText")
	}
	if n.Role == render.RoleSlider {
		out = append(out, "org.a11y.atspi.Value")
	}
	return out
}

const atspiPrefix = "/org/a11y/atspi/accessible"

// atspiPath is the object path of node id of window win.
func atspiPath(win uint32, id uint64) string { return fmt.Sprintf("%s/%d_%d", atspiPrefix, win, id) }

func atspiParsePath(p string) (win uint32, id uint64, ok bool) {
	rest, found := strings.CutPrefix(p, atspiPrefix+"/")
	if !found {
		return 0, 0, false
	}
	w, n, found := strings.Cut(rest, "_")
	if !found {
		return 0, 0, false
	}
	wn, err1 := strconv.ParseUint(w, 10, 32)
	nn, err2 := strconv.ParseUint(n, 10, 64)
	return uint32(wn), nn, err1 == nil && err2 == nil
}

// atspiEvent is a signal on org.a11y.atspi.Event.*: Kind, Detail1, Detail2 and Data are its body, then no
// properties. Data is a string, a uint32, a node (its id, of the same window) or nil (an int32 0).
type atspiEvent struct {
	ID      uint64
	Member  string // "Object.StateChanged", "Focus.Focus"…
	Kind    string
	Detail1 int32
	Detail2 int32
	Data    any
}

type atspiNodeRef uint64

// atspiEvents are the signals an update to window's tree sends.
func atspiEvents(c accessChange, t *accessTree) []atspiEvent {
	var out []atspiEvent
	ev := func(id uint64, member, kind string, d1, d2 int32, data any) {
		out = append(out, atspiEvent{ID: id, Member: member, Kind: kind, Detail1: d1, Detail2: d2, Data: data})
	}
	state := func(id uint64, st atspiState, on bool) {
		d := int32(0)
		if on {
			d = 1
		}
		ev(id, "Object.StateChanged", atspiStateNames[st], d, 0, nil)
	}
	for _, ch := range c.Changed {
		e, ok := t.get(ch.ID)
		if !ok {
			continue
		}
		n := &e.node
		if ch.What&changeChildren != 0 {
			for i, k := range ch.OldKids {
				if !slices.Contains(e.kids, k) {
					ev(ch.ID, "Object.ChildrenChanged", "remove", int32(i), 0, atspiNodeRef(k))
				}
			}
			for i, k := range e.kids {
				if !slices.Contains(ch.OldKids, k) {
					ev(ch.ID, "Object.ChildrenChanged", "add", int32(i), 0, atspiNodeRef(k))
				}
			}
		}
		if ch.What&changeRole != 0 {
			ev(ch.ID, "Object.PropertyChange", "accessible-role", 0, 0, atspiRole(n.Role))
		}
		if ch.What&changeName != 0 {
			ev(ch.ID, "Object.PropertyChange", "accessible-name", 0, 0, n.Name)
		}
		if ch.What&changeValue != 0 {
			if n.Role == render.RoleTextBox {
				at, del, ins := textEdit(ch.Old.Value, n.Value)
				if del != "" {
					ev(ch.ID, "Object.TextChanged", "delete", int32(at), int32(len([]rune(del))), del)
				}
				if ins != "" {
					ev(ch.ID, "Object.TextChanged", "insert", int32(at), int32(len([]rune(ins))), ins)
				}
			} else {
				ev(ch.ID, "Object.PropertyChange", "accessible-value", 0, 0, n.Value)
			}
		}
		if ch.What&changeSelection != 0 && n.Role == render.RoleTextBox {
			if ch.Old.Selection.End != n.Selection.End {
				ev(ch.ID, "Object.TextCaretMoved", "", int32(n.Selection.End), 0, nil)
			}
			if ch.Old.Selection.Start != ch.Old.Selection.End || n.Selection.Start != n.Selection.End {
				ev(ch.ID, "Object.TextSelectionChanged", "", 0, 0, nil)
			}
		}
		if ch.What&changeStates != 0 {
			o := &ch.Old
			if o.Disabled != n.Disabled {
				state(ch.ID, stateEnabled, !n.Disabled)
				state(ch.ID, stateSensitive, !n.Disabled)
			}
			if o.Selected != n.Selected {
				state(ch.ID, stateSelected, n.Selected)
			}
			if !sameBool(o.Expanded, n.Expanded) && n.Expanded != nil {
				state(ch.ID, stateExpanded, *n.Expanded)
			}
			if !sameBool(o.Checked, n.Checked) {
				state(ch.ID, stateChecked, n.Checked != nil && *n.Checked)
			}
		}
	}
	if c.Moved {
		if _, ok := t.get(c.Blurred); ok && c.Blurred != 0 {
			state(c.Blurred, stateFocused, false)
		}
		if c.Focus != 0 {
			state(c.Focus, stateFocused, true)
			ev(c.Focus, "Focus.Focus", "", 0, 0, nil)
		}
	}
	return out
}

// atspiTextAt is GetTextAtOffset and its neighbours for a boundary type, and GetStringAtOffset for a granularity
// (granular true).
func atspiTextAt(s string, offset int, kind uint32, granular bool, dir int) (string, int, int) {
	unit := unitLine
	switch {
	case granular && kind == 0, !granular && kind == 0:
		unit = unitChar
	case granular && kind == 1, !granular && (kind == 1 || kind == 2):
		unit = unitWord
	}
	_, start, end := textAround(s, offset, unit)
	switch {
	case dir < 0 && start > 0:
		return textAround(s, start-1, unit)
	case dir < 0:
		return "", 0, 0
	case dir > 0:
		if end >= len([]rune(s)) {
			return "", end, end
		}
		return textAround(s, end, unit)
	}
	return textAround(s, offset, unit)
}
