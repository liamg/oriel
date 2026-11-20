// Package ui is the immediate-mode toolkit: widgets, layout, theme, animation and input routing.
//
// Each frame, widget calls on a Ctx build a tree of nodes. The UI lays the tree out (fixed, fit, grow and
// percent sizes along rows and columns, then floats placed against anchors), paints it into a render.Frame
// in z-order, and keeps what must outlive the frame by id: rects, scroll offsets.
package ui

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"time"

	"github.com/liamg/oriel/internal/clock"
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
	key    any    // its key among its siblings, made unique (nextKey)
	base   dupKey // the key it was built from, which its duplicates share
	kind   kind
	style  Style
	text   string
	ts     TextStyle
	run    *typeset.Run
	join   string // copied after the line before, what joins them: set on a line that wraps on from it (WrappedText)
	soft   bool
	paint  func(f render.Frame, r render.Rect)
	kids   []*elem
	parent *elem
	scale  float32         // what it and its subtree are drawn at (Style.Scale, times its parent's); 0 is 1
	theme  *theme.Resolved // what it and its subtree are built in (Style.Theme, or its parent's); nil is the UI's

	controls bool // it or something in it can be clicked or focused, or keeps its width (Style.Keep)
	inlaid   bool // a float built under LayerPane: part of its view, so clipped as the view is

	pos, size [2]float32
	content   [2]float32     // children's extent, for scrolling
	keys      map[dupKey]int // how many children were built from each key, once there are dupIndex of them
	fx        fx
}

type state struct {
	rect       render.Rect
	laid       bool // rect is from a layout, not a node built since
	scroll     [2]float32
	ext, view  [2]float32 // a scrolling box's content and what of it shows, along each axis
	scrolledAt time.Time
	seen       int
	// A scroll set by ScrollTo glides there: shown is the offset drawn, easing from easeFrom while easing.
	shown, easeFrom [2]float32
	asked           [2]float32
	easing, placed  bool
	sharedAt        int // for a ScrollWith id: the frame its ext started being gathered in
	bar             barFade
	data            any // a widget's own state: State[T]
}

// atEnd is whether the box is scrolled to its end along axis, as it was last laid out.
func (st *state) atEnd(axis int) bool { return st.scroll[axis] >= st.ext[axis]-st.view[axis]-1 }

// UI holds what lasts between frames.
type UI struct {
	Title string // the window's accessible name

	sh    typeset.Shaper
	text  TextStyle
	opts  Options
	frame int
	clock clock.Clock

	root    *elem
	byID    map[ID]*elem
	floats  []*elem
	layered map[int]bool // the float layers (Style.Z) last frame drew
	asked   map[int]bool // the layers Layered was asked about this frame
	states  map[ID]*state
	hits    []hit
	flags   map[ID]flags // from the latest frame, which input resolves against
	parents map[ID]ID

	overlays  map[ID]bool // the floats over the flow (Z above 0) last frame drew
	opened    map[ID]bool // the FocusOnOpen nodes this frame built
	wasOpened map[ID]bool // and those the frame before
	lastTrap  ID          // the focus trap up last frame

	theme *theme.Resolved

	in        input
	springs   map[ID]*spring
	glides    map[ID]*glideState
	fills     map[ID]*fillState
	exitable  map[ID]*elem // this frame's nodes with an Exit
	exiting   map[ID]*exiting
	eases     map[ID]*easeState
	ring      ringState
	noHits    bool
	animating bool
	wake      time.Time
	waker     *elem           // what asked for wake through Ctx.WakeAt
	wakerAt   time.Time       // the time it asked for
	loopWake  time.Time       // the next step of a loop that only repaints (Ctx.Animate)
	looper    *elem           // what asked for it
	loops     []time.Duration // each loop's step, in the last build
	unfocused bool            // the window hasn't the OS's focus (SetWindow)
	hidden    bool            // the window can't be seen
	shown     bool            // it showed again since the last frame
	clip      render.Clipboard
	tracked   bool                 // the latest frame read where the pointer is (Interaction.Pointer)
	regions   []render.Rect        // and asked whether it was in these
	sel       *textSel             // the text selected (Style.Selectable)
	selLast   ID                   // the Selectable box last pressed in, which ⌘A selects all of
	services  map[reflect.Type]any // Provide's, by type

	arena [2]elemArena // the nodes of this frame and the last, by the frame's parity
	spare spareMaps    // last frame's maps, cleared and filled again by the next
}

