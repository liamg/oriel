package app

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// The Reviews page (ui.md, "46 · Reviews"): the pull requests waiting on your review from every source, or one, on
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
}

// reviewDetail is a request as its source's lookup told it.
type reviewDetail struct {
	d          registry.ReviewDetail
	err        string
	done       bool
	since      time.Time
	refreshing bool // asked again, keeping d until the answer
}

func (x *reviewsPlugin) openPage(source string) error {
	source = strings.TrimSpace(source)
	if source != "" && !slices.ContainsFunc(x.sh.sites.reviewSources(), func(s registry.HostSite) bool { return s.Name == source }) {
		return fmt.Errorf("no review source %q", source)
	}
	x.page = reviewsPage{open: true, source: source, scope: x.page.scope}
	for _, s := range x.pageSources() {
		x.fresh(s, reviewsSoon)
	}
	return nil
}

func (x *reviewsPlugin) setScope(scope string) error {
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
func (x *reviewsPlugin) pageSources() []registry.HostSite {
	return slices.DeleteFunc(x.sh.sites.reviewSources(), func(s registry.HostSite) bool {
		return s.Requested == nil || x.page.source != "" && s.Name != x.page.source
	})
}

// pageTitle is "GitHub reviews" for one source, else "Reviews".
func (x *reviewsPlugin) pageTitle() string {
	if ss := x.pageSources(); x.page.source != "" && len(ss) == 1 {
		return ss[0].Label() + " reviews"
	}
	return "Reviews"
}

// inScope says whether a request is in the page's scope.
func (x *reviewsPlugin) inScope(r registry.ReviewRequest) bool {
	switch reviewScopes[x.page.scope] {
	case "me":
		return r.Via == ""
	case "teams":
		return r.Via != ""
	}
	return true
}

// pageRows are the page's rows: rows() narrowed to its source, then to the scope, then to the filter.
func (x *reviewsPlugin) pageRows() []reviewRow {
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

// detail is the selected request in full, asked of its source once, off the loop.
func (x *reviewsPlugin) detail(s registry.HostSite, url string) *reviewDetail {
	x.mu.Lock()
	defer x.mu.Unlock()
	if d := x.details[url]; d != nil {
		return d
	}
	d := &reviewDetail{since: windowNow(x.h)}
	x.details[url] = d
	if s.Lookup == nil {
		d.done = true
		return d
	}
	go func() {
		ctx, cancel := context.WithTimeout(x.ctx, 2*time.Minute)
		defer cancel()
		got, err := registry.LookupReview(ctx, s, url)
		x.mu.Lock()
		d.d, d.done = got, true
		if err != nil {
			d.err = err.Error()
		}
		x.mu.Unlock()
		x.h.Redraw()
	}()
	return d
}

// selectedRow is the row whose request is selected, else the first request's.
func (x *reviewsPlugin) selectedRow(rows []reviewRow) (reviewRow, bool) {
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
func (x *reviewsPlugin) act(r reviewRow) {
	if err := x.start(context.Background(), r); err != nil {
		x.page.err = err.Error()
		return
	}
	if r.req != nil {
		x.page.open = false
	}
}

// browse opens the selected request in the browser.
func (x *reviewsPlugin) browse(r reviewRow) {
	if r.req == nil || r.req.URL == "" {
		return
	}
	if err := x.h.Run(context.Background(), "system.open", registry.Args{"url": r.req.URL}); err != nil {
		x.page.err = err.Error()
	}
}

// pageCommand runs one of the page's keys on the selected request.
func (x *reviewsPlugin) pageCommand(what string) error {
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

func (x *reviewsPlugin) pageView(c *ui.Ctx) {
	if !x.page.open || c.Theme().IsProbe() {
		return
	}
	rows := x.pageRows()
	widget.Page(c, "reviews", &x.page.open, widget.PageOpts{Over: mainArea(), KeyMode: ReviewsKeyMode, Label: x.pageTitle() + " page", Help: reviewsHelp},
		func(c *ui.Ctx) {
			th := c.Theme()
			x.pageHeader(c, rows)
			c.Row("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
				x.pageList(c, rows)
				c.Box("divider", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
				c.Column("detail", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true, Pad: ui.Insets{L: 18, R: 18, T: 14, B: 18}, Gap: 12,
					Fill: th.C(theme.BgBase)}, func(c *ui.Ctx) {
					if r, ok := x.selectedRow(rows); ok {
						x.pageDetail(c, r)
					}
				})
			})
		})
}

func (x *reviewsPlugin) pageHeader(c *ui.Ctx, rows []reviewRow) {
	th := c.Theme()
	c.Row("top", ui.Style{W: ui.Flex(1), H: ui.Px(42), Pad: ui.PadXY(18, 0), Gap: 12, Cross: ui.Center, Fill: th.C(tStatsHead)}, func(c *ui.Ctx) {
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
		if !at.IsZero() {
			if when := ago(c.Now(), at); when == "now" {
				meta += " · updated just now"
			} else {
				meta += " · updated " + when + " ago"
			}
		}
		c.Text("m", meta, monoFont(11, th.C(theme.FgFaint)))
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if x.page.err != "" {
			c.Text("err", truncate(x.page.err, 90), monoFont(11, th.C(theme.Error)))
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
func (x *reviewsPlugin) pageList(c *ui.Ctx, rows []reviewRow) {
	th := c.Theme()
	c.Column("list", ui.Style{W: ui.Px(440), H: ui.Flex(1), Fill: th.C(theme.BgSurface)}, func(c *ui.Ctx) {
		c.Row("filter", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 12, R: 12, T: 10, B: 6}}, func(c *ui.Ctx) {
			if x.page.focusFilter {
				id := c.Next("q")
				c.Focus(id)
				x.page.focusFilter = !c.Interaction(id).Focused
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
func (x *reviewsPlugin) pageRowsView(c *ui.Ctx, rows []reviewRow) {
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

// pageState is the page's sources' lists read together: loading while any hasn't answered yet, refreshing while
// any is asked again. A source that failed is a row of its own, with Retry.
func (x *reviewsPlugin) pageState() widget.LoadState {
	out := widget.LoadState{Phase: widget.Ready}
	for _, s := range x.pageSources() {
		if x.needsKey(s) || s.Requested == nil {
			continue
		}
		st := x.listState(s.Key())
		switch {
		case st.Phase == widget.Idle || st.Loading():
			if out.Phase != widget.Pending || out.Stale || st.Since.Before(out.Since) {
				out = widget.LoadState{Phase: widget.Pending, Since: st.Since}
			}
		case st.Phase == widget.Pending && out.Phase == widget.Ready:
			out = widget.LoadState{Phase: widget.Pending, Since: st.Since, Stale: true}
		}
	}
	return out
}

// sourceNames are the page's code hosts by name: "GitHub or GitLab".
func (x *reviewsPlugin) sourceNames() string {
	var names []string
	for _, s := range x.pageSources() {
		names = append(names, s.Label())
	}
	return cmpOr(strings.Join(names, " or "), "any code host")
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

func (x *reviewsPlugin) groupView(c *ui.Ctx, rows []reviewRow, g string) {
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
		c.Text("a", aside, monoFont(9.5, th.C(theme.FgFaint)))
	})
}

// emptyText is what the list says once the code hosts have answered with nothing.
func (x *reviewsPlugin) emptyText() string {
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

func (x *reviewsPlugin) reviewRowView(c *ui.Ctx, r reviewRow, many bool) {
	th := c.Theme()
	on := r.req != nil && r.req.URL == x.page.sel
	fill, edge := render.Color{}, render.Color{}
	if on {
		fill, edge = th.C(tStatsRowOn), th.C(theme.Accent)
	}
	c.Row("row", ui.Style{W: ui.Flex(1), Fill: fill}, func(c *ui.Ctx) {
		c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Flex(1), Fill: edge}, nil)
		if r.req == nil {
			c.Row("body", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 12, R: 14, T: 9, B: 9}, Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				tone, button := th.C(theme.Accent), "Set token"
				if r.action == "refresh" {
					tone, button = th.C(theme.Warn), "Retry"
				}
				if r.src.Icon != "" {
					icon(c, "src", r.src.Icon, 12, tone)
				}
				c.Box("t", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("t", reviewRowLabel(r), uiFont(12, tone)) })
				if widget.Button(c, "act", button, widget.ButtonOpts{Help: reviewsHelp + "#list"}) {
					x.act(r)
				}
			})
			return
		}
		q := r.req
		c.Row("body", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 12, R: 14, T: 9, B: 9}, Gap: 10}, func(c *ui.Ctx) {
			c.Box("av", ui.Style{W: ui.Px(24), H: ui.Px(24), Radius: 12, Fill: th.C(tReqAvatarGH), Main: ui.Center, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("i", cmpOr(initials(strings.NewReplacer("-", " ", "_", " ", ".", " ").Replace(q.Author)), "?"),
					ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 8.5, Weight: typeset.Bold}, Color: th.C(theme.AccentInk)})
			})
			c.Column("text", ui.Style{W: ui.Flex(1), Gap: 5, Clip: true}, func(c *ui.Ctx) {
				c.Row("l1", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
					c.Box("t", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("t", q.Title, uiFont(13, th.C(theme.Fg))) })
					if !q.Asked.IsZero() {
						c.Text("age", ago(c.Now(), q.Asked), monoFont(10, th.C(theme.FgFaint)))
					}
				})
				c.Row("l2", ui.Style{W: ui.Flex(1), Gap: 7, Cross: ui.Center}, func(c *ui.Ctx) {
					widget.Chip(c, "st", stateLabel(r), tokenColour(th, stateTone(r), th.C(theme.Accent)), reviewsHelp+"#list")
					c.Text("n", reviewNote(*q), monoFont(10, th.C(theme.FgFaint)))
					if mark, tone := ciMark(q.CI); mark != "" {
						c.Text("ci", mark, monoFont(10, tokenColour(th, tone, th.C(theme.FgFaint))))
					}
					if q.Via != "" {
						widget.Chip(c, "via", q.Via, th.C(theme.FgMuted), reviewsHelp+"#list")
					}
					if many {
						widget.Chip(c, "src", r.src.Label(), th.C(tReqAvatarGH), reviewsHelp+"#list")
					}
					if r.se != nil {
						c.Text("se", "Continue · "+r.se.Name(), monoFont(10, th.C(theme.Accent)))
					}
				})
			})
		})
	})
}

// checkout is the registered repo a request would be reviewed in: its name and path, or "".
func (x *reviewsPlugin) checkout(r registry.ReviewRequest) (string, string) {
	if r.Repo == "" {
		return "", ""
	}
	want := strings.ToLower(r.Repo)
	for _, rp := range x.h.State().Repos {
		if strings.Contains(strings.ToLower(rp.Remote), want) {
			return filepath.Base(rp.Path), tildePath(rp.Path)
		}
	}
	return "", ""
}

// pageDetail is the selected request: the list's fields at once, then its source's detail, and what to do with it.
func (x *reviewsPlugin) pageDetail(c *ui.Ctx, r reviewRow) {
	th := c.Theme()
	q := r.req
	d := x.detail(r.src, q.URL)
	x.mu.Lock()
	full, failed, done := d.d, d.err, d.done
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
		widget.Chip(c, "st", stateLabel(r), tokenColour(th, stateTone(r), th.C(theme.Accent)), help)
		parts := []string{q.Key()}
		if q.Author != "" {
			wants := q.Author
			if done && full.Head != "" {
				wants += " wants to merge " + full.Head + " → " + cmpOr(full.Base, "?")
			}
			parts = append(parts, wants)
		}
		if !q.Asked.IsZero() {
			when := ago(c.Now(), q.Asked)
			asked := "asked you " + when + " ago"
			if when == "now" {
				asked = "asked you just now"
			}
			if q.Via != "" {
				asked = strings.Replace(asked, "you", q.Via, 1)
			} else {
				asked += ", directly"
			}
			parts = append(parts, asked)
		}
		c.Text("m", strings.Join(parts, " · "), monoFont(11, th.C(theme.FgMuted)))
	})
	c.Row("cards", ui.Style{W: ui.Flex(1), Gap: 12}, func(c *ui.Ctx) {
		reviewCard(c, "status", "STATUS", ui.Flex(1), func(c *ui.Ctx) {
			checks := "none"
			if mark, _ := ciMark(q.CI); mark != "" {
				checks = map[string]string{"ok": "passed", "failed": "failed", "running": "running"}[q.CI]
			}
			if done && len(full.Checks) > 0 {
				checks = checksText(full.Checks)
			}
			previewField(c, "checks", checks)
			previewField(c, "size", fmt.Sprintf("%s  +%d −%d", counted(q.Files, "file"), q.Adds, q.Dels))
			if done && full.Mergeable != "" {
				previewField(c, "mergeable", full.Mergeable)
			}
			if done && len(full.Labels) > 0 {
				previewField(c, "labels", strings.Join(full.Labels, " · "))
			}
			if name, path := x.checkout(*q); name != "" {
				previewField(c, "checkout", name+"  "+path)
			} else {
				previewField(c, "checkout", "no checkout of "+q.Repo+" is registered")
			}
			if r.se != nil {
				previewField(c, "session", r.se.Name())
			}
		})
		if done && len(full.Reviewers) > 0 {
			reviewCard(c, "reviewers", "REVIEWERS", ui.Px(260), func(c *ui.Ctx) {
				for i, rv := range full.Reviewers {
					c.Row(i, ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
						c.Box("n", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("t", rv.Login, monoFont(11, th.C(theme.Fg))) })
						widget.Chip(c, "st", rv.State, tokenColour(th, reviewerTone(rv.State), th.C(theme.FgMuted)), help)
					})
				}
			})
		}
	})
	switch {
	case !done:
		widget.Busy(c, "asking", widget.LoadState{Phase: widget.Pending, Since: d.since}, widget.BusyOpts{Label: "Asking " + r.src.Label() + " for the rest",
			Size: 11, Help: help})
	case failed != "":
		widget.Wrap(c, "err", failed, 640, monoFont(11, th.C(theme.Error)))
	default:
		if strings.TrimSpace(full.Body) != "" {
			reviewCard(c, "description", "DESCRIPTION", ui.Flex(1), func(c *ui.Ctx) {
				markdownText(c, "body", full.Body, 640, uiFont(13, th.C(theme.FgMuted)))
			})
		}
		if len(full.Files) > 0 {
			reviewCard(c, "files", fmt.Sprintf("FILES  %d", len(full.Files)), ui.Flex(1), func(c *ui.Ctx) {
				for i, f := range full.Files {
					c.Row(i, ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
						c.Box("p", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("t", f.Path, monoFont(11, th.C(theme.Fg))) })
						c.Text("a", fmt.Sprintf("+%d", f.Adds), monoFont(11, th.C(theme.OK)))
						c.Text("d", fmt.Sprintf("−%d", f.Dels), monoFont(11, th.C(theme.Error)))
					})
				}
			})
		}
	}
}

// reviewCard is a titled box of the detail.
func reviewCard(c *ui.Ctx, key, title string, w ui.Size, body func(c *ui.Ctx)) {
	th := c.Theme()
	c.Column(key, ui.Style{W: w, Pad: ui.PadXY(12, 10), Gap: 8, Stroke: th.C(theme.Line), Border: 1, Radius: 6}, func(c *ui.Ctx) {
		c.Text("h", title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9.5, Tracking: .12}, Color: th.C(theme.FgFaint)})
		body(c)
	})
}

// checksText sums a PR's checks: "14 passed", "2 failed · 12 passed".
func checksText(cs []registry.Check) string {
	n := map[string]int{}
	for _, ch := range cs {
		n[ch.State]++
	}
	var parts []string
	for _, s := range []struct{ state, word string }{{"failed", "failed"}, {"running", "running"}, {"ok", "passed"}, {"skipped", "skipped"}} {
		if n[s.state] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n[s.state], s.word))
		}
	}
	return strings.Join(parts, " · ")
}

func reviewerTone(state string) string {
	switch state {
	case "approved":
		return "ok"
	case "changes":
		return "warn"
	case "requested":
		return "status.permission"
	}
	return "fg.muted"
}
