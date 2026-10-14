package ui

import (
	"cmp"
	"math"
	"slices"
	"time"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
)

// Interaction is a node's state this frame, from input up to the latest event: the same frame's layout
// can't be hit yet, so hover and press resolve against the previous frame's.
type Interaction struct {
	Hovered      bool // the pointer is over it, and it's the topmost interactive node there
	Inside       bool // the pointer is over it or over something interactive inside it: a row with a button in it
	Pressed      bool // the primary button went down on it and hasn't come up, and the pointer's still on it
	Held         bool // the primary button went down on it and hasn't come up, wherever the pointer is: a drag
	Clicked      bool // pressed and released on it since the last frame
	Focused      bool
	FocusVisible bool // focused from the keyboard, so it draws a focus ring
	Disabled     bool
	Keys         []render.KeyEvent // keys typed at it since the last frame, while focused
	Context      *render.Point     // where it was right-clicked since the last frame
	Pointer      render.Point      // the pointer, wherever it is
	Scroll       render.Point      // wheel movement over it since the last frame, for a box with Wheel
	Mods         render.Mods       // the modifiers held at its last press
	Dropped      *DragData         // a drag let go over it since the last frame, of a kind it takes (Style.Drop)
	DragOver     bool              // a drag of a kind it takes is over it now
}

// Options are how the UI looks and behaves where widgets don't say.
type Options struct {
	FocusRing      render.Color
	Hover          render.Color // washed over a clickable box under the pointer, unless it has NoHover
	Pressed        render.Color // and while it's pressed
	TooltipDelay   time.Duration
	Tooltip        TextStyle
	TooltipFill    render.Color
	TooltipStroke  render.Color
	TooltipScale   float32      // tooltips' size over the UI's (Style.Scale); 0 is 1
	Bars           Bars         // when BarsAuto boxes show their scrollbars: BarsAuto (while hovered or scrolling) or BarsAlways
	ScrollThumb    render.Color // a scrollbar's thumb
	ScrollThumbHot render.Color // under the pointer, or dragged
	ReducedMotion  bool         // springs jump to their targets
	MotionSpeed    float32      // the motion spring's speed (the theme's motion effect): 1 as designed, 0 off
}

func defaultOptions(text TextStyle) Options {
	tip := text
	tip.Font.Size = 11.5
	tip.Font.Family = typeset.UI
	return Options{
		FocusRing:      render.Color{R: 0xff, G: 0xd2, B: 0x3f, A: 0xff},
		Hover:          render.Color{R: 0xff, G: 0xff, B: 0xff, A: 0x0a},
		Pressed:        render.Color{R: 0xff, G: 0xff, B: 0xff, A: 0x16},
		TooltipDelay:   500 * time.Millisecond,
		Tooltip:        tip,
		TooltipFill:    render.Color{R: 0x19, G: 0x18, B: 0x15, A: 0xff},
		TooltipStroke:  render.Color{R: 0x33, G: 0x2f, B: 0x27, A: 0xff},
		MotionSpeed:    1,
		ScrollThumb:    render.Color{R: 0xff, G: 0xff, B: 0xff, A: 0x38},
		ScrollThumbHot: render.Color{R: 0xff, G: 0xff, B: 0xff, A: 0x6b},
	}
}

type input struct {
	pos     render.Point
	inside  bool
	hot     ID
	hotAt   time.Time
	active  ID // pressed
	focus   ID
	visible bool // focus came from the keyboard
	clicks  map[ID]bool
	keys    map[ID][]render.KeyEvent
	context map[ID]render.Point
	claimed map[ID]bool
	wheel   map[ID]render.Point
	mods    render.Mods
	help    string // a help page asked for since the last frame
	dnd     *dragging
	dropped map[ID]DragData
	passed  map[ID][]render.KeyEvent // keys a node was given and didn't take, by that node
	ask     focusAsk                 // Ctx.Focus's, this frame
	bar     *barDrag                 // a scrollbar thumb being dragged
}

type focusAsk struct {
	id  ID
	z   int // the layer it was asked from
	set bool
}

