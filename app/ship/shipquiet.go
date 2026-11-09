package ship

import (
	"fmt"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// shipColumnMin is the narrowest a project's column on the Ship page gets; past what fits, the page scrolls sideways.
const (
	shipColumnMin = 380
	shipColumnGap = 22
)

// tooWide says n columns don't fit the box id was drawn at last frame, inside pad, each at shipColumnMin.
func (x *Ship) tooWide(c *ui.Ctx, id ui.ID, pad ui.Insets, n int) bool {
	rc, ok := c.Rect(id)
	if !ok || n < 2 {
		return false
	}
	need := float32(n)*shipColumnMin + float32(n-1)*shipColumnGap
	return need > rc.Dx()-pad.L-pad.R
}

// shownShips are the projects the Ship page gives columns: every one, or those with something to ship while the
// rest are folded away (quietStrip).
func (x *Ship) shownShips(se core.SessionState, ships []shipRead) []shipRead {
	var shown []shipRead
	for _, s := range ships {
		if !quiet(s) {
			shown = append(shown, s)
		}
	}
	if len(shown) == 0 || len(shown) == len(ships) || x.showQuiet[se.ID] {
		return ships
	}
	return shown
}

// quiet says a project read has nothing to ship: nothing uncommitted, no commits on its branch, no pull request,
// no draft, nothing gone wrong, and on the session's branch.
func quiet(s shipRead) bool {
	return !s.GitRead.IsZero() && s.GitError == "" && s.HostError == "" && s.Off == nil && len(s.Files) == 0 && s.Commits == 0 &&
		len(s.Unpushed) == 0 && s.PR == nil && s.CommitDraft.Empty() && s.PRDraft.Empty()
}

// quietStrip is the line over the Ship page's columns naming the projects with nothing to ship, which it leaves
// out until Show: it returns the columns to draw. With every project quiet, or one, they're all shown.
func (x *Ship) quietStrip(c *ui.Ctx, se core.SessionState, ships []shipRead) []shipRead {
	var still []string
	for _, s := range ships {
		if quiet(s) {
			still = append(still, s.Project)
		}
	}
	if len(still) == 0 || len(still) == len(ships) {
		return ships
	}
	open := x.showQuiet[se.ID]
	th := c.Theme()
	c.Row("quiet", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Row("names", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
			c.Text("t", fmt.Sprintf("%s with nothing to ship: %s", kit.Counted(len(still), "project"), strings.Join(still, ", ")),
				kit.MonoFont(11, th.C(theme.FgFaint)))
		})
		label := "Show"
		if open {
			label = "Hide"
		}
		if widget.Button(c, "show", label, widget.ButtonOpts{Ghost: true, Help: "git/ship#page"}) {
			if x.showQuiet == nil {
				x.showQuiet = map[core.SessionID]bool{}
			}
			x.showQuiet[se.ID] = !open
		}
	})
	return x.shownShips(se, ships)
}
