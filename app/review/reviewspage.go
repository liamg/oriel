package review

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// The Reviews page: the pull requests waiting on your review from every source, or one, on
// the left, grouped by what you owe; the selected one on the right with Start or Continue review and Open.

// ReviewsKeyMode is the Reviews page's keymap mode.
const ReviewsKeyMode = "reviews"

const reviewsHelp = "agent/reviews"

// The page's scopes: every request, those asked of you, those asked of a team of yours.
var reviewScopes = []string{"all", "me", "teams"}

// reviewsPage is the page's state.
type reviewsPage struct {
	open        bool
	source      string // the one source it lists; every source's when ""
	filter      string
	focusFilter bool
	scope       int    // into reviewScopes
	sel         string // the selected request's URL
	err         string
	list        widget.SplitState // the list's width, dragged by its right edge
}

// The list's width: as it opens, the narrowest its rows read in, and the least it leaves the detail.
const (
	listDefault   = 440
	listMin       = 340
	detailLeastAt = 420
)

// reviewDetail is a request as its source's lookup told it.
type reviewDetail struct {
	d          registry.ReviewDetail
	err        string
	done       bool
	since      time.Time
	read       time.Time // when the source last answered
	refreshing bool      // asked again, keeping d until the answer
	again      bool      // asked again while a read was under way: it's read once more after
	stale      bool      // to be asked again when next shown
	asked      time.Time // when the page last asked, by its clock
}

func (x *Reviews) openPage(source string) error {
	source = strings.TrimSpace(source)
	if source != "" && !slices.ContainsFunc(kit.SitesOf(x.h).ReviewSources(), func(s registry.HostSite) bool { return s.Name == source }) {
		return fmt.Errorf("no review source %q", source)
	}
	x.page = reviewsPage{open: true, source: source, scope: x.page.scope}
	for _, s := range x.pageSources() {
		x.fresh(s, reviewsSoon)
	}
	return nil
}

func (x *Reviews) setScope(scope string) error {
	if scope == "" {
		x.page.scope = (x.page.scope + 1) % len(reviewScopes)
		return nil
	}
	i := slices.Index(reviewScopes, scope)
	if i < 0 {
		return fmt.Errorf("the Reviews page's scope is all, me or teams, not %q", scope)
	}
	x.page.scope = i
	return nil
}

// pageSources are the sources the page lists.
func (x *Reviews) pageSources() []registry.HostSite {
	return slices.DeleteFunc(kit.SitesOf(x.h).ReviewSources(), func(s registry.HostSite) bool {
		return s.Requested == nil || x.page.source != "" && s.Name != x.page.source
	})
}

// pageTitle is "GitHub reviews" for one source, else "Reviews".
func (x *Reviews) pageTitle() string {
	if ss := x.pageSources(); x.page.source != "" && len(ss) == 1 {
		return ss[0].Label() + " reviews"
	}
	return "Reviews"
}

// inScope says whether a request is in the page's scope.
func (x *Reviews) inScope(r registry.ReviewRequest) bool {
	switch reviewScopes[x.page.scope] {
	case "me":
		return r.Via == ""
	case "teams":
		return r.Via != ""
	}
	return true
}

// pageRows are the page's rows: rows() narrowed to its source, then to the scope, then to the filter.
func (x *Reviews) pageRows() []reviewRow {
	q := strings.ToLower(strings.TrimSpace(x.page.filter))
	return slices.DeleteFunc(x.rows(), func(r reviewRow) bool {
		if x.page.source != "" && r.src.Name != x.page.source {
			return true
		}
		if r.req == nil {
			return false
		}
		if !x.inScope(*r.req) {
			return true
		}
		return q != "" && !strings.Contains(strings.ToLower(r.req.Title+" "+r.req.Key()+" "+r.req.Author), q)
	})
}

