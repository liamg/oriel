// Package layout is the one tree every view lives in (ui.md 01d): splits and tab groups in main, docks on
// the left, right and bottom edges, floats, and a drop-down. Any view can be moved to any placement; zoom
// fills the window with one and restores the exact tree after. The model is plain data, saved and restored
// as JSON; View draws it with ui, with tabs you drag onto drop targets.
package layout

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/liamg/oriel/render"
)

// ViewRef is one open view: a registered view id and the instance of it ("terminal", "2").
type ViewRef struct {
	ID       string `json:"id"`
	Instance string `json:"instance,omitempty"`
	Title    string `json:"title,omitempty"`
}

func (v ViewRef) String() string {
	if v.Instance == "" {
		return v.ID
	}
	return v.ID + "#" + v.Instance
}

type Axis string

const (
	Horizontal Axis = "h" // children side by side
	Vertical   Axis = "v" // children stacked
)

type Edge string

const (
	Left   Edge = "left"
	Right  Edge = "right"
	Bottom Edge = "bottom"
)

// Node is a Split or a Leaf.
type Node struct {
	Split   Axis      `json:"split,omitempty"` // empty for a leaf
	Kids    []*Node   `json:"kids,omitempty"`
	Weights []float32 `json:"weights,omitempty"`
	Tabs    []ViewRef `json:"tabs,omitempty"`
	Active  int       `json:"active,omitempty"`
}

func (n *Node) leaf() bool { return n.Split == "" }

type Dock struct {
	Leaf *Node   `json:"leaf"`
	Size float32 `json:"size"` // width, or height for the bottom dock
	Open bool    `json:"open"`
}

type Float struct {
	Leaf *Node       `json:"leaf"`
	Rect render.Rect `json:"rect"`
}

// Root is a whole layout.
type Root struct {
	Main     *Node          `json:"main"`
	Docks    map[Edge]*Dock `json:"docks"`
	Floats   []*Float       `json:"floats,omitempty"`
	DropDown *Dock          `json:"dropdown"`
	Zoomed   *ViewRef       `json:"zoomed,omitempty"`
	Focus    *ViewRef       `json:"focus,omitempty"`
	prev     *ViewRef       // focus before the last FocusDir, which breaks its ties
	onClose  []func(ViewRef)
}

// OnClose calls fn after each view is closed.
func (r *Root) OnClose(fn func(ViewRef)) (cancel func()) {
	r.onClose = append(r.onClose, fn)
	i := len(r.onClose) - 1
	return func() { r.onClose[i] = nil }
}

// Shown is the view each of main's leaves shows, top-left to bottom-right.
func (r *Root) Shown() []ViewRef {
	var out []ViewRef
	var walk func(n *Node)
	walk = func(n *Node) {
		if n.leaf() {
			if len(n.Tabs) > 0 {
				out = append(out, n.Tabs[n.Active])
			}
			return
		}
		for _, k := range n.Kids {
			walk(k)
		}
	}
	walk(r.Main)
	return out
}

// Visible is every view on screen: main's (as Shown), then the open docks', the drop-down's when open, and the
// floats'.
func (r *Root) Visible() []ViewRef {
	out := r.Shown()
	leaf := func(n *Node) {
		if n != nil && len(n.Tabs) > 0 {
			out = append(out, n.Tabs[n.Active])
		}
	}
	for _, e := range []Edge{Left, Right, Bottom} {
		if d := r.Docks[e]; d != nil && d.Open {
			leaf(d.Leaf)
		}
	}
	if r.DropDown != nil && r.DropDown.Open {
		leaf(r.DropDown.Leaf)
	}
	for _, f := range r.Floats {
		leaf(f.Leaf)
	}
	return out
}

// All is every view in the layout: main's tabs, then the docks', the drop-down's and the floats'.
func (r *Root) All() []ViewRef {
	var out []ViewRef
	for _, n := range r.leaves() {
		out = append(out, n.Tabs...)
	}
	return out
}

