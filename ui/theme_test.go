package ui

import (
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
)

// A box's Theme is the theme its subtree is built in, all the way down; outside it, the UI's own.
func TestASubtreeIsBuiltInItsOwnTheme(t *testing.T) {
	st, _ := theme.Stack(theme.Builtins(), "oriel")
	th, err := theme.Resolve(st, theme.Dark)
	if err != nil {
		t.Fatal(err)
	}
	own := th.With(map[theme.TokenID]theme.Color{theme.Line: {R: 255, A: 255}})
	u := newUI()
	u.SetTheme(th)
	var inside, deeper, after, before *theme.Resolved
	u.Frame(new(render.List).Record(1), render.Point{X: 400, Y: 300}, func(c *Ctx) {
		before = c.Theme()
		c.Column("scoped", Style{Theme: own}, func(c *Ctx) {
			inside = c.Theme()
			c.Row("kid", Style{}, func(c *Ctx) { deeper = c.Theme() })
		})
		c.Box("next", Style{}, func(c *Ctx) { after = c.Theme() })
	})
	if before != th || after != th {
		t.Error("outside the scoped box, the theme isn't the UI's")
	}
	if inside != own || deeper != own {
		t.Error("inside the scoped box, the theme isn't its own")
	}
}
