package sessions

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// The Repos page: the repos on the left; the selected one's facts, stats, housekeeping and tags on
// the right, over the main area beside the sidebar.

var (
	tRepoTidy   = theme.Declare("repos.tidy", "amber")
	tRepoMerged = theme.Declare("repos.merged", "ok")
	tRepoGone   = theme.Declare("repos.gone", "violet")
	tRepoFound  = theme.Declare("repos.found", "amber")
)

// repoDetail is what the page has read of one repo.
type repoDetail struct {
	facts *plugin.Load[host.RepoFacts]
	busy  string // the action running: "fetch", "clean", "push"
	note  string // how the last action went

	plan      *host.HousekeepPlan
	planBusy  bool
	planSince time.Time
	planErr   string
	ticked    map[string]bool
	prune     bool
	results   map[string]string // a branch's error after a clean up; "" when it went

	tag *tagForm
}

func (x *Repos) detail(id core.RepoID) *repoDetail {
	d := x.details[id]
	if d == nil {
		w, _ := kit.SessionHost(x.h)
		d = &repoDetail{facts: plugin.NewLoad(x.h, func(ctx context.Context, _ func(string, float64)) (host.RepoFacts, error) {
			if w == nil {
				return host.RepoFacts{}, errors.New("there are no repos in this window")
			}
			return w.RepoFacts(ctx, id)
		}, plugin.LoadOpts{Timeout: 20 * time.Second})}
		x.details[id] = d
	}
	return d
}

// readFacts reads a repo's facts again, off the loop, keeping the old ones on show.
func (x *Repos) readFacts(id core.RepoID) { x.detail(id).facts.Reload() }

// readPlan reads what housekeeping would clean, fetching first if asked.
func (x *Repos) readPlan(id core.RepoID, fetch bool) {
	d := x.detail(id)
	w, _ := kit.SessionHost(x.h)
	if d.planBusy || w == nil {
		return
	}
	d.planBusy, d.planErr, d.planSince = true, "", x.h.UI().Now()
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
func (x *Repos) act(id core.RepoID, what string, fn func(ctx context.Context, w host.Access) (string, error)) {
	d := x.detail(id)
	w, _ := kit.SessionHost(x.h)
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
func (x *Repos) shown() []core.Repo {
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

func (x *Repos) page(c *ui.Ctx) {
	if !x.open || c.Theme().IsProbe() {
		return
	}
	rows := x.shown()
	if _, ok := x.find(string(x.sel)); !ok && len(rows) > 0 {
		x.sel = rows[0].ID
	}
	widget.Page(c, "repos", &x.open, widget.PageOpts{KeyMode: ReposKeyMode, Label: "Repos page", Help: "agent/repos#page"}, func(c *ui.Ctx) {
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

func (x *Repos) header(c *ui.Ctx) {
	th := c.Theme()
	c.Row("top", ui.Style{W: ui.Flex(1), H: ui.Px(42), Pad: ui.PadXY(18, 0), Gap: 12, Cross: ui.Center, Fill: th.C(kit.TStatsHead)}, func(c *ui.Ctx) {
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
		c.Text("m", strings.Join(meta, " · "), kit.MonoFont(11, th.C(theme.FgFaint)))
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if x.err != "" {
			c.Text("err", kit.FirstLine(x.err), kit.MonoFont(11, th.C(theme.Error)))
		}
		if widget.Button(c, "add", "+ Add repo", widget.ButtonOpts{Key: "a", Help: "agent/repos#add"}) {
			x.run("repos.new", nil)
		}
		if widget.Button(c, "scan", "Scan for repos", widget.ButtonOpts{Busy: x.scanning, BusyLabel: "Scanning", Help: "agent/repos#discovery"}) {
			x.run("repos.scan", nil)
		}
		if widget.Button(c, "close", "Close", widget.ButtonOpts{Key: "esc", Help: "agent/repos#page"}) {
			x.open = false
		}
	})
	c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
}

func (x *Repos) run(id string, a registry.Args) {
	if err := x.h.Run(context.Background(), id, a); err != nil {
		x.err = err.Error()
	}
}

func (x *Repos) list(c *ui.Ctx, rows []core.Repo) {
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
					widget.Wrap(c, "t", msg, 270, kit.UIFont(12, th.C(theme.FgFaint)))
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
			kit.Note(c, "a", "~/.oriel/repos.json · removing one forgets it")
			kit.Note(c, "b", "files are never deleted; removed repos stay removed")
		})
	})
}

func (x *Repos) rowView(c *ui.Ctx, r core.Repo, on bool) {
	th := c.Theme()
	fill, edge := render.Color{}, render.Color{}
	if on {
		fill, edge = th.C(kit.TStatsRowOn), th.C(theme.Accent)
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
				c.Text("t", string(r.ID), kit.UIFont(13, th.C(theme.Fg)))
				if isFound(r) {
					widget.Chip(c, "found", "found", th.C(tRepoFound), "agent/repos#discovery")
				}
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				c.Text("b", strings.TrimPrefix(r.BaseBranch, "origin/"), kit.MonoFont(10, th.C(theme.FgGhost)))
			})
			line := []string{kit.TildePath(r.Path)}
			switch {
			case r.Used.IsZero():
				line = append(line, "never used")
			case tidy:
				line = append(line, kit.Counted(len(d.plan.Branches)+len(d.plan.Stale), "to tidy"))
			}
			c.Text("l2", strings.Join(line, " · "), kit.MonoFont(10, th.C(theme.FgFaint)))
		})
	})
}