// leaves are the layout's leaves, in All's order.
func (r *Root) leaves() []*Node {
	var out []*Node
	var walk func(n *Node)
	walk = func(n *Node) {
		if n.leaf() {
			out = append(out, n)
			return
		}
		for _, k := range n.Kids {
			walk(k)
		}
	}
	walk(r.Main)
	for _, e := range []Edge{Left, Right, Bottom} {
		if d := r.Docks[e]; d != nil && d.Leaf != nil {
			out = append(out, d.Leaf)
		}
	}
	if r.DropDown != nil && r.DropDown.Leaf != nil {
		out = append(out, r.DropDown.Leaf)
	}
	for _, f := range r.Floats {
		out = append(out, f.Leaf)
	}
	return out
}

// Rename makes every reference to from one to to: its tab, the focus and the zoom.
func (r *Root) Rename(from, to ViewRef) {
	for _, n := range r.leaves() {
		for i, v := range n.Tabs {
			if v == from {
				n.Tabs[i] = to
			}
		}
	}
	for _, p := range []*ViewRef{r.Focus, r.Zoomed, r.prev} {
		if p != nil && *p == from {
			*p = to
		}
	}
}

// Row is the views shown side by side (or stacked) with v in the split holding v's leaf, in order, and v's place
// among them. A place that's a split itself holds the zero ViewRef. ok is false when v's leaf isn't in a split.
func (r *Root) Row(v ViewRef) (row []ViewRef, at int, ok bool) {
	l := r.leafIn(r.Main, v)
	if l == nil {
		return nil, 0, false
	}
	parent, i := r.parentOf(r.Main, l)
	if parent == nil {
		return nil, 0, false
	}
	for _, k := range parent.Kids {
		if k.leaf() && len(k.Tabs) > 0 {
			row = append(row, k.Tabs[k.Active])
		} else {
			row = append(row, ViewRef{})
		}
	}
	return row, i, true
}

// Swap exchanges where a and b are.
func (r *Root) Swap(a, b ViewRef) {
	wa, wb := r.Find(a), r.Find(b)
	if wa == nil || wb == nil {
		return
	}
	ia, ib := slices.Index(wa.Leaf.Tabs, a), slices.Index(wb.Leaf.Tabs, b)
	wa.Leaf.Tabs[ia], wb.Leaf.Tabs[ib] = b, a
}

// Show makes v the tab its leaf shows, and focuses it.
func (r *Root) Show(v ViewRef) {
	w := r.Find(v)
	if w == nil {
		return
	}
	w.Leaf.Active = slices.Index(w.Leaf.Tabs, v)
	r.Focus = &v
}

// New returns an empty layout: an empty main, closed docks.
func New() *Root {
	return &Root{
		Main:     &Node{},
		Docks:    map[Edge]*Dock{Left: {Leaf: &Node{}, Size: 250}, Right: {Leaf: &Node{}, Size: 380}, Bottom: {Leaf: &Node{}, Size: 200}},
		DropDown: &Dock{Leaf: &Node{}, Size: 360},
	}
}

type Placement string

const (
	Docked   Placement = "dock"
	Split    Placement = "split"
	Tab      Placement = "tab"
	Floating Placement = "float"
	DropDown Placement = "dropdown"
)

// Side is where a split goes, relative to the leaf it splits.
type Side string

const (
	SideLeft  Side = "left"
	SideRight Side = "right"
	SideUp    Side = "up"
	SideDown  Side = "down"
)

// Opts say where in a placement: the dock's edge, the split's side and the leaf it splits (the focused one
// by default), a float's rect.
type Opts struct {
	Edge   Edge        `json:"edge,omitempty"`
	Side   Side        `json:"side,omitempty"`
	Beside *ViewRef    `json:"beside,omitempty"`
	Rect   render.Rect `json:"rect,omitzero"`
	Size   float32     `json:"size,omitempty"` // a closed dock opens at this size
}