// detail is the selected request in full, asked of its source off the loop: once, then again in the background,
// keeping what it said, once that's stale or older than reviewsSoon.
func (x *Reviews) detail(s registry.HostSite, url string) *reviewDetail {
	x.mu.Lock()
	defer x.mu.Unlock()
	d := x.details[url]
	switch {
	case d == nil:
		d = &reviewDetail{since: kit.WindowNow(x.h), asked: x.now()}
		x.details[url] = d
		if s.Lookup == nil {
			d.done = true
			return d
		}
	case s.Lookup != nil && d.done && !d.refreshing && (d.stale || x.now().Sub(d.asked) >= reviewsSoon):
		d.refreshing, d.stale, d.asked = true, false, x.now()
	default:
		return d
	}
	plugin.Go(x.h, "reviews: "+url, func() {
		ctx, cancel := context.WithTimeout(x.ctx, 2*time.Minute)
		defer cancel()
		got, err := registry.LookupReview(ctx, s, url)
		x.mu.Lock()
		switch {
		case !d.done:
			d.d, d.done, d.read = got, true, time.Now()
			if err != nil {
				d.err = err.Error()
			}
		case err == nil:
			d.d, d.err, d.read = got, "", time.Now()
		}
		d.refreshing = false
		x.mu.Unlock()
		x.h.Redraw()
	})
	return d
}

// selectedRow is the row whose request is selected, else the first request's.
func (x *Reviews) selectedRow(rows []reviewRow) (reviewRow, bool) {
	first := -1
	for i, r := range rows {
		if r.req == nil {
			continue
		}
		if r.req.URL == x.page.sel {
			return r, true
		}
		if first < 0 {
			first = i
		}
	}
	if first < 0 {
		return reviewRow{}, false
	}
	return rows[first], true
}

// act starts or continues the row's review, closing the page as the launcher or session comes up.
func (x *Reviews) act(r reviewRow) {
	if err := x.start(context.Background(), r); err != nil {
		x.page.err = err.Error()
		return
	}
	if r.req != nil {
		x.page.open = false
	}
}

// browse opens the selected request in the browser.
func (x *Reviews) browse(r reviewRow) {
	if r.req == nil || r.req.URL == "" {
		return
	}
	if err := x.h.Run(context.Background(), "system.open", registry.Args{"url": r.req.URL}); err != nil {
		x.page.err = err.Error()
	}
}

// pageCommand runs one of the page's keys on the selected request.
func (x *Reviews) pageCommand(what string) error {
	r, ok := x.selectedRow(x.pageRows())
	if !x.page.open || !ok {
		return nil
	}
	switch what {
	case "start":
		x.act(r)
	case "browse":
		x.browse(r)
	}
	return nil
}

func (x *Reviews) pageView(c *ui.Ctx) {
	if !x.page.open || c.Theme().IsProbe() {
		return
	}
	rows := x.pageRows()
	widget.Page(c, "reviews", &x.page.open, widget.PageOpts{KeyMode: ReviewsKeyMode, Label: x.pageTitle() + " page", Help: reviewsHelp},
		func(c *ui.Ctx) {
			th := c.Theme()
			x.pageHeader(c, rows)
			c.Row("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
				split := widget.SplitterOpts{Default: listDefault, Min: listMin, Max: c.Size().X - detailLeastAt, Label: "Resize the list",
					Help: reviewsHelp + "#list"}
				x.pageList(c, rows, x.page.list.Width(split))
				widget.Splitter(c, "divider", &x.page.list, split)
				c.Column("detail", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true, Pad: ui.Insets{L: 18, R: 18, T: 14, B: 18}, Gap: 12,
					Fill: th.C(theme.BgBase)}, func(c *ui.Ctx) {
					if r, ok := x.selectedRow(rows); ok {
						x.pageDetail(c, r)
					}
				})
			})
		})
}

