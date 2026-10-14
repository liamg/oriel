package widget

import (
	"fmt"

	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

var (
	tDiffAddFg = theme.Declare("diffstat.add", "ok")
	tDiffDelFg = theme.Declare("diffstat.del", "error")
	tDiffTrack = theme.Declare("diffstat.track", "line.strong")
)

type DiffStatOpts struct {
	Bar   bool // a bar after the counts, as long as the change is big next to Scale
	Scale int  // lines that fill the bar; 100 if 0
	Width float32
}

// DiffStat is a change's size: "+31 -9" in add and delete colours, a zero side left out, and optionally a bar split
// between them.
func DiffStat(c *ui.Ctx, key any, add, del int, o DiffStatOpts) {
	th := c.Theme()
	t := monoText(10.5, typeset.Medium, col(c, tDiffAddFg), 0)
	label := fmt.Sprintf("+%d -%d", add, del)
	c.Row(key, ui.Style{Gap: 6, Cross: ui.Center, Role: ui.RoleText, Label: label}, func(c *ui.Ctx) {
		if add > 0 || del == 0 {
			c.Text("add", fmt.Sprintf("+%d", add), t)
		}
		if del > 0 {
			d := t
			d.Color = th.C(tDiffDelFg)
			c.Text("del", fmt.Sprintf("-%d", del), d)
		}
		if !o.Bar {
			return
		}
		w, scale := o.Width, o.Scale
		if w == 0 {
			w = 28
		}
		if scale == 0 {
			scale = 100
		}
		total := add + del
		fill := w * min(1, max(float32(total)/float32(scale), .15))
		if total == 0 {
			fill = 0
		}
		c.Row("bar", ui.Style{W: ui.Px(w), H: ui.Px(4), Radius: 2, Fill: alpha(th.C(tDiffTrack), .5), Clip: true}, func(c *ui.Ctx) {
			if total == 0 {
				return
			}
			a := fill * float32(add) / float32(total)
			c.Box("a", ui.Style{W: ui.Px(a), H: ui.Flex(1), Fill: th.C(tDiffAddFg)}, nil)
			c.Box("d", ui.Style{W: ui.Px(fill - a), H: ui.Flex(1), Fill: th.C(tDiffDelFg)}, nil)
		})
	})
}
