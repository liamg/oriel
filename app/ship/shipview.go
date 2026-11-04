package ship

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

var (
	tShipCard     = kit.TPRCard
	tShipCardOpen = theme.DeclareOn("ship.card.open.border", "alpha(amber, 55%)", "bg.base")
	tShipCardBg   = kit.TPRCardBg
	tShipDone     = theme.Declare("ship.step.done", "ok")
	tShipTodo     = kit.TPRTodo
	tShipRail     = theme.Declare("ship.rail", "line.strong")
	tShipHash     = theme.Declare("ship.commit.hash", "brand")
	tShipLink     = kit.TPRLink
	tShipCI       = kit.TPRRunning
)

func (x *Ship) views() {
	x.Add(x.h.View(kit.ShipView, uireg.ViewFunc(func(v uireg.ViewCtx) ui.Node {
		return &ui.Func{Key: "ship", Fn: func(c *ui.Ctx) { x.page(c) }}
	})))
	x.Add(x.h.View(kit.PullRequestView, uireg.ViewFunc(func(v uireg.ViewCtx) ui.Node {
		return &ui.Func{Key: "pull-request", Fn: func(c *ui.Ctx) { x.checklist(c, v.Project) }}
	})))
	x.Add(x.h.View(kit.GitView, uireg.ViewFunc(func(v uireg.ViewCtx) ui.Node {
		return &ui.Func{Key: "git", Fn: func(c *ui.Ctx) { x.gitPanel(c, v.Project) }}
	})))
	x.Add(x.h.View(kit.PRView, prView{x}))
}

func (x *Ship) run(id string, a registry.Args) {
	if err := x.h.Run(context.Background(), id, a); err != nil {
		if errors.Is(err, kit.ErrStopped) {
			return
		}
		x.h.Log().Error("ship", "command", id, "err", err)
		x.h.Notify(core.Notice{ID: "ship.error", Severity: core.SevError, Key: true, Text: err.Error()})
	}
}

// here is the session in front and a docked view's project ("" is the open one's).
func (x *Ship) here(project string) (core.SessionState, string, bool) {
	w := kit.ProjectsHere(x.h)
	if w.Session == nil {
		return core.SessionState{}, "", false
	}
	if project == "" {
		project = w.Open
	}
	return *w.Session, project, true
}

// --- the Ship page (18) ---

func (x *Ship) page(c *ui.Ctx) {
	th := c.Theme()
	se, _, ok := x.here("")
	c.Column("page", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(theme.BgBase)}, func(c *ui.Ctx) {
		if !ok {
			kit.EmptyNote(c, "Ship is a session's page: open a session with a repo that has a remote.")
			return
		}
		ships, rep := x.sessionShips(se)
		x.pageHeader(c, se, ships)
		c.Row("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
			c.Column("cols", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true, Pad: ui.Insets{L: 24, R: 24, T: 16, B: 24}, Gap: 10}, func(c *ui.Ctx) {
				if rep.err != "" && len(ships) == 0 {
					c.Text("err", rep.err, kit.MonoFont(11, th.C(theme.Error)))
				}
				c.Row("row", ui.Style{W: ui.Flex(1), Gap: 22}, func(c *ui.Ctx) {
					for _, s := range ships {
						c.Column(s.Project, ui.Style{W: ui.Flex(1), Gap: 10}, func(c *ui.Ctx) {
							x.columnHeader(c, se, s)
							widget.Async(c, "load", x.loadState(c, se.ID, s.Project), widget.AsyncOpts{Label: "Reading " + s.Project,
								Retry: func() { x.refresh(se.ID, true) }, Help: "git/ship#page", Loading: func(c *ui.Ctx) {
									widget.Skeleton(c, "skeleton", widget.SkeletonOpts{Block: render.Point{X: 360, Y: 36}, Count: 4, Dir: ui.Column,
										Label: "Reading " + s.Project, Help: "git/ship#page"})
								}}, func(c *ui.Ctx) {
								c.Column("body", ui.Style{W: ui.Flex(1), Gap: 10}, func(c *ui.Ctx) { x.column(c, se, s) })
							})
						})
					}
				})
			})
			c.Box("divider", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
			c.Column("side", ui.Style{W: ui.Px(300), H: ui.Flex(1), Fill: th.C(kit.TShipSide), Pad: ui.PadXY(16, 16), Gap: 8, ScrollY: true}, func(c *ui.Ctx) {
				x.gatesList(c, se)
				x.mergeOrder(c, se, ships)
				x.previous(c, se, "")
				x.ticket(c, se)
				c.Box("gap", ui.Style{H: ui.Flex(1)}, nil)
				widget.Wrap(c, "note", "Commits, pushes and PRs run with your git and gh. The agent can draft, never send.", 266,
					kit.UIFont(11, th.C(theme.FgFaint)))
			})
		})
	})
}