// settleFocus moves focus where this frame asked, unless what has it is in a higher layer and still there.
func (u *UI) settleFocus() {
	a := u.in.ask
	u.in.ask = focusAsk{}
	if !a.set {
		return
	}
	if e, ok := u.byID[a.id]; ok {
		a.z = max(a.z, e.layer())
	}
	if e, ok := u.byID[u.in.focus]; ok && u.in.focus != 0 && e.layer() > a.z {
		return
	}
	u.in.focus, u.in.visible = a.id, false
}

// pending reports whether input arrived for this frame's widgets to read.
func (in *input) pending() bool {
	return len(in.clicks) > 0 || len(in.keys) > 0 || len(in.context) > 0 || len(in.wheel) > 0 || len(in.dropped) > 0
}

// Pointer feeds a pointer event in. Hover and press resolve against the latest painted frame.
func (u *UI) Pointer(ev render.PointerEvent) {
	in := &u.in
	switch ev.Kind {
	case render.PointerLeave:
		in.inside = false
		u.setHot(0)
		return
	case render.PointerScroll:
		in.pos, in.inside = ev.Pos, true
		u.scrollAt(ev.Pos, ev.Scroll)
		return
	}
	in.pos, in.inside = ev.Pos, true
	if in.bar != nil {
		if ev.Kind == render.PointerRelease {
			in.bar = nil
		} else {
			u.barMove(ev.Pos)
		}
		return
	}
	if ev.Kind == render.PointerPress && ev.Buttons&render.ButtonPrimary != 0 && u.barPress(ev.Pos) {
		return
	}
	u.setHot(u.topInteractive(ev.Pos))
	u.dragPointer(ev)
	switch ev.Kind {
	case render.PointerPress:
		if ev.Buttons&render.ButtonSecondary != 0 && in.hot != 0 {
			if in.context == nil {
				in.context = map[ID]render.Point{}
			}
			in.context[in.hot] = ev.Pos
			return
		}
		if ev.Buttons&render.ButtonPrimary == 0 {
			return
		}
		in.active, in.mods = in.hot, ev.Mods
		// A press focuses the nearest focusable node at or above what's pressed, or nothing.
		in.focus, in.visible = 0, false
		for id := in.hot; id != 0; id = u.parents[id] {
			if f := u.flags[id]; f.focusable && !f.disabled {
				in.focus = id
				break
			}
		}
	case render.PointerRelease:
		if ev.Buttons&render.ButtonPrimary == 0 {
			return
		}
		if u.dragRelease() {
			in.active = 0
			return
		}
		if in.active != 0 && in.active == in.hot {
			if in.clicks == nil {
				in.clicks = map[ID]bool{}
			}
			in.clicks[in.active] = true
		}
		in.active = 0
	}
}

func (u *UI) setHot(id ID) {
	if id != u.in.hot {
		u.in.hot, u.in.hotAt = id, u.now()
	}
}

// topInteractive is the topmost interactive, enabled node at p in the latest frame.
func (u *UI) topInteractive(p render.Point) ID {
	for _, id := range u.Hit(p) {
		if f := u.flags[id]; f.interactive && !f.disabled {
			return id
		}
		if f := u.flags[id]; f.disabled {
			return 0
		}
	}
	return 0
}

func (u *UI) scrollAt(p render.Point, d render.Point) {
	for _, id := range u.Hit(p) {
		f := u.flags[id]
		if f.wheel {
			if u.in.wheel == nil {
				u.in.wheel = map[ID]render.Point{}
			}
			w := u.in.wheel[id]
			u.in.wheel[id] = render.Point{X: w.X + d.X, Y: w.Y + d.Y}
			return
		}
		if (f.scrollX && d.X != 0) || (f.scrollY && d.Y != 0) {
			if !f.scrollX {
				d.X = 0
			}
			if !f.scrollY {
				d.Y = 0
			}
			u.ScrollBy(id, d.X, d.Y)
			return
		}
	}
}

