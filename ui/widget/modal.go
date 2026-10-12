package widget

import (
	"time"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/ui"
)

type ModalOpts struct {
	Width float32 // 600 by default
	Help  string
}

type modalState struct{ shown bool }

// Modal shows a card over a scrim while *open, holding keyboard focus inside it. Escape, or a click on
// the scrim, closes it.
func Modal(c *ui.Ctx, key any, open *bool, title string, o ModalOpts, body func(c *ui.Ctx)) {
	id := c.Next(key)
	st := ui.State[modalState](c, id)
	if !*open {
		st.shown = false
		return
	}
	if hasKey(c.Interaction(id).Keys, "Esc") {
		*open, st.shown = false, false
		return
	}
	size := c.Size()
	scrim := c.Next(struct{ k any }{key})
	if c.Interaction(scrim).Clicked {
		*open, st.shown = false, false
		return
	}
	if !st.shown {
		st.shown = true
		c.Focus(id)
	}
	c.Box(struct{ k any }{key}, ui.Style{W: ui.Px(size.X), H: ui.Px(size.Y), Fill: col(c, tScrim), Z: 1000, Interactive: true,
		Help: o.Help, Float: &ui.Float{Point: &render.Point{}}}, nil)
	w := o.Width
	if w == 0 {
		w = 600
	}
	centre := render.Point{X: size.X / 2, Y: size.Y / 2}
	c.Column(key, ui.Style{W: ui.Px(w), Fill: col(c, tModalBg), Stroke: col(c, tModalBorder), Border: 1, Radius: 10, Z: 1001, Clip: true,
		Float: &ui.Float{Point: &centre, Self: render.Point{X: .5, Y: .5}}, Trap: true, Focusable: true, KeyMode: "modal", Help: o.Help}, func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(40), Pad: ui.PadXY(16, 0), Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("title", title, uiText(c, 13, typeset.SemiBold, col(c, tModalTitle)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			kbd(c, "esc")
		})
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: col(c, tMenuSeparator)}, nil)
		c.Column("body", ui.Style{W: ui.Flex(1), Pad: ui.Pad(16), Gap: 10}, body)
	})
}

// Toast is a notification card. It stays until dismissed, or for TTL when that's set.
type Toast struct {
	Title, Body string
	Tone        render.Color // the card's edge: a status colour
	TTL         time.Duration
	Help        string
	id          int
	at          time.Time
}

// Toasts is a stack of toasts, newest at the bottom.
type Toasts struct {
	list []Toast
	next int
}

func (t *Toasts) Push(x Toast) {
	t.next++
	x.id = t.next
	t.list = append(t.list, x)
}

func (t *Toasts) Len() int { return len(t.list) }

// ShowToasts draws t in the bottom right corner. A click, or Enter, Escape or x while one is focused,
// dismisses it; one with a TTL goes by itself.
func ShowToasts(c *ui.Ctx, key any, t *Toasts) {
	now := c.Now()
	keep := t.list[:0]
	for _, x := range t.list {
		if x.at.IsZero() {
			x.at = now
		}
		if x.TTL > 0 {
			due := x.at.Add(x.TTL)
			if !now.Before(due) {
				continue
			}
			c.WakeAt(due)
		}
		keep = append(keep, x)
	}
	t.list = keep
	if len(t.list) == 0 {
		return
	}
	size := c.Size()
	corner := render.Point{X: size.X - 16, Y: size.Y - 16}
	c.Column(key, ui.Style{Gap: 8, Z: 950, Float: &ui.Float{Point: &corner, Self: render.Point{X: 1, Y: 1}}}, func(c *ui.Ctx) {
		var gone []int
		for _, x := range t.list {
			in := c.Interaction(c.Next(x.id))
			if in.Clicked || hasKey(in.Keys, "CR", "Esc", "x") {
				gone = append(gone, x.id)
			}
			c.Row(x.id, ui.Style{W: ui.Px(340), Fill: col(c, tToastBg), Stroke: col(c, tToastBorder), Border: 1, Radius: 8, Clip: true,
				Focusable: true, KeyMode: "toast", Cursor: render.CursorPointer, Help: x.Help}, func(c *ui.Ctx) {
				c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Flex(1), Fill: x.Tone}, nil)
				c.Column("text", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(12, 10), Gap: 4}, func(c *ui.Ctx) {
					c.Text("title", x.Title, uiText(c, 12.5, typeset.SemiBold, col(c, tToastFg)))
					if x.Body != "" {
						c.Text("body", x.Body, monoText(10.5, typeset.Normal, col(c, tToastMuted), 0))
					}
				})
			})
		}
		for _, g := range gone {
			for i, x := range t.list {
				if x.id == g {
					t.list = append(t.list[:i], t.list[i+1:]...)
					break
				}
			}
		}
	})
}