func (x *Ship) pageHeader(c *ui.Ctx, se core.SessionState, ships []shipRead) {
	th := c.Theme()
	raisable, pushable, w := 0, 0, registry.CodeHost{}.Said()
	for _, s := range ships {
		switch {
		case s.ReadOnly || s.Code == nil:
		case s.PR.Open():
			if len(s.Unpushed) > 0 {
				pushable++
			}
		case s.Commits > 0 || len(s.Files) > 0 || s.PR != nil:
			if raisable == 0 {
				w = s.words()
			}
			raisable++
		}
	}
	c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(44), Pad: ui.PadXY(22, 0), Gap: 14, Cross: ui.Center, Fill: th.C(kit.TShipHead)}, func(c *ui.Ctx) {
		c.Text("h", "SHIP", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 14, Weight: typeset.SemiBold, Tracking: .06},
			Color: th.C(theme.Fg)})
		var meta []string
		if se.Ticket != "" {
			meta = append(meta, se.Ticket)
		}
		meta = append(meta, kit.Label(se), kit.Counted(len(se.Projects), "project"))
		if se.Branch != "" {
			meta = append(meta, "branch "+se.Branch)
		}
		c.Row("meta", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
			c.Text("m", strings.Join(meta, " · "), kit.MonoFont(11, th.C(theme.FgFaint)))
		})
		var reads []widget.LoadState
		for _, s := range ships {
			reads = append(reads, x.loadState(c, se.ID, s.Project))
		}
		waiting := widget.Waiting(reads...).Pending()
		if widget.Button(c, "sync", "Sync all", widget.ButtonOpts{Help: "git/ship#sync", Disabled: waiting}) {
			x.run("ship.sync-all", nil)
		}
		if widget.Button(c, "commit", "Commit all", widget.ButtonOpts{Help: "git/ship#commit", Disabled: waiting}) {
			x.run("ship.commit-all", nil)
		}
		label := "Push + raise " + w.Short
		switch {
		case raisable == 2:
			label = "Push + raise both " + w.Short + "s"
		case raisable > 2:
			label = fmt.Sprintf("Push + raise all %d %ss", raisable, w.Short)
		}
		if kit.AllMerged(se, projectShips(ships)) {
			if widget.Button(c, "finish", "Finish session", widget.ButtonOpts{Primary: true, Help: kit.EndHelp, Disabled: waiting}) {
				x.run("session.end", registry.Args{"id": string(se.ID)})
			}
		} else if raisable > 0 {
			if widget.Button(c, "raise", label, widget.ButtonOpts{Primary: true, Help: "git/ship#raise", Disabled: waiting}) {
				x.run("ship.raise-all", nil)
			}
		} else if pushable > 0 {
			if widget.Button(c, "push", "Push all", widget.ButtonOpts{Primary: true, Help: "git/ship#push", Disabled: waiting}) {
				x.run("ship.push-all", nil)
			}
		}
	})
	c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
}

// columnHeader is a project's name, colour, branch and base, and a chip for where it stands.
func (x *Ship) columnHeader(c *ui.Ctx, se core.SessionState, s shipRead) {
	th := c.Theme()
	c.Row("ph", ui.Style{W: ui.Flex(1), H: ui.Px(30), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Box("sq", ui.Style{W: ui.Px(11), H: ui.Px(11), Radius: 2, Fill: kit.TokenColour(th, se.ProjectColour(s.Project), th.C(theme.Accent))}, nil)
		c.Text("n", s.Project, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 14.5, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
		c.Row("br", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
			if s.Branch != "" {
				c.Text("b", s.Branch+" ← "+cmp.Or(s.Base, "?"), kit.MonoFont(11, th.C(theme.FgFaint)))
			}
		})
		if label, tone := statusChip(th, s); label != "" {
			widget.Chip(c, "chip", label, tone, "git/ship#page")
		}
	})
}

