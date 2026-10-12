package ui

import (
	"math"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
)

// Ctx builds the children of one node. Widgets are functions over a Ctx.
type Ctx struct {
	u *UI
	e *elem
}

// ID is the id of the node this Ctx builds into.
func (c *Ctx) ID() ID { return c.e.id }

func (c *Ctx) Shaper() typeset.Shaper { return c.u.sh }

// Next is the id the next child added with key will have, so a widget can ask for its interaction
// before it builds itself.
func (c *Ctx) Next(key any) ID {
	k, _ := c.e.nextKey(key)
	return childID(c.e.id, k)
}

// Interaction is id's hover, press, click and focus state.
func (c *Ctx) Interaction(id ID) Interaction { return c.u.Interaction(id) }

// Rect is where id was laid out last frame.
func (c *Ctx) Rect(id ID) (render.Rect, bool) { return c.u.Rect(id) }

// Box adds a box and builds its children with body, which may be nil.
func (c *Ctx) Box(key any, st Style, body func(c *Ctx)) ID {
	e := c.u.add(c.e, key, kBox)
	e.style = st
	if body != nil {
		body(&Ctx{u: c.u, e: e})
	}
	return e.id
}

func (c *Ctx) Row(key any, st Style, body func(c *Ctx)) ID {
	st.Dir = Row
	return c.Box(key, st, body)
}

func (c *Ctx) Column(key any, st Style, body func(c *Ctx)) ID {
	st.Dir = Column
	return c.Box(key, st, body)
}

// Text adds one line of text. A zero font size takes the UI's default style.
func (c *Ctx) Text(key any, s string, ts TextStyle) ID {
	e := c.u.add(c.e, key, kText)
	if ts.Font.Size == 0 {
		col := ts.Color
		ts = c.u.text
		if col.A > 0 {
			ts.Color = col
		}
	}
	e.text, e.ts = s, ts
	e.run = c.u.sh.Shape(s, ts.Font)
	return e.id
}

// Paint adds a node drawn by fn into its rect.
func (c *Ctx) Paint(key any, w, h Size, fn func(f render.Frame, r render.Rect)) ID {
	e := c.u.add(c.e, key, kPaint)
	e.style.W, e.style.H = w, h
	e.paint = fn
	return e.id
}

// Node adds a tree built elsewhere, such as a plugin's view.
func (c *Ctx) Node(n Node) {
	switch n := n.(type) {
	case *Box:
		c.Box(n.Key, n.Style, func(c *Ctx) {
			for _, k := range n.Children {
				c.Node(k)
			}
		})
	case *Text:
		c.Text(n.Key, n.Text, n.Style)
	case *Paint:
		c.Paint(n.Key, n.W, n.H, n.Fn)
	}
}

// List adds a vertically scrolling list of n rows, each rowH tall, and builds only the rows in view:
// the rows that last frame's height and the scroll offset show, plus one.
func (c *Ctx) List(key any, st Style, n int, rowH float32, row func(c *Ctx, i int)) ID {
	st.Dir, st.ScrollY = Column, true
	st.Gap = 0
	return c.Box(key, st, func(lc *Ctx) {
		view := float32(64 * rowH)
		if r, ok := lc.u.Rect(lc.e.id); ok && r.Dy() > 0 {
			view = r.Dy()
		}
		scroll := lc.u.Scroll(lc.e.id).Y
		first := max(0, min(n, int(math.Floor(float64(scroll/rowH)))))
		last := min(n, first+int(math.Ceil(float64(view/rowH)))+1)
		lc.Box("above", Style{H: Px(float32(first) * rowH)}, nil)
		for i := first; i < last; i++ {
			lc.Box(i, Style{W: Flex(1), H: Px(rowH)}, func(rc *Ctx) { row(rc, i) })
		}
		lc.Box("below", Style{H: Px(float32(n-last) * rowH)}, nil)
	})
}
