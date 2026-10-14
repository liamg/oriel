// Package ui is the immediate-mode toolkit: widgets, layout, theme, animation and input routing.
//
// Each frame, widget calls on a Ctx build a tree of nodes. The UI lays the tree out (fixed, fit, grow and
// percent sizes along rows and columns, then floats placed against anchors), paints it into a render.Frame
// in z-order, and keeps what must outlive the frame by id: rects, scroll offsets.
package ui

import (
	"fmt"
	"hash/fnv"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
)

// ID is a node's stable identity: its parent's id hashed with its key.
type ID uint64

type kind uint8

const (
	kBox kind = iota
	kText
	kPaint
)

type elem struct {
	id     ID
	name   string
	kind   kind
	style  Style
	text   string
	ts     TextStyle
	run    *typeset.Run
	paint  func(f render.Frame, r render.Rect)
	kids   []*elem
	parent *elem
	scale  float32 // what it and its subtree are drawn at (Style.Scale, times its parent's); 0 is 1

	controls bool // it or something in it can be clicked or focused, or keeps its width (Style.Keep)

	pos, size [2]float32
	content   [2]float32 // children's extent, for scrolling
	keys      map[string]int
	fx        fx
}

type state struct {
	rect       render.Rect
	scroll     [2]float32
	ext, view  [2]float32 // a scrolling box's content and what of it shows, along each axis
	scrolledAt time.Time
	seen       int
	data       any // a widget's own state: State[T]
}

// UI holds what lasts between frames.
type UI struct {
	Title string // the window's accessible name

	sh    typeset.Shaper
	text  TextStyle
	opts  Options
	frame int
	clock func() time.Time

	root    *elem
	byID    map[ID]*elem
	floats  []*elem
	layered map[int]bool // the float layers (Style.Z) last frame drew
	asked   map[int]bool // the layers Layered was asked about this frame
	states  map[ID]*state
	hits    []hit
	flags   map[ID]flags // from the latest frame, which input resolves against
	parents map[ID]ID

	theme *theme.Resolved

	in        input
	springs   map[ID]*spring
	glides    map[ID]*glideState
	fills     map[ID]*fillState
	exitable  map[ID]*elem // this frame's nodes with an Exit
	exiting   map[ID]*exiting
	noHits    bool
	animating bool
	wake      time.Time
	clip      render.Clipboard
}

type flags struct {
	interactive, focusable, disabled bool
	scrollX, scrollY, wheel          bool
	bars                             Bars
	cursor                           render.Cursor
	tooltip, help, keyMode           string
	trap                             bool
	z                                int
	parent                           ID
	drag                             *DragData
	drop                             []string
}

type hit struct {
	id   ID
	rect render.Rect
}

// New returns a UI that shapes text with sh, in text by default.
func New(sh typeset.Shaper, text TextStyle) *UI {
	return &UI{sh: sh, text: text, opts: defaultOptions(text), states: map[ID]*state{}, byID: map[ID]*elem{},
		flags: map[ID]flags{}, springs: map[ID]*spring{}}
}

// Fork is a copy of u to build and paint a frame with apart, with another theme say, starting from u's state (scroll
// offsets, widget state, focus) and leaving u as it was. A widget's own state (State[T]) is shared, not copied.
func (u *UI) Fork() *UI {
	f := *u
	f.byID, f.floats, f.hits = map[ID]*elem{}, nil, nil
	f.states = make(map[ID]*state, len(u.states))
	for id, st := range u.states {
		c := *st
		f.states[id] = &c
	}
	f.springs = make(map[ID]*spring, len(u.springs))
	for id, sp := range u.springs {
		c := *sp
		f.springs[id] = &c
	}
	f.flags = maps.Clone(u.flags)
	f.glides, f.fills, f.exitable, f.exiting = maps.Clone(u.glides), maps.Clone(u.fills), maps.Clone(u.exitable), maps.Clone(u.exiting)
	f.parents = maps.Clone(u.parents)
	f.in.clicks, f.in.keys, f.in.context, f.in.wheel, f.in.claimed, f.in.help = nil, nil, nil, nil, nil, ""
	return &f
}

// SetClipboard gives widgets the system clipboard, for a text field's cut, copy and paste.
func (u *UI) SetClipboard(c render.Clipboard) { u.clip = c }

