package app

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// The Repos page (45's panel 1): the repos on the left; the selected one's facts, stats, housekeeping and tags on
// the right, over the main area beside the sidebar.

var (
	tRepoTidy   = theme.Declare("repos.tidy", "amber")
	tRepoMerged = theme.Declare("repos.merged", "ok")
	tRepoGone   = theme.Declare("repos.gone", "violet")
	tRepoFound  = theme.Declare("repos.found", "amber")
)

// repoDetail is what the page has read of one repo.
type repoDetail struct {
	facts     fetched[host.RepoFacts]
	factsBusy bool
	busy      string // the action running: "fetch", "clean"
	note      string // how the last action went

	plan     *host.HousekeepPlan
	planBusy bool
	planErr  string
	ticked   map[string]bool
	prune    bool
	results  map[string]string // a branch's error after a clean up; "" when it went

	tag *tagForm
}

func (x *reposPlugin) detail(id core.RepoID) *repoDetail {
	d := x.details[id]
	if d == nil {
		d = &repoDetail{}
		x.details[id] = d
	}
	return d
}

// readFacts reads a repo's facts again, off the loop.
func (x *reposPlugin) readFacts(id core.RepoID) {
	d, w := x.detail(id), x.sh.cfg.Host
	if d.factsBusy || w == nil {
		return
	}
	d.factsBusy = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		f, err := w.RepoFacts(ctx, id)
		x.h.Do(func() { d.facts, d.factsBusy = fetched[host.RepoFacts]{v: f, at: time.Now(), err: err}, false })
		x.h.Redraw()
	}()
}

// readPlan reads what housekeeping would clean, fetching first if asked.
func (x *reposPlugin) readPlan(id core.RepoID, fetch bool) {
	d, w := x.detail(id), x.sh.cfg.Host
	if d.planBusy || w == nil {
		return
	}
	d.planBusy, d.planErr = true, ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		p, err := w.Housekeeping(ctx, host.HousekeepReq{Repo: id, Fetch: fetch})
		x.h.Do(func() {
			d.planBusy = false
			if err != nil {
				d.planErr = err.Error()
				return
			}
			d.plan, d.ticked, d.prune = &p, map[string]bool{}, len(p.Stale) > 0
			for _, b := range p.Branches {
				d.ticked[b.Name] = b.Session == "" && !b.Dirty
			}
		})
		x.h.Redraw()
	}()
}

// act runs a host call for a repo off the loop, then reads its facts again.
func (x *reposPlugin) act(id core.RepoID, what string, fn func(ctx context.Context, w *host.Host) (string, error)) {
	d, w := x.detail(id), x.sh.cfg.Host
	if d.busy != "" || w == nil {
		return
	}
	d.busy, d.note, x.err = what, "", ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		note, err := fn(ctx, w)
		x.h.Do(func() {
			d.busy, d.note = "", note
			if err != nil {
				x.err = err.Error()
			}
			x.readFacts(id)
		})
		x.h.Redraw()
	}()
}

// shown are the repos the filter lets through: found ones last, else by name.
func (x *reposPlugin) shown() []core.Repo {
	q := strings.ToLower(strings.TrimSpace(x.filter))
	rs := slices.DeleteFunc(slices.Clone(x.repos()), func(r core.Repo) bool {
		return q != "" && !strings.Contains(strings.ToLower(string(r.ID)+" "+r.Path), q)
	})
	slices.SortStableFunc(rs, func(a, b core.Repo) int {
		if fa, fb := isFound(a), isFound(b); fa != fb {
			if fa {
				return 1
			}
			return -1
		}
		return strings.Compare(string(a.ID), string(b.ID))
	})
	return rs
}

func (x *reposPlugin) page(c *ui.Ctx) {
	if !x.open || c.Theme().IsProbe() {
		return
	}
	rows := x.shown()
	if _, ok := x.find(string(x.sel)); !ok && len(rows) > 0 {
		x.sel = rows[0].ID
	}
	widget.Page(c, "repos", &x.open, widget.PageOpts{Over: mainArea(), KeyMode: ReposKeyMode, Label: "Repos page", Help: "agent/repos#page"}, func(c *ui.Ctx) {
		th := c.Theme()
		x.header(c)
		c.Row("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
			x.list(c, rows)
			c.Box("divider", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
			c.Column("detail", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true, Pad: ui.Insets{L: 18, R: 18, T: 14, B: 18}, Gap: 14,
				Fill: th.C(theme.BgBase)}, func(c *ui.Ctx) {
				r, ok := x.find(string(x.sel))
				if !ok {
					x.emptyPage(c)
					return
				}
				x.repoView(c, r)
			})
		})
	})
}

