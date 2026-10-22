package gio

import (
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf16"

	"gioui.org/io/event"

	"github.com/liamg/oriel/render"
)

// access is a window's side of the platform's accessibility bridge: the tree the app sent, the actions a screen
// reader asks for, and the bridge, which does nothing until a screen reader asks.
type access struct {
	tree    accessTree
	actions chan render.AccessAction
	geom    func() accessGeom
	bridge  accessBridge
}

// accessGeom is where the window is, for the coordinates a screen reader wants.
type accessGeom struct {
	Origin render.Point // the window's top left on the screen, in the platform's units
	Scale  float32      // pixels per app unit
	Dpi    float32      // pixels per Dp, which is a point on macOS
	Active bool         // the window has the keyboard
}

// accessBridge is a platform's accessibility API, fed from the tree's changes.
type accessBridge interface {
	view(ev event.Event) // the native view event Gio sends once the window's made
	update(c accessChange)
	activate(active bool)
	close()
}

func newAccess(geom func() accessGeom) *access {
	return &access{actions: make(chan render.AccessAction, 64), geom: geom}
}

// act queues a screen reader's request for the app's loop. It never blocks the platform's thread: a request past a
// full queue is dropped.
func (a *access) act(x render.AccessAction) bool {
	if e, ok := a.tree.get(x.ID); !ok || e.node.Disabled {
		return false
	}
	select {
	case a.actions <- x:
		return true
	default:
		return false
	}
}

// accessTree mirrors the app's accessibility tree from its updates, for a bridge to read from its own threads.
type accessTree struct {
	mu    sync.Mutex
	nodes map[uint64]*accessEntry
	focus uint64
}

type accessEntry struct {
	node   render.AccessNode // without its Children
	kids   []uint64
	parent uint64
}

// accessChange is what an update did to the tree.
type accessChange struct {
	Added   []uint64
	Removed []uint64
	Changed []nodeChange
	Focus   uint64 // the focused node, if FocusMoved
	Blurred uint64 // the node focused before
	Moved   bool   // focus moved
}

type nodeChange struct {
	ID      uint64
	What    changeKind
	Old     render.AccessNode
	OldKids []uint64
}

type changeKind uint16

const (
	changeName changeKind = 1 << iota
	changeDescription
	changeValue
	changeSelection
	changeStates
	changeBounds
	changeChildren
	changeRole
	changeActions
)

func (c accessChange) empty() bool {
	return len(c.Added) == 0 && len(c.Removed) == 0 && len(c.Changed) == 0 && !c.Moved
}

func (t *accessTree) apply(u render.AccessUpdate) accessChange {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.nodes == nil {
		t.nodes = map[uint64]*accessEntry{}
	}
	var c accessChange
	for _, id := range u.Removed {
		if _, ok := t.nodes[id]; ok {
			delete(t.nodes, id)
			c.Removed = append(c.Removed, id)
		}
	}
	for _, n := range u.Changed {
		kids := make([]uint64, len(n.Children))
		for i, k := range n.Children {
			kids[i] = k.ID
		}
		flat := *n
		flat.Children = nil
		e, ok := t.nodes[n.ID]
		if !ok {
			e = &accessEntry{}
			t.nodes[n.ID] = e
			c.Added = append(c.Added, n.ID)
		} else if what := nodeDiff(&e.node, &flat, e.kids, kids); what != 0 {
			c.Changed = append(c.Changed, nodeChange{ID: n.ID, What: what, Old: e.node, OldKids: e.kids})
		}
		e.node, e.kids = flat, kids
	}
	for _, n := range u.Changed {
		for _, k := range n.Children {
			if e, ok := t.nodes[k.ID]; ok {
				e.parent = n.ID
			}
		}
	}
	if u.Focus != t.focus {
		c.Moved, c.Blurred, c.Focus = true, t.focus, u.Focus
		t.focus = u.Focus
	}
	return c
}

func nodeDiff(a, b *render.AccessNode, ak, bk []uint64) changeKind {
	var what changeKind
	flag := func(on bool, k changeKind) {
		if on {
			what |= k
		}
	}
	flag(a.Name != b.Name, changeName)
	flag(a.Description != b.Description, changeDescription)
	flag(a.Value != b.Value, changeValue)
	flag(a.Selection != b.Selection, changeSelection)
	flag(a.Focused != b.Focused || a.Disabled != b.Disabled || a.Selected != b.Selected || a.Modal != b.Modal ||
		!sameBool(a.Expanded, b.Expanded) || !sameBool(a.Checked, b.Checked), changeStates)
	flag(a.Bounds != b.Bounds, changeBounds)
	flag(!slices.Equal(ak, bk), changeChildren)
	flag(a.Role != b.Role, changeRole)
	flag(!slices.Equal(a.Actions, b.Actions), changeActions)
	return what
}