// statusChip is where a project stands, as its column's chip.
func statusChip(th *theme.Resolved, s shipRead) (string, render.Color) {
	switch {
	case s.ReadOnly:
		return "read-only", th.C(theme.FgFaint)
	case s.PR != nil && s.PR.Merged:
		return "merged", th.C(theme.StatusPermission)
	case len(s.Conflicts) > 0, len(s.Files) > 0:
		return "needs you", th.C(theme.Error)
	}
	switch ciOf(s.Checks) {
	case registry.CheckFailed:
		return "ci failing", th.C(theme.Error)
	case registry.CheckRunning:
		return "ci running", th.C(tShipCI)
	case registry.CheckOK:
		return "ready", th.C(theme.OK)
	}
	return "", render.Color{}
}

// column is a project's checklist: uncommitted → behind → unpushed → PR and its checks.
func (x *Ship) column(c *ui.Ctx, se core.SessionState, s shipRead) {
	th := c.Theme()
	if s.ReadOnly {
		kit.EmptyNote(c, "This worktree is pinned or read-only: nothing to ship from it.")
		return
	}
	if s.GitError != "" {
		c.Text("gerr", s.GitError, kit.MonoFont(11, th.C(theme.Error)))
	}
	if f := x.failed(se.ID, s.Project); f != "" && x.doing(se.ID, s.Project) == "" {
		widget.Wrap(c, "failed", f, 420, kit.MonoFont(11, th.C(theme.Error)))
	}
	cur := currentStep(s)
	x.stepRow(c, "s1", 1, cur, len(s.Files) == 0, func(c *ui.Ctx) { x.uncommitted(c, se, s, cur == 1) })
	x.stepRow(c, "s2", 2, cur, s.Behind == 0, func(c *ui.Ctx) { x.behind(c, se, s) })
	x.stepRow(c, "s3", 3, cur, len(s.Unpushed) == 0 && s.Pushed, func(c *ui.Ctx) { x.unpushed(c, se, s) })
	x.stepRow(c, "s4", 4, cur, s.PR.Open() && ciOf(s.Checks) == registry.CheckOK || s.PR != nil && s.PR.Merged, func(c *ui.Ctx) { x.prStep(c, se, s) })
}

// currentStep is the first step not done: the one to do next.
func currentStep(s shipRead) int {
	switch {
	case len(s.Files) > 0:
		return 1
	case s.Behind > 0:
		return 2
	case len(s.Unpushed) > 0 || !s.Pushed && s.Commits > 0:
		return 3
	}
	return 4
}

// stepRow is a step's marker on the column's rail, and its card.
func (x *Ship) stepRow(c *ui.Ctx, key string, n, cur int, done bool, card func(c *ui.Ctx)) {
	th := c.Theme()
	c.Row(key, ui.Style{W: ui.Flex(1), Gap: 10}, func(c *ui.Ctx) {
		c.Column("mark", ui.Style{W: ui.Px(20), H: ui.Flex(1), Cross: ui.Center, Pad: ui.Insets{T: 9}}, func(c *ui.Ctx) {
			tone, label := th.C(tShipRail), fmt.Sprint(n)
			switch {
			case done:
				tone, label = th.C(tShipDone), "✓"
			case n == cur:
				tone = th.C(tShipTodo)
			}
			c.Box("dot", ui.Style{W: ui.Px(18), H: ui.Px(18), Radius: 9, Stroke: tone, Border: 1, Main: ui.Center, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("n", label, kit.Mono(9.5, typeset.SemiBold, tone))
			})
			c.Box("line", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(tShipRail)}, nil)
		})
		c.Column("card", ui.Style{W: ui.Flex(1), Pad: ui.Insets{B: 2}}, card)
	})
}

// card is a step's box: its header line, and a body when it's open.
func shipCard(c *ui.Ctx, open bool, head func(c *ui.Ctx), body func(c *ui.Ctx)) {
	th := c.Theme()
	edge := th.C(tShipCard)
	if open {
		edge = th.C(tShipCardOpen)
	}
	c.Column("box", ui.Style{W: ui.Flex(1), Radius: 6, Stroke: edge, Border: 1, Fill: th.C(tShipCardBg), Clip: true}, func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(36), Pad: ui.PadXY(12, 0), Gap: 8, Cross: ui.Center}, head)
		if body != nil {
			c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			c.Column("body", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(12, 10), Gap: 8}, body)
		}
	})
}