func (u *UI) Options() Options     { return u.opts }
func (u *UI) SetOptions(o Options) { u.opts = o }

// Frame builds, lays out and paints one frame of the given logical size.
func (u *UI) Frame(f render.Frame, size render.Point, build func(c *Ctx)) {
	u.Layout(size, build)
	u.Paint(f)
}

// Layout builds and lays out a frame without painting it.
func (u *UI) Layout(size render.Point, build func(c *Ctx)) {
	prev := make(map[ID]bool, len(u.byID))
	for id := range u.byID {
		prev[id] = true
	}
	u.frame++
	u.root = &elem{name: "root", style: Style{Dir: Column, W: Px(size.X), H: Px(size.Y)}}
	u.byID = map[ID]*elem{0: u.root}
	u.floats = u.floats[:0]
	u.animating, u.wake = false, time.Time{}
	build(&Ctx{u: u, e: u.root})
	u.settleFocus()
	if u.in.pending() {
		u.wake = u.now() // what the input changed shows next frame
	}
	u.in.clicks, u.in.keys, u.in.context, u.in.wheel, u.in.claimed, u.in.dropped = nil, nil, nil, nil, nil, nil
	u.bubble()
	u.tooltip()
	u.dragShown()
	for axis := range 2 {
		u.fit(u.root, axis)
		u.grow(u.root, axis)
	}
	u.place(u.root, 0, 0)
	layered := map[int]bool{}
	for i := 0; i < len(u.floats); i++ {
		u.placeFloat(u.floats[i])
		layered[u.floats[i].style.Z] = true
	}
	for z := range u.asked {
		if layered[z] != u.layered[z] {
			u.wakeAt(u.now()) // what was built asked about this layer (Layered), and it came or went
		}
	}
	u.layered, u.asked = layered, nil
	u.animate(prev)
	for id, st := range u.states {
		if st.seen != u.frame {
			delete(u.states, id)
		}
	}
	for id, sp := range u.springs {
		if sp.seen != u.frame {
			delete(u.springs, id)
		}
	}
	clear(u.flags)
	for id, e := range u.byID {
		s := e.style
		if s.Drag != nil || len(s.Drop) > 0 {
			s.Interactive, s.Focusable = true, true // a drag can start, and end, from the keyboard
			if s.KeyMode == "" {
				s.KeyMode = DragKeyMode
			}
		}
		if s.Interactive || s.Focusable || s.Disabled || s.ScrollX || s.ScrollY || s.Cursor != 0 || s.Tooltip != "" || s.Help != "" || s.Trap {
			var parent ID
			if e.parent != nil {
				parent = e.parent.id
			}
			u.flags[id] = flags{interactive: s.Interactive || s.Focusable || s.Tooltip != "", focusable: s.Focusable, disabled: s.Disabled,
				scrollX: s.ScrollX, scrollY: s.ScrollY, wheel: s.Wheel, bars: s.Bars, cursor: s.Cursor, tooltip: s.Tooltip, help: s.Help, keyMode: s.KeyMode,
				trap: s.Trap, z: s.Z, parent: parent, drag: s.Drag, drop: s.Drop}
		}
	}
	u.parents = map[ID]ID{}
	for id, e := range u.byID {
		if e.parent != nil {
			u.parents[id] = e.parent.id
		}
	}
	if _, ok := u.byID[u.in.focus]; !ok || u.flags[u.in.focus].disabled {
		u.in.focus = 0
	}
	if _, ok := u.byID[u.in.hot]; !ok {
		u.in.hot = 0
	}
}

// Layered reports whether last frame drew a float on layer z (a page at LayerPage, say): what's built beside one can
// depend on it.
func (u *UI) Layered(z int) bool {
	if u.asked == nil {
		u.asked = map[int]bool{}
	}
	u.asked[z] = true
	return u.layered[z]
}

// tooltip shows the hovered node's tooltip once the pointer has rested on it for the delay.
func (u *UI) tooltip() {
	in := &u.in
	tip := u.flags[in.hot].tooltip
	if tip == "" || !in.inside || in.active != 0 {
		return
	}
	due := in.hotAt.Add(u.opts.TooltipDelay)
	if u.now().Before(due) {
		u.wake = due
		return
	}
	at := in.pos
	c := &Ctx{u: u, e: u.root}
	c.Box(tooltipKey{}, Style{Pad: PadXY(8, 4), Fill: u.opts.TooltipFill, Stroke: u.opts.TooltipStroke, Border: 1, Radius: 4,
		Z: LayerTooltip, Float: &Float{Point: &at, Offset: render.Point{X: 12, Y: 18}}, Scale: u.opts.TooltipScale}, func(c *Ctx) {
		c.Text(nil, tip, u.opts.Tooltip)
	})
}