// Open puts v at p and focuses it. A view already open is moved instead.
func (r *Root) Open(v ViewRef, p Placement, o Opts) error {
	if r.Find(v) != nil {
		return r.Move(v, p, o)
	}
	switch p {
	case Docked:
		edge := o.Edge
		if edge == "" {
			edge = Bottom
		}
		d, ok := r.Docks[edge]
		if !ok {
			return fmt.Errorf("no dock on edge %q", edge)
		}
		if !d.Open && o.Size > 0 {
			d.Size = o.Size
		}
		d.Leaf.add(v)
		d.Open = true
	case Tab:
		r.mainLeaf(o.Beside).add(v)
	case Split:
		side := o.Side
		if side == "" {
			side = SideRight
		}
		target := r.mainLeaf(o.Beside)
		if len(target.Tabs) == 0 {
			target.add(v)
			break
		}
		r.split(target, side, &Node{Tabs: []ViewRef{v}})
	case Floating:
		rect := o.Rect
		if rect == (render.Rect{}) {
			rect = render.XYWH(200, 120, 640, 400)
		}
		r.Floats = append(r.Floats, &Float{Leaf: &Node{Tabs: []ViewRef{v}}, Rect: rect})
	case DropDown:
		r.DropDown.Leaf.add(v)
		r.DropDown.Open = true
	default:
		return fmt.Errorf("unknown placement %q", p)
	}
	r.Focus = &v
	return nil
}

func (n *Node) add(v ViewRef) {
	n.Tabs = append(n.Tabs, v)
	n.Active = len(n.Tabs) - 1
}

// mainLeaf is the main leaf holding beside, else the focused view's, else the first.
func (r *Root) mainLeaf(beside *ViewRef) *Node {
	for _, v := range []*ViewRef{beside, r.Focus} {
		if v == nil {
			continue
		}
		if l := r.leafIn(r.Main, *v); l != nil {
			return l
		}
	}
	return firstLeaf(r.Main)
}

func firstLeaf(n *Node) *Node {
	for !n.leaf() {
		n = n.Kids[0]
	}
	return n
}

func (r *Root) leafIn(n *Node, v ViewRef) *Node {
	if n.leaf() {
		if slices.Contains(n.Tabs, v) {
			return n
		}
		return nil
	}
	for _, k := range n.Kids {
		if l := r.leafIn(k, v); l != nil {
			return l
		}
	}
	return nil
}

// split replaces target with a split of target and add, add on side.
func (r *Root) split(target *Node, side Side, add *Node) {
	axis := Horizontal
	if side == SideUp || side == SideDown {
		axis = Vertical
	}
	before := side == SideLeft || side == SideUp
	parent, i := r.parentOf(r.Main, target)
	if parent != nil && parent.Split == axis {
		at := i + 1
		if before {
			at = i
		}
		w := parent.Weights[i] / 2
		parent.Weights[i] = w
		parent.Kids = slices.Insert(parent.Kids, at, add)
		parent.Weights = slices.Insert(parent.Weights, at, w)
		return
	}
	old := *target
	kids := []*Node{&old, add}
	if before {
		kids = []*Node{add, &old}
	}
	*target = Node{Split: axis, Kids: kids, Weights: []float32{1, 1}}
}

func (r *Root) parentOf(n, target *Node) (*Node, int) {
	if n.leaf() {
		return nil, -1
	}
	for i, k := range n.Kids {
		if k == target {
			return n, i
		}
		if p, j := r.parentOf(k, target); p != nil {
			return p, j
		}
	}
	return nil, -1
}

// Where is a view, as the layout found it.
type Where struct {
	Placement Placement
	Edge      Edge
	Leaf      *Node
}

