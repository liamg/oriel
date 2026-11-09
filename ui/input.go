package ui

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
)

// Interaction is a node's state this frame, from input up to the latest event: the same frame's layout
// can't be hit yet, so hover and press resolve against the previous frame's.
// DoubleClick is how close together clicks come to count as a double (or triple) click.
const DoubleClick = 400 * time.Millisecond

// clickSlop is how far the pointer may drift between the presses of a double or triple click.
const clickSlop = 4

type Interaction struct {
	Hovered bool // the pointer is over it, and it's the topmost interactive node there
	Inside  bool // the pointer is over it or over something interactive inside it: a row with a button in it
	Pressed bool // the primary button went down on it and hasn't come up, and the pointer's still on it
	Held    bool // the primary button went down on it and hasn't come up, wherever the pointer is: a drag
	Clicked bool // pressed and released on it since the last frame
	// DoubleClicked is a click that came within DoubleClick of the one before it, on the same node.
	DoubleClicked bool
	// Presses counts its last press and those quickly before it in the same place: 2 for a double click, 3 for a triple.
	Presses      int
	Focused      bool
	FocusVisible bool // focused from the keyboard, so it draws a focus ring
	Disabled     bool
	Keys         []render.KeyEvent // keys typed at it since the last frame, while focused
	Context      *render.Point     // where it was right-clicked since the last frame
	Scroll       render.Point      // wheel movement over it since the last frame, for a box with Wheel
	Mods         render.Mods       // the modifiers held at its last press
	Dropped      *DragData         // a drag let go over it since the last frame, of a kind it takes (Style.Drop)
	DragOver     bool              // a drag of a kind it takes is over it now
	Access       []AccessAction    // what assistive technology asked of it since the last frame, of its Style.Actions

	id  ID
	pos render.Point
	u   *UI
}

// PressedAt is where the last primary press on it went down, while it's held or since: a drag measures from there,
// not from where the pointer had got to by the first frame built after the press, which on a busy machine can be well
// along.
func (it Interaction) PressedAt() (render.Point, bool) {
	if it.u == nil || it.id == 0 || it.u.in.pressID != it.id || it.u.in.presses == 0 {
		return render.Point{}, false
	}
	return it.u.in.pressPt, true
}

// Pointer is where the pointer is, wherever it is. A frame that reads it is built again whenever the pointer moves.
func (it Interaction) Pointer() render.Point {
	if it.u != nil {
		it.u.tracked = true
	}
	return it.pos
}

// over reports whether the pointer is in r, and has the next pointer move build a frame if that changes.
func (u *UI) over(r render.Rect) bool {
	u.regions = append(u.regions, r)
	return u.in.inside && inside(u.in.pos, r)
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
	pos      render.Point
	inside   bool
	hot      ID
	hotAt    time.Time
	tip      ID // the node whose tooltip is under the pointer
	tipAt    time.Time
	active   ID // pressed
	focus    ID
	visible  bool // focus came from the keyboard
	clicks   map[ID]bool
	doubles  map[ID]bool // the clicks in clicks that complete a double-click
	lastID   ID          // the last click, for a double-click
	lastAt   time.Time
	presses  int // the last press, and those quickly before it in the same place on the same node
	pressID  ID
	pressAt  time.Time
	pressPt  render.Point
	keys     map[ID][]render.KeyEvent
	context  map[ID]render.Point
	claimed  map[ID]bool
	wheel    map[ID]render.Point
	mods     render.Mods
	help     string // a help page asked for since the last frame
	dnd      *dragging
	dropped  map[ID]DragData
	access   map[ID][]AccessAction    // UI.Act's actions for widgets, by node
	passed   map[ID][]render.KeyEvent // keys a node was given and didn't take, by that node
	ask      focusAsk                 // Ctx.Focus's or Ctx.FocusInto's, this frame
	request  focusAsk                 // Ctx.RequestFocus's, until its target is built or the user moves on
	touched  int                      // presses and keys so far: a request asked before the latest is stale
	lastText time.Time                // when a key was last typed into a text field
	trail    []ID                     // what held focus before, latest last: focus goes back there when an overlay closes
	settled  ID                       // focus as the last frame left it
	blurred  ID                       // what Ctx.Blur took focus from, this frame
	bar      *barDrag                 // a scrollbar thumb being dragged
	held     render.Mods              // the modifier keys down
	heldAt   time.Time                // when the set held last changed
	chorded  bool                     // a key or button went down while they were held
}

