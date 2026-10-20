package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

var (
	tShipHead     = theme.Declare("ship.header.bg", "bg.surface")
	tShipCard     = theme.Declare("ship.card.border", "line.strong")
	tShipCardOpen = theme.DeclareOn("ship.card.open.border", "alpha(amber, 55%)", "bg.base")
	tShipCardBg   = theme.Declare("ship.card.bg", "bg.base")
	tShipDone     = theme.Declare("ship.step.done", "ok")
	tShipTodo     = theme.Declare("ship.step.todo", "amber")
	tShipRail     = theme.Declare("ship.rail", "line.strong")
	tShipHash     = theme.Declare("ship.commit.hash", "brand")
	tShipLink     = theme.Declare("ship.link", "violet")
	tShipCI       = theme.Declare("ship.ci.running", "status.ci")
	tShipSide     = theme.Declare("ship.side.bg", "bg.surface")
	tShipCapFg    = theme.Declare("ship.caption", "fg.faint")
)

func (x *shipPlugin) views() {
	x.add(x.h.View(ShipView, registry.ViewFunc(func(v registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "ship", Fn: func(c *ui.Ctx) { x.page(c) }}
	})))
	x.add(x.h.View(PullRequestView, registry.ViewFunc(func(v registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "pull-request", Fn: func(c *ui.Ctx) { x.checklist(c, v.Project) }}
	})))
	x.add(x.h.View(GitView, registry.ViewFunc(func(v registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "git", Fn: func(c *ui.Ctx) { x.gitPanel(c, v.Project) }}
	})))
	x.add(x.h.View(PRView, prView{x}))
}

func (x *shipPlugin) run(id string, a registry.Args) {
	if err := x.h.Run(context.Background(), id, a); err != nil {
		if errors.Is(err, errStopped) {
			return
		}
		x.h.Log().Error("ship", "command", id, "err", err)
		x.h.Notify(core.Notice{ID: "ship.error", Severity: core.SevError, Key: true, Text: err.Error()})
	}
}

// here is the session in front and a docked view's project ("" is the open one's).
func (x *shipPlugin) here(project string) (core.SessionState, string, bool) {
	w := projectsHere(x.h)
	if w.session == nil {
		return core.SessionState{}, "", false
	}
	if project == "" {
		project = w.open
	}
	return *w.session, project, true
}

// shipCap is a section's caption: "CHECKS · 5 OF 7".
func shipCap(c *ui.Ctx, s string) { c.Text(s, s, capText(c.Theme().C(tShipCapFg))) }

// empty says why there's nothing to show.
func shipEmpty(c *ui.Ctx, msg string) {
	c.Column("empty", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(16, 14)}, func(c *ui.Ctx) {
		widget.Wrap(c, "t", msg, 280, uiFont(12, c.Theme().C(theme.FgFaint)))
	})
}

// --- the Ship page (18) ---

func (x *shipPlugin) page(c *ui.Ctx) {
	th := c.Theme()
	se, _, ok := x.here("")
	c.Column("page", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(theme.BgBase)}, func(c *ui.Ctx) {
		if !ok {
			shipEmpty(c, "Ship is a session's page: open a session with a repo that has a remote.")
			return
		}
		ships, rep := x.sessionShips(se)
		x.pageHeader(c, se, ships)
		c.Row("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
			c.Column("cols", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true, Pad: ui.Insets{L: 24, R: 24, T: 16, B: 24}, Gap: 10}, func(c *ui.Ctx) {
				if rep.err != "" && len(ships) == 0 {
					c.Text("err", rep.err, monoFont(11, th.C(theme.Error)))
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
			c.Column("side", ui.Style{W: ui.Px(300), H: ui.Flex(1), Fill: th.C(tShipSide), Pad: ui.PadXY(16, 16), Gap: 8, ScrollY: true}, func(c *ui.Ctx) {
				x.gatesList(c, se)
				x.mergeOrder(c, se, ships)
				x.ticket(c, se)
				c.Box("gap", ui.Style{H: ui.Flex(1)}, nil)
				widget.Wrap(c, "note", "Commits, pushes and PRs run with your git and gh. The agent can draft, never send.", 266,
					uiFont(11, th.C(theme.FgFaint)))
			})
		})
	})
}

