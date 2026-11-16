package ui

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
)

// paintOrder is the floats by layer; within a layer, in the order of the outermost floats holding them, so a float
// nested in another (a dialog's box in its scrim) stays under the floats built after its holder.
func (u *UI) paintOrder() []*elem {
	at := make(map[*elem]int, len(u.floats))
	for i, e := range u.floats {
		at[e] = i
	}
	outer := func(e *elem) int {
		top := e
		for p := e.parent; p != nil; p = p.parent {
			if p.style.Float != nil {
				top = p
			}
		}
		return at[top]
	}
	floats := slices.Clone(u.floats)
	slices.SortStableFunc(floats, func(a, b *elem) int {
		if a.style.Z != b.style.Z {
			return a.style.Z - b.style.Z
		}
		return outer(a) - outer(b)
	})
	return floats
}

// overTrap says whether id is in a float layered over the trap t.
func (u *UI) overTrap(id, t ID) bool {
	e, ok := u.byID[id]
	return ok && id != 0 && e.layer() > u.flags[t].z
}

// Paint draws the latest layout: the flow, then floats in Z order, each outside its parent's clip but for one inlaid
// in its view (under LayerPane), which keeps the view's clips.
// Hover then follows whatever is now under a pointer that hasn't moved.
func (u *UI) Paint(f render.Frame) {
	defer func() {
		if u.in.inside {
			u.setHot(u.topInteractive(u.in.pos))
			u.setTip(u.topTip(u.in.pos))
		}
		// While a trap is up, focus can't leave it, other than for a float layered over it: a window kept on top.
		// Coming in from outside, it goes to the trap's default target; dropped inside it (Escape in a field), to the
		// trap itself.
		// A trap that's just come up takes focus from a card that took it as it appeared: what opened last has it.
		t := u.trap()
		fresh := t != 0 && t != u.lastTrap && u.openedHolds(u.in.focus)
		u.lastTrap = t
		if t != 0 && !u.within(u.in.focus, t) && (!u.overTrap(u.in.focus, t) || fresh) {
			to := cmp.Or(u.defaultIn(t), t)
			if n := len(u.in.trail); u.in.focus == 0 && n > 0 && u.within(u.in.trail[n-1], t) {
				to = t
			}
			u.in.focus, u.in.visible = to, false
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
	floats := u.paintOrder()
	for _, e := range floats {
		if e.style.Z >= 0 {
			u.paintFloat(f, e, full)
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

// paintFloat paints a float outside its parent's clip; an inlaid one stays inside the clips of the view it's in.
func (u *UI) paintFloat(f render.Frame, e *elem, clip render.Rect) {
	var clips []*elem
	for p := e; p.inlaid; {
		for p = p.parent; p != nil && p.style.Float == nil; p = p.parent {
			if s := p.style; s.Clip || s.ScrollX || s.ScrollY {
				clips = append(clips, p)
			}
		}
		if p == nil {
			break
		}
	}
	for _, p := range slices.Backward(clips) {
		f.PushClip(p.rect(), p.style.Radius)
		clip = intersect(clip, p.rect())
	}
	u.paint(f, e, clip)
	for range clips {
		f.Pop()
	}
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
		u.paintSel(f, e, r)
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
	if w := e.fx.wash; w.A > 0 {
		f.Rect(r, render.Style{Fill: w, Radius: s.Radius})
	}
	if clips {
		f.PushClip(r, s.Radius)
		clip = intersect(clip, r)
	}
	kf := f
	if e.fx.sink != 0 {
		kf = render.Offset(f, render.Point{Y: e.fx.sink})
	}
	for _, c := range e.kids {
		if c.style.Float != nil && c.style.Z < 0 {
			u.paint(kf, c, clip) // an underlay, behind the content
		}
	}
	for _, c := range e.kids {
		if c.style.Float != nil {
			continue
		}
		if cr := c.rect(); clips && (cr.Max.X <= clip.Min.X || cr.Min.X >= clip.Max.X || cr.Max.Y <= clip.Min.Y || cr.Min.Y >= clip.Max.Y) {
			continue
		}
		u.paint(kf, c, clip)
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
		// The ring settles in from 3 px further out, and is drawn where it's gliding from (focusRing).
		const w, gap = 2, 2
		g := gap + w + 3*(1-u.ring.in)
		d := u.ring.delta
		ring := render.Rect{Min: render.Point{X: r.Min.X - g + d.Min.X, Y: r.Min.Y - g + d.Min.Y}, Max: render.Point{X: r.Max.X + g + d.Max.X, Y: r.Max.Y + g + d.Max.Y}}
		c := u.opts.FocusRing
		c.A = uint8(math.Round(float64(c.A) * float64(max(0, min(1, u.ring.in)))))
		f.Rect(ring, render.Style{Stroke: c, Width: w, Radius: s.Radius + g})
	}
}

// Dump writes the latest layout as an indented tree, for golden tests.
func (u *UI) Dump() string {
	var b strings.Builder
	var walk func(e *elem, depth int)
	walk = func(e *elem, depth int) {
		fmt.Fprintf(&b, "%s%s %s %s", strings.Repeat("  ", depth), [...]string{"box", "text", "paint"}[e.kind], e.name(), fmtRect(e.rect()))
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