// Interaction reports id's state.
func (u *UI) Interaction(id ID) Interaction {
	f := u.flags[id]
	if f.disabled {
		return Interaction{Disabled: true}
	}
	in := &u.in
	it := Interaction{
		Hovered:      in.hot == id && id != 0,
		Inside:       id != 0 && u.within(in.hot, id),
		Pressed:      in.active == id && in.hot == id && id != 0,
		Held:         in.active == id && id != 0,
		Clicked:      in.clicks[id],
		Focused:      in.focus == id && id != 0,
		FocusVisible: in.focus == id && in.visible && id != 0,
		Keys:         in.keys[id],
		Pointer:      in.pos,
		Scroll:       in.wheel[id],
		Mods:         in.mods,
	}
	if p, ok := in.context[id]; ok {
		it.Context = &p
	}
	if d, ok := in.dropped[id]; ok {
		it.Dropped = &d
	}
	it.DragOver = in.dnd != nil && in.dnd.moving && in.dnd.target == id
	return it
}

// Key gives a key to the focused node, for its next frame. It reports whether the focused node takes keys
// (it has a KeyMode); if not, nothing does.
func (u *UI) Key(ev render.KeyEvent) bool {
	id := u.in.focus
	if id == 0 || u.flags[id].keyMode == "" {
		return false
	}
	if u.in.keys == nil {
		u.in.keys = map[ID][]render.KeyEvent{}
	}
	u.in.keys[id] = append(u.in.keys[id], ev)
	u.in.visible = true
	return true
}

// PassKeys hands keys id was given and doesn't take to the nearest node above it that takes keys, for its next frame:
// Escape on a focused toggle reaches the page it's on.
func (u *UI) PassKeys(id ID, keys []render.KeyEvent) {
	if len(keys) == 0 {
		return
	}
	if u.in.passed == nil {
		u.in.passed = map[ID][]render.KeyEvent{}
	}
	u.in.passed[id] = append(u.in.passed[id], keys...)
}

// bubble gives this frame's passed keys to the nodes above that take keys, for the next frame.
func (u *UI) bubble() {
	for id, keys := range u.in.passed {
		e, ok := u.byID[id]
		if !ok {
			continue
		}
		for e = e.parent; e != nil && e.style.KeyMode == ""; e = e.parent {
		}
		if e == nil {
			continue
		}
		if u.in.keys == nil {
			u.in.keys = map[ID][]render.KeyEvent{}
		}
		u.in.keys[e.id] = append(u.in.keys[e.id], keys...)
		u.wake = u.now()
	}
	u.in.passed = nil
}

// KeyMode is the focused node's key mode, or "" when nothing focused takes keys.
func (u *UI) KeyMode() string { return u.flags[u.in.focus].keyMode }

// Help is id's help id, or that of the nearest ancestor with one.
func (u *UI) Help(id ID) string {
	for id != 0 {
		if h := u.flags[id].help; h != "" {
			return h
		}
		id = u.parents[id]
	}
	return ""
}

// HelpNode is a node with a help id of its own, and where it was drawn.
type HelpNode struct {
	ID   ID
	Rect render.Rect
	Help string
}

// HelpNodes are the nodes of the latest frame with a help id of their own, for help mode's markers.
func (u *UI) HelpNodes() []HelpNode {
	var out []HelpNode
	for id, f := range u.flags {
		if f.help == "" {
			continue
		}
		if r, ok := u.Rect(id); ok && r.Dx() > 0 && r.Dy() > 0 {
			out = append(out, HelpNode{id, r, f.help})
		}
	}
	slices.SortFunc(out, func(a, b HelpNode) int {
		return cmp.Or(cmp.Compare(a.Rect.Min.Y, b.Rect.Min.Y), cmp.Compare(a.Rect.Min.X, b.Rect.Min.X))
	})
	return out
}

// ContextClick is a right-click since the last frame on a node no widget showed a context menu for: its target and
// where. Something else (help's own menu) can answer it.
func (u *UI) ContextClick() (ID, render.Point, bool) {
	var best ID
	var at render.Point
	for id, p := range u.in.context {
		if u.in.claimed[id] {
			continue
		}
		if best == 0 || u.within(id, best) {
			best, at = id, p
		}
	}
	return best, at, best != 0
}

// RequestHelp asks for help page id (a context menu's "Help" item does); HelpRequest takes the request.
func (u *UI) RequestHelp(id string) { u.in.help = id }

func (u *UI) HelpRequest() (string, bool) {
	h := u.in.help
	u.in.help = ""
	return h, h != ""
}