type tooltipKey struct{}

func (u *UI) state(id ID) *state {
	st, ok := u.states[id]
	if !ok {
		st = &state{}
		u.states[id] = st
	}
	return st
}

// Rect is where id was laid out in the latest frame.
func (u *UI) Rect(id ID) (render.Rect, bool) {
	st, ok := u.states[id]
	if !ok {
		return render.Rect{}, false
	}
	return st.rect, true
}

// Scroll is id's scroll offset.
func (u *UI) Scroll(id ID) render.Point {
	st := u.state(id)
	return render.Point{X: st.scroll[0], Y: st.scroll[1]}
}

// ScrollBy moves id's scroll offset; the next layout clamps it to the content.
func (u *UI) ScrollBy(id ID, dx, dy float32) {
	st := u.state(id)
	st.seen = u.frame
	st.scrolledAt = u.now()
	st.scroll[0] += dx
	st.scroll[1] += dy
}

// ScrollTo sets id's scroll offset; the next layout clamps it to the content.
func (u *UI) ScrollTo(id ID, p render.Point) {
	st := u.state(id)
	st.seen = u.frame
	st.scroll = [2]float32{p.X, p.Y}
}

// Hit lists the nodes painted at p in the latest frame, topmost first, clipped as they were drawn.
func (u *UI) Hit(p render.Point) []ID {
	var out []ID
	for i := len(u.hits) - 1; i >= 0; i-- {
		r := u.hits[i].rect
		if p.X >= r.Min.X && p.X < r.Max.X && p.Y >= r.Min.Y && p.Y < r.Max.Y {
			out = append(out, u.hits[i].id)
		}
	}
	return out
}

func childID(parent ID, key any) ID {
	h := fnv.New64a()
	var b [8]byte
	for i := range 8 {
		b[i] = byte(parent >> (8 * i))
	}
	h.Write(b[:])
	fmt.Fprintf(h, "%T:%v", key, key)
	return ID(h.Sum64())
}

func keyName(key any) string {
	switch k := key.(type) {
	case string:
		return k
	case int:
		return strconv.Itoa(k)
	}
	return fmt.Sprint(key)
}

// nextKey is the key the next child of e built from key gets: key itself, its index when nil, or key#n for
// the nth duplicate among its siblings.
func (e *elem) nextKey(key any) (any, string) {
	if key == nil {
		key = len(e.kids)
	}
	name := keyName(key)
	if n := e.keys[name]; n > 0 {
		key = fmt.Sprintf("%v#%d", key, n)
		name = fmt.Sprintf("%s#%d", name, n)
	}
	return key, name
}

// add appends a child built from key to e, giving it an id unique among its siblings.
func (u *UI) add(e *elem, key any, k kind) *elem {
	base := key
	if base == nil {
		base = len(e.kids)
	}
	key, name := e.nextKey(key)
	if e.keys == nil {
		e.keys = map[string]int{}
	}
	e.keys[keyName(base)]++
	c := &elem{id: childID(e.id, key), name: name, kind: k, parent: e, scale: e.scale}
	e.kids = append(e.kids, c)
	u.byID[c.id] = c
	st := u.state(c.id)
	st.seen = u.frame
	return c
}

func (s Size) clamp(v float32) float32 {
	if s.Max > 0 && v > s.Max {
		v = s.Max
	}
	return max(v, s.Min)
}

func (e *elem) sz(axis int) Size {
	if e.kind == kPaint || e.kind == kBox {
		if axis == 0 {
			return e.style.W
		}
		return e.style.H
	}
	return Size{}
}

func (e *elem) pad(axis int) (float32, float32) {
	if axis == 0 {
		return e.style.Pad.L, e.style.Pad.R
	}
	return e.style.Pad.T, e.style.Pad.B
}

func (e *elem) main(axis int) bool { return (axis == 0) == (e.style.Dir == Row) }

func (e *elem) scrolls(axis int) bool {
	if axis == 0 {
		return e.style.ScrollX
	}
	return e.style.ScrollY
}