// spareMaps are per-frame maps kept from the frame before last, to fill again rather than make.
type spareMaps struct {
	byID     map[ID]*elem
	parents  map[ID]ID
	overlays map[ID]bool
	layered  map[int]bool
	exitable map[ID]*elem
}

const arenaSlab = 256

// elemArena hands out a frame's nodes from slabs kept from frame to frame. A node lasts until the arena is reset,
// two frames on; what must outlive that (a node on its way out) is detached.
type elemArena struct {
	slabs [][]elem
	n     int
}

func (a *elemArena) reset() { a.n = 0 }

// get is a cleared node, keeping the room its last use made for children.
func (a *elemArena) get() *elem {
	i := a.n / arenaSlab
	if i == len(a.slabs) {
		a.slabs = append(a.slabs, make([]elem, arenaSlab))
	}
	e := &a.slabs[i][a.n%arenaSlab]
	a.n++
	kids, keys := e.kids[:0], e.keys
	clear(keys)
	*e = elem{kids: kids, keys: keys}
	return e
}

// newElem is a node from this frame's arena.
func (u *UI) newElem() *elem { return u.arena[u.frame%2].get() }

// fresh is m emptied, or a new map when there's none.
func fresh[K comparable, V any](m map[K]V) map[K]V {
	if m == nil {
		return map[K]V{}
	}
	clear(m)
	return m
}

type flags struct {
	interactive, focusable, disabled bool
	scrollX, scrollY, wheel          bool
	sideways                         bool
	scrollWith                       ID
	bars                             Bars
	cursor                           render.Cursor
	tooltip, help, keyMode, subject  string
	tipView                          func(c *Ctx)
	offer                            any
	trap, text                       bool
	z                                int
	parent                           ID
	drag                             *DragData
	drop                             []string
	actions                          []Action
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
	f.byID, f.floats, f.hits, f.loops = map[ID]*elem{}, nil, nil, nil
	f.arena, f.spare = [2]elemArena{}, spareMaps{}
	f.layered, f.overlays = maps.Clone(u.layered), maps.Clone(u.overlays)
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
	f.eases = make(map[ID]*easeState, len(u.eases))
	for id, x := range u.eases {
		c := *x
		f.eases[id] = &c
	}
	f.parents = maps.Clone(u.parents)
	f.in.clicks, f.in.doubles, f.in.keys, f.in.context, f.in.wheel, f.in.claimed, f.in.access, f.in.help = nil, nil, nil, nil, nil, nil, nil, ""
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
	prev := u.byID
	u.frame++
	u.arena[u.frame%2].reset()
	u.root = u.newElem()
	u.root.style = Style{Dir: Column, W: Px(size.X), H: Px(size.Y)}
	u.byID, u.spare.byID = fresh(u.spare.byID), prev
	u.byID[0] = u.root
	u.floats = u.floats[:0]
	u.animating, u.wake, u.waker = false, time.Time{}, nil
	u.loopWake, u.looper, u.loops = time.Time{}, nil, u.loops[:0]
	u.shown = false
	u.tracked, u.regions = false, u.regions[:0]
	u.in.dragged = false
	build(&Ctx{u: u, e: u.root})
	u.settleFocus(prev)
	if u.in.pending() {
		u.wake = u.now() // what the input changed shows next frame
	}
	u.in.clicks, u.in.doubles, u.in.keys, u.in.context, u.in.wheel, u.in.claimed, u.in.dropped, u.in.access = nil, nil, nil, nil, nil, nil, nil, nil
	u.bubble()
	u.tooltip()
	u.dragShown()
	for axis := range 2 {
		u.fit(u.root, axis)
		u.grow(u.root, axis)
	}
	u.place(u.root, 0, 0)
	layered := fresh(u.spare.layered)
	for i := 0; i < len(u.floats); i++ {
		u.placeFloat(u.floats[i])
		layered[u.floats[i].style.Z] = true
	}
	for z := range u.asked {
		if layered[z] != u.layered[z] {
			u.wakeAt(u.now()) // what was built asked about this layer (Layered), and it came or went
		}
	}
	u.spare.layered, u.layered, u.asked = u.layered, layered, nil
	u.overlays, u.spare.overlays = fresh(u.spare.overlays), u.overlays
	for _, e := range u.floats {
		if e.style.Z > 0 {
			u.overlays[e.id] = true
		}
	}
	u.selFrame()
	u.selScroll()
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
			s.Interactive = true
			if !s.NoFocus {
				s.Focusable = true // a drag can start, and end, from the keyboard
				if s.KeyMode == "" {
					s.KeyMode = DragKeyMode
				}
			}
		}
		if s.Selectable && s.Cursor == 0 {
			s.Cursor = render.CursorText
		}
		if s.Interactive || s.Focusable || s.Disabled || s.ScrollX || s.ScrollY || s.Cursor != 0 || s.Tooltip != "" || s.TooltipView != nil || s.Help != "" || s.Subject != "" || s.Trap {
			var parent ID
			if e.parent != nil {
				parent = e.parent.id
			}
			u.flags[id] = flags{interactive: s.Interactive || s.Focusable, focusable: s.Focusable, disabled: s.Disabled,
				scrollX: s.ScrollX, scrollY: s.ScrollY, wheel: s.Wheel, sideways: s.Sideways, scrollWith: s.ScrollWith, bars: s.Bars, cursor: s.Cursor, tooltip: s.Tooltip, tipView: s.TooltipView, help: s.Help, subject: s.Subject, offer: s.Offer, keyMode: s.KeyMode,
				trap: s.Trap, text: s.Text && !s.Disabled, z: s.Z, parent: parent, drag: s.Drag, drop: s.Drop, actions: s.Actions}
		}
	}
	u.parents, u.spare.parents = fresh(u.spare.parents), u.parents
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

