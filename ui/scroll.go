package ui

import "github.com/liamg/oriel/render"

// Scroll is id's scroll offset.
func (u *UI) Scroll(id ID) render.Point {
	st := u.state(id)
	return render.Point{X: st.scroll[0], Y: st.scroll[1]}
}

// ScrollBy moves id's scroll offset at once, from where it's drawn, as far as its last layout had to scroll; a box
// not laid out yet is clamped by its first.
func (u *UI) ScrollBy(id ID, dx, dy float32) {
	st := u.state(u.scroller(id))
	st.seen = u.frame
	st.stopEasing()
	was := st.scroll
	for axis, d := range [2]float32{dx, dy} {
		st.scroll[axis] += d
		if st.placed {
			st.scroll[axis] = max(0, min(st.scroll[axis], st.ext[axis]-st.view[axis]))
		}
	}
	if st.scroll != was {
		st.scrolledAt = u.now()
	}
}

// ScrollTo sets id's scroll offset, which the next layout clamps to the content. Once the box has been laid out it
// glides there, over a view's length at most.
func (u *UI) ScrollTo(id ID, p render.Point) {
	st := u.state(u.scroller(id))
	st.seen = u.frame
	to := [2]float32{p.X, p.Y}
	if !st.placed || st.sharedAt != 0 || u.opts.ReducedMotion || u.opts.MotionSpeed <= 0 {
		st.scroll, st.shown = to, to
		return
	}
	if st.easing && to == st.asked {
		st.scroll = to // asked again, past where the layout clamped it: the content may have grown
		return
	}
	if !st.easing && to == st.scroll {
		return
	}
	for axis := range 2 {
		d := max(-st.view[axis], min(st.view[axis], to[axis]-st.shown[axis]))
		st.easeFrom[axis] = to[axis] - d
	}
	st.scroll, st.asked, st.easing = to, to, true
	u.springs[childID(id, motionKey{"scroll"})] = &spring{x: 0, at: u.now(), seen: u.frame}
}

// scroller is the id whose offset id's box scrolls by: its ScrollWith, else its own.
func (u *UI) scroller(id ID) ID {
	if w := u.flags[id].scrollWith; w != 0 {
		return w
	}
	return id
}

// ScrollShown is id's scroll offset as it's drawn, which lags Scroll while a ScrollTo glides.
func (u *UI) ScrollShown(id ID) render.Point {
	st := u.state(id)
	if !st.easing {
		return render.Point{X: st.scroll[0], Y: st.scroll[1]}
	}
	p := float32(0)
	if sp, ok := u.springs[childID(id, motionKey{"scroll"})]; ok {
		p = sp.x
	}
	return render.Point{X: st.easeFrom[0] + (st.scroll[0]-st.easeFrom[0])*p, Y: st.easeFrom[1] + (st.scroll[1]-st.easeFrom[1])*p}
}

// stopEasing leaves the scroll where it's drawn, for a move that takes it from there: the wheel, a scrollbar.
func (st *state) stopEasing() {
	if st.easing {
		st.scroll, st.easing = st.shown, false
	}
}