type focusAsk struct {
	id      ID
	z       int // the layer it was asked from
	set     bool
	into    bool // a transition into id: focus goes to its default target
	touched int  // input.touched when it was asked
	frame   int  // the frame it was asked in
}

// trailMax is how much of what held focus before is remembered.
const trailMax = 32

// settleFocus moves focus where this frame asked, unless what has it is in a higher layer and still there. When what
// had focus is gone and nothing asked, focus goes back to what held it before an overlay that closed, or else to the
// default target of the nearest container still there.
func (u *UI) settleFocus(prev map[ID]*elem) {
	in := &u.in
	u.noteFocus()
	lost := in.focus != 0 && !u.alive(in.focus)
	a := in.ask
	in.ask = focusAsk{}
	if o := u.justOpened(prev); o != nil && !a.set {
		a = focusAsk{id: o.id, z: o.layer(), set: true, into: true}
	}
	r := in.request
	_, built := u.byID[r.id]
	if r.set && (r.touched != in.touched || !built && r.into) {
		in.request, r = focusAsk{}, focusAsk{}
	}
	ready := r.set && built && !(r.into && u.defaultIn(r.id) == 0)
	if ready {
		in.request = focusAsk{}
	}
	request := func() bool {
		if !ready {
			return false
		}
		if u.applyFocus(r, lost) {
			return true
		}
		in.request = r // under what holds focus: it waits for that to close
		return false
	}
	ask := func() bool { return a.set && u.applyFocus(a, lost) }
	var moved bool
	if a.z > r.z {
		moved = ask() || request()
	} else {
		moved = request() || ask()
	}
	switch {
	case !moved && lost:
		u.restoreFocus()
	case !moved && in.focus == 0 && in.blurred != 0:
		in.focus = u.containerDefault(in.blurred)
	}
	in.blurred = 0
	u.noteFocus()
}

// containerDefault is where focus goes when from gives it up: the default target of the nearest container of it, but
// from, short of the overlay it's on; or nothing.
func (u *UI) containerDefault(from ID) ID {
	e, ok := u.byID[from]
	if !ok {
		return 0
	}
	for e = e.parent; e != nil; e = e.parent {
		if t := u.defaultIn(e.id); t != 0 && t != from {
			return t
		}
		if e.style.Focusable && !e.style.Disabled && e.id != 0 {
			return e.id
		}
		if e.style.Float != nil && e.style.Z > 0 {
			return 0
		}
	}
	return 0
}

// noteFocus adds what held focus to the trail when focus has moved on from it.
func (u *UI) noteFocus() {
	in := &u.in
	if in.focus == in.settled {
		return
	}
	if in.settled != 0 {
		in.trail = append(slices.DeleteFunc(in.trail, func(id ID) bool { return id == in.settled }), in.settled)
		if len(in.trail) > trailMax {
			in.trail = in.trail[len(in.trail)-trailMax:]
		}
	}
	in.settled = in.focus
}

// applyFocus moves focus as a asks; it says whether a was honoured.
func (u *UI) applyFocus(a focusAsk, lost bool) bool {
	in := &u.in
	target := a.id
	if a.into {
		if !lost && in.focus != 0 && in.focus != a.id && u.builtWithin(in.focus, a.id) {
			return true
		}
		target = u.defaultIn(a.id)
		if target == 0 {
			if _, built := u.byID[a.id]; built {
				// Nothing in it takes focus yet: it may be loading. Focus goes there when it does, unless the user
				// moves on first; until then it stays where it is.
				in.request = focusAsk{id: a.id, z: a.z, set: true, into: true, touched: in.touched}
			}
			return true
		}
	}
	z := a.z
	if e, ok := u.byID[a.id]; ok {
		z = max(z, e.layer())
	}
	if e, ok := u.byID[target]; ok && target != 0 {
		z = max(z, e.layer())
	}
	if e, ok := u.byID[in.focus]; ok && !lost && in.focus != 0 && e.layer() > z && !u.openedHolds(in.focus) {
		return false
	}
	in.focus, in.visible = target, false
	return true
}