func (x *Reviews) pageHeader(c *ui.Ctx, rows []reviewRow) {
	th := c.Theme()
	c.Row("top", ui.Style{W: ui.Flex(1), H: ui.Px(42), Pad: ui.PadXY(18, 0), Gap: 12, Cross: ui.Center, Fill: th.C(kit.TStatsHead)}, func(c *ui.Ctx) {
		c.Text("h", strings.ToUpper(x.pageTitle()), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 14, Weight: typeset.SemiBold, Tracking: .04},
			Color: th.C(theme.Fg)})
		waiting, going := 0, 0
		for _, r := range rows {
			switch {
			case r.req == nil:
			case r.waiting():
				waiting++
			default:
				going++
			}
		}
		meta := fmt.Sprintf("%d waiting on you · %d in progress", waiting, going)
		var at time.Time
		for _, s := range x.pageSources() {
			if t := x.listAt(s.Key()); !t.IsZero() && (at.IsZero() || t.Before(at)) {
				at = t
			}
		}
		parts := []widget.AgoPart{{Text: meta}}
		if !at.IsZero() {
			parts = append(parts, widget.AgoPart{Text: "updated ", At: at, Long: true})
		}
		widget.AgoLine(c, "m", widget.AgoLineOpts{Text: kit.MonoFont(11, th.C(theme.FgFaint))}, parts...)
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if x.page.err != "" {
			c.Text("err", x.page.err, kit.MonoFont(11, th.C(theme.Error)))
		}
		counts := make([]string, len(reviewScopes))
		for _, r := range x.rows() {
			if r.req == nil || x.page.source != "" && r.src.Name != x.page.source {
				continue
			}
			counts[0] = fmt.Sprint(atoiOr(counts[0]) + 1)
			if r.req.Via == "" {
				counts[1] = fmt.Sprint(atoiOr(counts[1]) + 1)
			} else {
				counts[2] = fmt.Sprint(atoiOr(counts[2]) + 1)
			}
		}
		widget.Segmented(c, "scope", []string{"all", "to me", "my teams"}, &x.page.scope, widget.SegmentedOpts{Label: "Whose asks", Badges: counts,
			Help: reviewsHelp + "#page"})
		if widget.Button(c, "refresh", "Refresh", widget.ButtonOpts{Key: "r", Busy: x.pageState().Phase == widget.Pending, BusyLabel: "Refreshing",
			Help: reviewsHelp + "#page"}) {
			x.refreshAll()
		}
		if widget.Button(c, "close", "✕", widget.ButtonOpts{Key: "esc", Help: reviewsHelp + "#page"}) {
			x.page.open = false
		}
	})
	c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
}

func atoiOr(s string) int {
	n := 0
	fmt.Sscan(s, &n)
	return n
}

// pageList is the requests, filtered, under Waiting on you and In progress, then what a source needs first.
func (x *Reviews) pageList(c *ui.Ctx, rows []reviewRow, width float32) {
	th := c.Theme()
	c.Column("list", ui.Style{W: ui.Px(width), H: ui.Flex(1), Fill: th.C(theme.BgSurface)}, func(c *ui.Ctx) {
		c.Row("filter", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 12, R: 12, T: 10, B: 6}}, func(c *ui.Ctx) {
			if x.page.focusFilter {
				c.RequestFocus(c.Next("q"))
				x.page.focusFilter = false
			}
			if r := widget.Input(c, "q", &x.page.filter, widget.InputOpts{Label: "Filter reviews", Placeholder: "⌕ filter by title, repo, author",
				Help: reviewsHelp + "#list"}); r.Cancelled {
				x.page.filter = ""
			}
		})
		c.Column("rows", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true}, func(c *ui.Ctx) { x.pageRowsView(c, rows) })
	})
}

// pageRowsView is the list's rows: skeleton rows while the code hosts first answer, then the requests, or why
// there are none above what a source needs first; a refresh bar runs along the top while they're asked again.
func (x *Reviews) pageRowsView(c *ui.Ctx, rows []reviewRow) {
	st := x.pageState()
	none := !slices.ContainsFunc(rows, func(r reviewRow) bool { return r.req != nil })
	draw := func(c *ui.Ctx) {
		if len(rows) == 0 {
			return
		}
		sel := 0
		if r, ok := x.selectedRow(rows); ok {
			sel = slices.IndexFunc(rows, func(o reviewRow) bool { return o.req == r.req })
		}
		many := len(x.pageSources()) > 1
		res := widget.Rows(c, "reviews", len(rows), &sel, widget.RowsOpts{Label: "Reviews", KeyMode: ReviewsKeyMode, Help: reviewsHelp + "#list",
			ClickSelects: true, Static: func(i int) bool { return rows[i].req == nil }, RowLabel: func(i int) string { return reviewRowLabel(rows[i]) }},
			func(c *ui.Ctx, i int, _ bool) {
				c.Column("item", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
					if h := groupHead(rows, i); h != "" {
						x.groupView(c, rows, h)
					}
					x.reviewRowView(c, rows[i], many)
				})
			})
		if sel >= 0 && sel < len(rows) && rows[sel].req != nil && (res.Changed || x.page.sel == "") {
			x.page.sel = rows[sel].req.URL
		}
		if res.Activated >= 0 {
			x.act(rows[res.Activated])
		}
	}
	pad := ui.Insets{}
	if none || st.Loading() {
		pad = ui.PadXY(14, 12)
	}
	c.Column("load", ui.Style{W: ui.Flex(1), Pad: pad}, func(c *ui.Ctx) {
		widget.Async(c, "load", st, widget.AsyncOpts{Label: "Asking " + x.sourceNames() + " for your reviews", Empty: x.emptyText(), IsEmpty: none,
			Width: 400, Help: reviewsHelp + "#list", Loading: func(c *ui.Ctx) {
				widget.Skeleton(c, "rows", widget.SkeletonOpts{Rows: 5, RowH: 14, Gap: 16, Widths: []float32{.9, .6, .8}, Label: "Reviews",
					Help: reviewsHelp + "#list"})
			}}, draw)
	})
	if none && !st.Loading() {
		draw(c)
	}
}