func (x *Repos) emptyPage(c *ui.Ctx) {
	th := c.Theme()
	c.Column("empty", ui.Style{W: ui.Flex(1), Pad: ui.Pad(24), Gap: 12}, func(c *ui.Ctx) {
		c.Text("t", "No repos yet.", kit.UIFont(14, th.C(theme.Fg)))
		widget.Wrap(c, "d", "Add a checkout, or clone one into a folder you choose. Oriel finds the repos beside the ones you add.", 460,
			kit.UIFont(12, th.C(theme.FgFaint)))
		if widget.Button(c, "add", "+ Add repo", widget.ButtonOpts{Primary: true, Help: "agent/repos#add"}) {
			x.run("repos.new", nil)
		}
	})
}

func (x *Repos) repoView(c *ui.Ctx, r core.Repo) {
	d := x.detail(r.ID)
	d.facts.Start()
	if d.plan == nil && !d.planBusy && d.planErr == "" {
		x.readPlan(r.ID, false)
	}
	x.repoHead(c, r, d)
	x.statsCard(c, r, d)
	x.housekeepCard(c, r, d)
	x.tagCard(c, r, d)
}

func (x *Repos) repoHead(c *ui.Ctx, r core.Repo, d *repoDetail) {
	th := c.Theme()
	c.Column("head", ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) {
		c.Row("l1", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("name", string(r.ID), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 18, Weight: typeset.Bold}, Color: th.C(theme.Fg)})
			meta := nonEmpty(kit.TildePath(r.Path), remoteHost(r.Remote))
			if b := strings.TrimPrefix(r.BaseBranch, "origin/"); b != "" {
				meta = append(meta, "default "+b)
			}
			c.Text("meta", strings.Join(meta, " · "), kit.MonoFont(11, th.C(theme.FgFaint)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			busy := d.busy != ""
			if r.Remote != "" && widget.Button(c, "fetch", "Fetch", widget.ButtonOpts{Key: "f", Disabled: busy, Busy: d.busy == "fetch", BusyLabel: "Fetching",
				Help: "agent/repos#detail"}) {
				x.act(r.ID, "fetch", func(ctx context.Context, w host.Access) (string, error) {
					return "fetched", w.FetchRepo(ctx, r.ID)
				})
				d.plan = nil
			}
			if widget.Button(c, "reveal", "Reveal", widget.ButtonOpts{Help: "agent/repos#detail"}) {
				if err := kit.OpenURL(x.h, r.Path); err != nil {
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
		f, _ := d.facts.Value()
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
			line = append(line, "last commit "+kit.AgoText(time.Now(), f.Last.When)+" by "+f.Last.Author)
		}
		if !r.Added.IsZero() {
			line = append(line, "added "+r.Added.Local().Format("Jan 2"))
		}
		if !r.Used.IsZero() {
			line = append(line, "used "+kit.AgoText(time.Now(), r.Used))
		}
		if d.busy == "" && d.note != "" {
			line = append(line, d.note)
		}
		c.Text("l2", strings.Join(line, " · "), kit.MonoFont(10.5, th.C(theme.FgMuted)))
	})
}

// card is a section of the detail: a caption and its note, then the body.
func repoCard(c *ui.Ctx, key, title, note string, right func(c *ui.Ctx), body func(c *ui.Ctx)) {
	th := c.Theme()
	c.Column(key, ui.Style{W: ui.Flex(1), Fill: th.C(theme.BgSurface), Stroke: th.C(theme.Line), Border: 1, Radius: 8, Clip: true,
		Role: ui.RoleRegion, Label: title}, func(c *ui.Ctx) {
		c.Row("cap", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(14, 9), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			kit.Caption(c, "t", title)
			c.Text("n", note, kit.MonoFont(10, th.C(theme.FgFaint)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if right != nil {
				right(c)
			}
		})
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
		c.Column("body", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(14, 12), Gap: 10}, body)
	})
}

func (x *Repos) statsCard(c *ui.Ctx, r core.Repo, d *repoDetail) {
	th := c.Theme()
	repoCard(c, "stats", "Stats", "the overview's panels, cached by HEAD", nil, func(c *ui.Ctx) {
		f, _ := d.facts.Value()
		tag := "tags"
		if f.LatestTag != "" {
			tag = "tags · " + f.LatestTag
		}
		widget.Async(c, "facts", d.facts.State(), widget.AsyncOpts{Label: "Reading the repo", Retry: d.facts.Reload, Help: "agent/repos#detail",
			Loading: func(c *ui.Ctx) {
				widget.Skeleton(c, "tiles", widget.SkeletonOpts{Rows: 1, RowH: 46, Widths: []float32{1}, Label: "Reading the repo", Help: "agent/repos#detail"})
			}}, func(c *ui.Ctx) {
			c.Row("facts", ui.Style{W: ui.Flex(1), Gap: 8}, func(c *ui.Ctx) {
				for i, t := range [][2]string{
					{fmt.Sprint(f.Branches), "branches"}, {fmt.Sprint(f.Worktrees), "worktrees"}, {fmt.Sprint(f.Tags), tag},
					{kit.Bytes(f.SizeKiB << 10), "on disk"},
				} {
					c.Column(i, ui.Style{W: ui.Flex(1), Pad: ui.PadXY(10, 7), Gap: 2, Stroke: th.C(theme.Line), Border: 1, Radius: 6, Clip: true,
						Role: ui.RoleText, Label: t[0] + " " + t[1]}, func(c *ui.Ctx) {
						c.Text("v", t[0], ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 16, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
						c.Text("l", strings.ToUpper(t[1]), kit.MonoFont(9, th.C(theme.FgFaint)))
					})
				}
			})
		})
		if v, ok := x.h.ViewByID(kit.OverviewPanelsView); ok {
			c.Column("panels", ui.Style{W: ui.Flex(1), Stroke: th.C(theme.Line), Border: 1, Radius: 6, Clip: true}, func(c *ui.Ctx) {
				c.Node(v.Build(uireg.ViewCtx{Instance: r.Path}))
			})
		}
	})
}

func (x *Repos) housekeepCard(c *ui.Ctx, r core.Repo, d *repoDetail) {
	th := c.Theme()
	p := d.plan
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
	repoCard(c, "housekeeping", "Housekeeping", housekeepSummary(d), right, func(c *ui.Ctx) {
		if p == nil {
			st := widget.LoadState{Phase: widget.Pending, Since: d.planSince}
			if d.planErr != "" {
				st = widget.LoadState{Phase: widget.Failed, Err: errors.New(d.planErr)}
			}
			widget.Async(c, "plan", st, widget.AsyncOpts{Label: "Reading what could be cleaned up", Help: "agent/repos#housekeeping",
				Retry: func() { x.readPlan(r.ID, false) }}, func(*ui.Ctx) {})
			return
		}
		for _, b := range slices.Sorted(maps.Keys(d.results)) {
			if slices.ContainsFunc(p.Branches, func(s host.StaleBranch) bool { return s.Name == b }) {
				continue
			}
			if e := d.results[b]; e != "" {
				c.Text("res."+b, "✗ "+b+": "+kit.FirstLine(e), kit.MonoFont(11, th.C(theme.Error)))
			} else {
				c.Text("res."+b, "✓ deleted "+b, kit.MonoFont(11, th.C(theme.OK)))
			}
		}
		if len(p.Branches)+len(p.Stale) == 0 {
			c.Text("none", "Nothing to clean: no merged branches, none whose upstream went, no stale worktrees.", kit.UIFont(12, th.C(theme.FgFaint)))
			return
		}
		head := kit.MonoFont(9, th.C(theme.FgFaint))
		c.Row("cols", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Box("check", ui.Style{W: ui.Px(14)}, nil)
			housekeepCol(c, "b", ui.Flex(2), "BRANCH", head)
			housekeepCol(c, "w", ui.Px(110), "WHY", head)
			housekeepCol(c, "t", ui.Flex(1.4), "WORKTREE", head)
			housekeepCol(c, "a", ui.Px(80), "LAST COMMIT", head)
			housekeepCol(c, "n", ui.Flex(1.6), "NOTE", head)
		})
		for _, b := range p.Branches {
			housekeepBranch(c, b, d)
		}
		for _, s := range p.Stale {
			housekeepStale(c, s, d)
		}
		x.housekeepFoot(c, r, d, ticked)
	})
}

func housekeepSummary(d *repoDetail) string {
	p := d.plan
	summary := ""
	switch {
	case d.planErr != "":
		summary = kit.FirstLine(d.planErr)
	case p != nil:
		wts := 0
		for _, b := range p.Branches {
			if b.Worktree != "" {
				wts++
			}
		}
		summary = cmp.Or(strings.Join(nonEmpty(plural0(len(p.Branches), "branch", "branches"), plural0(wts, "with a worktree", "with worktrees"),
			plural0(len(p.Stale), "stale worktree", "stale worktrees")), ", "), "nothing to clean")
		if p.Base != "" {
			summary = "into " + p.Base + " · " + summary
		}
	}
	return summary
}

func housekeepCol(c *ui.Ctx, key string, w ui.Size, s string, ts ui.TextStyle) {
	c.Row(key, ui.Style{W: w, Clip: true}, func(c *ui.Ctx) { c.Text("t", s, ts) })
}

func housekeepBranch(c *ui.Ctx, b host.StaleBranch, d *repoDetail) {
	th := c.Theme()
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
		housekeepCol(c, "b", ui.Flex(2), b.Name, kit.MonoFont(11.5, fg))
		c.Row("w", ui.Style{W: ui.Px(110)}, func(c *ui.Ctx) {
			tone := th.C(tRepoMerged)
			if b.Why == host.WhyGone {
				tone = th.C(tRepoGone)
			}
			widget.Chip(c, "why", b.Why, tone, "agent/repos#housekeeping")
		})
		housekeepCol(c, "t", ui.Flex(1.4), cmp.Or(kit.TildePath(b.Worktree), "none"), kit.MonoFont(11, th.C(theme.FgMuted)))
		when := ""
		if !b.When.IsZero() {
			when = kit.AgoText(time.Now(), b.When)
		}
		housekeepCol(c, "a", ui.Px(80), when, kit.MonoFont(11, th.C(theme.FgMuted)))
		c.Row("n", ui.Style{W: ui.Flex(1.6), Gap: 6, Cross: ui.Center, Clip: true}, func(c *ui.Ctx) {
			res, done := d.results[b.Name]
			switch {
			case done && res == "":
				c.Text("t", "✓ done", kit.MonoFont(10.5, th.C(theme.OK)))
			case done:
				c.Text("t", "✗ "+kit.FirstLine(res), kit.MonoFont(10.5, th.C(theme.Error)))
			case b.Session != "":
				c.Text("t", "in session "+b.Session+" · archive it", kit.MonoFont(10.5, th.C(theme.FgFaint)))
			case b.Dirty:
				widget.Chip(c, "dirty", "dirty", th.C(theme.Warn), "agent/repos#housekeeping")
				c.Text("t", "tick to force", kit.MonoFont(10.5, th.C(theme.FgFaint)))
			}
		})
	})
}