// get is a copy of node id's entry.
func (t *accessTree) get(id uint64) (accessEntry, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.nodes[id]
	if !ok {
		return accessEntry{}, false
	}
	out := *e
	out.kids = slices.Clone(e.kids)
	return out, true
}

func (t *accessTree) focused() uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.focus
}

// hit is the deepest node under p, in app units from the window's top left, the topmost first; the root if none.
func (t *accessTree) hit(p render.Point) uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	id := uint64(0)
	for {
		e, ok := t.nodes[id]
		if !ok {
			return id
		}
		next, found := uint64(0), false
		for _, k := range slices.Backward(e.kids) {
			if ke, ok := t.nodes[k]; ok && inside(ke.node.Bounds, p) {
				next, found = k, true
				break
			}
		}
		if !found {
			return id
		}
		id = next
	}
}

func inside(r render.Rect, p render.Point) bool {
	return p.X >= r.Min.X && p.X < r.Max.X && p.Y >= r.Min.Y && p.Y < r.Max.Y
}

// index is id's place among its parent's children, or -1.
func (t *accessTree) index(id uint64) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.nodes[id]
	if !ok {
		return -1
	}
	if p, ok := t.nodes[e.parent]; ok {
		return slices.Index(p.kids, id)
	}
	return -1
}

func has(n *render.AccessNode, a render.Action) bool { return slices.Contains(n.Actions, a) }

// toUTF16 is r, in runes of s, as a location and length in UTF-16 code units, as AppKit counts.
func toUTF16(s string, r render.TextRange) (loc, n int) {
	var i, at int
	for _, c := range s {
		if i == r.Start {
			loc = at
		}
		if i == r.End {
			return loc, at - loc
		}
		at += utf16.RuneLen(c)
		i++
	}
	if r.Start >= i {
		loc = at
	}
	return loc, max(at-loc, 0)
}

// fromUTF16 is the location and length in UTF-16 code units, as a range of runes of s.
func fromUTF16(s string, loc, n int) render.TextRange {
	var r render.TextRange
	var i, at int
	r.Start, r.End = -1, -1
	for _, c := range s {
		if r.Start < 0 && at >= loc {
			r.Start = i
		}
		if r.End < 0 && at >= loc+n {
			r.End = i
		}
		at += utf16.RuneLen(c)
		i++
	}
	if r.Start < 0 {
		r.Start = i
	}
	if r.End < 0 {
		r.End = i
	}
	return r
}

type textUnit int

const (
	unitChar textUnit = iota
	unitWord
	unitLine
)

// textAround is the char, word or line of s at offset (in runes), with its range.
func textAround(s string, offset int, unit textUnit) (string, int, int) {
	rs := []rune(s)
	if offset < 0 || offset > len(rs) {
		return "", 0, 0
	}
	switch unit {
	case unitChar:
		if offset == len(rs) {
			return "", offset, offset
		}
		return string(rs[offset]), offset, offset + 1
	case unitWord:
		is := unicode.IsSpace
		start, end := offset, offset
		for start > 0 && !is(rs[start-1]) {
			start--
		}
		for end < len(rs) && !is(rs[end]) {
			end++
		}
		for end < len(rs) && is(rs[end]) {
			end++
		}
		return string(rs[start:end]), start, end
	default:
		start, end := offset, offset
		for start > 0 && rs[start-1] != '\n' {
			start--
		}
		for end < len(rs) && rs[end] != '\n' {
			end++
		}
		if end < len(rs) {
			end++
		}
		return string(rs[start:end]), start, end
	}
}

// textEdit is how old became new as one edit: at offset (in runes), deleted gave way to inserted.
func textEdit(old, new string) (offset int, deleted, inserted string) {
	a, b := []rune(old), []rune(new)
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	s := 0
	for s < len(a)-p && s < len(b)-p && a[len(a)-1-s] == b[len(b)-1-s] {
		s++
	}
	return p, string(a[p : len(a)-s]), string(b[p : len(b)-s])
}

// spliced is s with runes [start, end) replaced by with.
func spliced(s string, start, end int, with string) string {
	rs := []rune(s)
	start, end = min(max(start, 0), len(rs)), min(max(end, 0), len(rs))
	if end < start {
		start, end = end, start
	}
	var b strings.Builder
	b.WriteString(string(rs[:start]))
	b.WriteString(with)
	b.WriteString(string(rs[end:]))
	return b.String()
}