// justOpened is the FocusOnOpen node built this frame and not the last, on the highest layer; the opened set is kept
// for restoreFocus.
func (u *UI) justOpened(prev map[ID]*elem) *elem {
	var best *elem
	var opened map[ID]bool // nil, as nearly every frame has none
	for id, e := range u.byID {
		if !e.style.FocusOnOpen || e.style.Disabled {
			continue
		}
		if opened == nil {
			opened = map[ID]bool{}
		}
		opened[id] = true
		if _, was := prev[id]; !was && (best == nil || e.layer() > best.layer()) {
			best = e
		}
	}
	u.wasOpened, u.opened = u.opened, opened
	return best
}

// openedHolds says focus at id is in a FocusOnOpen node: it took focus as it appeared, without trapping it, so what
// opens or asks for focus after takes it, on any layer.
func (u *UI) openedHolds(id ID) bool {
	for ; id != 0; id = u.parents[id] {
		if u.opened[id] {
			return true
		}
	}
	return false
}

// restoreFocus finds focus a home after what had it went: what held it before, when it was on an overlay that's
// closed; otherwise the default target of the nearest container of it that's still there.
func (u *UI) restoreFocus() {
	in := &u.in
	gone := in.focus
	in.focus, in.visible = 0, false
	for id := gone; id != 0; id = u.parents[id] {
		if u.overlays[id] || u.wasOpened[id] {
			if _, open := u.byID[id]; !open {
				for i := len(in.trail) - 1; i >= 0; i-- {
					if u.alive(in.trail[i]) {
						in.focus, in.trail = in.trail[i], in.trail[:i]
						return
					}
				}
			}
			break
		}
	}
	for id := u.parents[gone]; ; id = u.parents[id] {
		if _, ok := u.byID[id]; ok {
			for e := u.byID[id]; e != nil; e = e.parent {
				if t := u.defaultIn(e.id); t != 0 {
					in.focus = t
					return
				}
			}
			return
		}
		if id == 0 {
			return
		}
	}
}

// alive says id is built this frame and enabled, so it can hold focus.
func (u *UI) alive(id ID) bool {
	e, ok := u.byID[id]
	return ok && !e.style.Disabled
}

// builtWithin is Within for this frame's tree, as it's built.
func (u *UI) builtWithin(id, root ID) bool {
	for e := u.byID[id]; e != nil; e = e.parent {
		if e.id == root {
			return true
		}
	}
	return false
}

// defaultIn is where focus goes on entering id: the DefaultFocus node in it on the highest layer, the first built
// of those; else id itself if it takes focus; else nothing.
func (u *UI) defaultIn(id ID) ID {
	root, ok := u.byID[id]
	if !ok {
		return 0
	}
	var best *elem
	var walk func(e *elem)
	walk = func(e *elem) {
		for _, k := range e.kids {
			if k.style.Disabled {
				continue
			}
			if k.style.DefaultFocus && (best == nil || k.layer() > best.layer()) {
				best = k
			}
			walk(k)
		}
	}
	walk(root)
	switch {
	case best != nil:
		return best.id
	case root.style.Focusable && !root.style.Disabled && id != 0:
		return id
	}
	return 0
}

// pending reports whether input arrived for this frame's widgets to read.
func (in *input) pending() bool {
	return len(in.clicks) > 0 || len(in.keys) > 0 || len(in.context) > 0 || len(in.wheel) > 0 || len(in.dropped) > 0 || len(in.access) > 0
}

// Pointer feeds a pointer event in. Hover and press resolve against the latest painted frame. It reports whether the
// next frame can differ for it: a move with no button held that changes nothing the latest frame read (what's
// hovered, Interaction.Pointer, the regions it tested) doesn't need one.
func (u *UI) Pointer(ev render.PointerEvent) bool {
	in := &u.in
	from, inside0, hot, tip := in.pos, in.inside, in.hot, in.tip
	u.pointer(ev)
	if ev.Kind != render.PointerMove || ev.Buttons != 0 || in.active != 0 || in.bar != nil || in.dnd != nil {
		return true
	}
	if u.tracked || in.hot != hot || in.tip != tip || in.inside != inside0 {
		return true
	}
	for _, r := range u.regions {
		if inside(from, r) != inside(in.pos, r) {
			return true
		}
	}
	return false
}