func (x *shipPlugin) pageHeader(c *ui.Ctx, se core.SessionState, ships []shipRead) {
	th := c.Theme()
	raisable, w := 0, registry.CodeHost{}.Said()
	for _, s := range ships {
		if !s.ReadOnly && s.Code != nil && (s.Commits > 0 || len(s.Files) > 0 || s.PR != nil) {
			if raisable == 0 {
				w = s.words()
			}
			raisable++
		}
	}
	c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(44), Pad: ui.PadXY(22, 0), Gap: 14, Cross: ui.Center, Fill: th.C(tShipHead)}, func(c *ui.Ctx) {
		c.Text("h", "SHIP", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 14, Weight: typeset.SemiBold, Tracking: .06},
			Color: th.C(theme.Fg)})
		var meta []string
		if se.Ticket != "" {
			meta = append(meta, se.Ticket)
		}
		meta = append(meta, cmpOr(se.Name, string(se.ID)), counted(len(se.Projects), "project"))
		if se.Branch != "" {
			meta = append(meta, "branch "+se.Branch)
		}
		c.Row("meta", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
			c.Text("m", strings.Join(meta, " · "), monoFont(11, th.C(theme.FgFaint)))
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
		if allMerged(ships) {
			if widget.Button(c, "finish", "Finish session", widget.ButtonOpts{Primary: true, Help: endHelp, Disabled: waiting}) {
				x.run("session.end", registry.Args{"id": string(se.ID)})
			}
		} else if widget.Button(c, "raise", label, widget.ButtonOpts{Primary: true, Help: "git/ship#raise", Disabled: raisable == 0 || waiting}) {
			x.run("ship.raise-all", nil)
		}
	})
	c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
}