// stepTitle is a step's name and the dim note after it.
func stepTitle(c *ui.Ctx, title, note string, at ...widget.AgoPart) {
	th := c.Theme()
	c.Text("t", title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12.5, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
	c.Row("note", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
		if note != "" || len(at) > 0 {
			widget.AgoLine(c, "n", widget.AgoLineOpts{Text: kit.MonoFont(10.5, th.C(theme.FgFaint))}, append([]widget.AgoPart{{Text: note}}, at...)...)
		}
	})
}

func (x *Ship) uncommitted(c *ui.Ctx, se core.SessionState, s shipRead, open bool) {
	th := c.Theme()
	if len(s.Files) == 0 {
		shipCard(c, false, func(c *ui.Ctx) { stepTitle(c, "Uncommitted", "clean") }, nil)
		return
	}
	add, del := 0, 0
	for _, f := range s.Files {
		add, del = add+f.Add, del+f.Del
	}
	f := x.commitForm(se.ID, s.Project)
	led, ledOK := x.ledger(se, s.Project)
	shipCard(c, open, func(c *ui.Ctx) {
		stepTitle(c, "Uncommitted", fmt.Sprintf("%s · +%d -%d", kit.Counted(len(s.Files), "file"), add, del))
		if n := led.Uncommitted.NotRead(); ledOK && n > 0 {
			widget.Chip(c, "unread", fmt.Sprintf("%d %s unreviewed", n, kit.Plural(n, "line", "lines")), th.C(theme.Error), "git/ship#gates")
		}
	}, func(c *ui.Ctx) {
		x.fileRows(c, se, s, f, false)
		x.commitBox(c, se, s, f)
	})
}

// fileRows are a project's uncommitted files, ticked to go in the commit, with what each adds and takes away; with
// discard, each has a button that throws its changes away.
func (x *Ship) fileRows(c *ui.Ctx, se core.SessionState, s shipRead, f *commitForm, discard bool) {
	th := c.Theme()
	led, ledOK := x.ledger(se, s.Project)
	busy := x.doing(se.ID, s.Project) != ""
	for _, fl := range s.Files {
		c.Row(fl.Path, ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Row("cb", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
				st := widget.CheckOn
				if f.skip[fl.Path] {
					st = widget.CheckOff
				}
				if widget.Check(c, "cb", st, widget.CheckOpts{Text: fl.Path, Plain: true, Help: "git/ship#commit",
					Tooltip: "Ticked files go in the commit", Disabled: busy}) {
					f.skip[fl.Path] = !f.skip[fl.Path]
				}
			})
			if fl.Code == "?" {
				c.Text("new", "new", kit.MonoFont(10, th.C(theme.FgFaint)))
			}
			widget.DiffStat(c, "ds", fl.Add, fl.Del, widget.DiffStatOpts{Binary: fl.Binary})
			if discard && widget.IconButton(c, "discard", "↺", widget.IconButtonOpts{Label: "Discard " + fl.Path + "'s changes", Help: "git/ship#git",
				Disabled: busy}) {
				x.run("git.discard", registry.Args{"path": filepath.Join(sessionProjectDir(se, s.Project), fl.Path)})
			}
			if lf, ok := led.File(fl.Path); ledOK && ok && lf.Uncommitted.Total() > 0 {
				c.Row("read", ui.Style{W: ui.Px(64), Main: ui.End}, func(c *ui.Ctx) {
					if n := lf.Uncommitted; n.NotRead() == 0 {
						c.Text("r", "reviewed ✓", kit.MonoFont(10, th.C(theme.OK)))
					} else {
						c.Text("r", fmt.Sprintf("%d / %d", n.Read, n.Total()), kit.MonoFont(10, th.C(theme.Warn)))
					}
				})
			}
		})
	}
}

// draftChip says who wrote a draft, and whether the code has moved on since: "drafted by codex · stale".
func draftChip(c *ui.Ctx, d host.Draft) {
	th := c.Theme()
	text, tone := "drafted by "+d.By, th.C(tShipTodo)
	if d.By == host.DraftByYou {
		text, tone = "written by you", th.C(theme.FgMuted)
	}
	if d.Stale {
		text, tone = text+" · stale", th.C(theme.Warn)
	}
	widget.Chip(c, "drafted", text, tone, "git/ship#drafts")
}