func (x *reposPlugin) header(c *ui.Ctx) {
	th := c.Theme()
	c.Row("top", ui.Style{W: ui.Flex(1), H: ui.Px(42), Pad: ui.PadXY(18, 0), Gap: 12, Cross: ui.Center, Fill: th.C(tStatsHead)}, func(c *ui.Ctx) {
		c.Text("h", "REPOS", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 14, Weight: typeset.SemiBold, Tracking: .04},
			Color: th.C(theme.Fg)})
		tidy := 0
		for _, d := range x.details {
			if d.plan != nil && len(d.plan.Branches)+len(d.plan.Stale) > 0 {
				tidy++
			}
		}
		meta := nonEmpty(x.count())
		if tidy > 0 {
			meta = append(meta, fmt.Sprintf("%d to tidy", tidy))
		}
		c.Text("m", strings.Join(meta, " · "), monoFont(11, th.C(theme.FgFaint)))
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if x.err != "" {
			c.Text("err", truncate(firstLine(x.err), 90), monoFont(11, th.C(theme.Error)))
		}
		if widget.Button(c, "add", "+ Add repo", widget.ButtonOpts{Key: "a", Help: "agent/repos#add"}) {
			x.run("repos.new", nil)
		}
		if widget.Button(c, "scan", "Scan for repos", widget.ButtonOpts{Help: "agent/repos#discovery"}) {
			x.run("repos.scan", nil)
		}
		if widget.Button(c, "close", "Close", widget.ButtonOpts{Key: "esc", Help: "agent/repos#page"}) {
			x.open = false
		}
	})
	c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
}

func (x *reposPlugin) run(id string, a registry.Args) {
	if err := x.h.Run(context.Background(), id, a); err != nil {
		x.err = err.Error()
	}
}

func (x *reposPlugin) list(c *ui.Ctx, rows []core.Repo) {
	th := c.Theme()
	c.Column("list", ui.Style{W: ui.Px(300), H: ui.Flex(1), Fill: th.C(theme.BgSurface)}, func(c *ui.Ctx) {
		c.Row("filter", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 12, R: 12, T: 10, B: 6}}, func(c *ui.Ctx) {
			if r := widget.Input(c, "q", &x.filter, widget.InputOpts{Label: "Filter repos", Placeholder: "⌕ filter repos", Help: "agent/repos#list"}); r.Cancelled {
				x.filter = ""
			}
		})
		c.Column("rows", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true}, func(c *ui.Ctx) {
			if len(rows) == 0 {
				c.Column("none", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(14, 12)}, func(c *ui.Ctx) {
					msg := "No repos yet: + Add repo, or start a session on a checkout."
					if strings.TrimSpace(x.filter) != "" {
						msg = "No repo matches."
					}
					widget.Wrap(c, "t", msg, 270, uiFont(12, th.C(theme.FgFaint)))
				})
				return
			}
			sel := max(0, slices.IndexFunc(rows, func(r core.Repo) bool { return r.ID == x.sel }))
			res := widget.Rows(c, "repos", len(rows), &sel, widget.RowsOpts{Label: "Repos", KeyMode: ReposKeyMode, Help: "agent/repos#list",
				RowLabel: func(i int) string { return string(rows[i].ID) }}, func(c *ui.Ctx, i int, _ bool) {
				x.rowView(c, rows[i], rows[i].ID == x.sel)
			})
			if res.Changed || res.Activated >= 0 {
				if id := rows[sel].ID; id != x.sel {
					x.sel, x.confirm = id, ""
				}
			}
		})
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
		c.Column("foot", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(14, 10), Gap: 3}, func(c *ui.Ctx) {
			note(c, "a", "~/.oriel/repos.json · removing one forgets it")
			note(c, "b", "files are never deleted; removed repos stay removed")
		})
	})
}