// pageState is the page's sources' lists read together: loading while none has answered yet, refreshing while any
// is asked again or yet to answer once another has, so the rows that are in show (as the header's count and the
// detail of the one selected do). A source that failed is a row of its own, with Retry.
func (x *Reviews) pageState() widget.LoadState {
	out := widget.LoadState{Phase: widget.Ready}
	answered := false
	for _, s := range x.pageSources() {
		if x.needsKey(s) || s.Requested == nil {
			continue
		}
		st := x.listState(s.Key())
		answered = answered || st.Phase == widget.Ready || st.Phase == widget.Failed || st.Stale
		switch {
		case st.Phase == widget.Idle || st.Loading():
			if out.Phase != widget.Pending || out.Stale || st.Since.Before(out.Since) {
				out = widget.LoadState{Phase: widget.Pending, Since: st.Since}
			}
		case st.Phase == widget.Pending && out.Phase == widget.Ready:
			out = widget.LoadState{Phase: widget.Pending, Since: st.Since, Stale: true}
		}
	}
	if out.Phase == widget.Pending && answered {
		out.Stale = true
	}
	return out
}

// sourceNames are the page's code hosts by name: "GitHub or GitLab".
func (x *Reviews) sourceNames() string {
	var names []string
	for _, s := range x.pageSources() {
		names = append(names, s.Label())
	}
	return cmp.Or(strings.Join(names, " or "), "any code host")
}

// groupHead is the heading above row i, when it starts a group: "waiting" or "progress".
func groupHead(rows []reviewRow, i int) string {
	r := rows[i]
	if r.req == nil {
		return ""
	}
	g := "progress"
	if r.waiting() {
		g = "waiting"
	}
	if i > 0 && rows[i-1].req != nil && rows[i-1].waiting() == r.waiting() {
		return ""
	}
	return g
}

func (x *Reviews) groupView(c *ui.Ctx, rows []reviewRow, g string) {
	th := c.Theme()
	n := 0
	for _, r := range rows {
		if r.req != nil && r.waiting() == (g == "waiting") {
			n++
		}
	}
	title, aside := "WAITING ON YOU", "oldest ask first"
	if g == "progress" {
		title, aside = "IN PROGRESS", "a session of yours, or their move"
	}
	c.Row("group", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 14, R: 14, T: 10, B: 4}, Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Text("t", fmt.Sprintf("%s  %d", title, n), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9.5, Tracking: .12}, Color: th.C(theme.FgFaint)})
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		c.Text("a", aside, kit.MonoFont(9.5, th.C(theme.FgFaint)))
	})
}

// emptyText is what the list says once the code hosts have answered with nothing.
func (x *Reviews) emptyText() string {
	if strings.TrimSpace(x.page.filter) != "" {
		return "No pull request matches."
	}
	return "Nothing waiting on your review in " + x.sourceNames() + "."
}

// reviewRowLabel is a row's accessible name.
func reviewRowLabel(r reviewRow) string {
	switch r.action {
	case "key":
		return r.src.Label() + " needs a token to list your reviews"
	case "refresh":
		return r.src.Label() + ": " + r.err
	}
	return r.req.Key() + " " + r.req.Title
}

// ciMark is a request's checks as a glyph and its tone.
func ciMark(ci string) (string, string) {
	switch ci {
	case "ok":
		return "✓ ci", "ok"
	case "failed":
		return "✕ ci", "error"
	case "running":
		return "◌ ci", "status.working"
	}
	return "", ""
}