// Find says where v is, or nil.
func (r *Root) Find(v ViewRef) *Where {
	if l := r.leafIn(r.Main, v); l != nil {
		return &Where{Placement: Tab, Leaf: l}
	}
	for _, e := range []Edge{Left, Right, Bottom} {
		if slices.Contains(r.Docks[e].Leaf.Tabs, v) {
			return &Where{Placement: Docked, Edge: e, Leaf: r.Docks[e].Leaf}
		}
	}
	for _, f := range r.Floats {
		if slices.Contains(f.Leaf.Tabs, v) {
			return &Where{Placement: Floating, Leaf: f.Leaf}
		}
	}
	if slices.Contains(r.DropDown.Leaf.Tabs, v) {
		return &Where{Placement: DropDown, Leaf: r.DropDown.Leaf}
	}
	return nil
}

// Close removes v, tidying away an empty leaf, split or float.
func (r *Root) Close(v ViewRef) {
	w := r.Find(v)
	if w == nil {
		return
	}
	l := w.Leaf
	i := slices.Index(l.Tabs, v)
	l.Tabs = slices.Delete(l.Tabs, i, i+1)
	l.Active = max(0, min(l.Active, len(l.Tabs)-1))
	if i < l.Active {
		l.Active--
	}
	if len(l.Tabs) == 0 {
		switch w.Placement {
		case Tab:
			r.prune(l)
		case Floating:
			r.Floats = slices.DeleteFunc(r.Floats, func(f *Float) bool { return f.Leaf == l })
		case Docked:
			r.Docks[w.Edge].Open = false
		case DropDown:
			r.DropDown.Open = false
		}
	}
	if r.Zoomed != nil && *r.Zoomed == v {
		r.Zoomed = nil
	}
	if r.Focus != nil && *r.Focus == v {
		r.Focus = nil
		if len(l.Tabs) > 0 {
			f := l.Tabs[l.Active]
			r.Focus = &f
		} else if shown := r.Shown(); len(shown) > 0 {
			r.Focus = &shown[0]
		}
	}
	for _, fn := range r.onClose {
		if fn != nil {
			fn(v)
		}
	}
}

// prune takes an empty leaf out of main, collapsing a split left with one child.
func (r *Root) prune(l *Node) {
	parent, i := r.parentOf(r.Main, l)
	if parent == nil {
		return // main's only leaf stays, empty
	}
	parent.Kids = slices.Delete(parent.Kids, i, i+1)
	parent.Weights = slices.Delete(parent.Weights, i, i+1)
	if len(parent.Kids) == 1 {
		*parent = *parent.Kids[0]
	}
}

// Move takes v from wherever it is to p.
func (r *Root) Move(v ViewRef, p Placement, o Opts) error {
	if r.Find(v) == nil {
		return fmt.Errorf("%s isn't open", v)
	}
	if p == Split && o.Beside != nil && *o.Beside == v {
		o.Beside = nil
	}
	r.Close(v)
	return r.Open(v, p, o)
}

// Zone is where on a leaf a drag drops: its middle (a tab there) or an edge (a split).
type Zone string

const (
	ZoneCenter Zone = "center"
	ZoneLeft   Zone = "left"
	ZoneRight  Zone = "right"
	ZoneTop    Zone = "top"
	ZoneBottom Zone = "bottom"
)

// Target is where a dragged view lands: a zone of a main leaf (by a view in it), a dock, or a float.
type Target struct {
	Beside *ViewRef     `json:"beside,omitempty"` // a view in the leaf dropped on; nil drops on main's first leaf
	Zone   Zone         `json:"zone,omitempty"`
	Dock   Edge         `json:"dock,omitempty"`
	Float  *render.Rect `json:"float,omitempty"`
}