func (u *UI) pointer(ev render.PointerEvent) {
	in := &u.in
	switch ev.Kind {
	case render.PointerLeave:
		in.inside = false
		u.setHot(0)
		u.setTip(0)
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
	u.setTip(u.topTip(ev.Pos))
	u.dragPointer(ev)
	switch ev.Kind {
	case render.PointerMove:
		u.selMove(ev.Pos)
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
		u.Chord()
		u.countPress(ev)
		u.selPress(ev)
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
		// A drag that selected text isn't a click on what it started over.
		selected := u.sel != nil && u.sel.held && u.sel.anchor != u.sel.focus
		if u.sel != nil {
			u.sel.held = false
		}
		if u.dragRelease() {
			in.active = 0
			return
		}
		if in.active != 0 && in.active == in.hot && !selected {
			if in.clicks == nil {
				in.clicks = map[ID]bool{}
			}
			in.clicks[in.active] = true
			now := u.at(ev)
			if in.lastID == in.active && now.Sub(in.lastAt) < DoubleClick {
				if in.doubles == nil {
					in.doubles = map[ID]bool{}
				}
				in.doubles[in.active] = true
				in.lastID = 0
			} else {
				in.lastID, in.lastAt = in.active, now
			}
		}
		in.active = 0
	}
}

func (u *UI) setHot(id ID) {
	if id != u.in.hot {
		u.in.hot, u.in.hotAt = id, u.now()
	}
}

func (u *UI) setTip(id ID) {
	if id != u.in.tip {
		u.in.tip, u.in.tipAt = id, u.now()
	}
}

// topTip is the topmost node at p with a tooltip, down to the topmost interactive one: text with a tooltip in a
// clickable row shows it, and the row still takes the click. A disabled control's own tooltip shows (why it is).
func (u *UI) topTip(p render.Point) ID {
	for _, id := range u.Hit(p) {
		f := u.flags[id]
		switch {
		case f.tooltip != "":
			return id
		case f.disabled:
			return 0
		case f.interactive:
			return 0
		}
	}
	return 0
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
		if f.sideways && f.scrollX && !f.scrollY && d.X == 0 {
			d = render.Point{X: d.Y}
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

// at is when a pointer event happened: its own time, as the window took it, so clicks a busy loop gets to late still
// count as the double or triple click they were; now for an event with none.
func (u *UI) at(ev render.PointerEvent) time.Time {
	if !ev.At.IsZero() {
		return ev.At
	}
	return u.now()
}

// countPress counts a primary press into the run of quick presses in one place on one node.
func (u *UI) countPress(ev render.PointerEvent) {
	in := &u.in
	now, p := u.at(ev), ev.Pos
	d := render.Point{X: p.X - in.pressPt.X, Y: p.Y - in.pressPt.Y}
	if in.presses > 0 && in.pressID == in.hot && now.Sub(in.pressAt) < DoubleClick && d.X*d.X+d.Y*d.Y <= clickSlop*clickSlop {
		in.presses++
	} else {
		in.presses = 1
	}
	in.pressID, in.pressAt, in.pressPt = in.hot, now, p
}

func (in *input) pressCount(id ID) int {
	if id == 0 || in.pressID != id {
		return 0
	}
	return in.presses
}

// Interaction reports id's state.
func (u *UI) Interaction(id ID) Interaction {
	f := u.flags[id]
	if f.disabled {
		return Interaction{Disabled: true}
	}
	in := &u.in
	it := Interaction{
		Hovered:       in.hot == id && id != 0,
		Inside:        id != 0 && u.within(in.hot, id),
		Pressed:       in.active == id && in.hot == id && id != 0,
		Held:          in.active == id && id != 0,
		Clicked:       in.clicks[id],
		DoubleClicked: in.doubles[id],
		Presses:       in.pressCount(id),
		Focused:       in.focus == id && id != 0,
		FocusVisible:  in.focus == id && in.visible && id != 0,
		Keys:          in.keys[id],
		Scroll:        in.wheel[id],
		Mods:          in.mods,
		id:            id,
		pos:           in.pos,
		u:             u,
	}
	if p, ok := in.context[id]; ok {
		it.Context = &p
	}
	if d, ok := in.dropped[id]; ok {
		it.Dropped = &d
	}
	it.DragOver = in.dnd != nil && in.dnd.moving && in.dnd.target == id
	it.Access = in.access[id]
	return it
}

// Key gives a key to the focused node, for its next frame. It reports whether the focused node takes keys
// (it has a KeyMode); if not, nothing does.
func (u *UI) Key(ev render.KeyEvent) bool {
	u.in.touched++
	if u.selKey(ev) {
		return true
	}
	id := u.in.focus
	if id == 0 || u.flags[id].keyMode == "" {
		return false
	}
	if u.in.keys == nil {
		u.in.keys = map[ID][]render.KeyEvent{}
	}
	u.in.keys[id] = append(u.in.keys[id], ev)
	u.in.visible = true
	if u.flags[id].text {
		u.in.lastText = u.now()
	}
	return true
}

// Mods is the modifier keys held now (render.ModsEvent). A change of them starts their being held alone afresh.
func (u *UI) Mods(m render.Mods) {
	in := &u.in
	if m == in.held {
		return
	}
	in.held, in.heldAt = m, u.now()
	if m == 0 {
		in.chorded = false
	}
}

// Chord says a key went down: the modifiers held are part of a chord now, not held alone. A press does too.
// With none held there's no chord.
func (u *UI) Chord() {
	u.in.touched++
	if u.in.held != 0 {
		u.in.chorded = true
	}
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

// TakesText reports whether the focused node inserts what's typed at it as text (Style.Text), last frame.
func (u *UI) TakesText() bool {
	f := u.flags[u.in.focus]
	return f.text && f.keyMode != ""
}

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

// Subject is id's Style.Subject, or that of the nearest ancestor with one.
func (u *UI) Subject(id ID) string {
	for id != 0 {
		if s := u.flags[id].subject; s != "" {
			return s
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
// the help lint.
func (u *UI) WithoutHelp() []ID {
	var out []ID
	for id, f := range u.flags {
		if f.interactive && u.Help(id) == "" {
			out = append(out, id)
		}
	}
	return out
}

// Within reports whether id is root or inside it, in the latest frame.
func (u *UI) Within(id, root ID) bool { return u.within(id, root) }

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

// Frames is how many frames have been built.
func (u *UI) Frames() int { return u.frame }

// Now is the time on the UI's clock: the frame's, a test's fixed one.
func (u *UI) Now() time.Time { return u.now() }

func (u *UI) now() time.Time {
	if u.clock != nil {
		return u.clock()
	}
	return time.Now()
}

// WokenBy says what asked for the next frame in the last build: the keys down to the element whose WakeAt did,
// "animation" for a spring, "toolkit" for the UI's own, or "" for nothing.
func (u *UI) WokenBy() string {
	by := u.waker
	switch {
	case u.animating:
		return "animation"
	case !u.loopWake.IsZero() && (u.wake.IsZero() || !u.wake.Before(u.loopWake)):
		by = u.looper
	case u.wake.IsZero():
		return ""
	case u.waker == nil || !u.wake.Equal(u.wakerAt):
		return "toolkit"
	}
	var keys []string
	for e := by; e != nil && e.parent != nil; e = e.parent {
		keys = append(keys, fmt.Sprint(e.key))
	}
	slices.Reverse(keys)
	return strings.Join(keys, "/")
}

// NextFrame is when the UI next needs a frame without any input: now while a spring is moving, at a
// tooltip's delay, or zero when nothing is pending or the window is hidden.
func (u *UI) NextFrame() time.Time {
	switch {
	case u.hidden:
		return time.Time{}
	case u.shown:
		return u.now()
	}
	if u.animating {
		return u.now()
	}
	if u.wake.IsZero() || !u.loopWake.IsZero() && u.loopWake.Before(u.wake) {
		return u.loopWake
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