// columnHeader is a project's name, colour, branch and base, and a chip for where it stands.
func (x *shipPlugin) columnHeader(c *ui.Ctx, se core.SessionState, s shipRead) {
	th := c.Theme()
	c.Row("ph", ui.Style{W: ui.Flex(1), H: ui.Px(30), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Box("sq", ui.Style{W: ui.Px(11), H: ui.Px(11), Radius: 2, Fill: tokenColour(th, se.ProjectColour(s.Project), th.C(theme.Accent))}, nil)
		c.Text("n", s.Project, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 14.5, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
		c.Row("br", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
			if s.Branch != "" {
				c.Text("b", s.Branch+" ← "+cmpOr(s.Base, "?"), monoFont(11, th.C(theme.FgFaint)))
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
func (x *shipPlugin) column(c *ui.Ctx, se core.SessionState, s shipRead) {
	th := c.Theme()
	if s.ReadOnly {
		shipEmpty(c, "This worktree is pinned or read-only: nothing to ship from it.")
		return
	}
	if s.GitError != "" {
		c.Text("gerr", s.GitError, monoFont(11, th.C(theme.Error)))
	}
	if f := x.failed(se.ID, s.Project); f != "" && x.doing(se.ID, s.Project) == "" {
		widget.Wrap(c, "failed", f, 420, monoFont(11, th.C(theme.Error)))
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
func (x *shipPlugin) stepRow(c *ui.Ctx, key string, n, cur int, done bool, card func(c *ui.Ctx)) {
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
				c.Text("n", label, mono(9.5, typeset.SemiBold, tone))
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
func stepTitle(c *ui.Ctx, title, note string) {
	th := c.Theme()
	c.Text("t", title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12.5, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
	c.Row("note", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
		if note != "" {
			c.Text("n", note, monoFont(10.5, th.C(theme.FgFaint)))
		}
	})
}

func (x *shipPlugin) uncommitted(c *ui.Ctx, se core.SessionState, s shipRead, open bool) {
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
		stepTitle(c, "Uncommitted", fmt.Sprintf("%s · +%d -%d", counted(len(s.Files), "file"), add, del))
		if n := led.Uncommitted.NotRead(); ledOK && n > 0 {
			widget.Chip(c, "unread", fmt.Sprintf("%d %s unreviewed", n, plural(n, "line", "lines")), th.C(theme.Error), "git/ship#gates")
		}
	}, func(c *ui.Ctx) {
		x.fileRows(c, se, s, f, false)
		x.commitBox(c, se, s, f)
	})
}

// fileRows are a project's uncommitted files, ticked to go in the commit, with what each adds and takes away; with
// discard, each has a button that throws its changes away.
func (x *shipPlugin) fileRows(c *ui.Ctx, se core.SessionState, s shipRead, f *commitForm, discard bool) {
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
				c.Text("new", "new", monoFont(10, th.C(theme.FgFaint)))
			}
			widget.DiffStat(c, "ds", fl.Add, fl.Del, widget.DiffStatOpts{Binary: fl.Binary})
			if discard && widget.IconButton(c, "discard", "↺", widget.IconButtonOpts{Label: "Discard " + fl.Path + "'s changes", Help: "git/ship#git",
				Disabled: busy}) {
				x.run("git.discard", registry.Args{"path": filepath.Join(sessionProjectDir(se, s.Project), fl.Path)})
			}
			if lf, ok := led.File(fl.Path); ledOK && ok && lf.Uncommitted.Total() > 0 {
				c.Row("read", ui.Style{W: ui.Px(64), Main: ui.End}, func(c *ui.Ctx) {
					if n := lf.Uncommitted; n.NotRead() == 0 {
						c.Text("r", "reviewed ✓", monoFont(10, th.C(theme.OK)))
					} else {
						c.Text("r", fmt.Sprintf("%d / %d", n.Read, n.Total()), monoFont(10, th.C(theme.Warn)))
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
func (x *shipPlugin) commitBox(c *ui.Ctx, se core.SessionState, s shipRead, f *commitForm) {
	th := c.Theme()
	if !f.edited {
		f.message = s.CommitDraft.Body
	}
	c.Column("msg", ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) {
		if !f.edited && s.CommitDraft.By != "" {
			c.Row("by", ui.Style{W: ui.Flex(1), Main: ui.End}, func(c *ui.Ctx) { draftChip(c, s.CommitDraft) })
		}
		r := widget.Input(c, "message", &f.message, widget.InputOpts{Label: "Commit message", Placeholder: "The commit message (the agent drafts one when it's idle)",
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
				c.Text("on", "review gate on", monoFont(10, th.C(theme.FgGhost)))
			}
		})
	})
	if f.veto != "" {
		widget.Wrap(c, "veto", f.veto, 520, monoFont(10.5, th.C(theme.Warn)))
	}
}

func (x *shipPlugin) behind(c *ui.Ctx, se core.SessionState, s shipRead) {
	th := c.Theme()
	base := cmpOr(s.Base, "base")
	if s.Behind == 0 {
		note := ""
		if !s.Synced.IsZero() {
			note = "rebased " + agoText(c.Now(), s.Synced)
		}
		shipCard(c, false, func(c *ui.Ctx) { stepTitle(c, "Up to date with "+base, note) }, nil)
		return
	}
	shipCard(c, false, func(c *ui.Ctx) {
		note := counted(s.Behind, "commit") + " · no conflicts predicted"
		if len(s.Conflicts) > 0 {
			note = counted(s.Behind, "commit")
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
				monoFont(10.5, th.C(theme.Warn)))
			if t, ok := conflictTrouble(s); ok {
				c.Row("twins", ui.Style{Gap: 8}, func(c *ui.Ctx) {
					askTwinsLabelled(c, x.h, "ship.conflict."+s.Project, t, "git/ship#sync", "Explain with agent", "Fix conflicts with agent")
				})
				explainKept(c, x.sh, "ship.conflict."+s.Project, "git/ship#sync")
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

func (x *shipPlugin) unpushed(c *ui.Ctx, se core.SessionState, s shipRead) {
	th := c.Theme()
	if len(s.Unpushed) == 0 {
		note := "nothing committed yet"
		title := "Unpushed"
		if s.Pushed {
			title, note = "Pushed", counted(s.Commits, "commit")
		}
		shipCard(c, false, func(c *ui.Ctx) { stepTitle(c, title, note) }, nil)
		return
	}
	shipCard(c, false, func(c *ui.Ctx) {
		note := counted(len(s.Unpushed), "commit")
		if s.Replaced > 0 {
			note += " · rebased: replaces " + counted(s.Replaced, "commit") + " on origin"
		}
		stepTitle(c, "Unpushed", note)
		if s.Remote && widget.Button(c, "push", pushLabel(s), x.stepOpts(se.ID, s.Project, "Pushing", widget.ButtonOpts{Help: "git/ship#push"})) {
			x.run("ship.push", registry.Args{"project": s.Project})
		}
	}, func(c *ui.Ctx) {
		for i, cm := range s.Unpushed {
			c.Row(i, ui.Style{W: ui.Flex(1), Gap: 10, Clip: true}, func(c *ui.Ctx) {
				c.Text("h", cm.Hash, monoFont(11, th.C(tShipHash)))
				c.Text("s", cm.Subject, monoFont(11, th.C(theme.FgMuted)))
			})
		}
	})
}

// prStep is the PR: not raised, with its draft and reviewers; or raised, with its checks.
func (x *shipPlugin) prStep(c *ui.Ctx, se core.SessionState, s shipRead) {
	th := c.Theme()
	w := s.words()
	if s.Code == nil || s.Unkeyed {
		note := "no remote"
		switch {
		case s.Unkeyed:
			note = "needs your " + s.Code.Label() + " token for " + s.Site.Host
		case s.Unknown != "":
			note = s.Unknown + " isn't a code host Oriel knows yet"
		case s.Remote:
			note = "origin isn't on a code host Oriel knows"
		}
		shipCard(c, false, func(c *ui.Ctx) {
			stepTitle(c, sentence(w.Long), note)
			if (s.Unkeyed || s.Unknown != "") && widget.Button(c, "setup", "Set up…", widget.ButtonOpts{Help: codeSetupHelp}) {
				x.run("code.setup", setupArgs(s))
			}
		}, nil)
		return
	}
	if s.PR == nil {
		f := x.prForm(se.ID, s)
		note := "not raised"
		switch {
		case f.edited:
			note += " · draft ready"
		case !s.PRDraft.Empty() && s.PRDraft.Stale:
			note += " · draft stale"
		case !s.PRDraft.Empty():
			note += " · draft ready"
		}
		shipCard(c, false, func(c *ui.Ctx) {
			stepTitle(c, sentence(w.Long), note)
			if s.Template != "" {
				widget.Chip(c, "tmpl", "template filled", th.C(tShipTodo), "git/ship#raise")
			}
		}, func(c *ui.Ctx) {
			x.reviewersLine(c, s, f.reviewersOf(s), s.Owners)
			x.links(c, se, s)
			c.Row("buttons", ui.Style{W: ui.Flex(1), Gap: 8}, func(c *ui.Ctx) {
				if widget.Button(c, "raise", "Raise "+w.Short+"…", widget.ButtonOpts{Help: "git/ship#raise"}) {
					x.run("ship.pr.open", registry.Args{"project": s.Project})
				}
			})
			if s.HostError != "" {
				widget.Wrap(c, "gherr", s.HostError, 420, monoFont(10.5, th.C(theme.Error)))
			}
		})
		return
	}
	shipCard(c, s.PR.Open() && ciOf(s.Checks) != registry.CheckOK, func(c *ui.Ctx) {
		c.Row("t", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
			c.Text("t", fmt.Sprintf("%s%d · %s", w.Sigil, s.PR.Number, s.PR.Title),
				ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12.5, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
		})
		prStateChip(c, s.PR)
	}, func(c *ui.Ctx) {
		x.checks(c, s)
		x.reviewersLine(c, s, s.PR.Reviewers, s.Owners)
		x.links(c, se, s)
		c.Row("buttons", ui.Style{W: ui.Flex(1), Gap: 8}, func(c *ui.Ctx) {
			x.readyButton(c, se, s)
			if s.PR.Open() && widget.Button(c, "edit", "Edit "+w.Short, widget.ButtonOpts{Help: "git/ship#raise"}) {
				x.run("ship.pr.open", registry.Args{"project": s.Project, "edit": true})
			}
			x.mergeButton(c, se, s)
			x.prActions(c, se, s)
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if widget.Button(c, "browse", "Open on "+s.Code.Label()+" ↗", widget.ButtonOpts{Help: "git/ship#pr"}) {
				x.run("ship.browse", registry.Args{"project": s.Project})
			}
		})
		if t, ok := ciTrouble(s); ok {
			c.Row("ci", ui.Style{W: ui.Flex(1), Gap: 8}, func(c *ui.Ctx) {
				askTwinsLabelled(c, x.h, "ship.ci."+s.Project, t, "git/ship#checks", "Explain with agent", "Fix CI with agent")
			})
		}
		explainKept(c, x.sh, "ship.ci."+s.Project, "git/ship#checks")
	})
}

// prStateChip is where a PR stands: open, draft, merged or closed.
func prStateChip(c *ui.Ctx, pr *registry.PR) {
	th := c.Theme()
	label, tone := "open", th.C(theme.OK)
	switch {
	case pr.Merged:
		label, tone = "merged", th.C(theme.StatusPermission)
	case pr.State == "closed":
		label, tone = "closed", th.C(theme.FgFaint)
	case pr.Draft:
		label, tone = "draft", th.C(theme.FgMuted)
	}
	widget.Chip(c, "state", label, tone, "git/ship#pr")
}

// readyButton is Ready for review, on a draft PR.
func (x *shipPlugin) readyButton(c *ui.Ctx, se core.SessionState, s shipRead) {
	if s.PR.Open() && s.PR.Draft && widget.Button(c, "ready", "Ready for review", x.stepOpts(se.ID, s.Project, "Marking ready", widget.ButtonOpts{Help: "git/ship#pr"})) {
		x.run("ship.ready", registry.Args{"project": s.Project})
	}
}

// mergeButton is Merge…, on an open PR whose code host merges.
func (x *shipPlugin) mergeButton(c *ui.Ctx, se core.SessionState, s shipRead) {
	if s.PR.Open() && s.Code != nil && s.Code.Merge != nil && widget.Button(c, "merge", "Merge…", x.stepOpts(se.ID, s.Project, "Merging", widget.ButtonOpts{Help: mergeHelp})) {
		x.run("ship.merge", registry.Args{"project": s.Project})
	}
}

// prActions are the buttons plugins add to a raised PR (registry.PRAction).
func (x *shipPlugin) prActions(c *ui.Ctx, se core.SessionState, s shipRead) {
	if s.PR == nil {
		return
	}
	pr := registry.PullRequest{Session: string(se.ID), Project: s.Project, Repo: s.Repo, Number: s.PR.Number, Title: s.PR.Title,
		URL: s.PR.URL, State: s.PR.State, Draft: s.PR.Draft, Merged: s.PR.Merged}
	for _, a := range registry.PRActions(x.h, pr) {
		args := a.RunArgs()
		if widget.Button(c, "action."+a.Name, a.Label, widget.ButtonOpts{Disabled: !x.h.Available(a.Command, args), Help: cmpOr(a.Help, "git/ship#pr")}) {
			x.run(a.Command, args)
		}
	}
}

// checks lists a PR's checks, with what the first failing one's log says.
func (x *shipPlugin) checks(c *ui.Ctx, s shipRead) {
	th := c.Theme()
	pass := 0
	for _, ch := range s.Checks {
		if ch.State == registry.CheckOK || ch.State == registry.CheckSkipped {
			pass++
		}
	}
	if len(s.Checks) == 0 {
		shipCap(c, "CHECKS · NONE YET")
		return
	}
	shipCap(c, fmt.Sprintf("CHECKS · %d OF %d", pass, len(s.Checks)))
	now := c.Now()
	for i, ch := range s.Checks {
		c.Row(i, ui.Style{W: ui.Flex(1), H: ui.Px(22), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			glyph, tone, note := "✓", th.C(theme.OK), dur(ch.Took(now))
			switch ch.State {
			case registry.CheckFailed:
				glyph, tone = "✗", th.C(theme.Error)
			case registry.CheckSkipped:
				glyph, tone = "–", th.C(theme.FgFaint)
			case registry.CheckRunning, registry.CheckQueued:
				glyph, tone = "◌", th.C(tShipCI)
				note = "queued"
				if ch.State == registry.CheckRunning {
					note = "running " + dur(ch.Took(now))
				}
			}
			c.Box("g", ui.Style{W: ui.Px(14)}, func(c *ui.Ctx) { c.Text("g", glyph, uiFont(12, tone)) })
			c.Row("n", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("n", ch.Name, uiFont(12, th.C(theme.FgMuted))) })
			c.Text("d", note, monoFont(10.5, th.C(theme.FgFaint)))
		})
	}
	if s.FailLine != "" {
		c.Row("fail", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 22}, Clip: true}, func(c *ui.Ctx) {
			c.Text("f", s.FailLine, monoFont(10.5, th.C(theme.Error)))
		})
	}
}

// dur is a check's time: "38s", "1m 42s".
func dur(d time.Duration) string {
	d = d.Round(time.Second)
	if d <= 0 {
		return ""
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// reviewersLine is who reviews: each with their initials, CODEOWNERS' marked.
func (x *shipPlugin) reviewersLine(c *ui.Ctx, s shipRead, reviewers, owners []string) {
	th := c.Theme()
	c.Row("reviewers", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center, Clip: true}, func(c *ui.Ctx) {
		shipCap(c, "REVIEWERS")
		if len(reviewers) == 0 {
			c.Text("none", "none yet", uiFont(12, th.C(theme.FgFaint)))
			return
		}
		for _, r := range reviewers {
			c.Row(r, ui.Style{Gap: 6, Cross: ui.Center}, func(c *ui.Ctx) {
				reviewerAvatar(c, r)
				c.Text("n", shortName(r), uiFont(12, th.C(theme.FgMuted)))
			})
		}
		if len(owners) > 0 && len(slices.DeleteFunc(slices.Clone(reviewers), func(r string) bool { return !slices.Contains(owners, r) })) > 0 {
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			c.Text("co", "CODEOWNERS", monoFont(10, th.C(theme.FgFaint)))
		}
	})
}

// shortName is a reviewer as shown: a team without its org.
func shortName(r string) string {
	if _, t, ok := strings.Cut(r, "/"); ok {
		return t
	}
	return r
}

// reviewerAvatar is a reviewer's initials on a tint.
func reviewerAvatar(c *ui.Ctx, who string) {
	th := c.Theme()
	tone := th.C(tShipLink)
	if !strings.Contains(who, "/") {
		tone = th.C(tShipTodo)
	}
	c.Box("av", ui.Style{W: ui.Px(17), H: ui.Px(17), Radius: 4, Fill: tone, Main: ui.Center, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Text("i", projectInitials(shortName(who)), mono(8, typeset.Bold, th.C(theme.AccentInk)))
	})
}

// links is how the project's PR relates to the session's others: what it depends on, or pairs with, and the order.
func (x *shipPlugin) links(c *ui.Ctx, se core.SessionState, s shipRead) {
	if len(se.Projects) < 2 {
		return
	}
	th := c.Theme()
	ships, _ := x.sessionShips(se)
	var order, before, after []string
	seen := false
	for _, o := range ships {
		if o.ReadOnly || o.Code == nil {
			continue
		}
		order = append(order, o.Project)
		switch {
		case o.Project == s.Project:
			seen = true
		case !seen:
			before = append(before, prRef(o))
		default:
			after = append(after, o.Project)
		}
	}
	if len(order) < 2 {
		return
	}
	var text string
	switch {
	case len(before) > 0:
		text = "depends on " + strings.Join(before, ", ")
	case s.PR == nil:
		text = "pairs with " + strings.Join(after, ", ") + " → will cross-link both descriptions"
	default:
		text = "merges before " + strings.Join(after, ", ")
	}
	c.Row("links", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(10, 7), Gap: 8, Radius: 5, Stroke: th.C(tShipCard), Border: 1, Cross: ui.Center, Clip: true},
		func(c *ui.Ctx) {
			c.Text("i", "⟷", uiFont(12, th.C(tShipLink)))
			c.Text("t", text+" · merge order: "+strings.Join(order, " → "), monoFont(10.5, th.C(theme.FgMuted)))
		})
}

// prRef is a project's PR as another names it: "api #790", or "api PR (not raised)".
func prRef(s shipRead) string {
	w := s.words()
	if s.PR == nil {
		return s.Project + " " + w.Short + " (not raised)"
	}
	ref := fmt.Sprintf("%s %s%d", s.Project, w.Sigil, s.PR.Number)
	if ciOf(s.Checks) == registry.CheckFailed {
		ref += " · CI failing"
	}
	return ref
}

// gatesList is "before this ships": every gate's state for the session.
func (x *shipPlugin) gatesList(c *ui.Ctx, se core.SessionState) {
	th := c.Theme()
	shipCap(c, "BEFORE THIS SHIPS")
	gs := registry.ShipGates(x.h, string(se.ID), string(se.Kind))
	if len(gs) == 0 {
		c.Text("none", "No gates for this session.", uiFont(12, th.C(theme.FgFaint)))
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
					}
					c.Box("g", ui.Style{W: ui.Px(14)}, func(c *ui.Ctx) { c.Text("g", glyph, uiFont(13, tone)) })
					c.Row("t", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("t", g.Title, uiFont(12.5, th.C(theme.FgMuted))) })
					noteTone := th.C(theme.FgFaint)
					if g.State == registry.GateWarn {
						noteTone = th.C(theme.Warn)
					}
					c.Text("n", g.Note, monoFont(10.5, noteTone))
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
func (x *shipPlugin) mergeOrder(c *ui.Ctx, se core.SessionState, ships []shipRead) {
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
	shipCap(c, "MERGE ORDER")
	for i, s := range rows {
		c.Row(s.Project, ui.Style{W: ui.Flex(1), H: ui.Px(22), Gap: 6, Cross: ui.Center}, func(c *ui.Ctx) {
			pr := s.words().Sigil + "—"
			if s.PR != nil {
				pr = s.words().Sigil + strconv.Itoa(s.PR.Number)
			}
			c.Text("t", fmt.Sprintf("%d · %s → %s", i+1, s.Project, pr), monoFont(11, th.C(theme.FgMuted)))
		})
	}
	c.Box("sp", ui.Style{H: ui.Px(6)}, nil)
	c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
	c.Box("sp2", ui.Style{H: ui.Px(6)}, nil)
}

func (x *shipPlugin) ticket(c *ui.Ctx, se core.SessionState) {
	if se.Ticket == "" {
		return
	}
	th := c.Theme()
	shipCap(c, "TICKET")
	c.Text("t", se.Ticket, monoFont(11.5, th.C(tShipLink)))
}

// --- the dock: Pull request (01h) and Git ---

// checklist is a project's Pull request panel: committed → up to date → pushed → PR → checks, in a line each.
func (x *shipPlugin) checklist(c *ui.Ctx, project string) {
	se, p, ok := x.here(project)
	c.Column("pr", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.PadXY(12, 10), Gap: 4, ScrollY: true}, func(c *ui.Ctx) {
		if !ok {
			shipEmpty(c, "Pull request is a session's project's: open a session.")
			return
		}
		widget.Async(c, "load", x.loadState(c, se.ID, p), widget.AsyncOpts{Label: "Reading " + p, Retry: func() { x.refresh(se.ID, true) },
			Help: "git/ship#pr"}, func(c *ui.Ctx) {
			c.Column("body", ui.Style{W: ui.Flex(1), Gap: 4}, func(c *ui.Ctx) { x.checklistBody(c, se, p) })
		})
	})
}

// checklistBody is the Pull request panel once the project's been read.
func (x *shipPlugin) checklistBody(c *ui.Ctx, se core.SessionState, p string) {
	th := c.Theme()
	s, _ := x.ship(se.ID, p)
	w := s.words()
	title := "Not raised"
	if s.PR != nil {
		title = s.PR.Title
	}
	c.Row("title", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
		c.Text("t", title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 13, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
	})
	c.Row("sub", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
		c.Text("s", fmt.Sprintf("%s → %s · %s", s.Branch, cmpOr(s.Base, "?"), counted(s.Commits, "commit")), monoFont(10.5, th.C(theme.FgFaint)))
	})
	c.Box("sp", ui.Style{H: ui.Px(6)}, nil)
	line := func(key, state, label, note string) {
		c.Row(key, ui.Style{W: ui.Flex(1), H: ui.Px(24), Gap: 8, Cross: ui.Center, Role: ui.RoleText, Label: label + " " + state}, func(c *ui.Ctx) {
			glyph, tone := "✓", th.C(theme.OK)
			switch state {
			case "warn":
				glyph, tone = "!", th.C(theme.Warn)
			case "fail":
				glyph, tone = "✗", th.C(theme.Error)
			case "doing":
				glyph, tone = "●", th.C(tShipTodo)
			case "todo":
				glyph, tone = "○", th.C(theme.FgFaint)
			}
			c.Box("g", ui.Style{W: ui.Px(14)}, func(c *ui.Ctx) { c.Text("g", glyph, uiFont(12.5, tone)) })
			c.Row("l", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("l", label, uiFont(12.5, th.C(theme.FgMuted))) })
			c.Text("n", note, monoFont(10, th.C(theme.FgFaint)))
		})
	}
	if len(s.Files) > 0 {
		line("committed", "warn", "Committed", counted(len(s.Files), "file")+" left")
	} else {
		line("committed", "ok", "Committed", "")
	}
	if s.Behind > 0 {
		line("uptodate", "warn", "Up to date with "+s.Base, fmt.Sprintf("%d behind", s.Behind))
	} else {
		line("uptodate", "ok", "Up to date with "+cmpOr(s.Base, "base"), "")
	}
	switch {
	case len(s.Unpushed) > 0:
		line("pushed", "warn", "Pushed", fmt.Sprintf("%d to push", len(s.Unpushed)))
	case s.Pushed:
		line("pushed", "ok", "Pushed", "")
	default:
		line("pushed", "todo", "Pushed", "")
	}
	switch {
	case s.PR != nil:
		line("pr", "ok", w.Short+" open", w.Sigil+strconv.Itoa(s.PR.Number))
	case !s.PRDraft.Empty() || x.prForm(se.ID, s).edited:
		line("pr", "doing", w.Short, "drafting")
	default:
		line("pr", "todo", w.Short, "not raised")
	}
	failing := 0
	for _, ch := range s.Checks {
		if ch.State == registry.CheckFailed {
			failing++
		}
	}
	switch ci := ciOf(s.Checks); {
	case ci == registry.CheckFailed:
		line("checks", "fail", "Checks", fmt.Sprintf("%d of %d failing", failing, len(s.Checks)))
		if s.FailLine != "" {
			c.Row("fail", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 22}, Clip: true}, func(c *ui.Ctx) {
				c.Text("f", s.FailLine, monoFont(10, th.C(theme.Error)))
			})
		}
	case ci == registry.CheckRunning:
		line("checks", "doing", "Checks", "running")
	case ci == registry.CheckOK:
		line("checks", "ok", "Checks", fmt.Sprintf("%d passed", len(s.Checks)))
	default:
		line("checks", "todo", "Checks", "")
	}
	c.Box("sp2", ui.Style{H: ui.Px(8)}, nil)
	c.Row("buttons", ui.Style{W: ui.Flex(1), Gap: 8}, func(c *ui.Ctx) {
		if s.PR.Open() {
			if widget.Button(c, "open", "Open "+w.Short, widget.ButtonOpts{Help: "git/ship#pr"}) {
				x.run("ship.pr.open", registry.Args{"project": p})
			}
			x.prActions(c, se, s)
			return
		}
		if s.Code != nil && s.PR == nil && widget.Button(c, "raise", "Raise "+w.Short, widget.ButtonOpts{Primary: true, Help: "git/ship#raise"}) {
			x.run("ship.pr.open", registry.Args{"project": p})
		}
		if widget.Button(c, "ship", "Ship ⇉", widget.ButtonOpts{Help: "git/ship#page"}) {
			x.run("ship.open", nil)
		}
	})
	if t, ok := ciTrouble(s); ok && s.PR.Open() {
		c.Column("ci", ui.Style{Gap: 6}, func(c *ui.Ctx) {
			askTwinsLabelled(c, x.h, "ship.ci."+p, t, "git/ship#checks", "Explain with agent", "Fix CI with agent")
		})
	}
	explainKept(c, x.sh, "ship.ci."+p, "git/ship#checks")
	note := "opens the Raise " + w.Short + " tab →"
	if s.PR.Open() {
		note = "Open " + w.Short + " opens as a main tab"
	}
	c.Text("note", note, monoFont(10, th.C(theme.FgGhost)))
}