// commitBox is the commit message, filled with the agent's draft until you type, and its buttons.
func (x *Ship) commitBox(c *ui.Ctx, se core.SessionState, s shipRead, f *commitForm) {
	th := c.Theme()
	if !f.edited {
		f.message = s.CommitDraft.Body
	}
	c.Column("msg", ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) {
		if !f.edited && s.CommitDraft.By != "" {
			c.Row("by", ui.Style{W: ui.Flex(1), Main: ui.End}, func(c *ui.Ctx) { draftChip(c, s.CommitDraft) })
		}
		r := widget.Prose(c, "message", &f.message, widget.ProseOpts{Label: "Commit message", Placeholder: "The commit message (the agent drafts one when it's idle)",
			Lines: 10, MinLines: 4, Help: "git/ship#commit", Disabled: x.doing(se.ID, s.Project) != ""})
		first := r.Changed && !f.edited
		if r.Changed {
			f.edited = true
		} else if r.Submitted {
			x.run("ship.commit", registry.Args{"project": s.Project})
		}
		x.keepTyped(c, &f.save, r.Changed, first, host.DraftReq{Session: se.ID, Project: s.Project, Kind: "commit", Body: f.message})
	})
	c.Row("buttons", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
		for _, g := range f.held {
			if g.Review != "" && g.ReviewLabel != "" {
				if widget.Button(c, "review."+g.Gate.Name, g.ReviewLabel, widget.ButtonOpts{Primary: true, Help: "git/ship#gates"}) {
					x.run(g.Review, g.ReviewArgs)
				}
			}
		}
		if f.veto == "" {
			if widget.Button(c, "commit", "Commit", x.stepOpts(se.ID, s.Project, "Committing", widget.ButtonOpts{Primary: true, Help: "git/ship#commit"})) {
				x.run("ship.commit", registry.Args{"project": s.Project})
			}
		} else if widget.Button(c, "anyway", "Commit anyway…", x.stepOpts(se.ID, s.Project, "Committing", widget.ButtonOpts{Help: "git/ship#gates"})) {
			x.run("ship.commit", registry.Args{"project": s.Project, "force": true})
		}
		if se.Agent.Provider != "" && widget.Button(c, "redraft", "Redraft", widget.ButtonOpts{Help: "git/ship#drafts", Disabled: !x.h.Available("ship.redraft", nil)}) {
			x.run("ship.redraft", registry.Args{"project": s.Project, "kind": "commit"})
		}
		c.Row("gate", ui.Style{W: ui.Flex(1), Clip: true, Main: ui.End}, func(c *ui.Ctx) {
			if x.gated(se, s.Project) {
				c.Text("on", "review gate on", kit.MonoFont(10, th.C(theme.FgGhost)))
			}
		})
	})
	if f.veto != "" {
		widget.Wrap(c, "veto", f.veto, 520, kit.MonoFont(10.5, th.C(theme.Warn)))
	}
}

func (x *Ship) behind(c *ui.Ctx, se core.SessionState, s shipRead) {
	th := c.Theme()
	base := cmp.Or(s.Base, "base")
	if s.Behind == 0 {
		var rebased []widget.AgoPart
		if !s.Synced.IsZero() {
			rebased = append(rebased, widget.AgoPart{Text: "rebased ", At: s.Synced, Long: true})
		}
		shipCard(c, false, func(c *ui.Ctx) { stepTitle(c, "Up to date with "+base, "", rebased...) }, nil)
		return
	}
	shipCard(c, false, func(c *ui.Ctx) {
		note := kit.Counted(s.Behind, "commit") + " · no conflicts predicted"
		if len(s.Conflicts) > 0 {
			note = kit.Counted(s.Behind, "commit")
		}
		stepTitle(c, "Behind "+base, note)
		if widget.Button(c, "rebase", "Rebase", x.stepOpts(se.ID, s.Project, "Rebasing", widget.ButtonOpts{Help: "git/ship#sync"})) {
			x.run("ship.sync", registry.Args{"project": s.Project})
		}
	}, func() func(c *ui.Ctx) {
		if len(s.Conflicts) == 0 {
			return nil
		}
		return func(c *ui.Ctx) {
			widget.Wrap(c, "conf", "conflicts predicted in "+strings.Join(s.Conflicts, ", ")+": a rebase stops and is undone", 420,
				kit.MonoFont(10.5, th.C(theme.Warn)))
			if t, ok := conflictTrouble(s); ok {
				c.Row("twins", ui.Style{Gap: 8}, func(c *ui.Ctx) {
					kit.AskTwinsLabelled(c, x.h, "ship.conflict."+s.Project, t, "git/ship#sync", "Explain with agent", "Fix conflicts with agent")
				})
				kit.ExplainKept(c, x.h, "ship.conflict."+s.Project, "git/ship#sync")
			}
		}
	}())
}