func (x *reposPlugin) rowView(c *ui.Ctx, r core.Repo, on bool) {
	th := c.Theme()
	fill, edge := render.Color{}, render.Color{}
	if on {
		fill, edge = th.C(tStatsRowOn), th.C(theme.Accent)
	}
	d := x.details[r.ID]
	tidy := d != nil && d.plan != nil && len(d.plan.Branches)+len(d.plan.Stale) > 0
	c.Row("row", ui.Style{W: ui.Flex(1), Fill: fill}, func(c *ui.Ctx) {
		c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Flex(1), Fill: edge}, nil)
		c.Column("body", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 12, R: 14, T: 8, B: 8}, Gap: 4, Clip: true}, func(c *ui.Ctx) {
			c.Row("l1", ui.Style{W: ui.Flex(1), Gap: 7, Cross: ui.Center}, func(c *ui.Ctx) {
				if tidy {
					c.Box("dot", ui.Style{W: ui.Px(6), H: ui.Px(6), Radius: 3, Fill: th.C(tRepoTidy), Role: ui.RoleText, Label: "to tidy"}, nil)
				}
				c.Text("t", string(r.ID), uiFont(13, th.C(theme.Fg)))
				if isFound(r) {
					widget.Chip(c, "found", "found", th.C(tRepoFound), "agent/repos#discovery")
				}
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				c.Text("b", strings.TrimPrefix(r.BaseBranch, "origin/"), monoFont(10, th.C(theme.FgGhost)))
			})
			line := []string{tildePath(r.Path)}
			switch {
			case r.Used.IsZero():
				line = append(line, "never used")
			case tidy:
				line = append(line, counted(len(d.plan.Branches)+len(d.plan.Stale), "to tidy"))
			}
			c.Text("l2", strings.Join(line, " · "), monoFont(10, th.C(theme.FgFaint)))
		})
	})
}

func (x *reposPlugin) emptyPage(c *ui.Ctx) {
	th := c.Theme()
	c.Column("empty", ui.Style{W: ui.Flex(1), Pad: ui.Pad(24), Gap: 12}, func(c *ui.Ctx) {
		c.Text("t", "No repos yet.", uiFont(14, th.C(theme.Fg)))
		widget.Wrap(c, "d", "Add a checkout, or clone one into a folder you choose. Oriel finds the repos beside the ones you add.", 460,
			uiFont(12, th.C(theme.FgFaint)))
		if widget.Button(c, "add", "+ Add repo", widget.ButtonOpts{Primary: true, Help: "agent/repos#add"}) {
			x.run("repos.new", nil)
		}
	})
}

func (x *reposPlugin) repoView(c *ui.Ctx, r core.Repo) {
	d := x.detail(r.ID)
	if d.facts.at.IsZero() {
		x.readFacts(r.ID)
	}
	if d.plan == nil && !d.planBusy && d.planErr == "" {
		x.readPlan(r.ID, false)
	}
	x.repoHead(c, r, d)
	x.statsCard(c, r, d)
	x.housekeepCard(c, r, d)
	x.tagCard(c, r, d)
}

func (x *reposPlugin) repoHead(c *ui.Ctx, r core.Repo, d *repoDetail) {
	th := c.Theme()
	c.Column("head", ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) {
		c.Row("l1", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("name", string(r.ID), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 18, Weight: typeset.Bold}, Color: th.C(theme.Fg)})
			meta := nonEmpty(tildePath(r.Path), remoteHost(r.Remote))
			if b := strings.TrimPrefix(r.BaseBranch, "origin/"); b != "" {
				meta = append(meta, "default "+b)
			}
			c.Text("meta", strings.Join(meta, " · "), monoFont(11, th.C(theme.FgFaint)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			busy := d.busy != ""
			if r.Remote != "" && widget.Button(c, "fetch", "Fetch", widget.ButtonOpts{Key: "f", Disabled: busy, Help: "agent/repos#detail"}) {
				x.act(r.ID, "fetch", func(ctx context.Context, w *host.Host) (string, error) {
					return "fetched", w.FetchRepo(ctx, r.ID)
				})
				d.plan = nil
			}
			if widget.Button(c, "reveal", "Reveal", widget.ButtonOpts{Help: "agent/repos#detail"}) {
				if err := x.sh.desktop.OpenURL(r.Path); err != nil {
					x.err = err.Error()
				}
			}
			if x.confirm == "remove" {
				if widget.Button(c, "remove", "Sure? Remove", widget.ButtonOpts{Primary: true, Tone: th.C(theme.Error), Help: "agent/repos#detail"}) {
					x.confirm = ""
					x.run("repos.remove", registry.Args{"repo": string(r.ID)})
				}
			} else if widget.Button(c, "remove", "Remove", widget.ButtonOpts{Help: "agent/repos#detail"}) {
				x.confirm = "remove"
			}
			if widget.Button(c, "new", "New session", widget.ButtonOpts{Primary: true, Help: "agent/repos#detail"}) {
				x.run("session.new", registry.Args{"repos": []string{string(r.ID)}})
			}
		})
		f := d.facts.v
		var line []string
		if f.Branch != "" {
			on := "on " + f.Branch
			if t := f.Tracking; t.Upstream != "" {
				if t.Ahead > 0 {
					on += fmt.Sprintf(" ↑%d", t.Ahead)
				}
				if t.Behind > 0 {
					on += fmt.Sprintf(" ↓%d behind %s", t.Behind, t.Upstream)
				}
			}
			line = append(line, on)
		}
		if !f.Last.When.IsZero() {
			line = append(line, "last commit "+agoText(time.Now(), f.Last.When)+" by "+f.Last.Author)
		}
		if !r.Added.IsZero() {
			line = append(line, "added "+r.Added.Local().Format("Jan 2"))
		}
		if !r.Used.IsZero() {
			line = append(line, "used "+agoText(time.Now(), r.Used))
		}
		if d.busy != "" {
			line = append(line, d.busy+"ing…")
		} else if d.note != "" {
			line = append(line, d.note)
		}
		c.Text("l2", strings.Join(line, " · "), monoFont(10.5, th.C(theme.FgMuted)))
	})
}