func (x *Reviews) reviewRowView(c *ui.Ctx, r reviewRow, many bool) {
	th := c.Theme()
	on := r.req != nil && r.req.URL == x.page.sel
	fill, edge := render.Color{}, render.Color{}
	if on {
		fill, edge = th.C(kit.TStatsRowOn), th.C(theme.Accent)
	}
	subject := ""
	if r.req != nil {
		subject = requestSubject + r.req.URL
	}
	rs := ui.Style{W: ui.Flex(1), Fill: fill, Subject: subject}
	if r.req != nil {
		rs.Tooltip, rs.TooltipView = reviewRowLabel(r), func(c *ui.Ctx) { x.rowTip(c, r, many) }
	}
	c.Row("row", rs, func(c *ui.Ctx) {
		c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Flex(1), Fill: edge}, nil)
		if r.req == nil {
			c.Row("body", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 12, R: 14, T: 9, B: 9}, Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				tone, button := th.C(theme.Accent), "Set token"
				if r.action == "refresh" {
					tone, button = th.C(theme.Warn), "Retry"
				}
				if r.src.Icon != "" {
					kit.Icon(c, x.h, "src", r.src.Icon, 12, tone)
				}
				c.Box("t", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("t", reviewRowLabel(r), kit.UIFont(12, tone)) })
				if widget.Button(c, "act", button, widget.ButtonOpts{Help: reviewsHelp + "#list"}) {
					x.act(r)
				}
			})
			return
		}
		q := r.req
		c.Row("body", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 12, R: 14, T: 9, B: 9}, Gap: 10}, func(c *ui.Ctx) {
			c.Box("av", ui.Style{W: ui.Px(24), H: ui.Px(24), Radius: 12, Fill: th.C(kit.TAvatarGitHub), Main: ui.Center, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("i", cmp.Or(kit.Initials(strings.NewReplacer("-", " ", "_", " ", ".", " ").Replace(q.Author)), "?"),
					ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 8.5, Weight: typeset.Bold}, Color: th.C(theme.AccentInk)})
			})
			c.Column("text", ui.Style{W: ui.Flex(1), Gap: 5, Clip: true}, func(c *ui.Ctx) {
				c.Row("l1", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
					c.Box("t", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("t", q.Title, kit.UIFont(13, th.C(theme.Fg))) })
					if !q.Asked.IsZero() {
						widget.Ago(c, "age", q.Asked, widget.AgoOpts{Text: kit.MonoFont(10, th.C(theme.FgFaint))})
					}
				})
				c.Row("l2", ui.Style{W: ui.Flex(1), Gap: 7, Cross: ui.Center}, func(c *ui.Ctx) {
					widget.Chip(c, "st", stateLabel(r), kit.TokenColour(th, stateTone(r), th.C(theme.Accent)), reviewsHelp+"#list")
					c.Text("n", reviewNote(*q), kit.MonoFont(10, th.C(theme.FgFaint)))
					if mark, tone := ciMark(q.CI); mark != "" {
						c.Text("ci", mark, kit.MonoFont(10, kit.TokenColour(th, tone, th.C(theme.FgFaint))))
					}
					if q.Via != "" {
						widget.Chip(c, "via", q.Via, th.C(theme.FgMuted), reviewsHelp+"#list")
					}
					if many {
						widget.Chip(c, "src", r.src.Label(), th.C(kit.TAvatarGitHub), reviewsHelp+"#list")
					}
					if r.se != nil {
						c.Text("se", "Continue · "+r.se.Name(), kit.MonoFont(10, th.C(theme.Accent)))
					}
				})
			})
		})
	})
}

// checkout is the registered repo a request would be reviewed in: its name and path, or "".
func (x *Reviews) checkout(r registry.ReviewRequest) (string, string) {
	if r.Repo == "" {
		return "", ""
	}
	want := strings.ToLower(r.Repo)
	for _, rp := range x.h.State().Repos {
		if strings.Contains(strings.ToLower(rp.Remote), want) {
			return filepath.Base(rp.Path), kit.TildePath(rp.Path)
		}
	}
	return "", ""
}