func housekeepStale(c *ui.Ctx, s string, d *repoDetail) {
	th := c.Theme()
	c.Row("s."+s, ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
		st := widget.CheckOff
		if d.prune {
			st = widget.CheckOn
		}
		if widget.Check(c, "check", st, widget.CheckOpts{Label: "Prune stale worktrees", Help: "agent/repos#housekeeping"}) {
			d.prune = !d.prune
		}
		housekeepCol(c, "b", ui.Flex(2), "—", kit.MonoFont(11.5, th.C(theme.FgFaint)))
		c.Row("w", ui.Style{W: ui.Px(110)}, func(c *ui.Ctx) {
			widget.Chip(c, "why", "stale", th.C(theme.StatusQuiet), "agent/repos#housekeeping")
		})
		housekeepCol(c, "t", ui.Flex(1.4), kit.TildePath(s), kit.MonoFont(11, th.C(theme.FgFaint)))
		housekeepCol(c, "a", ui.Px(80), "—", kit.MonoFont(11, th.C(theme.FgFaint)))
		housekeepCol(c, "n", ui.Flex(1.6), "directory gone · worktree prune", kit.MonoFont(10.5, th.C(theme.FgFaint)))
	})
}

func (x *Repos) housekeepFoot(c *ui.Ctx, r core.Repo, d *repoDetail, ticked int) {
	c.Row("foot", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
		kit.Note(c, "never", "never offered: the default branch, the checked-out one")
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		n := ticked
		if d.prune {
			n++
		}
		label := fmt.Sprintf("Clean up %d…", n)
		if x.confirm == "clean" {
			label = fmt.Sprintf("Sure? Delete %d", n)
		}
		if widget.Button(c, "clean", label, widget.ButtonOpts{Primary: true, Disabled: n == 0 || d.busy != "", Busy: d.busy == "clean", BusyLabel: "Cleaning up",
			Help: "agent/repos#housekeeping"}) {
			if x.confirm != "clean" {
				x.confirm = "clean"
			} else {
				x.confirm = ""
				x.clean(r.ID, d)
			}
		}
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
func (x *Repos) clean(id core.RepoID, d *repoDetail) {
	req := host.HousekeepReq{Repo: id, Prune: d.prune}
	for _, b := range d.plan.Branches {
		if d.ticked[b.Name] {
			req.Apply = append(req.Apply, b.Name)
			if b.Dirty {
				req.Force = append(req.Force, b.Name)
			}
		}
	}
	x.act(id, "clean", func(ctx context.Context, w host.Access) (string, error) {
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
	since   time.Time // when the preview's read started
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

func (x *Repos) loadTag(id core.RepoID, t *tagForm) {
	w, _ := kit.SessionHost(x.h)
	if t.loading || w == nil {
		return
	}
	t.loading, t.since = true, x.h.UI().Now()
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

func (x *Repos) tagCard(c *ui.Ctx, r core.Repo, d *repoDetail) {
	if d.tag == nil {
		d.tag = &tagForm{}
	}
	t := d.tag
	if !t.loaded && t.err == "" {
		x.loadTag(r.ID, t)
	}
	summary := "no tags yet"
	switch {
	case t.preview.Latest != "":
		summary = "latest " + t.preview.Latest
	case !t.loaded && t.err == "":
		summary = "" // not "no tags" before they're read
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
			kit.Note(c, "how", "an annotated tag on a branch, pulled first; only the new tag is pushed")
			return
		case !t.loaded:
			st := widget.LoadState{Phase: widget.Pending, Since: t.since}
			if t.err != "" && !t.loading {
				st = widget.LoadState{Phase: widget.Failed, Err: errors.New(t.err)}
			}
			widget.Async(c, "preview", st, widget.AsyncOpts{Label: "Reading the branches and tags", Help: "agent/repos#tags",
				Retry: func() { t.err = ""; x.loadTag(r.ID, t) }}, func(*ui.Ctx) {})
			return
		}
		x.tagFormView(c, r, t)
	})
}

func (x *Repos) tagFormView(c *ui.Ctx, r core.Repo, t *tagForm) {
	th := c.Theme()
	if n := gitx.BumpVersion(t.preview.Latest, bumps[t.bump]); t.name == t.nameFor && n != t.nameFor {
		t.name, t.nameFor = n, n
	}
	if m := tagMessage(x.optString("repos.tag.message"), strings.TrimSpace(t.name), t.branch, r.ID); t.message == t.msgFor && m != t.msgFor {
		t.message, t.msgFor = m, m
	}
	labelled := func(c *ui.Ctx, key, label string, body func(c *ui.Ctx)) {
		c.Row(key, ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Row("l", ui.Style{W: ui.Px(90)}, func(c *ui.Ctx) { c.Text("t", label, kit.UIFont(12.5, th.C(theme.FgMuted))) })
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
				kit.Note(c, "from", "from repos.tag.message")
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
			kit.Caption(c, "cap", what)
			c.Text("ref", t.branch+" "+kit.ShortSHA(t.preview.Commit), kit.MonoFont(11.5, th.C(theme.Accent)))
			widget.Wrap(c, "subj", t.preview.Subject, 220, kit.MonoFont(11, th.C(theme.Fg)))
		})
	})
	c.Row("foot", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
		if t.err != "" {
			c.Text("err", kit.FirstLine(t.err), kit.MonoFont(11, th.C(theme.Error)))
		}
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if widget.Button(c, "cancel", "Cancel", widget.ButtonOpts{Help: "agent/repos#tags"}) {
			t.open, t.err = false, ""
		}
		name := strings.TrimSpace(t.name)
		if widget.Button(c, "create", "Create tag "+name, widget.ButtonOpts{Primary: true, Disabled: name == "", Busy: t.busy, BusyLabel: "Tagging",
			Help: "agent/repos#tags"}) {
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
		c.Text("l", label, kit.UIFont(12, fg))
		if note != "" {
			c.Text("n", note, kit.MonoFont(10, th.C(theme.FgFaint)))
		}
	})
}

func (x *Repos) createTag(id core.RepoID, t *tagForm) {
	if t.busy {
		return
	}
	t.busy, t.err = true, ""
	a := registry.Args{"repo": string(id), "branch": t.branch, "name": strings.TrimSpace(t.name), "message": t.message,
		"pull": t.pull && t.preview.Remote, "sign": t.sign && t.preview.Signing, "push": t.push && t.preview.Remote}
	w, _ := kit.SessionHost(x.h)
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
func (x *Repos) tagDone(c *ui.Ctx, r core.Repo, t *tagForm) {
	th := c.Theme()
	res := t.done
	c.Row("done", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Text("t", "✓ tagged "+res.Name+" at "+kit.ShortSHA(res.Commit)+" "+res.Subject, kit.MonoFont(11.5, th.C(theme.OK)))
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		switch {
		case res.Pushed:
			c.Text("p", "pushed", kit.MonoFont(10.5, th.C(theme.FgFaint)))
		case res.PushErr != "":
			c.Text("p", "push failed: "+kit.FirstLine(res.PushErr), kit.MonoFont(10.5, th.C(theme.Error)))
			if widget.Button(c, "again", "Push again", widget.ButtonOpts{Busy: x.detail(r.ID).busy == "push", BusyLabel: "Pushing", Help: "agent/repos#tags"}) {
				name := res.Name
				x.act(r.ID, "push", func(ctx context.Context, w host.Access) (string, error) {
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