// WithoutHelp lists interactive nodes in the latest frame that have no help id, themselves or above them:
// help.md's lint.
func (u *UI) WithoutHelp() []ID {
	var out []ID
	for id, f := range u.flags {
		if f.interactive && u.Help(id) == "" {
			out = append(out, id)
		}
	}
	return out
}

// within reports whether id is root or inside it.
func (u *UI) within(id, root ID) bool {
	for ; id != 0; id = u.parents[id] {
		if id == root {
			return true
		}
	}
	return false
}

// trap is the focus trap traversal is held in: the topmost open one, or 0.
func (u *UI) trap() ID {
	var best ID
	z := math.MinInt
	for _, h := range u.hits {
		if f := u.flags[h.id]; f.trap && f.z >= z {
			best, z = h.id, f.z
		}
	}
	return best
}

// Focus moves keyboard focus to id, drawing its focus ring.
func (u *UI) Focus(id ID) { u.in.focus, u.in.visible = id, true }

// Focused is the node with keyboard focus, or 0.
func (u *UI) Focused() ID { return u.in.focus }

// FocusNext moves focus to the next focusable, enabled node in paint order (back with -1), wrapping.
func (u *UI) FocusNext(dir int) {
	var order []ID
	trap := u.trap()
	for _, h := range u.hits {
		if f := u.flags[h.id]; f.focusable && !f.disabled && (trap == 0 || u.within(h.id, trap)) {
			order = append(order, h.id)
		}
	}
	if len(order) == 0 {
		return
	}
	i := -1
	for j, id := range order {
		if id == u.in.focus {
			i = j
		}
	}
	switch {
	case i < 0 && dir < 0:
		i = len(order) - 1
	case i < 0:
		i = 0
	default:
		i = (i + dir + len(order)) % len(order)
	}
	u.Focus(order[i])
}

// Cursor is the pointer shape for what's under it: the topmost node under the pointer that sets one.
func (u *UI) Cursor() render.Cursor {
	if !u.in.inside {
		return render.CursorDefault
	}
	for _, id := range u.Hit(u.in.pos) {
		if f := u.flags[id]; f.cursor != render.CursorDefault {
			if f.disabled {
				return render.CursorNotAllowed
			}
			return f.cursor
		}
	}
	return render.CursorDefault
}

// SetClock replaces time.Now, for tests.
func (u *UI) SetClock(now func() time.Time) { u.clock = now }

func (u *UI) now() time.Time {
	if u.clock != nil {
		return u.clock()
	}
	return time.Now()
}

// NextFrame is when the UI next needs a frame without any input: now while a spring is moving, at a
// tooltip's delay, or zero when nothing is pending.
func (u *UI) NextFrame() time.Time {
	if u.animating {
		return u.now()
	}
	return u.wake
}

type spring struct {
	x, v float32
	at   time.Time
	seen int
}

// Springs are critically damped: within 2% of their target after 200 ms, and settled (under 0.01 away,
// moving under 0.05 a second) by 500 ms.
const (
	stiffness = 1000
	settle    = 0.01
	settleV   = 0.05
)

// Anim moves a value towards target on a spring keyed by key under this node, and returns where it is now.
// The UI asks for frames until it settles; with reduced motion it jumps.
func (c *Ctx) Anim(key any, target float32) float32 {
	u := c.u
	id := childID(c.e.id, animKey{key})
	s, ok := u.springs[id]
	now := u.now()
	if !ok || u.opts.ReducedMotion {
		s = &spring{x: target, at: now}
		u.springs[id] = s
	}
	s.seen = u.frame
	dt := float32(min(now.Sub(s.at).Seconds(), 0.05))
	s.at = now
	if dt > 0 {
		k := float32(stiffness)
		damp := 2 * float32(math.Sqrt(stiffness))
		// Semi-implicit Euler in small steps keeps it stable at any frame rate.
		const step = 1.0 / 480
		for t := float32(0); t < dt; t += step {
			h := min(step, dt-t)
			s.v += (k*(target-s.x) - damp*s.v) * h
			s.x += s.v * h
		}
	}
	if abs32(target-s.x) < settle && abs32(s.v) < settleV {
		s.x, s.v = target, 0
	} else {
		u.animating = true
	}
	return s.x
}

type animKey struct{ key any }

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