func (e *elem) flow() []*elem {
	var out []*elem
	for _, c := range e.kids {
		if c.style.Float == nil {
			out = append(out, c)
		}
	}
	return out
}

// fit sizes e along axis from its content, children first.
func (u *UI) fit(e *elem, axis int) {
	for _, c := range e.kids {
		u.fit(c, axis)
	}
	e.controls = e.style.Interactive || e.style.Focusable || e.style.Keep
	for _, c := range e.kids {
		e.controls = e.controls || c.controls
	}
	var content float32
	switch e.kind {
	case kText:
		if axis == 0 {
			content = e.run.Advance
		} else {
			content = e.run.Ascent + e.run.Descent
		}
	case kBox:
		kids := e.flow()
		for i, c := range kids {
			if e.main(axis) {
				content += c.size[axis]
				if i > 0 {
					content += e.style.Gap
				}
			} else {
				content = max(content, c.size[axis])
			}
		}
		a, b := e.pad(axis)
		content += a + b
	}
	e.content[axis] = content
	s := e.sz(axis)
	v := content
	if s.Kind == Fixed {
		v = s.Value
	}
	e.size[axis] = s.clamp(v)
}

// grow hands e's space along axis to its percent and grow children, then recurses.
func (u *UI) grow(e *elem, axis int) {
	kids := e.flow()
	a, b := e.pad(axis)
	inner := e.size[axis] - a - b
	if !e.scrolls(axis) {
		for _, c := range kids {
			if s := c.sz(axis); s.Kind == Percent {
				c.size[axis] = s.clamp(s.Value * inner)
			}
		}
		if e.main(axis) {
			u.share(e, kids, axis, inner)
		} else {
			for _, c := range kids {
				if s := c.sz(axis); s.Kind == Grow {
					c.size[axis] = s.clamp(inner)
				}
			}
		}
		if axis == 0 {
			squeeze(e, kids, inner)
		}
	}
	if e.kind == kText && e.size[0] < e.run.Advance-slack {
		e.run = u.sh.Shape(Elide(u.sh, e.text, e.ts.Font, e.size[0]), e.ts.Font)
	}
	for _, c := range e.kids {
		u.grow(c, axis)
	}
}

// slack is how far, in pixels, content may pass its box unseen: sums of widths differ by rounding.
const slack = .5

// squeeze narrows e's content-sized children to fit its inner width so no text runs out of its box, last first (a
// row's start is what says most): content, then what holds a control (a tab, a button); a text narrowed this way is cut with "…". Fixed sizes keep theirs; a box
// that scrolls sideways keeps its content.
func squeeze(e *elem, kids []*elem, inner float32) {
	var content, controls []*elem
	for _, c := range kids {
		switch {
		case c.sz(0).Kind != Fit:
		case c.controls:
			controls = append(controls, c)
		default:
			content = append(content, c)
		}
	}
	if !e.main(0) {
		for _, c := range append(content, controls...) {
			if c.size[0] > inner+slack {
				c.size[0] = max(inner, c.sz(0).Min)
			}
		}
		return
	}
	used := e.style.Gap * float32(max(len(kids)-1, 0))
	for _, c := range kids {
		used += c.size[0]
	}
	over := used - inner
	for _, soft := range [][]*elem{content, controls} {
		if over > slack {
			over -= trim(soft, over)
		}
	}
}

// trim takes over off soft's widths, last first, as far as their Min allows, and says how much it took.
func trim(soft []*elem, over float32) float32 {
	var took float32
	for i := len(soft) - 1; i >= 0 && over-took > 0; i-- {
		c := soft[i]
		cut := min(over-took, max(c.size[0]-c.sz(0).Min, 0))
		c.size[0] -= cut
		took += cut
	}
	return took
}

