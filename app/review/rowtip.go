package review

import (
	"cmp"
	"fmt"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit/prui"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// rowTipWidth is how wide a row's tooltip wraps its title.
const rowTipWidth = 380

// rowTip is a request's row as its tooltip: what the row hasn't room for. Its whole title, where it is, who opened
// it and when, which branch goes into which, its size and checks, who's asked and who has reviewed it.
func (x *Reviews) rowTip(c *ui.Ctx, r reviewRow, many bool) {
	th := c.Theme()
	q := r.req
	faint, body := kit.MonoFont(10.5, th.C(theme.FgMuted)), kit.UIFont(12, th.C(theme.Fg))
	c.Column("tip", ui.Style{Gap: 6, Pad: ui.PadXY(2, 4)}, func(c *ui.Ctx) {
		c.Row("where", ui.Style{Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("key", q.Key(), faint)
			if q.Draft {
				c.Text("draft", "draft", kit.MonoFont(10.5, th.C(theme.Warn)))
			}
			if many && r.src.Label() != "" {
				c.Text("src", r.src.Label(), faint)
			}
		})
		widget.Wrap(c, "title", q.Title, rowTipWidth, kit.UIFont(13.5, th.C(theme.Fg)))
		for i, line := range rowTipLines(*q, x.now()) {
			c.Text(i, line, body)
		}
		if mark, tone := ciMark(q.CI); mark != "" {
			c.Text("ci", mark, kit.MonoFont(10.5, kit.TokenColour(th, tone, th.C(theme.FgMuted))))
		}
		if r.se != nil {
			c.Text("se", "Session: "+r.se.Name(), kit.UIFont(12, th.C(theme.Accent)))
		}
	})
}

// rowTipLines are a request's facts for its tooltip, a line each, those it doesn't know left out.
func rowTipLines(q registry.ReviewRequest, now time.Time) []string {
	var out []string
	add := func(parts ...string) {
		var kept []string
		for _, p := range parts {
			if p != "" {
				kept = append(kept, p)
			}
		}
		if len(kept) > 0 {
			out = append(out, strings.Join(kept, " · "))
		}
	}
	opened, updated := "", ""
	if q.Author != "" {
		opened = "opened by " + q.Author
	}
	if !q.Opened.IsZero() {
		opened = strings.TrimSpace(cmp.Or(opened, "opened") + " " + widget.AgoLong(now, q.Opened))
	}
	if !q.Updated.IsZero() {
		updated = "updated " + widget.AgoLong(now, q.Updated)
	}
	add(opened, updated)
	if q.Head != "" && q.Base != "" {
		add(q.Head + " → " + q.Base)
	}
	size := ""
	if q.Adds+q.Dels > 0 {
		size = fmt.Sprintf("+%d −%d", q.Adds, q.Dels)
	}
	files := ""
	if q.Files > 0 {
		files = kit.Counted(q.Files, "file")
	}
	add(size, files)
	var asked, reviewed []string
	for _, rv := range prui.MergeReviewers(q.Reviewers) {
		switch rv.State {
		case "requested":
			asked = append(asked, rv.Login)
		case registry.ReviewReRequested:
			asked = append(asked, rv.Login+" (again)")
		case "changes":
			reviewed = append(reviewed, rv.Login+" changes requested")
		case "":
		default:
			reviewed = append(reviewed, rv.Login+" "+rv.State)
		}
	}
	if len(asked) > 0 {
		add("Asked: " + strings.Join(asked, ", "))
	} else if q.Via != "" {
		add("Asked: " + q.Via)
	}
	if len(reviewed) > 0 {
		add("Reviewed: " + strings.Join(reviewed, ", "))
	}
	return out
}
