package ui

import (
	"time"

	"github.com/liamg/oriel/render"
)

// Bars is when a scrolling box shows its scrollbars. Every box with ScrollX or ScrollY gets them, drawn over its
// content at its far edges, only along an axis its content overflows.
type Bars uint8

const (
	BarsAuto   Bars = iota // the UI's default (Options.Bars): by default, while the pointer is over it and for a moment after it scrolls
	BarsAlways             // whenever its content overflows
	BarsHidden             // never, though it still scrolls
)

const (
	barWidth      = 6
	barWidthHot   = 9
	barInset      = 2
	barMinThumb   = 24
	barLinger     = 900 * time.Millisecond // shown after a scroll in BarsAuto
	barFade       = 300 * time.Millisecond
	barGrabMargin = 4 // the bar takes presses this far beyond its thumb's width
)

// barDrag is a scrollbar thumb being dragged.
type barDrag struct {
	id   ID
	axis int
	grab float32 // where along the thumb it was pressed
}

// bar is one scrollbar's track and thumb, in window coordinates.
type bar struct {
	track, thumb render.Rect
	max          float32 // the scroll offset at the end
}

// bars are a box's scrollbars along each axis that overflows; a zero bar where it doesn't.
func (u *UI) bars(id ID) (out [2]bar, ok [2]bool) {
	st, found := u.states[id]
	if !found {
		return
	}
	f := u.flags[id]
	scrolls := [2]bool{f.scrollX, f.scrollY}
	r := st.rect
	for axis := range 2 {
		if !scrolls[axis] || st.ext[axis] <= st.view[axis]+0.5 || st.view[axis] <= 0 {
			continue
		}
		ok[axis] = true
	}
	for axis := range 2 {
		if !ok[axis] {
			continue
		}
		var track render.Rect
		corner := float32(0)
		if ok[1-axis] {
			corner = barWidthHot + barInset
		}
		if axis == 1 {
			track = render.Rect{Min: render.Point{X: r.Max.X - barInset - barWidthHot, Y: r.Min.Y + barInset},
				Max: render.Point{X: r.Max.X - barInset, Y: r.Max.Y - barInset - corner}}
		} else {
			track = render.Rect{Min: render.Point{X: r.Min.X + barInset, Y: r.Max.Y - barInset - barWidthHot},
				Max: render.Point{X: r.Max.X - barInset - corner, Y: r.Max.Y - barInset}}
		}
		length := track.Dy()
		if axis == 0 {
			length = track.Dx()
		}
		thumbLen := max(barMinThumb, length*st.view[axis]/st.ext[axis])
		thumbLen = min(thumbLen, length)
		maxScroll := st.ext[axis] - st.view[axis]
		at := (length - thumbLen) * st.scroll[axis] / maxScroll
		thumb := track
		if axis == 1 {
			thumb.Min.Y, thumb.Max.Y = track.Min.Y+at, track.Min.Y+at+thumbLen
		} else {
			thumb.Min.X, thumb.Max.X = track.Min.X+at, track.Min.X+at+thumbLen
		}
		out[axis] = bar{track: track, thumb: thumb, max: maxScroll}
	}
	return out, ok
}

func inside(p render.Point, r render.Rect) bool {
	return p.X >= r.Min.X && p.X < r.Max.X && p.Y >= r.Min.Y && p.Y < r.Max.Y
}

func grow(r render.Rect, by float32) render.Rect {
	return render.Rect{Min: render.Point{X: r.Min.X - by, Y: r.Min.Y - by}, Max: render.Point{X: r.Max.X + by, Y: r.Max.Y + by}}
}

// barAt is the scrollbar of the topmost scrolling box under p whose bar is shown there, if any.
func (u *UI) barAt(p render.Point) (ID, int, bar, bool) {
	for _, id := range u.Hit(p) {
		f := u.flags[id]
		if !f.scrollX && !f.scrollY {
			continue
		}
		bs, ok := u.bars(id)
		for axis := range 2 {
			if ok[axis] && u.barMode(id) != BarsHidden && inside(p, grow(bs[axis].track, barGrabMargin)) {
				return id, axis, bs[axis], true
			}
		}
	}
	return 0, 0, bar{}, false
}