// share splits what's left along the main axis between grow children by weight, honouring Min and Max.
func (u *UI) share(e *elem, kids []*elem, axis int, inner float32) {
	left := inner - e.style.Gap*float32(max(len(kids)-1, 0))
	var growing []*elem
	for _, c := range kids {
		if c.sz(axis).Kind == Grow {
			growing = append(growing, c)
		} else {
			left -= c.size[axis]
		}
	}
	// With room for each one's content, none gets less: its content is its Min. A box that scrolls needs none.
	need := map[*elem]float32{}
	var total float32
	for _, c := range growing {
		if !c.scrolls(axis) {
			need[c] = c.size[axis]
			total += c.size[axis]
		}
	}
	if total > left {
		clear(need)
	}
	for len(growing) > 0 {
		var weight float32
		for _, c := range growing {
			weight += max(c.sz(axis).Value, 0)
		}
		if weight == 0 {
			return
		}
		frozen := false
		for i := 0; i < len(growing); i++ {
			c := growing[i]
			s := c.sz(axis)
			s.Min = max(s.Min, need[c])
			want := max(left, 0) * s.Value / weight
			if got := s.clamp(want); got != want {
				c.size[axis] = got
				left -= got
				growing = slices.Delete(growing, i, i+1)
				frozen = true
				break
			}
		}
		if !frozen {
			for _, c := range growing {
				c.size[axis] = max(left, 0) * c.sz(axis).Value / weight
			}
			return
		}
	}
}

// place positions e at (x, y) and its flow children inside it.
func (u *UI) place(e *elem, x, y float32) {
	e.pos = [2]float32{x, y}
	st := u.state(e.id)
	st.rect = render.XYWH(x, y, e.size[0], e.size[1])
	if e.kind != kBox {
		return
	}
	kids := e.flow()
	origin := [2]float32{x + e.style.Pad.L, y + e.style.Pad.T}
	var inner [2]float32
	for axis := range 2 {
		a, b := e.pad(axis)
		inner[axis] = e.size[axis] - a - b
		if e.scrolls(axis) {
			ext := e.content[axis] - a - b
			if e.main(axis) {
				ext = 0
				for i, c := range kids {
					ext += c.size[axis]
					if i > 0 {
						ext += e.style.Gap
					}
				}
			} else {
				ext = 0
				for _, c := range kids {
					ext = max(ext, c.size[axis])
				}
			}
			st.scroll[axis] = max(0, min(st.scroll[axis], ext-inner[axis]))
			st.ext[axis], st.view[axis] = ext, inner[axis]
			origin[axis] -= st.scroll[axis]
		} else {
			st.scroll[axis] = 0
		}
	}
	m := 0
	if e.style.Dir == Column {
		m = 1
	}
	cr := 1 - m
	var used float32
	for _, c := range kids {
		used += c.size[m]
	}
	gap := e.style.Gap
	free := inner[m] - used - gap*float32(max(len(kids)-1, 0))
	off := float32(0)
	switch e.style.Main {
	case Center:
		off = free / 2
	case End:
		off = free
	case SpaceBetween:
		if len(kids) > 1 && free > 0 {
			gap += free / float32(len(kids)-1)
		}
	}
	if e.scrolls(m) {
		off = max(off, 0)
	}
	for _, c := range kids {
		var p [2]float32
		p[m] = origin[m] + off
		p[cr] = origin[cr]
		switch e.style.Cross {
		case Center:
			p[cr] += (inner[cr] - c.size[cr]) / 2
		case End:
			p[cr] += inner[cr] - c.size[cr]
		}
		u.place(c, p[0], p[1])
		off += c.size[m] + gap
	}
	for _, c := range e.kids {
		if c.style.Float != nil {
			u.floats = append(u.floats, c)
		}
	}
}

func (u *UI) placeFloat(e *elem) {
	fl := e.style.Float
	anchor := render.XYWH(e.parent.pos[0], e.parent.pos[1], e.parent.size[0], e.parent.size[1])
	switch {
	case fl.To != 0:
		if t, ok := u.byID[fl.To]; ok {
			anchor = render.XYWH(t.pos[0], t.pos[1], t.size[0], t.size[1])
		}
	case fl.Point != nil:
		anchor = render.Rect{Min: *fl.Point, Max: *fl.Point}
	}
	x := anchor.Min.X + fl.At.X*anchor.Dx() - fl.Self.X*e.size[0] + fl.Offset.X
	y := anchor.Min.Y + fl.At.Y*anchor.Dy() - fl.Self.Y*e.size[1] + fl.Offset.Y
	u.place(e, x, y)
}

