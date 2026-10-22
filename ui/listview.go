package ui

// KeepRowInView scrolls list, a List of rows rowH tall, the least it takes to show row i when i is new, as a
// selection that moves: between moves the pointer and the scrollbar scroll it freely. The row needn't be built.
func (c *Ctx) KeepRowInView(list ID, i int, rowH float32) {
	k := State[keptView](c, Child(list, "\x00keptRow"))
	target := Child(list, i)
	if k.seen && k.target == target {
		return
	}
	r, ok := c.u.Rect(list)
	if !ok || r.Dy() <= 0 {
		return // not laid out yet: next frame
	}
	s := c.u.Scroll(list)
	top := float32(i) * rowH
	switch {
	case top < s.Y:
		s.Y = top
	case top+rowH > s.Y+r.Dy():
		s.Y = top + rowH - r.Dy()
	}
	if s != c.u.Scroll(list) {
		c.u.ScrollTo(list, s)
	}
	k.target, k.seen = target, true
}