func (u *UI) barMode(id ID) Bars {
	if m := u.flags[id].bars; m != BarsAuto {
		return m
	}
	return u.opts.Bars
}

// barPress takes a press on a scrollbar: on the thumb it starts a drag, on the track it pages towards the press.
func (u *UI) barPress(p render.Point) bool {
	id, axis, b, ok := u.barAt(p)
	if !ok {
		return false
	}
	at, lo, hi := p.Y, b.thumb.Min.Y, b.thumb.Max.Y
	if axis == 0 {
		at, lo, hi = p.X, b.thumb.Min.X, b.thumb.Max.X
	}
	st := u.state(id)
	st.scrolledAt = u.now()
	switch {
	case at < lo:
		st.scroll[axis] = max(0, st.scroll[axis]-st.view[axis])
	case at >= hi:
		st.scroll[axis] = min(b.max, st.scroll[axis]+st.view[axis])
	default:
		u.in.bar = &barDrag{id: id, axis: axis, grab: at - lo}
	}
	u.wake = u.now()
	return true
}

// barMove drags the thumb being dragged.
func (u *UI) barMove(p render.Point) bool {
	d := u.in.bar
	if d == nil {
		return false
	}
	bs, ok := u.bars(d.id)
	if !ok[d.axis] {
		return true
	}
	b := bs[d.axis]
	at, start, length, thumb := p.Y, b.track.Min.Y, b.track.Dy(), b.thumb.Dy()
	if d.axis == 0 {
		at, start, length, thumb = p.X, b.track.Min.X, b.track.Dx(), b.thumb.Dx()
	}
	if room := length - thumb; room > 0 {
		st := u.state(d.id)
		st.scroll[d.axis] = max(0, min(b.max, (at-d.grab-start)/room*b.max))
		st.scrolledAt = u.now()
	}
	u.wake = u.now()
	return true
}

// barAlpha is how much of a box's scrollbar shows now, 0 to 1.
func (u *UI) barAlpha(id ID, r render.Rect) float32 {
	switch u.barMode(id) {
	case BarsHidden:
		return 0
	case BarsAlways:
		return 1
	}
	if (u.in.bar != nil && u.in.bar.id == id) || (u.in.inside && inside(u.in.pos, r)) {
		return 1
	}
	st := u.states[id]
	if st == nil || st.scrolledAt.IsZero() {
		return 0
	}
	since := u.now().Sub(st.scrolledAt)
	switch {
	case since < barLinger:
		u.wakeAt(st.scrolledAt.Add(barLinger))
		return 1
	case since < barLinger+barFade:
		u.wakeAt(u.now().Add(16 * time.Millisecond))
		return 1 - float32(since-barLinger)/float32(barFade)
	}
	return 0
}

func (u *UI) wakeAt(t time.Time) {
	if u.wake.IsZero() || t.Before(u.wake) {
		u.wake = t
	}
}

// paintBars draws a box's scrollbars over its content.
func (u *UI) paintBars(f render.Frame, id ID, r render.Rect) {
	bs, ok := u.bars(id)
	if !ok[0] && !ok[1] {
		return
	}
	a := u.barAlpha(id, r)
	if a <= 0 {
		return
	}
	for axis := range 2 {
		if !ok[axis] {
			continue
		}
		b := bs[axis]
		hot := (u.in.bar != nil && u.in.bar.id == id && u.in.bar.axis == axis) || (u.in.inside && inside(u.in.pos, grow(b.track, barGrabMargin)))
		w := float32(barWidth)
		col := u.opts.ScrollThumb
		if hot {
			w, col = barWidthHot, u.opts.ScrollThumbHot
		}
		t := b.thumb
		if axis == 1 {
			t.Min.X = t.Max.X - w
		} else {
			t.Min.Y = t.Max.Y - w
		}
		col.A = uint8(float32(col.A) * a)
		f.Rect(t, render.Style{Fill: col, Radius: w / 2})
	}
}
