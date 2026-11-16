package ui

import (
	"slices"

	"github.com/liamg/oriel/render"
)

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

// flow is e's children that aren't floats: e.kids itself when none is, so it's not to be changed.
func (e *elem) flow() []*elem {
	floats := false
	for _, c := range e.kids {
		floats = floats || c.style.Float != nil
	}
	if !floats {
		return e.kids
	}
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
		if e.wraps() {
			content = wrapContent(e, kids, axis)
			break
		}
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
	if e.wraps() {
		for _, c := range kids {
			if axis == 0 && c.size[0] > inner {
				c.size[0] = max(inner, 0)
			}
		}
	} else if !e.scrolls(axis) {
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
	} else if e.main(axis) {
		stretch(e, kids, axis, inner)
	}
	if e.kind == kText && e.size[0] < e.run.Advance-slack {
		e.run = u.sh.Shape(Elide(u.sh, e.text, e.ts.Font, e.size[0]), e.ts.Font)
	}
	for _, c := range e.kids {
		if fl := c.style.Float; fl != nil && fl.Fill && fl.To == 0 && fl.Point == nil {
			if s := c.sz(axis); s.Kind == Grow {
				c.size[axis] = s.clamp(e.size[axis])
			}
		}
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

// stretch hands what a scrolling box's view has left past its content to its grow children, by weight; none shrinks.
func stretch(e *elem, kids []*elem, axis int, inner float32) {
	left := inner - e.style.Gap*float32(max(len(kids)-1, 0))
	var weight float32
	for _, c := range kids {
		left -= c.size[axis]
		if s := c.sz(axis); s.Kind == Grow {
			weight += max(s.Value, 0)
		}
	}
	if left <= 0 || weight == 0 {
		return
	}
	for _, c := range kids {
		if s := c.sz(axis); s.Kind == Grow {
			c.size[axis] = s.clamp(c.size[axis] + left*max(s.Value, 0)/weight)
		}
	}
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
	if len(growing) == 0 {
		return
	}
	// With room for each one's content, none gets less: its content is its Min. A box that scrolls or clips needs
	// none: its content is cut, not its neighbours.
	need := make([]float32, len(growing))
	var total float32
	for i, c := range growing {
		if !c.scrolls(axis) && !c.style.Clip {
			need[i] = c.size[axis]
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
			s.Min = max(s.Min, need[i])
			want := max(left, 0) * s.Value / weight
			if got := s.clamp(want); got != want {
				c.size[axis] = got
				left -= got
				growing = slices.Delete(growing, i, i+1)
				need = slices.Delete(need, i, i+1)
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
	st.rect, st.laid = render.XYWH(x, y, e.size[0], e.size[1]), true
	if e.kind != kBox {
		return
	}
	kids := e.flow()
	origin := [2]float32{x + e.style.Pad.L, y + e.style.Pad.T}
	var inner [2]float32
	ease := float32(1)
	if st.easing {
		ease = u.motion(childID(e.id, motionKey{"scroll"}), 1, 1)
	}
	for axis := range 2 {
		a, b := e.pad(axis)
		inner[axis] = e.size[axis] - a - b
		if e.scrolls(axis) && e.style.ScrollWith != 0 {
			ext := e.extent(kids, axis)
			off := u.shared(e.style.ScrollWith, axis, ext-inner[axis])
			st.scroll[axis], st.shown[axis], st.ext[axis], st.view[axis], st.placed = off, off, ext, inner[axis], true
			origin[axis] -= off
		} else if e.scrolls(axis) {
			ext := e.extent(kids, axis)
			if e.style.Follow && st.atEnd(axis) {
				st.scroll[axis] = ext - inner[axis]
			}
			st.scroll[axis] = max(0, min(st.scroll[axis], ext-inner[axis]))
			st.ext[axis], st.view[axis] = ext, inner[axis]
			st.shown[axis] = st.scroll[axis]
			if st.easing {
				st.shown[axis] = st.easeFrom[axis] + (st.scroll[axis]-st.easeFrom[axis])*ease
			}
			origin[axis] -= st.shown[axis]
			st.placed = true
		} else {
			st.scroll[axis], st.shown[axis] = 0, 0
		}
	}
	if ease >= 1 {
		st.easing = false
	}
	if e.wraps() {
		y := origin[1]
		for _, line := range e.lines(inner[0]) {
			x, h := origin[0], float32(0)
			for _, c := range line {
				u.place(c, x, y)
				x += c.size[0] + e.style.Gap
				h = max(h, c.size[1])
			}
			y += h + e.style.Gap
		}
		u.floatKids(e)
		return
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
	u.floatKids(e)
}

// extent is how far a scrolling box's flow children reach along axis.
func (e *elem) extent(kids []*elem, axis int) float32 {
	var ext float32
	for i, c := range kids {
		switch {
		case !e.main(axis):
			ext = max(ext, c.size[axis])
		case i > 0:
			ext += c.size[axis] + e.style.Gap
		default:
			ext += c.size[axis]
		}
	}
	return ext
}

// shared is the offset along axis of the boxes scrolling with id, one of which reaches rng past its view. The first
// of them in a frame clamps the offset to how far the widest went last frame, so all are drawn at one offset.
func (u *UI) shared(id ID, axis int, rng float32) float32 {
	g := u.state(id)
	g.seen = u.frame
	if g.sharedAt != u.frame {
		if g.placed {
			for ax := range 2 {
				g.scroll[ax] = max(0, min(g.scroll[ax], g.ext[ax]))
			}
		}
		g.sharedAt, g.placed, g.easing = u.frame, true, false
		g.ext, g.view = [2]float32{}, [2]float32{}
	}
	g.ext[axis] = max(g.ext[axis], rng)
	g.shown = g.scroll
	return g.scroll[axis]
}

func (u *UI) floatKids(e *elem) {
	for _, c := range e.kids {
		if c.style.Float != nil {
			u.floats = append(u.floats, c)
		}
	}
}

// wraps says e is a row that wraps its children onto lines.
func (e *elem) wraps() bool { return e.style.Wrap && e.style.Dir == Row && !e.style.ScrollX }

// lines are a wrapping row's children, as many on each line as fit inner.
func (e *elem) lines(inner float32) [][]*elem {
	var out [][]*elem
	var line []*elem
	var used float32
	for _, c := range e.flow() {
		if len(line) > 0 && used+e.style.Gap+c.size[0] > inner+slack {
			out, line, used = append(out, line), nil, 0
		}
		if len(line) > 0 {
			used += e.style.Gap
		}
		line = append(line, c)
		used += c.size[0]
	}
	if len(line) > 0 {
		out = append(out, line)
	}
	return out
}

// wrapContent is a wrapping row's content along axis: across, its widest child; down, its lines at its width.
func wrapContent(e *elem, kids []*elem, axis int) float32 {
	var content float32
	if axis == 0 {
		for _, c := range kids {
			content = max(content, c.size[0])
		}
	} else {
		l, r := e.pad(0)
		lines := e.lines(e.size[0] - l - r)
		for i, line := range lines {
			var h float32
			for _, c := range line {
				h = max(h, c.size[1])
			}
			content += h
			if i > 0 {
				content += e.style.Gap
			}
		}
	}
	a, b := e.pad(axis)
	return content + a + b
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
	if fl.Flip && u.root != nil && y+e.size[1] > u.root.size[1] {
		y = anchor.Min.Y + (1-fl.At.Y)*anchor.Dy() - (1-fl.Self.Y)*e.size[1] - fl.Offset.Y
	}
	if fl.Flip {
		y = max(y, 0)
	}
	if fl.FlipX && u.root != nil && x+e.size[0] > u.root.size[0] {
		x = max(anchor.Min.X+(1-fl.At.X)*anchor.Dx()-(1-fl.Self.X)*e.size[0]-fl.Offset.X, 0)
	}
	u.place(e, x, y)
}