// card is a section of the detail: a caption and its note, then the body.
func repoCard(c *ui.Ctx, key, title, note string, right func(c *ui.Ctx), body func(c *ui.Ctx)) {
	th := c.Theme()
	c.Column(key, ui.Style{W: ui.Flex(1), Fill: th.C(theme.BgSurface), Stroke: th.C(theme.Line), Border: 1, Radius: 8, Clip: true,
		Role: ui.RoleRegion, Label: title}, func(c *ui.Ctx) {
		c.Row("cap", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(14, 9), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			caption(c, "t", title)
			c.Text("n", note, monoFont(10, th.C(theme.FgFaint)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if right != nil {
				right(c)
			}
		})
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
		c.Column("body", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(14, 12), Gap: 10}, body)
	})
}

func (x *reposPlugin) statsCard(c *ui.Ctx, r core.Repo, d *repoDetail) {
	th := c.Theme()
	repoCard(c, "stats", "Stats", "the overview's panels, cached by HEAD", nil, func(c *ui.Ctx) {
		f := d.facts.v
		if d.facts.err != nil {
			c.Text("err", firstLine(d.facts.err.Error()), monoFont(10.5, th.C(theme.Warn)))
		}
		tag := "tags"
		if f.LatestTag != "" {
			tag = "tags · " + f.LatestTag
		}
		c.Row("facts", ui.Style{W: ui.Flex(1), Gap: 8}, func(c *ui.Ctx) {
			for i, t := range [][2]string{
				{fmt.Sprint(f.Branches), "branches"}, {fmt.Sprint(f.Worktrees), "worktrees"}, {fmt.Sprint(f.Tags), tag},
				{sizeText(f.SizeKiB), "on disk"},
			} {
				if d.facts.at.IsZero() {
					t[0] = "…"
				}
				c.Column(i, ui.Style{W: ui.Flex(1), Pad: ui.PadXY(10, 7), Gap: 2, Stroke: th.C(theme.Line), Border: 1, Radius: 6, Clip: true,
					Role: ui.RoleText, Label: t[0] + " " + t[1]}, func(c *ui.Ctx) {
					c.Text("v", t[0], ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 16, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
					c.Text("l", strings.ToUpper(t[1]), monoFont(9, th.C(theme.FgFaint)))
				})
			}
		})
		if v, ok := x.h.ViewByID(OverviewPanelsView); ok {
			c.Column("panels", ui.Style{W: ui.Flex(1), Stroke: th.C(theme.Line), Border: 1, Radius: 6, Clip: true}, func(c *ui.Ctx) {
				c.Node(v.Build(registry.ViewCtx{Instance: r.Path}))
			})
		}
	})
}

// sizeText is a size in KiB as the stats read it: 48 MB.
func sizeText(kib int64) string {
	switch {
	case kib >= 1<<20:
		return fmt.Sprintf("%.1f GB", float64(kib)/(1<<20))
	case kib >= 1<<10:
		return fmt.Sprintf("%d MB", kib>>10)
	}
	return fmt.Sprintf("%d KB", kib)
}

func (x *reposPlugin) housekeepCard(c *ui.Ctx, r core.Repo, d *repoDetail) {
	th := c.Theme()
	p := d.plan
	summary := "reading…"
	switch {
	case d.planErr != "":
		summary = firstLine(d.planErr)
	case p != nil:
		wts := 0
		for _, b := range p.Branches {
			if b.Worktree != "" {
				wts++
			}
		}
		summary = cmpOr(strings.Join(nonEmpty(plural0(len(p.Branches), "branch", "branches"), plural0(wts, "with a worktree", "with worktrees"),
			plural0(len(p.Stale), "stale worktree", "stale worktrees")), ", "), "nothing to clean")
		if p.Base != "" {
			summary = "into " + p.Base + " · " + summary
		}
	}
	ticked := d.tickedCount()
	right := func(c *ui.Ctx) {
		if r.Remote != "" && widget.Button(c, "fetch", "Fetch and read again", widget.ButtonOpts{Disabled: d.planBusy, Help: "agent/repos#housekeeping"}) {
			x.readPlan(r.ID, true)
		}
		if p == nil || len(p.Branches) == 0 {
			return
		}
		if widget.Button(c, "all", "Select all", widget.ButtonOpts{Help: "agent/repos#housekeeping"}) {
			for _, b := range p.Branches {
				d.ticked[b.Name] = b.Session == ""
			}
		}
	}
	repoCard(c, "housekeeping", "Housekeeping", summary, right, func(c *ui.Ctx) {
		if p == nil {
			return
		}
		for _, b := range slices.Sorted(maps.Keys(d.results)) {
			if slices.ContainsFunc(p.Branches, func(s host.StaleBranch) bool { return s.Name == b }) {
				continue
			}
			if e := d.results[b]; e != "" {
				c.Text("res."+b, "✗ "+b+": "+firstLine(e), monoFont(11, th.C(theme.Error)))
			} else {
				c.Text("res."+b, "✓ deleted "+b, monoFont(11, th.C(theme.OK)))
			}
		}
		if len(p.Branches)+len(p.Stale) == 0 {
			c.Text("none", "Nothing to clean: no merged branches, none whose upstream went, no stale worktrees.", uiFont(12, th.C(theme.FgFaint)))
			return
		}
		col := func(c *ui.Ctx, key string, w ui.Size, s string, ts ui.TextStyle) {
			c.Row(key, ui.Style{W: w, Clip: true}, func(c *ui.Ctx) { c.Text("t", s, ts) })
		}
		head := monoFont(9, th.C(theme.FgFaint))
		c.Row("cols", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Box("check", ui.Style{W: ui.Px(14)}, nil)
			col(c, "b", ui.Flex(2), "BRANCH", head)
			col(c, "w", ui.Px(110), "WHY", head)
			col(c, "t", ui.Flex(1.4), "WORKTREE", head)
			col(c, "a", ui.Px(80), "LAST COMMIT", head)
			col(c, "n", ui.Flex(1.6), "NOTE", head)
		})
		for _, b := range p.Branches {
			c.Row("b."+b.Name, ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
				st := widget.CheckOff
				if d.ticked[b.Name] {
					st = widget.CheckOn
				}
				if widget.Check(c, "check", st, widget.CheckOpts{Label: "Clean up " + b.Name, Disabled: b.Session != "" || d.busy != "",
					Help: "agent/repos#housekeeping"}) {
					d.ticked[b.Name] = !d.ticked[b.Name]
				}
				fg := th.C(theme.Fg)
				if b.Session != "" {
					fg = th.C(theme.FgFaint)
				}
				col(c, "b", ui.Flex(2), b.Name, monoFont(11.5, fg))
				c.Row("w", ui.Style{W: ui.Px(110)}, func(c *ui.Ctx) {
					tone := th.C(tRepoMerged)
					if b.Why == host.WhyGone {
						tone = th.C(tRepoGone)
					}
					widget.Chip(c, "why", b.Why, tone, "agent/repos#housekeeping")
				})
				col(c, "t", ui.Flex(1.4), cmpOr(tildePath(b.Worktree), "none"), monoFont(11, th.C(theme.FgMuted)))
				when := ""
				if !b.When.IsZero() {
					when = agoText(time.Now(), b.When)
				}
				col(c, "a", ui.Px(80), when, monoFont(11, th.C(theme.FgMuted)))
				c.Row("n", ui.Style{W: ui.Flex(1.6), Gap: 6, Cross: ui.Center, Clip: true}, func(c *ui.Ctx) {
					res, done := d.results[b.Name]
					switch {
					case done && res == "":
						c.Text("t", "✓ done", monoFont(10.5, th.C(theme.OK)))
					case done:
						c.Text("t", "✗ "+firstLine(res), monoFont(10.5, th.C(theme.Error)))
					case b.Session != "":
						c.Text("t", "in session "+b.Session+" · archive it", monoFont(10.5, th.C(theme.FgFaint)))
					case b.Dirty:
						widget.Chip(c, "dirty", "dirty", th.C(theme.Warn), "agent/repos#housekeeping")
						c.Text("t", "tick to force", monoFont(10.5, th.C(theme.FgFaint)))
					}
				})
			})
		}
		for _, s := range p.Stale {
			c.Row("s."+s, ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
				st := widget.CheckOff
				if d.prune {
					st = widget.CheckOn
				}
				if widget.Check(c, "check", st, widget.CheckOpts{Label: "Prune stale worktrees", Help: "agent/repos#housekeeping"}) {
					d.prune = !d.prune
				}
				col(c, "b", ui.Flex(2), "—", monoFont(11.5, th.C(theme.FgFaint)))
				c.Row("w", ui.Style{W: ui.Px(110)}, func(c *ui.Ctx) {
					widget.Chip(c, "why", "stale", th.C(theme.StatusQuiet), "agent/repos#housekeeping")
				})
				col(c, "t", ui.Flex(1.4), tildePath(s), monoFont(11, th.C(theme.FgFaint)))
				col(c, "a", ui.Px(80), "—", monoFont(11, th.C(theme.FgFaint)))
				col(c, "n", ui.Flex(1.6), "directory gone · worktree prune", monoFont(10.5, th.C(theme.FgFaint)))
			})
		}
		c.Row("foot", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			note(c, "never", "never offered: the default branch, the checked-out one")
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			n := ticked
			if d.prune {
				n++
			}
			label := fmt.Sprintf("Clean up %d…", n)
			if x.confirm == "clean" {
				label = fmt.Sprintf("Sure? Delete %d", n)
			}
			if widget.Button(c, "clean", label, widget.ButtonOpts{Primary: true, Disabled: n == 0 || d.busy != "", Help: "agent/repos#housekeeping"}) {
				if x.confirm != "clean" {
					x.confirm = "clean"
				} else {
					x.confirm = ""
					x.clean(r.ID, d)
				}
			}
		})
	})
}

