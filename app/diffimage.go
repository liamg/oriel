package app

import (
	"fmt"
	"image"

	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

const diffImageHelp = "files/diff#images"

// imageDiff is a changed image drawn old and new: side by side, each labelled with its dimensions and size, or as a
// slider over the two. An added or deleted image is its one side.
func (x *diffPlugin) imageDiff(c *ui.Ctx, st *diffState, file string, d *imageDiff) {
	th := c.Theme()
	id := c.Next("images")
	width := float32(800)
	if r, ok := c.Rect(id); ok && r.Dx() > 0 {
		width = r.Dx() - 2*24
	}
	group := "diff:" + st.root + ":" + file
	c.Column("images", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(24, 14), Gap: 12, Role: ui.RoleGroup, Label: file + " images", Help: diffImageHelp}, func(c *ui.Ctx) {
		both := d.old != nil && d.new != nil
		if both {
			if st.slider == nil {
				st.slider = map[string]bool{}
			}
			mode := 0
			if st.slider[file] {
				mode = 1
			}
			if widget.Segmented(c, "mode", []string{"Side by side", "Slider"}, &mode, widget.SegmentedOpts{Label: "Show the images", Help: diffImageHelp}) {
				st.slider[file] = mode == 1
			}
		}
		if both && st.slider[file] {
			w := min(width, float32(max(d.old.Bounds().Dx(), d.new.Bounds().Dx())))
			widget.Compare(c, "slider", d.old, d.new, w, widget.CompareOpts{Before: "OLD", After: "NEW", Label: file, Help: diffImageHelp})
			c.Text("sizes", "old "+imageFacts(d.old, d.oldSize)+"   new "+imageFacts(d.new, d.newSize), monoFont(10.5, th.C(theme.FgFaint)))
			return
		}
		side := func(c *ui.Ctx, key, label string, img image.Image, size int, tone theme.TokenID, w float32) {
			c.Column(key, ui.Style{W: ui.Px(w), Gap: 6}, func(c *ui.Ctx) {
				c.Row("head", ui.Style{Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
					c.Text("l", label, kickerFont(th.C(tone)))
					c.Text("facts", imageFacts(img, size), monoFont(10.5, th.C(theme.FgFaint)))
				})
				iw := min(w, float32(img.Bounds().Dx()))
				c.Box("img", ui.Style{W: ui.Px(iw), Stroke: th.C(theme.Line), Border: 1}, func(c *ui.Ctx) {
					widget.Image(c, "px", img, iw, widget.ImageOpts{Label: label + " " + file, Help: diffImageHelp, Zoom: true, Group: group})
				})
			})
		}
		c.Row("sides", ui.Style{W: ui.Flex(1), Gap: 24}, func(c *ui.Ctx) {
			half := (width - 24) / 2
			switch {
			case both:
				side(c, "old", "OLD", d.old, d.oldSize, tDiffDelEdge, half)
				side(c, "new", "NEW", d.new, d.newSize, tDiffAddEdge, half)
			case d.old != nil:
				side(c, "old", "DELETED", d.old, d.oldSize, tDiffDelEdge, width)
			default:
				side(c, "new", "ADDED", d.new, d.newSize, tDiffAddEdge, width)
			}
		})
	})
}

// imageFacts is an image's dimensions and file size: "640×400 · 2.3 KB".
func imageFacts(img image.Image, size int) string {
	b := img.Bounds()
	return fmt.Sprintf("%d×%d · %s", b.Dx(), b.Dy(), humanBytes(int64(size)))
}