// Drop moves v to t.
func (r *Root) Drop(v ViewRef, t Target) error {
	switch {
	case t.Dock != "":
		return r.Move(v, Docked, Opts{Edge: t.Dock})
	case t.Float != nil:
		return r.Move(v, Floating, Opts{Rect: *t.Float})
	case t.Zone == ZoneCenter || t.Zone == "":
		return r.Move(v, Tab, Opts{Beside: t.Beside})
	}
	side := map[Zone]Side{ZoneLeft: SideLeft, ZoneRight: SideRight, ZoneTop: SideUp, ZoneBottom: SideDown}[t.Zone]
	if t.Beside != nil && *t.Beside == v {
		// Splitting a view off its own leaf, which must keep something.
		w := r.Find(v)
		if w == nil || w.Placement != Tab || len(w.Leaf.Tabs) == 1 {
			return nil
		}
		leaf := w.Leaf
		r.Close(v)
		r.split(leaf, side, &Node{Tabs: []ViewRef{v}})
		r.Focus = &v
		return nil
	}
	return r.Move(v, Split, Opts{Side: side, Beside: t.Beside})
}

// ToggleZoom fills the window with v, or restores the layout if v is zoomed. The tree isn't touched, so
// what comes back is exactly what was there.
func (r *Root) ToggleZoom(v ViewRef) {
	if r.Zoomed != nil && *r.Zoomed == v {
		r.Zoomed = nil
		return
	}
	r.Zoomed = &v
}

// ToggleDock opens or closes a dock.
func (r *Root) ToggleDock(e Edge) {
	if d, ok := r.Docks[e]; ok {
		d.Open = !d.Open
	}
}

// Equalize evens out every split's weights.
func (r *Root) Equalize() {
	var walk func(n *Node)
	walk = func(n *Node) {
		for i := range n.Weights {
			n.Weights[i] = 1
		}
		for _, k := range n.Kids {
			walk(k)
		}
	}
	walk(r.Main)
}

// Snapshot is a saved layout.
type Snapshot []byte

func (r *Root) Save() Snapshot {
	b, _ := json.Marshal(r)
	return b
}

// Restore reads a saved layout into a new Root.
func Restore(s Snapshot) (*Root, error) {
	r := New()
	if err := json.Unmarshal(s, r); err != nil {
		return nil, err
	}
	return r, nil
}

// Load replaces the layout with a saved one in place, keeping whoever listens for closes.
func (r *Root) Load(s Snapshot) error {
	n, err := Restore(s)
	if err != nil {
		return err
	}
	n.onClose = r.onClose
	*r = *n
	return nil
}

// Dump writes the layout as an indented tree, for golden tests.
func (r *Root) Dump() string {
	var b []byte
	w := func(depth int, f string, args ...any) {
		for range depth {
			b = append(b, "  "...)
		}
		b = fmt.Appendf(b, f+"\n", args...)
	}
	var node func(n *Node, depth int)
	node = func(n *Node, depth int) {
		if n.leaf() {
			w(depth, "leaf %s", tabs(n))
			return
		}
		w(depth, "split %s %v", n.Split, n.Weights)
		for _, k := range n.Kids {
			node(k, depth+1)
		}
	}
	w(0, "main")
	node(r.Main, 1)
	for _, e := range []Edge{Left, Right, Bottom} {
		d := r.Docks[e]
		if d.Open || len(d.Leaf.Tabs) > 0 {
			w(0, "dock %s open=%v %s", e, d.Open, tabs(d.Leaf))
		}
	}
	for _, f := range r.Floats {
		w(0, "float %v,%v %vx%v %s", f.Rect.Min.X, f.Rect.Min.Y, f.Rect.Dx(), f.Rect.Dy(), tabs(f.Leaf))
	}
	if r.DropDown.Open || len(r.DropDown.Leaf.Tabs) > 0 {
		w(0, "dropdown open=%v %s", r.DropDown.Open, tabs(r.DropDown.Leaf))
	}
	if r.Zoomed != nil {
		w(0, "zoomed %s", r.Zoomed)
	}
	if r.Focus != nil {
		w(0, "focus %s", r.Focus)
	}
	return string(b)
}

func tabs(n *Node) string {
	var out []byte
	out = append(out, '[')
	for i, t := range n.Tabs {
		if i > 0 {
			out = append(out, ' ')
		}
		if i == n.Active {
			out = append(out, '*')
		}
		out = append(out, t.String()...)
	}
	return string(append(out, ']'))
}