func plural0(n int, one, many string) string {
	switch n {
	case 0:
		return ""
	case 1:
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func (d *repoDetail) tickedCount() int {
	n := 0
	for _, on := range d.ticked {
		if on {
			n++
		}
	}
	return n
}

// clean deletes the ticked branches, forcing the dirty worktrees ticked, and prunes if ticked.
func (x *reposPlugin) clean(id core.RepoID, d *repoDetail) {
	req := host.HousekeepReq{Repo: id, Prune: d.prune}
	for _, b := range d.plan.Branches {
		if d.ticked[b.Name] {
			req.Apply = append(req.Apply, b.Name)
			if b.Dirty {
				req.Force = append(req.Force, b.Name)
			}
		}
	}
	x.act(id, "clean", func(ctx context.Context, w *host.Host) (string, error) {
		res, err := w.Housekeep(ctx, req)
		x.h.Do(func() {
			d.results = map[string]string{}
			for _, r := range res {
				d.results[r.Branch] = r.Error
			}
			d.plan = nil
			x.readPlan(id, false)
		})
		failed := 0
		for _, r := range res {
			if r.Error != "" {
				failed++
			}
		}
		note := fmt.Sprintf("cleaned %d", len(res)-failed)
		if failed > 0 {
			note += fmt.Sprintf(", %d failed", failed)
		}
		return note, err
	})
}

var bumps = []string{"patch", "minor", "major"}

// tagForm is the New tag form of a repo.
type tagForm struct {
	open    bool
	preview host.TagPreview
	loaded  bool
	loading bool
	branch  string
	name    string
	nameFor string // the name the bump made: it follows the bump while it's this
	message string
	msgFor  string // the message the template made: it follows the tag while it's this
	bump    int
	pull    bool
	sign    bool
	push    bool
	busy    bool
	err     string
	done    *host.Tagged
}

func (x *reposPlugin) loadTag(id core.RepoID, t *tagForm) {
	w := x.sh.cfg.Host
	if t.loading || w == nil {
		return
	}
	t.loading = true
	branch := t.branch
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		p, err := w.TagPreview(ctx, id, branch)
		x.h.Do(func() {
			t.loading = false
			if err != nil {
				t.err = err.Error()
				return
			}
			first := !t.loaded
			t.preview, t.loaded = p, true
			if first {
				t.branch = p.Default
				t.pull, t.push = x.optBool("repos.tag.pull"), x.optBool("repos.tag.push") && p.Remote
				t.sign = x.signTags(p.Signing)
				t.name = gitx.BumpVersion(p.Latest, bumps[t.bump])
				t.nameFor = t.name
			}
		})
		x.h.Redraw()
	}()
}

func (x *reposPlugin) tagCard(c *ui.Ctx, r core.Repo, d *repoDetail) {
	th := c.Theme()
	if d.tag == nil {
		d.tag = &tagForm{}
	}
	t := d.tag
	if !t.loaded && t.err == "" {
		x.loadTag(r.ID, t)
	}
	summary := "no tags yet"
	if t.preview.Latest != "" {
		summary = "latest " + t.preview.Latest
	}
	right := func(c *ui.Ctx) {
		if !t.open && widget.Button(c, "new", "New tag…", widget.ButtonOpts{Help: "agent/repos#tags"}) {
			t.open, t.done, t.err = true, nil, ""
		}
	}
	repoCard(c, "tags", "Tags", summary, right, func(c *ui.Ctx) {
		switch {
		case t.done != nil:
			x.tagDone(c, r, t)
			return
		case !t.open:
			note(c, "how", "an annotated tag on a branch, pulled first; only the new tag is pushed")
			return
		case !t.loaded:
			c.Text("wait", cmpOr(t.err, "reading the branches and tags…"), monoFont(10.5, th.C(theme.FgFaint)))
			return
		}
		x.tagFormView(c, r, t)
	})
}

func (x *reposPlugin) tagFormView(c *ui.Ctx, r core.Repo, t *tagForm) {
	th := c.Theme()
	if n := gitx.BumpVersion(t.preview.Latest, bumps[t.bump]); t.name == t.nameFor && n != t.nameFor {
		t.name, t.nameFor = n, n
	}
	if m := tagMessage(x.optString("repos.tag.message"), strings.TrimSpace(t.name), t.branch, r.ID); t.message == t.msgFor && m != t.msgFor {
		t.message, t.msgFor = m, m
	}
	labelled := func(c *ui.Ctx, key, label string, body func(c *ui.Ctx)) {
		c.Row(key, ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Row("l", ui.Style{W: ui.Px(90)}, func(c *ui.Ctx) { c.Text("t", label, uiFont(12.5, th.C(theme.FgMuted))) })
			body(c)
		})
	}
	c.Row("form", ui.Style{W: ui.Flex(1), Gap: 16}, func(c *ui.Ctx) {
		c.Column("fields", ui.Style{W: ui.Flex(1), Gap: 10}, func(c *ui.Ctx) {
			labelled(c, "branch", "Branch", func(c *ui.Ctx) {
				label := t.branch
				if t.branch == t.preview.Default {
					label += " · default"
				}
				if v, ok := widget.Select(c, "branch", label, t.preview.Branches, widget.SelectOpts{Label: "Branch", Help: "agent/repos#tags", Width: 240}); ok && v != t.branch {
					t.branch = v
					x.loadTag(r.ID, t)
				}
			})
			labelled(c, "tag", "Tag", func(c *ui.Ctx) {
				if in := widget.Input(c, "name", &t.name, widget.InputOpts{Label: "Tag", Placeholder: "v0.1.0", Width: ui.Px(160), Help: "agent/repos#tags",
					Invalid: strings.TrimSpace(t.name) == ""}); in.Submitted {
					x.createTag(r.ID, t)
				}
				if widget.Segmented(c, "bump", bumps, &t.bump, widget.SegmentedOpts{Label: "Bump", Help: "agent/repos#tags"}) {
					t.name = gitx.BumpVersion(t.preview.Latest, bumps[t.bump])
					t.nameFor = t.name
				}
			})
			labelled(c, "msg", "Message", func(c *ui.Ctx) {
				widget.Input(c, "message", &t.message, widget.InputOpts{Label: "Message", Help: "agent/repos#tags"})
				note(c, "from", "from repos.tag.message")
			})
			c.Column("toggles", ui.Style{W: ui.Flex(1), Gap: 8, Pad: ui.Insets{L: 100}}, func(c *ui.Ctx) {
				pullNote := ""
				if !t.preview.Remote {
					pullNote = "no remote"
				}
				toggleInline(c, "pull", "Pull latest first", pullNote, &t.pull, !t.preview.Remote)
				signNote := ""
				if !t.preview.Signing {
					signNote = "no signing key configured"
				}
				toggleInline(c, "sign", "Signed", signNote, &t.sign, !t.preview.Signing && x.optString("repos.tag.sign") != "always")
				pushNote := "only refs/tags/" + strings.TrimSpace(t.name)
				if !t.preview.Remote {
					pushNote = "no remote"
				}
				toggleInline(c, "push", "Push this tag", pushNote, &t.push, !t.preview.Remote)
			})
		})
		c.Column("will", ui.Style{W: ui.Px(250), Pad: ui.Pad(12), Gap: 6, Stroke: th.C(theme.Line), Border: 1, Radius: 6, Clip: true}, func(c *ui.Ctx) {
			what := "will tag"
			if t.pull && t.preview.Remote {
				what += " · after pulling"
			}
			caption(c, "cap", what)
			c.Text("ref", t.branch+" "+shortSHA(t.preview.Commit), monoFont(11.5, th.C(theme.Accent)))
			widget.Wrap(c, "subj", t.preview.Subject, 220, monoFont(11, th.C(theme.Fg)))
		})
	})
	c.Row("foot", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
		if t.err != "" {
			c.Text("err", truncate(firstLine(t.err), 90), monoFont(11, th.C(theme.Error)))
		}
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if widget.Button(c, "cancel", "Cancel", widget.ButtonOpts{Help: "agent/repos#tags"}) {
			t.open, t.err = false, ""
		}
		name := strings.TrimSpace(t.name)
		if widget.Button(c, "create", "Create tag "+name, widget.ButtonOpts{Primary: true, Disabled: t.busy || name == "", Help: "agent/repos#tags"}) {
			x.createTag(r.ID, t)
		}
	})
}