// tooltip shows the tooltip under the pointer once the pointer has rested on it for the delay.
func (u *UI) tooltip() {
	in := &u.in
	tip, view := u.flags[in.tip].tooltip, u.flags[in.tip].tipView
	if tip == "" && view == nil || !in.inside || in.active != 0 {
		return
	}
	due := in.tipAt.Add(u.opts.TooltipDelay)
	if u.now().Before(due) {
		if u.wake.IsZero() || due.Before(u.wake) {
			u.wake = due
		}
		return
	}
	at := in.pos
	u.tracked = true // it follows the pointer
	c := &Ctx{u: u, e: u.root}
	c.Box(tooltipKey{}, Style{Pad: PadXY(8, 4), Fill: u.opts.TooltipFill, Stroke: u.opts.TooltipStroke, Border: 1, Radius: 4,
		Enter: &Transition{Fade: true, Shift: render.Point{Y: 3}}, Exit: &Transition{Fade: true}, Z: LayerTooltip, Float: &Float{Point: &at, Offset: render.Point{X: 12, Y: 18}}, Scale: u.opts.TooltipScale}, func(c *Ctx) {
		if view != nil {
			view(c)
			return
		}
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

// Rect is where id was laid out in the latest frame; ok is false for a node not laid out yet.
func (u *UI) Rect(id ID) (render.Rect, bool) {
	st, ok := u.states[id]
	if !ok || !st.laid {
		return render.Rect{}, false
	}
	return st.rect, true
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

// add appends a child built from key to e, giving it an id unique among its siblings.
func (u *UI) add(e *elem, key any, k kind) *elem {
	key, d := e.nextKey(key)
	c := u.newElem()
	c.id, c.key, c.base, c.kind, c.parent, c.scale, c.theme = childID(e.id, key), key, d, k, e, e.scale, e.theme
	e.kids = append(e.kids, c)
	switch {
	case e.keys != nil:
		e.keys[d]++
	case len(e.kids) >= dupIndex:
		e.keys = make(map[dupKey]int, 2*dupIndex)
		for _, k := range e.kids {
			e.keys[k.base]++
		}
	}
	u.byID[c.id] = c
	st := u.state(c.id)
	st.seen = u.frame
	return c
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
				out = append(out, fmt.Sprintf("%q (%s–%s) past %s (%s)", e.text, num(l), num(r), a.name(), fmtRect(a.rect())))
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
