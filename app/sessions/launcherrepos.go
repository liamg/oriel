package sessions

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

func (x *Launcher) addRepo(r core.RepoID) {
	if r != "" && !slices.Contains(x.chosen, r) {
		x.chosen = append(x.chosen, r)
	}
}

// repoRowsShown is how many repo rows the launcher shows before its list scrolls; repoFilterAfter, how many repos
// there are before it has a filter.
const (
	repoRowsShown   = 6
	repoFilterAfter = 8
	repoRowHeight   = 28
)

// repoChoices are the repos the launcher lists: ticked first, then the most recently used, then by name. A path
// ticked that isn't registered is listed as it is.
func (x *Launcher) repoChoices() []core.Repo {
	repos := slices.Clone(x.h.State().Repos)
	for _, id := range x.chosen {
		if !slices.ContainsFunc(repos, func(r core.Repo) bool { return r.ID == id || r.Path == string(id) }) {
			repos = append(repos, core.Repo{ID: id, Path: string(id)})
		}
	}
	ticked := func(r core.Repo) bool { return x.ticked(r) }
	slices.SortStableFunc(repos, func(a, b core.Repo) int {
		if ta, tb := ticked(a), ticked(b); ta != tb {
			if ta {
				return -1
			}
			return 1
		}
		if c := b.Used.Compare(a.Used); c != 0 {
			return c
		}
		return strings.Compare(string(a.ID), string(b.ID))
	})
	return repos
}

func (x *Launcher) ticked(r core.Repo) bool {
	return slices.ContainsFunc(x.chosen, func(id core.RepoID) bool { return id == r.ID || string(id) == r.Path })
}

func (x *Launcher) toggleRepo(r core.Repo) {
	if x.ticked(r) {
		x.chosen = slices.DeleteFunc(x.chosen, func(id core.RepoID) bool { return id == r.ID || string(id) == r.Path })
		return
	}
	x.addRepo(r.ID)
}

func (x *Launcher) reposSection(c *ui.Ctx, k core.KindSpec) {
	th := c.Theme()
	about := "one worktree each · same branch"
	if startsFrom(k) == core.FromPR {
		about = "the PR's repo, found by its remote if none"
	}
	kit.Heading(c, "head", "Repositories", about)
	all := x.repoChoices()
	addNew := func(c *ui.Ctx) {
		if widget.Button(c, "addrepo", "+ Add repo", widget.ButtonOpts{Help: "agent/launcher#repositories"}) {
			if err := x.h.Run(context.Background(), "repos.new", registry.Args{"then": kit.Then(kit.Call{Command: "launcher.pick-repo"})}); err != nil {
				x.err = err.Error()
			}
		}
	}
	if len(all) == 0 {
		c.Row("none", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("t", "No repos yet.", kit.UIFont(12.5, th.C(theme.FgFaint)))
			addNew(c)
		})
	} else {
		c.Row("tools", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			if len(all) > repoFilterAfter {
				if r := widget.Input(c, "repofilter", &x.repoFilter, widget.InputOpts{Label: "Filter repos", Placeholder: "⌕ filter repos",
					Help: "agent/launcher#repositories", KeyMode: LauncherKeyMode, TabMoves: true}); r.Cancelled {
					x.repoFilter = ""
				}
			} else {
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			}
			addNew(c)
		})
		q := strings.ToLower(strings.TrimSpace(x.repoFilter))
		shown := slices.DeleteFunc(slices.Clone(all), func(r core.Repo) bool {
			return q != "" && !x.ticked(r) && !strings.Contains(strings.ToLower(string(r.ID)+" "+r.Path), q)
		})
		h := ui.Size{}
		if len(shown) > repoRowsShown {
			h = ui.Px(repoRowsShown * repoRowHeight)
		}
		c.Column("repolist", ui.Style{W: ui.Flex(1), H: h, ScrollY: len(shown) > repoRowsShown, Stroke: th.C(theme.Line), Border: 1, Radius: 6,
			Clip: true, Role: ui.RoleList, Label: "Repositories"}, func(c *ui.Ctx) {
			for _, r := range shown {
				x.repoRow(c, r)
			}
			if len(shown) == 0 {
				c.Row("nomatch", ui.Style{W: ui.Flex(1), H: ui.Px(repoRowHeight), Pad: ui.PadXY(10, 0), Cross: ui.Center}, func(c *ui.Ctx) {
					c.Text("t", "No repo matches.", kit.UIFont(12, th.C(theme.FgFaint)))
				})
			}
		})
		if n := len(x.chosen); n > 0 {
			kit.Note(c, "selected", fmt.Sprintf("%d selected", n))
		}
	}
	if k.Worktrees == core.WorktreesNow || k.Worktrees == core.WorktreesStartWork {
		c.Row("branch", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("l", "Branch", kit.UIFont(12.5, th.C(theme.FgMuted)))
			x.field(c, "branch", &x.branch, "Branch", "named from the ticket and prompt", false, ui.Flex(1), 1)
		})
	}
}

// repoRow is a repo in the launcher's checklist: its box and name, path, default branch, and when it was last used.
func (x *Launcher) repoRow(c *ui.Ctx, r core.Repo) {
	th := c.Theme()
	on := x.ticked(r)
	fill := render.Color{}
	if on {
		fill = th.C(tLaunchCardOn)
	}
	c.Row("r."+string(r.ID), ui.Style{W: ui.Flex(1), H: ui.Px(repoRowHeight), Pad: ui.PadXY(10, 0), Gap: 12, Cross: ui.Center, Fill: fill}, func(c *ui.Ctx) {
		st := widget.CheckOff
		if on {
			st = widget.CheckOn
		}
		c.Row("name", ui.Style{W: ui.Px(170), Clip: true}, func(c *ui.Ctx) {
			if widget.Check(c, "check", st, widget.CheckOpts{Text: string(r.ID), Plain: true, Label: string(r.ID), Help: "agent/launcher#repositories"}) {
				x.toggleRepo(r)
			}
		})
		c.Row("path", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
			c.Text("t", kit.TildePath(r.Path), kit.MonoFont(11, th.C(theme.FgFaint)))
		})
		c.Text("branch", strings.TrimPrefix(r.BaseBranch, "origin/"), kit.MonoFont(10.5, th.C(theme.FgFaint)))
		c.Row("when", ui.Style{W: ui.Px(110), Main: ui.End}, func(c *ui.Ctx) {
			switch {
			case r.ID == x.justAdded:
				widget.Chip(c, "chip", "just added", th.C(theme.OK), "agent/launcher#repositories")
			case isFound(r):
				widget.Chip(c, "chip", "found", th.C(tRepoFound), "agent/launcher#repositories")
			case !r.Used.IsZero():
				widget.Ago(c, "t", r.Used, widget.AgoOpts{Text: kit.MonoFont(10, th.C(theme.FgGhost)), Long: true, Prefix: "used ", Now: time.Now()})
			}
		})
	})
}