// toggleInline is a toggle with its label and a dim note after it.
func toggleInline(c *ui.Ctx, key, label, note string, on *bool, disabled bool) {
	th := c.Theme()
	c.Row(key, ui.Style{Gap: 7, Cross: ui.Center}, func(c *ui.Ctx) {
		if widget.Toggle(c, "t", *on && !disabled, widget.ToggleOpts{Label: label, Disabled: disabled, Help: "agent/repos#tags"}) {
			*on = !*on
		}
		fg := th.C(theme.Fg)
		if disabled {
			fg = th.C(theme.FgFaint)
		}
		c.Text("l", label, uiFont(12, fg))
		if note != "" {
			c.Text("n", note, monoFont(10, th.C(theme.FgFaint)))
		}
	})
}

func (x *reposPlugin) createTag(id core.RepoID, t *tagForm) {
	if t.busy {
		return
	}
	t.busy, t.err = true, ""
	a := registry.Args{"repo": string(id), "branch": t.branch, "name": strings.TrimSpace(t.name), "message": t.message,
		"pull": t.pull && t.preview.Remote, "sign": t.sign && t.preview.Signing, "push": t.push && t.preview.Remote}
	w := x.sh.cfg.Host
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		res, err := w.Tag(ctx, host.TagReq{Repo: id, Branch: a.String("branch"), Name: a.String("name"), Message: a.String("message"),
			Pull: a.Bool("pull"), Sign: a.Bool("sign"), Push: a.Bool("push")})
		x.h.Do(func() {
			t.busy = false
			if err != nil {
				t.err = err.Error()
				return
			}
			t.done, t.open, t.loaded = &res, false, false
			x.readFacts(id)
		})
		x.h.Redraw()
	}()
}