// Paint draws the latest layout: the flow, then floats in Z order, each float outside its parent's clip.
// Hover then follows whatever is now under a pointer that hasn't moved.
func (u *UI) Paint(f render.Frame) {
	defer func() {
		if u.in.inside {
			u.setHot(u.topInteractive(u.in.pos))
		}
		// While a trap is up, focus can't leave it.
		if t := u.trap(); t != 0 && !u.within(u.in.focus, t) {
			u.in.focus, u.in.visible = t, false
		}
	}()
	u.hits = u.hits[:0]
	if u.root == nil {
		return
	}
	full := render.XYWH(0, 0, u.root.size[0], u.root.size[1])
	if u.theme != nil {
		f.Backdrop(u.theme.C(theme.BgBase))
	}
	u.paint(f, u.root, full)
	floats := slices.Clone(u.floats)
	slices.SortStableFunc(floats, func(a, b *elem) int { return a.style.Z - b.style.Z })
	for _, e := range floats {
		if e.style.Z >= 0 {
			u.paint(f, e, full)
		}
	}
	// What's on its way out is drawn last, in Z order, and takes no input.
	var gone []*elem
	for _, x := range u.exiting {
		gone = append(gone, x.e)
	}
	slices.SortStableFunc(gone, func(a, b *elem) int {
		if a.style.Z != b.style.Z {
			return a.style.Z - b.style.Z
		}
		return int(a.id - b.id)
	})
	u.noHits = true
	for _, e := range gone {
		u.paint(f, e, full)
	}
	u.noHits = false
}

func (e *elem) rect() render.Rect { return render.XYWH(e.pos[0], e.pos[1], e.size[0], e.size[1]) }

func intersect(a, b render.Rect) render.Rect {
	r := render.Rect{
		Min: render.Point{X: max(a.Min.X, b.Min.X), Y: max(a.Min.Y, b.Min.Y)},
		Max: render.Point{X: min(a.Max.X, b.Max.X), Y: min(a.Max.Y, b.Max.Y)},
	}
	if r.Max.X < r.Min.X || r.Max.Y < r.Min.Y {
		return render.Rect{Min: r.Min, Max: r.Min}
	}
	return r
}

func (u *UI) paint(f render.Frame, e *elem, clip render.Rect) {
	r := e.rect()
	if vis := intersect(r, clip); vis.Dx() > 0 && vis.Dy() > 0 && e != u.root && !u.noHits {
		u.hits = append(u.hits, hit{e.id, vis})
	}
	if op := e.fx.opacity * (1 - e.style.Fade); op < 1 {
		if op <= 0 {
			return
		}
		f.PushOpacity(op)
		defer f.Pop()
	}
	if e.fx.shift != (render.Point{}) {
		f = render.Offset(f, e.fx.shift)
	}
	switch e.kind {
	case kText:
		f.Glyphs(e.run, render.Point{X: r.Min.X, Y: r.Min.Y + e.run.Ascent}, e.ts.Color)
		return
	case kPaint:
		if e.paint != nil {
			e.paint(f, r)
		}
		return
	}
	s := e.style
	if e.fx.fill != nil {
		s.Fill = *e.fx.fill
	}
	clips := s.Clip || s.ScrollX || s.ScrollY
	edge := s.Border > 0 && s.Stroke.A > 0
	edgeOver := edge && clips && len(e.kids) > 0 // a clipping box's edge goes over what's in it
	switch {
	case edgeOver && s.Fill.A > 0:
		f.Rect(r, render.Style{Fill: s.Fill, Radius: s.Radius})
	case !edgeOver && (s.Fill.A > 0 || edge):
		f.Rect(r, render.Style{Fill: s.Fill, Radius: s.Radius, Stroke: s.Stroke, Width: s.Border})
	}
	if w := u.wash(e); w.A > 0 {
		f.Rect(r, render.Style{Fill: w, Radius: s.Radius})
	}
	if clips {
		f.PushClip(r, s.Radius)
		clip = intersect(clip, r)
	}
	for _, c := range e.kids {
		if c.style.Float != nil && c.style.Z < 0 {
			u.paint(f, c, clip) // an underlay, behind the content
		}
	}
	for _, c := range e.kids {
		if c.style.Float != nil {
			continue
		}
		if cr := c.rect(); clips && (cr.Max.X <= clip.Min.X || cr.Min.X >= clip.Max.X || cr.Max.Y <= clip.Min.Y || cr.Min.Y >= clip.Max.Y) {
			continue
		}
		u.paint(f, c, clip)
	}
	if clips {
		f.Pop()
	}
	if s.ScrollX || s.ScrollY {
		u.paintBars(f, e.id, r)
	}
	if edgeOver {
		f.Rect(r, render.Style{Radius: s.Radius, Stroke: s.Stroke, Width: s.Border})
	}
	if e.id == u.in.focus && u.in.visible && e.id != 0 {
		const w, gap = 2, 2
		ring := render.Rect{Min: render.Point{X: r.Min.X - gap - w, Y: r.Min.Y - gap - w}, Max: render.Point{X: r.Max.X + gap + w, Y: r.Max.Y + gap + w}}
		f.Rect(ring, render.Style{Stroke: u.opts.FocusRing, Width: w, Radius: s.Radius + gap + w})
	}
}