// pushLabel is Push, or Force push once a rebase has rewritten what origin has (gitx.Repo.Push leases it).
func pushLabel(s shipRead) string {
	if s.Replaced > 0 {
		return "Force push"
	}
	return "Push"
}

func (x *Ship) unpushed(c *ui.Ctx, se core.SessionState, s shipRead) {
	th := c.Theme()
	if len(s.Unpushed) == 0 {
		note := "nothing committed yet"
		title := "Unpushed"
		if s.Pushed {
			title, note = "Pushed", kit.Counted(s.Commits, "commit")
		}
		shipCard(c, false, func(c *ui.Ctx) { stepTitle(c, title, note) }, nil)
		return
	}
	shipCard(c, false, func(c *ui.Ctx) {
		note := kit.Counted(len(s.Unpushed), "commit")
		if s.Replaced > 0 {
			note += " · rebased: replaces " + kit.Counted(s.Replaced, "commit") + " on origin"
		}
		stepTitle(c, "Unpushed", note)
		if s.Remote && widget.Button(c, "push", pushLabel(s), x.stepOpts(se.ID, s.Project, "Pushing", widget.ButtonOpts{Help: "git/ship#push"})) {
			x.run("ship.push", registry.Args{"project": s.Project})
		}
	}, func(c *ui.Ctx) {
		for i, cm := range s.Unpushed {
			c.Row(i, ui.Style{W: ui.Flex(1), Gap: 10, Clip: true}, func(c *ui.Ctx) {
				c.Text("h", cm.Hash, kit.MonoFont(11, th.C(tShipHash)))
				c.Text("s", cm.Subject, kit.MonoFont(11, th.C(theme.FgMuted)))
			})
		}
	})
}

// gatesList is "before this ships": every gate's state for the session.
func (x *Ship) gatesList(c *ui.Ctx, se core.SessionState) {
	th := c.Theme()
	kit.SectionCap(c, "BEFORE THIS SHIPS")
	gs := registry.ShipGates(x.h, string(se.ID), string(se.Kind))
	if len(gs) == 0 {
		c.Text("none", "No gates for this session.", kit.UIFont(12, th.C(theme.FgFaint)))
	}
	for _, g := range gs {
		c.Column(g.Name, ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
			c.Row("row", ui.Style{W: ui.Flex(1), H: ui.Px(30), Gap: 10, Cross: ui.Center, Role: ui.RoleText, Label: g.Title + " " + g.State},
				func(c *ui.Ctx) {
					glyph, tone := "✓", th.C(theme.OK)
					switch g.State {
					case registry.GateWarn:
						glyph, tone = "!", th.C(theme.Warn)
					case registry.GateFail:
						glyph, tone = "✗", th.C(theme.Error)
					case registry.GatePending, registry.GateOptional:
						glyph, tone = "○", th.C(theme.FgFaint)
					case registry.GateSkipped:
						glyph, tone = "–", th.C(theme.FgFaint)
					}
					title := th.C(theme.FgMuted)
					if g.State == registry.GateSkipped {
						title = th.C(theme.FgFaint)
					}
					c.Box("g", ui.Style{W: ui.Px(14)}, func(c *ui.Ctx) { c.Text("g", glyph, kit.UIFont(13, tone)) })
					c.Row("t", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("t", g.Title, kit.UIFont(12.5, title)) })
					noteTone := th.C(theme.FgFaint)
					if g.State == registry.GateWarn {
						noteTone = th.C(theme.Warn)
					}
					c.Text("n", g.Note, kit.MonoFont(10.5, noteTone))
				})
			if g.Action != "" {
				c.Row("act", ui.Style{Pad: ui.Insets{L: 24, B: 6}}, func(c *ui.Ctx) {
					if widget.Button(c, "act", g.Action, widget.ButtonOpts{Disabled: !x.h.Available(g.Command, g.Args), Help: "git/ship#gates"}) {
						x.run(g.Command, g.Args)
					}
				})
			}
		})
	}
	c.Box("sp", ui.Style{H: ui.Px(6)}, nil)
	c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
	c.Box("sp2", ui.Style{H: ui.Px(6)}, nil)
}