// tagDone is a tag just made, and how its push went.
func (x *reposPlugin) tagDone(c *ui.Ctx, r core.Repo, t *tagForm) {
	th := c.Theme()
	res := t.done
	c.Row("done", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Text("t", "✓ tagged "+res.Name+" at "+shortSHA(res.Commit)+" "+res.Subject, monoFont(11.5, th.C(theme.OK)))
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		switch {
		case res.Pushed:
			c.Text("p", "pushed", monoFont(10.5, th.C(theme.FgFaint)))
		case res.PushErr != "":
			c.Text("p", "push failed: "+truncate(firstLine(res.PushErr), 60), monoFont(10.5, th.C(theme.Error)))
			if widget.Button(c, "again", "Push again", widget.ButtonOpts{Help: "agent/repos#tags"}) {
				name := res.Name
				x.act(r.ID, "push", func(ctx context.Context, w *host.Host) (string, error) {
					err := w.PushTag(ctx, r.ID, name)
					if err == nil {
						x.h.Do(func() { res.Pushed, res.PushErr = true, "" })
					}
					return "pushed " + name, err
				})
			}
		}
		if widget.Button(c, "ok", "Done", widget.ButtonOpts{Help: "agent/repos#tags"}) {
			t.done = nil
		}
	})
}

func shortSHA(s string) string { return s[:min(len(s), 7)] }