// pageDetail is the selected request: the list's fields at once, then its source's detail, and what to do with it.
func (x *Reviews) pageDetail(c *ui.Ctx, r reviewRow) {
	th := c.Theme()
	q := r.req
	d := x.detail(r.src, q.URL)
	x.mu.Lock()
	full, failed, done, read := d.d, d.err, d.done, d.read
	x.mu.Unlock()
	help := reviewsHelp + "#pr"
	c.Row("head", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Box("title", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
			widget.Wrap(c, "t", q.Title, 560, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 17, Weight: typeset.SemiBold},
				Color: th.C(theme.Fg)})
		})
		if q.URL != "" && widget.Button(c, "open", "Open in "+r.src.Label(), widget.ButtonOpts{Ghost: true, Key: "o", Help: help}) {
			x.browse(r)
		}
		if r.se == nil && done && full.Clone != "" {
			if name, _ := x.checkout(*q); name == "" && widget.Button(c, "add", "Add repo…", widget.ButtonOpts{Ghost: true, Help: help}) {
				if err := x.h.Run(context.Background(), "repos.new", registry.Args{"mode": "clone", "url": full.Clone}); err != nil {
					x.page.err = err.Error()
				}
			}
		}
		label := "Start review…"
		if r.se != nil {
			label = "Continue review…"
		}
		if widget.Button(c, "start", label, widget.ButtonOpts{Primary: true, Key: "↵", Disabled: r.se == nil && r.src.Checkout == nil, Help: help}) {
			x.act(r)
		}
	})
	c.Row("meta", ui.Style{W: ui.Flex(1), Gap: 7, Cross: ui.Center}, func(c *ui.Ctx) {
		widget.Chip(c, "st", stateLabel(r), kit.TokenColour(th, stateTone(r), th.C(theme.Accent)), help)
		if done && full.State != "" {
			widget.Chip(c, "pr", full.State, kit.PRStateTone(c, full.State), help)
		}
		parts := []widget.AgoPart{{Text: q.Key()}}
		if q.Author != "" {
			wants := q.Author
			if done && full.Head != "" {
				wants += " wants to merge " + full.Head + " → " + cmp.Or(full.Base, "?")
			}
			parts = append(parts, widget.AgoPart{Text: wants})
		}
		if !q.Asked.IsZero() {
			asked := widget.AgoPart{Text: "asked you ", At: q.Asked, Long: true, Suffix: ", directly"}
			if q.Via != "" {
				asked.Text, asked.Suffix = "asked "+q.Via+" ", ""
			}
			parts = append(parts, asked)
		}
		widget.AgoLine(c, "m", widget.AgoLineOpts{Text: kit.MonoFont(11, th.C(theme.FgMuted))}, parts...)
	})
	fields := [][2]string{{"size", fmt.Sprintf("%s  +%d −%d", kit.Counted(q.Files, "file"), q.Adds, q.Dels)}}
	if name, path := x.checkout(*q); name != "" {
		fields = append(fields, [2]string{"checkout", name + "  " + path})
	} else {
		fields = append(fields, [2]string{"checkout", "no checkout of " + q.Repo + " is registered"})
	}
	if r.se != nil {
		fields = append(fields, [2]string{"session", r.se.Name()})
	}
	c.Row("cards", ui.Style{W: ui.Flex(1), Gap: 12}, func(c *ui.Ctx) {
		prStatusBoxes(c, prStatus{full: full, done: done, read: read, host: r.src.Label(), fields: fields})
	})
	switch {
	case !done:
		widget.Busy(c, "asking", widget.LoadState{Phase: widget.Pending, Since: d.since}, widget.BusyOpts{Label: "Asking " + r.src.Label() + " for the rest",
			Size: 11, Help: help})
	case failed != "":
		widget.Wrap(c, "err", failed, 640, kit.MonoFont(11, th.C(theme.Error)))
	default:
		prDescription(c, x.h, full, 640)
		prActivity(c, x.h, full.Events, 640)
		if len(full.Files) > 0 {
			reviewCard(c, "files", fmt.Sprintf("FILES  %d", len(full.Files)), ui.Flex(1), func(c *ui.Ctx) {
				for i, f := range full.Files {
					c.Row(i, ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
						c.Box("p", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("t", f.Path, kit.MonoFont(11, th.C(theme.Fg))) })
						c.Text("a", fmt.Sprintf("+%d", f.Adds), kit.MonoFont(11, th.C(theme.OK)))
						c.Text("d", fmt.Sprintf("−%d", f.Dels), kit.MonoFont(11, th.C(theme.Error)))
					})
				}
			})
		}
	}
}

// reviewCard is a titled box of the detail.
func reviewCard(c *ui.Ctx, key, title string, w ui.Size, body func(c *ui.Ctx)) {
	th := c.Theme()
	c.Column(key, ui.Style{W: w, Pad: ui.PadXY(12, 10), Gap: 8, Stroke: th.C(kit.TReviewCard), Border: 1, Radius: 6}, func(c *ui.Ctx) {
		c.Text("h", title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9.5, Tracking: .12}, Color: th.C(theme.FgFaint)})
		body(c)
	})
}
