package kit

import "github.com/liamg/oriel/ui"

// Scroll is how a scrolled column follows its end and keeps a block in view.
type Scroll struct {
	Follow bool   // stay at the end as it grows, while scrolled to it
	Reveal string // a block to bring into view next frame
}

// Scrolled is a column that scrolls (with the toolkit's scrollbars), following its end or bringing a block keyed in
// it into view on request; everything the console scrolls goes through it.
func Scrolled(c *ui.Ctx, key any, st *Scroll, s, content ui.Style, body func(c *ui.Ctx, block func(key string) ui.ID)) {
	id := c.Next(key)
	inner := ui.Child(id, "content")
	_, okV := c.Rect(id)
	_, okC := c.Rect(inner)
	blocks := map[string]ui.ID{}
	s.ScrollY, s.Clip, s.Follow = true, true, st.Follow
	c.Column(key, s, func(c *ui.Ctx) {
		content.W = ui.Flex(1)
		c.Column("content", content, func(c *ui.Ctx) {
			body(c, func(k string) ui.ID {
				blocks[k] = ui.Child(inner, k)
				return blocks[k]
			})
		})
	})
	if k := st.Reveal; k != "" && okV && okC {
		c.ScrollIntoView(id, blocks[k])
		st.Follow, st.Reveal = false, ""
	}
}
