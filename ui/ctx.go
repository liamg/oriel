package ui

import (
	"math"
	"runtime/debug"
	"time"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
)

// Ctx builds the children of one node. Widgets are functions over a Ctx.
type Ctx struct {
	u *UI
	e *elem
}

// Scale is what what's built here is drawn at (Style.Scale), for a widget that measures in pixels itself.
func (c *Ctx) Scale() float32 {
	if c.e.scale == 0 {
		return 1
	}
	return c.e.scale
}

// scaled is st drawn at its own Scale times the parent's k, which it sets *at to.
func scaled(st Style, at *float32, k float32) Style {
	if st.Scale > 0 {
		k *= st.Scale
		*at = k
	}
	if k == 1 {
		return st
	}
	st.W, st.H = st.W.times(k), st.H.times(k)
	st.Pad = Insets{st.Pad.L * k, st.Pad.T * k, st.Pad.R * k, st.Pad.B * k}
	st.Gap *= k
	st.Radius *= k
	if st.Float != nil {
		f := *st.Float
		f.Offset = render.Point{X: f.Offset.X * k, Y: f.Offset.Y * k}
		st.Float = &f
	}
	return st
}

// ID is the id of the node this Ctx builds into.
func (c *Ctx) ID() ID { return c.e.id }

func (c *Ctx) Shaper() typeset.Shaper { return c.u.sh }

// Theme is the resolved theme widgets read their colours from, by token id.
func (c *Ctx) Theme() *theme.Resolved { return c.u.theme }

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
	e.style = scaled(st, &e.scale, c.Scale())
	stackFloat(e)
	if body != nil {
		body(&Ctx{u: c.u, e: e})
	}
	return e.id
}

// stackFloat lifts a float inside another float to at least that float's Z, so a popover in a page paints over it.
func stackFloat(e *elem) {
	if e.style.Float == nil || e.style.Z < 0 {
		return
	}
	e.style.Z = max(e.style.Z, e.parent.layer())
}

// Guard adds a box like Box, but a panic in body doesn't escape: what body built is dropped, and fallback
// builds the box instead, given the panic's value and stack.
func (c *Ctx) Guard(key any, st Style, body func(c *Ctx), fallback func(c *Ctx, v any, stack []byte)) ID {
	e := c.u.add(c.e, key, kBox)
	e.style = scaled(st, &e.scale, c.Scale())
	if v, stack := try(func() { body(&Ctx{u: c.u, e: e}) }); stack != nil {
		e.kids, e.keys = nil, nil
		fallback(&Ctx{u: c.u, e: e}, v, stack)
	}
	return e.id
}

// try runs fn, and returns what it panicked with and where; stack is nil when it didn't.
func try(fn func()) (v any, stack []byte) {
	defer func() {
		if v = recover(); v != nil {
			stack = debug.Stack()
		}
	}()
	fn()
	return nil, nil
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
	if k := c.Scale(); k != 1 {
		ts.Font.Size *= k
	}
	e.text, e.ts = s, ts
	e.run = c.u.sh.Shape(s, ts.Font)
	return e.id
}

// Paint adds a node drawn by fn into its rect.
func (c *Ctx) Paint(key any, w, h Size, fn func(f render.Frame, r render.Rect)) ID {
	e := c.u.add(c.e, key, kPaint)
	k := c.Scale()
	e.style.W, e.style.H = w.times(k), h.times(k)
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
	case *Func:
		c.Box(n.Key, Style{W: Flex(1), H: Flex(1), Dir: Column}, n.Fn)
	}
}

// List adds a vertically scrolling list of n rows, each rowH tall, and builds only the rows in view:
// the rows that last frame's height and the scroll offset show, plus one.
func (c *Ctx) List(key any, st Style, n int, rowH float32, row func(c *Ctx, i int)) ID {
	return c.ListBehind(key, st, n, rowH, nil, row)
}