// wash is what's washed over e for hover or press: a box clickable with the pointer that doesn't draw its own.
func (u *UI) wash(e *elem) render.Color {
	s := e.style
	if e.id == 0 || e.id != u.in.hot || !(s.Interactive || s.Focusable) || s.Disabled || s.NoHover || s.Cursor != render.CursorPointer {
		return render.Color{}
	}
	if u.in.active == e.id {
		return u.opts.Pressed
	}
	return u.opts.Hover
}

// Dump writes the latest layout as an indented tree, for golden tests.
func (u *UI) Dump() string {
	var b strings.Builder
	var walk func(e *elem, depth int)
	walk = func(e *elem, depth int) {
		fmt.Fprintf(&b, "%s%s %s %s", strings.Repeat("  ", depth), [...]string{"box", "text", "paint"}[e.kind], e.name, fmtRect(e.rect()))
		if e.kind == kText {
			fmt.Fprintf(&b, " %q", e.text)
		}
		if e.style.Float != nil {
			fmt.Fprintf(&b, " float z=%d", e.style.Z)
		}
		if st := u.states[e.id]; st != nil && (st.scroll != [2]float32{}) {
			fmt.Fprintf(&b, " scroll=%s,%s", num(st.scroll[0]), num(st.scroll[1]))
		}
		b.WriteByte('\n')
		for _, c := range e.kids {
			walk(c, depth+1)
		}
	}
	if u.root != nil {
		walk(u.root, 0)
	}
	return b.String()
}

func fmtRect(r render.Rect) string {
	return fmt.Sprintf("%s,%s %sx%s", num(r.Min.X), num(r.Min.Y), num(r.Dx()), num(r.Dy()))
}

func num(v float32) string {
	if v == float32(math.Round(float64(v))) {
		return strconv.Itoa(int(v))
	}
	return strconv.FormatFloat(float64(v), 'f', 2, 32)
}

// SetTheme takes the UI's own colours (text, focus ring, tooltips) from r, and paints over its base background.
func (u *UI) SetTheme(r *theme.Resolved) {
	u.opts.MotionSpeed = float32(r.Effect("motion"))
	u.theme = r
	u.text.Color = r.C(theme.Fg)
	u.opts.FocusRing = r.C(theme.FocusRing)
	u.opts.ScrollThumb, u.opts.ScrollThumbHot = r.C(theme.ScrollThumb), r.C(theme.ScrollThumbHot)
	u.opts.Hover, u.opts.Pressed = r.C(theme.Hover), r.C(theme.Pressed)
	u.opts.Tooltip.Color = r.C(theme.Fg)
	u.opts.TooltipFill = r.C(theme.BgOverlay)
	u.opts.TooltipStroke = r.C(theme.LineStrong)
}

// Theme is the theme set last, or nil.
func (u *UI) Theme() *theme.Resolved { return u.theme }

// Overflowing lists the latest frame's texts drawn past the inner edge of a box they're in, up to one that scrolls:
// a lint, as nothing the toolkit lays out should.
func (u *UI) Overflowing() []string {
	var out []string
	for _, e := range u.byID {
		if e.kind != kText {
			continue
		}
		l, r := e.pos[0], e.pos[0]+e.run.Advance
		for a := e.parent; a != nil && !a.style.ScrollX; a = a.parent {
			if l < a.pos[0]+a.style.Pad.L-slack || r > a.pos[0]+a.size[0]-a.style.Pad.R+slack {
				out = append(out, fmt.Sprintf("%q (%s–%s) past %s (%s)", e.text, num(l), num(r), a.name, fmtRect(a.rect())))
				break
			}
			if a.style.Float != nil {
				break
			}
		}
	}
	slices.Sort(out)
	return out
}
