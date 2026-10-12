package scene

import "image/color"

// item is one flex item in a row: its width and how to draw it centred on cy.
type item struct {
	w    float32
	draw func(x, cy float32)
}

func (c *Canvas) txt(s string, t TextStyle) item {
	return item{c.Measure(s, t), func(x, cy float32) { c.TextMid(x, cy, s, t) }}
}

func gap(w float32) item { return item{w: w} }

func (c *Canvas) dot(col color.NRGBA, glow float32) item {
	return item{7, func(x, cy float32) {
		if glow > 0 {
			c.GlowDisc(x+3.5, cy, 3.5, glow, alpha(col, .9))
		}
		c.Disc(x+3.5, cy, 3.5, col)
	}}
}

// chip is the uppercase pill: 600 9.5px mono, padding 2px 7px, radius 10.
func (c *Canvas) chip(s string, col color.NRGBA) item {
	t := TextStyle{Face: Mono, Weight: 600, Size: 9.5, Color: col, Spacing: .06}
	w := c.Measure(s, t) + 14 + 2
	return item{w, func(x, cy float32) {
		r := Rect{x, cy - 8.5, w, 17}
		c.RRect(r, 10, alpha(col, .07))
		c.Border(r, 10, 1, alpha(col, .35))
		c.TextMid(x+8, cy, s, t)
	}}
}

// bar is the tat meter: 46×4, radius 2.
func (c *Canvas) bar(frac float32, col color.NRGBA) item {
	return item{46, func(x, cy float32) {
		c.RRect(Rect{x, cy - 2, 46, 4}, 2, bg4)
		c.RRect(Rect{x, cy - 2, 46 * frac, 4}, 2, col)
	}}
}

func width(items []item, g float32) float32 {
	var w float32
	for i, it := range items {
		if i > 0 && it.draw != nil && items[i-1].draw != nil {
			w += g
		}
		w += it.w
	}
	return w
}

// row lays items left to right from x, with g between drawn items, and returns the end.
func row(x, cy, g float32, items ...item) float32 {
	for i, it := range items {
		if i > 0 && it.draw != nil && items[i-1].draw != nil {
			x += g
		}
		if it.draw != nil {
			it.draw(x, cy)
		}
		x += it.w
	}
	return x
}

// rowEnd lays items so the last ends at x.
func rowEnd(x, cy, g float32, items ...item) float32 {
	start := x - width(items, g)
	row(start, cy, g, items...)
	return start
}