// mergeOrder is the session's PRs in the order they merge: its projects' order.
func (x *Ship) mergeOrder(c *ui.Ctx, se core.SessionState, ships []shipRead) {
	th := c.Theme()
	var rows []shipRead
	for _, s := range ships {
		if !s.ReadOnly && s.Code != nil {
			rows = append(rows, s)
		}
	}
	if len(rows) < 2 {
		return
	}
	kit.SectionCap(c, "MERGE ORDER")
	for i, s := range rows {
		c.Row(s.Project, ui.Style{W: ui.Flex(1), H: ui.Px(22), Gap: 6, Cross: ui.Center}, func(c *ui.Ctx) {
			pr := s.words().Sigil + "—"
			if s.PR != nil {
				pr = s.words().Sigil + strconv.Itoa(s.PR.Number)
			}
			c.Text("t", fmt.Sprintf("%d · %s → %s", i+1, s.Project, pr), kit.MonoFont(11, th.C(theme.FgMuted)))
		})
	}
	c.Box("sp", ui.Style{H: ui.Px(6)}, nil)
	c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
	c.Box("sp2", ui.Style{H: ui.Px(6)}, nil)
}

// previous is the PRs the session has moved on from, newest first: one project's, or every project's for "".
// Each opens on its code host.
func (x *Ship) previous(c *ui.Ctx, se core.SessionState, project string) {
	th := c.Theme()
	var past []core.PastPR
	for _, pr := range slices.Backward(se.Past) {
		if project == "" || pr.Project == project {
			past = append(past, pr)
		}
	}
	if len(past) == 0 {
		return
	}
	kit.SectionCap(c, "PREVIOUS")
	for i, pr := range past {
		c.Row(i, ui.Style{W: ui.Flex(1), H: ui.Px(24), Gap: 6, Cross: ui.Center}, func(c *ui.Ctx) {
			state := cmp.Or(pr.State, "merged here")
			widget.Chip(c, "state", state, kit.PRStateTone(c, state), "git/ship#next")
			c.Row("t", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
				label := pr.Branch
				if pr.Number > 0 {
					label = fmt.Sprintf("#%d · %s", pr.Number, cmp.Or(pr.Title, pr.Branch))
				}
				if project == "" {
					label = pr.Project + " " + label
				}
				if pr.URL == "" {
					c.Text("l", label, kit.MonoFont(11, th.C(theme.FgMuted)))
					return
				}
				if widget.Link(c, "l", label, "git/ship#next") {
					x.run("ship.browse", registry.Args{"project": pr.Project, "number": pr.Number})
				}
			})
		})
	}
	c.Box("sp", ui.Style{H: ui.Px(6)}, nil)
}

func (x *Ship) ticket(c *ui.Ctx, se core.SessionState) {
	if se.Ticket == "" {
		return
	}
	th := c.Theme()
	kit.SectionCap(c, "TICKET")
	c.Text("t", se.Ticket, kit.MonoFont(11.5, th.C(tShipLink)))
}

// --- the dock: Pull request (01h) and Git ---

// checklist is a project's Pull request panel: committed → up to date → pushed → PR → checks, in a line each.
func (x *Ship) checklist(c *ui.Ctx, project string) {
	se, p, ok := x.here(project)
	c.Column("pr", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.PadXY(12, 10), Gap: 4, ScrollY: true}, func(c *ui.Ctx) {
		if !ok {
			kit.EmptyNote(c, "Pull request is a session's project's: open a session.")
			return
		}
		widget.Async(c, "load", x.loadState(c, se.ID, p), widget.AsyncOpts{Label: "Reading " + p, Retry: func() { x.refresh(se.ID, true) },
			Help: "git/ship#pr"}, func(c *ui.Ctx) {
			c.Column("body", ui.Style{W: ui.Flex(1), Gap: 4}, func(c *ui.Ctx) { x.checklistBody(c, se, p) })
		})
	})
}