// ListBehind is List with behind, if it's set, building what's drawn behind the rows (an underlay float, Z < 0, as a
// selection highlight that glides): top is where row 0's top is in the window, scrolling included.
func (c *Ctx) ListBehind(key any, st Style, n int, rowH float32, behind func(c *Ctx, top float32), row func(c *Ctx, i int)) ID {
	st.Dir, st.ScrollY = Column, true
	st.Gap = 0
	return c.Box(key, st, func(lc *Ctx) {
		if behind != nil {
			top := float32(0)
			if r, ok := lc.u.Rect(lc.e.id); ok {
				top = r.Min.Y - lc.u.Scroll(lc.e.id).Y
			}
			behind(lc, top)
		}
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

// State is the widget state kept for id while it's built each frame, created zero the first time.
func State[T any](c *Ctx, id ID) *T {
	st := c.u.state(id)
	st.seen = c.u.frame
	if v, ok := st.data.(*T); ok {
		return v
	}
	v := new(T)
	st.data = v
	return v
}

// Focus moves keyboard focus to id once the frame is built, without a focus ring: focus a widget moves itself isn't
// the keyboard's. Of the frame's calls the one from the highest layer wins, and none from under the float that holds
// focus: a page behind a dialog can't take it back.
func (c *Ctx) Focus(id ID) {
	z := c.e.layer()
	if r := &c.u.in.ask; !r.set || z >= r.z {
		*r = focusAsk{id: id, z: z, set: true}
	}
}

// layer is the Z of the float e is in, or 0 in the flow.
func (e *elem) layer() int {
	for ; e != nil; e = e.parent {
		if e.style.Float != nil && e.style.Z >= 0 {
			return e.style.Z
		}
	}
	return 0
}

// PassKeys hands keys id was given and doesn't take to the nearest node above it that takes keys, next frame.
func (c *Ctx) PassKeys(id ID, keys []render.KeyEvent) { c.u.PassKeys(id, keys) }

// Blur drops keyboard focus.
func (c *Ctx) Blur() { c.u.in.focus = 0 }

// FocusNext moves keyboard focus to the next focusable node (back with -1), as last frame laid them out.
func (c *Ctx) FocusNext(dir int) { c.u.FocusNext(dir) }

// PressedWithin reports whether the primary button is down on id or on something inside it.
func (c *Ctx) PressedWithin(id ID) bool { return c.u.in.active != 0 && c.u.within(c.u.in.active, id) }

// FocusWithin reports whether id or something inside it has keyboard focus, as last frame laid them out.
func (c *Ctx) FocusWithin(id ID) bool { return c.u.in.focus != 0 && c.u.within(c.u.in.focus, id) }

// Clipboard is the system clipboard, or one that holds nothing when the UI has none.
func (c *Ctx) Clipboard() render.Clipboard {
	if c.u.clip == nil {
		return noClipboard{}
	}
	return c.u.clip
}

type noClipboard struct{}

func (noClipboard) SetClipboard(string)           {}
func (noClipboard) ReadClipboard() (string, bool) { return "", false }

// Now is the UI's clock.
func (c *Ctx) Now() time.Time { return c.u.now() }

// WakeAt asks for a frame at t, for a widget waiting on time (a toast's timeout).
func (c *Ctx) WakeAt(t time.Time) {
	if c.u.wake.IsZero() || t.Before(c.u.wake) {
		c.u.wake = t
	}
}

// Size is the window's logical size.
func (c *Ctx) Size() render.Point {
	return render.Point{X: c.u.root.style.W.Value, Y: c.u.root.style.H.Value}
}

// Scroll is id's scroll offset.
func (c *Ctx) Scroll(id ID) render.Point { return c.u.Scroll(id) }

// ScrollTo sets id's scroll offset; layout clamps it.
func (c *Ctx) ScrollTo(id ID, p render.Point) { c.u.ScrollTo(id, p) }

// ScrollIntoView scrolls box the least it takes to show target, a node inside it, as both were laid out last frame.
func (c *Ctx) ScrollIntoView(box, target ID) {
	br, ok := c.u.Rect(box)
	tr, ok2 := c.u.Rect(target)
	if !ok || !ok2 {
		return
	}
	s := c.u.Scroll(box)
	switch {
	case tr.Min.Y < br.Min.Y:
		s.Y -= br.Min.Y - tr.Min.Y
	case tr.Max.Y > br.Max.Y:
		s.Y += min(tr.Max.Y-br.Max.Y, tr.Min.Y-br.Min.Y)
	}
	switch {
	case tr.Min.X < br.Min.X:
		s.X -= br.Min.X - tr.Min.X
	case tr.Max.X > br.Max.X:
		s.X += min(tr.Max.X-br.Max.X, tr.Min.X-br.Min.X)
	}
	if s != c.u.Scroll(box) {
		c.u.ScrollTo(box, s)
	}
}

// ClaimContext says a widget answered id's right-click with a context menu of its own.
func (c *Ctx) ClaimContext(id ID) {
	if c.u.in.claimed == nil {
		c.u.in.claimed = map[ID]bool{}
	}
	c.u.in.claimed[id] = true
}

// RequestHelp asks for help page id.
func (c *Ctx) RequestHelp(id string) { c.u.RequestHelp(id) }

// Help is the help id of id or its nearest ancestor with one.
func (c *Ctx) Help(id ID) string { return c.u.Help(id) }

// Child is the id a child built with key under parent gets (the first with that key).
func Child(parent ID, key any) ID { return childID(parent, key) }
