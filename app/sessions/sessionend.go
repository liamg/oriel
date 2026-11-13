package sessions

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
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

// Ending a session (help shell/tabs#end): session.end archives or deletes one, and with no {how} opens the
// End session dialog, which says per project what would be lost first. The tab's menu, the
// Workspace page and Ship reach it through the command. session.close, a session tab's ✕, archives it, asking first
// when something would be lost or when it's done (registry.SessionDone), when deleting it is offered too.

// SessionEndKeyMode is the End session dialog's keys: ⌘↵ ends it, Esc cancels.
const SessionEndKeyMode = "session.end"

const (
	endArchive        = "archive"
	endDelete         = "delete"
	endPreviewedFiles = 3
)

var endHows = []string{"Archive", "Delete"}

type End struct {
	kit.Disposer
	h   plugin.Host
	ask *endAsk
}

// endAsk is the dialog while it's open.
type endAsk struct {
	id           core.SessionID
	how          int // into endHows
	picked       bool
	deleteBranch bool
	focus        bool
	closing      bool   // its tab was closed: with nothing to lose and not done, it's archived without asking
	done         string // why it looks finished, when closing its tab opened the dialog
}

func (x *End) Manifest() plugin.Manifest { return plugin.Manifest{ID: "sessionend"} }

func (x *End) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	for _, c := range x.commands() {
		x.Add(h.Command(c))
	}
	for _, k := range []registry.Keymap{
		{Keys: "<D-CR>", Command: "session.end.confirm", Desc: "End the session"},
		{Keys: "<Esc>", Command: "session.end.cancel", Desc: "Cancel"},
	} {
		k.Mode = SessionEndKeyMode
		x.Add(h.Keymap(k))
	}
	onSession := func(run func(ctx registry.Ctx, id string) error) func(registry.Ctx, registry.MenuCtx) error {
		return func(ctx registry.Ctx, at registry.MenuCtx) error {
			return run(ctx, strings.TrimPrefix(at.Subject, kit.SessionSubject))
		}
	}
	for _, it := range []registry.MenuItem{
		{Name: "session.close-tab", Label: "Close tab", Group: "session", Order: 10, When: registry.MenuWhen{Subject: kit.SessionSubject, Help: "shell/tabs"},
			Source: "builtin:sessionend", Run: onSession(func(ctx registry.Ctx, id string) error {
				return h.Run(ctx, "session.close", registry.Args{"id": id})
			})},
		{Name: "session.end", Label: "End session…", Group: "session", Order: 90, When: registry.MenuWhen{Subject: kit.SessionSubject},
			Source: "builtin:sessionend", Run: onSession(func(ctx registry.Ctx, id string) error {
				return h.Run(ctx, "session.end", registry.Args{"id": id})
			})},
	} {
		x.Add(h.Contribute(it))
	}
	x.Add(h.Option(registry.OptionSpec{Name: kit.ArchiveOnMergeOption, Section: "git", Type: registry.Bool, Default: false,
		Scopes: registry.ScopeUser | registry.ScopeProject,
		Help:   "Archive a session by itself once every one of its pull requests is merged and nothing in its worktrees is uncommitted."}))
	x.Add(registry.On(h, registry.Hook{Kind: registry.Observe, Label: "archive the session once all its PRs are merged (session.archive_on_merge)"},
		func(ctx registry.Ctx, ev *kit.PrMerged) (registry.Verdict, error) {
			x.archiveIfMerged(ctx, core.SessionID(ev.Session))
			return registry.Verdict{}, nil
		}))
	x.Add(h.View(kit.OverlayPrefix+"session-end", uireg.ViewFunc(func(uireg.ViewCtx) ui.Node {
		return &ui.Func{Key: "session-end", Fn: x.view}
	})))
	return nil
}

func (x *End) commands() []registry.Command {
	h := x.h
	has := func(a registry.Args) bool {
		return kit.HasSessions(x.h) && (a.String("id") != "" || h.State().Focus != "")
	}
	open := func(registry.Args) bool { return x.ask != nil }
	return []registry.Command{
		{ID: "session.end", Title: "End {id}, or the session in front: {how} archive or delete, {discard} uncommitted work, {delete_branch}; no {how} opens the dialog",
			Available: has, Run: func(ctx registry.Ctx, a registry.Args) error {
				id := core.SessionID(cmp.Or(a.String("id"), string(h.State().Focus)))
				switch a.String("how") {
				case "":
					return x.open(id)
				case endArchive, endDelete:
					return x.end(ctx, host.EndReq{Session: id, Delete: a.String("how") == endDelete, Discard: a.Bool("discard"),
						DeleteBranch: a.Bool("delete_branch")})
				}
				return fmt.Errorf("a session ends by archive or delete, not %q", a.String("how"))
			}},
		{ID: "session.close", Title: "Close {id}, or the session in front: stop its agent and archive it, asking first when something would be lost, or archive or delete it when it's done",
			Available: has, Run: func(_ registry.Ctx, a registry.Args) error {
				return x.close(core.SessionID(cmp.Or(a.String("id"), string(h.State().Focus))))
			}},
		{ID: "session.end.confirm", Title: "End the session as the End session dialog says", Available: open,
			Run: func(ctx registry.Ctx, _ registry.Args) error { return x.confirm(ctx) }},
		{ID: "session.end.cancel", Title: "Close the End session dialog, ending nothing", Available: open,
			Run: func(registry.Ctx, registry.Args) error { x.ask = nil; return nil }},
	}
}

// open opens the dialog on id, reading its projects afresh for what it would lose.
func (x *End) open(id core.SessionID) error {
	if _, ok := x.h.State().Session(id); !ok {
		return fmt.Errorf("no session %q", id)
	}
	x.ask = &endAsk{id: id, focus: true}
	if s, ok := registry.ShipsOf(x.h); ok {
		s.Refresh(id, true)
	}
	return nil
}

// close opens the dialog on id to archive it once its projects are read afresh; one that looks done offers delete
// too, its local branches ticked to go, and one with nothing to lose is archived without asking (view). One whose
// kind ends by deleting (core.DeletesOnEnd) is deleted, its local branches too, asking first, on Delete, only when
// something would be lost.
func (x *End) close(id core.SessionID) error {
	if err := x.open(id); err != nil {
		return err
	}
	s, _ := x.h.State().Session(id)
	x.ask.closing = true
	se := s.Snapshot()
	if why := registry.Done(x.h, se); why != "" {
		x.ask.done, x.ask.deleteBranch = why, true
	}
	if registry.Can(x.h, se.Kind, core.DeletesOnEnd) {
		x.ask.deleteBranch = true
	}
	return nil
}

// preview is the session's projects as Ship last read them, and how far that read is.
func (x *End) preview(c *ui.Ctx, se core.SessionState) ([]registry.ProjectShip, widget.LoadState) {
	s, ok := registry.ShipsOf(x.h)
	if !ok {
		return nil, widget.LoadState{Phase: widget.Ready}
	}
	ships := s.For(se)
	out := widget.LoadState{Phase: widget.Ready}
	for _, p := range ships {
		switch st := kit.LoadState(p.Load); {
		case st.Loading():
			return ships, st
		case st.Phase == widget.Failed && out.Phase != widget.Failed:
			out = st
		}
	}
	return ships, out
}

// leftMachine is whether anything of the session was pushed or raised: archiving keeps its history.
func leftMachine(ships []registry.ProjectShip) bool {
	for _, s := range ships {
		if s.Pushed || s.PR != nil {
			return true
		}
	}
	return false
}

// archiveIfMerged archives id when session.archive_on_merge is on, every PR is merged and nothing is uncommitted.
func (x *End) archiveIfMerged(ctx registry.Ctx, id core.SessionID) {
	s, ok := x.h.State().Session(id)
	ships, shipping := registry.ShipsOf(x.h)
	if !ok || !shipping {
		return
	}
	se := s.Snapshot()
	if !kit.ArchivesOnMerge(x.h, se, ships.For(se)) {
		return
	}
	if err := x.end(ctx, host.EndReq{Session: id}); err != nil {
		x.h.Notify(core.Notice{ID: "session.end." + string(id), Severity: core.SevError, Session: id, Text: err.Error(), Time: time.Now()})
	}
}

// settle picks the choice the dialog starts on, until one is picked: Delete for a kind that ends that way
// (core.DeletesOnEnd); Archive once anything left the machine or when closing a tab of a session that isn't done;
// Delete otherwise. A session whose kind can't be deleted (a direct one's directory isn't Oriel's) archives.
func (a *endAsk) settle(canDelete, deletes bool, ships []registry.ProjectShip, st widget.LoadState) {
	switch {
	case !canDelete:
		a.how = 0
	case a.picked:
	case deletes:
		a.how = 1
	case st.Loading():
	case leftMachine(ships), a.closing && a.done == "":
		a.how = 0
	default:
		a.how = 1
	}
}

// confirm ends the session as the dialog says, discarding uncommitted work it has shown.
func (x *End) confirm(ctx registry.Ctx) error {
	a := x.ask
	if a == nil {
		return errors.New("no session is waiting to end")
	}
	s, ok := x.h.State().Session(a.id)
	if !ok {
		x.ask = nil
		return fmt.Errorf("no session %q", a.id)
	}
	se := s.Snapshot()
	var ships []registry.ProjectShip
	if s, ok := registry.ShipsOf(x.h); ok {
		ships = s.For(se)
	}
	x.ask = nil
	return x.end(ctx, host.EndReq{Session: a.id, Delete: a.how == 1, Discard: a.how == 0 && kit.Uncommitted(ships) > 0, DeleteBranch: a.deleteBranch})
}

// end closes the session's tab, then ends it off the loop; an archive offers Undo while its branches stay.
func (x *End) end(ctx registry.Ctx, r host.EndReq) error {
	w, _ := kit.SessionCalls(x.h)
	if w == nil {
		return errors.New("there are no sessions in this window")
	}
	name := string(r.Session)
	if s, ok := x.h.State().Session(r.Session); ok {
		name = kit.Label(s.Snapshot())
	}
	if slices.Contains(x.h.State().Tabs, r.Session) {
		if err := x.h.Run(ctx, "session.close-tab", registry.Args{"id": string(r.Session)}); err != nil {
			return err
		}
	}
	doing := "Archiving " + name
	if r.Delete {
		doing = "Deleting " + name
	}
	return plugin.RunSlow(ctx, x.h, plugin.Slow{ID: "session.end." + string(r.Session), Doing: doing},
		func(ctx context.Context) (struct{}, error) { return struct{}{}, w.End(ctx, r) },
		func(registry.Ctx, struct{}) error {
			n := core.Notice{ID: "session.ended." + string(r.Session), Severity: core.SevInfo, Time: time.Now(), Text: "Deleted " + name + "."}
			if !r.Delete {
				n.Session, n.Text = r.Session, "Archived "+name+"."
				if !r.DeleteBranch {
					n.Actions = []core.Action{{Title: "Undo", Command: "session.open", Args: map[string]any{"id": string(r.Session)}}}
				}
			}
			x.h.Notify(n)
			return nil
		})
}

func (x *End) view(c *ui.Ctx) {
	a := x.ask
	if a == nil {
		return
	}
	s, ok := x.h.State().Session(a.id)
	if !ok {
		x.ask = nil
		return
	}
	se := s.Snapshot()
	ships, st := x.preview(c, se)
	deletes := registry.Can(x.h, se.Kind, core.DeletesOnEnd) && registry.Can(x.h, se.Kind, core.Delete)
	if a.closing && (a.done == "" || deletes) && st.Phase == widget.Ready && !slices.ContainsFunc(x.lossesOf(se, ships, a.deleteBranch), loss.matters) {
		x.ask = nil
		x.h.Do(func() {
			if err := x.end(context.Background(), host.EndReq{Session: a.id, Delete: deletes, DeleteBranch: deletes}); err != nil {
				x.h.Notify(core.Notice{ID: "session.end." + string(a.id), Severity: core.SevError, Session: a.id, Text: err.Error(), Time: time.Now()})
			}
		})
		return
	}
	a.settle(registry.Can(x.h, se.Kind, core.Delete), deletes, ships, st)
	th := c.Theme()
	size := c.Size()
	centre := render.Point{X: size.X / 2, Y: size.Y / 2}
	w := min(520, size.X-32)
	closed := widget.Scrim(c, "session-end", widget.ScrimOpts{Z: ui.LayerDialog, Fill: th.C(kit.TScrimOmni), Help: kit.EndHelp}, func(c *ui.Ctx) {
		box := c.Next("box")
		if a.focus {
			c.RequestFocus(box)
			a.focus = false
		}
		c.Column("box", ui.Style{W: ui.Px(w), Fill: th.C(kit.TOmniBg), Stroke: th.C(kit.TOmniBorder), Border: 1, Radius: 10, Clip: true,
			Trap: true, Interactive: true, Focusable: true, KeyMode: SessionEndKeyMode, Float: &ui.Float{Point: &centre, Self: render.Point{X: .5, Y: .5}},
			Role: ui.RoleDialog, Label: "End " + kit.Label(se), Help: kit.EndHelp}, func(c *ui.Ctx) {
			c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(52), Pad: ui.PadXY(20, 0), Gap: 14, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("title", "END SESSION", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 14, Weight: typeset.Bold, Tracking: .24},
					Color: th.C(theme.Fg)})
				c.Row("t", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("t", kit.Label(se), kit.MonoFont(10.5, th.C(theme.FgFaint))) })
				if widget.Button(c, "close", "esc", widget.ButtonOpts{Ghost: true, Help: kit.EndHelp}) {
					x.ask = nil
				}
			})
			c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			c.Column("body", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 24, R: 24, T: 16, B: 18}, Gap: 12}, func(c *ui.Ctx) {
				x.body(c, a, se, ships, st, w-48)
			})
			c.Box("rule2", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			c.Row("foot", ui.Style{W: ui.Flex(1), H: ui.Px(60), Pad: ui.PadXY(24, 0), Gap: 10, Cross: ui.Center, Main: ui.End}, func(c *ui.Ctx) {
				if widget.Button(c, "cancel", "Cancel", widget.ButtonOpts{Key: "esc", Help: kit.EndHelp}) {
					x.ask = nil
					return
				}
				label := endHows[a.how]
				if a.how == 0 && kit.Uncommitted(ships) > 0 {
					label = "Discard and archive"
				}
				if widget.Button(c, "end", label, widget.ButtonOpts{Primary: true, Key: "⌘↵", Help: kit.EndHelp, Disabled: st.Loading()}) {
					x.run("session.end.confirm")
				}
			})
		})
	})
	if closed {
		x.ask = nil
	}
}

func (x *End) run(id string) {
	kit.RunOrNotify(x.h, "sessionend", id, nil)
}

// body is the choice, what it does, and what would be lost, project by project.
func (x *End) body(c *ui.Ctx, a *endAsk, se core.SessionState, ships []registry.ProjectShip, st widget.LoadState, width float32) {
	th := c.Theme()
	if registry.Can(x.h, se.Kind, core.Delete) {
		was := a.how
		if widget.Segmented(c, "how", endHows, &a.how, widget.SegmentedOpts{Label: "How to end it", Help: kit.EndHelp}) && a.how != was {
			a.picked = true
		}
	}
	if a.done != "" {
		c.Row("done", ui.Style{W: ui.Flex(1), Gap: 10, Role: ui.RoleText, Label: a.done}, func(c *ui.Ctx) {
			c.Box("g", ui.Style{W: ui.Px(14)}, func(c *ui.Ctx) { c.Text("g", "✓", kit.UIFont(13, th.C(theme.OK))) })
			widget.Wrap(c, "t", a.done, width-24, kit.UIFont(12.5, th.C(theme.Fg)))
		})
	}
	what := "Its worktrees go; its threads, transcript and test runs stay, searchable, and it can be reopened."
	if a.how == 1 {
		what = "Its worktrees, workspace and log go, with everything in them. The repos themselves stay."
	}
	widget.Wrap(c, "what", what, width, kit.UIFont(12.5, th.C(theme.FgMuted)))
	widget.Async(c, "read", st, widget.AsyncOpts{Policy: widget.Gate, Label: "Reading what would be lost", Help: kit.EndHelp,
		Retry: func() {
			if s, ok := registry.ShipsOf(x.h); ok {
				s.Refresh(se.ID, true)
			}
		}}, func(c *ui.Ctx) {
		c.Column("losses", ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) { x.losses(c, a, se, ships, width) })
	})
	st2 := widget.CheckOff
	if a.deleteBranch {
		st2 = widget.CheckOn
	}
	if registry.Can(x.h, se.Kind, core.OwnsBranch) && widget.Check(c, "branch", st2, widget.CheckOpts{Text: "Delete the local branches too (remote branches stay)", Help: kit.EndHelp,
		Tooltip: "Each project's local branch; the remote's stays"}) {
		a.deleteBranch = !a.deleteBranch
	}
}

// loss is a line of what ending a session would lose or leave behind.
type loss struct {
	key, text string
	alarm     bool // marked !, not ·
	warn      bool // in the warning colour
	info      bool // worth knowing, but nothing to stop for
}

func (l loss) matters() bool { return !l.info }

// lossesOf are what plugins say would be lost, then, per project, what archiving or deleting se would lose or leave behind; then its agent and sandbox.
// unsaved are what plugins say it would lose (registry.Unsaved); an open PR is listed when it's the session's own
// (ownPRs).
func lossesOf(se core.SessionState, ships []registry.ProjectShip, deleteBranch, ownPRs bool, unsaved []string) []loss {
	var out []loss
	for i, u := range unsaved {
		out = append(out, loss{key: fmt.Sprintf("unsaved/%d", i), alarm: true, warn: true, text: u})
	}
	for _, s := range ships {
		if s.ReadOnly {
			continue
		}
		if k := len(s.Files); k > 0 {
			out = append(out, loss{key: s.Project + "/files", alarm: true, warn: true,
				text: fmt.Sprintf("%s: %s uncommitted, lost: %s", s.Project, kit.Counted(k, "file"), previewed(s.Files))})
		}
		if k := s.Unpushed; k > 0 {
			fate := "they stay on its branch"
			if deleteBranch {
				fate = "lost with its branch"
			}
			out = append(out, loss{key: s.Project + "/unpushed", alarm: true, warn: deleteBranch,
				text: fmt.Sprintf("%s: %s not pushed: %s", s.Project, kit.Counted(k, "commit"), fate)})
		}
		if ownPRs && s.PR.Open() {
			w := kit.WordsOf(s.Code)
			out = append(out, loss{key: s.Project + "/pr", text: fmt.Sprintf("%s: %s%d stays open on %s", s.Project, w.Sigil, s.PR.Number, s.Code.Label())})
		}
	}
	if se.Agent.Working {
		out = append(out, loss{key: "agent", alarm: true, warn: true, text: "The agent is mid-turn: it's stopped where it is."})
	}
	var helpers []string
	for _, sa := range se.Subagents {
		if sa.Running {
			helpers = append(helpers, cmp.Or(sa.Description, sa.Type, sa.ID))
		}
	}
	for _, w := range []struct {
		key, one, many string
		names          []string
	}{
		{"subagents", "subagent", "subagents", helpers},
		{"background", "background task", "background tasks", tasks(se.Agent.Background)},
		{"processes", "process", "processes", tasks(se.Agent.Processes)},
	} {
		if k := len(w.names); k > 0 {
			out = append(out, loss{key: w.key, alarm: true, warn: true,
				text: fmt.Sprintf("%d %s still running, stopped with the agent: %s", k, kit.Plural(k, w.one, w.many), previewed(w.names))})
		}
	}
	if se.Agent.Sandboxed && se.Agent.Running {
		out = append(out, loss{key: "sandbox", info: true, text: "Its sandbox stops."})
	}
	return out
}

// lossesOf is lossesOf for se, asking its kind and the plugins.
func (x *End) lossesOf(se core.SessionState, ships []registry.ProjectShip, deleteBranch bool) []loss {
	return lossesOf(se, ships, deleteBranch, registry.Can(x.h, se.Kind, core.Ships), registry.Unsaved(x.h, se))
}

// losses draws lossesOf, or that nothing is lost.
func (x *End) losses(c *ui.Ctx, a *endAsk, se core.SessionState, ships []registry.ProjectShip, width float32) {
	th := c.Theme()
	line := func(key, glyph string, tone render.Color, text string) {
		c.Row(key, ui.Style{W: ui.Flex(1), Gap: 10, Role: ui.RoleText, Label: text}, func(c *ui.Ctx) {
			c.Box("g", ui.Style{W: ui.Px(14)}, func(c *ui.Ctx) { c.Text("g", glyph, kit.UIFont(13, tone)) })
			widget.Wrap(c, "t", text, width-24, kit.UIFont(12.5, th.C(theme.FgMuted)))
		})
	}
	ls := x.lossesOf(se, ships, a.deleteBranch)
	for _, l := range ls {
		glyph, tone := "·", th.C(theme.FgFaint)
		if l.alarm {
			glyph = "!"
		}
		if l.warn {
			tone = th.C(theme.Warn)
		}
		line(l.key, glyph, tone, l.text)
	}
	if len(ls) == 0 {
		line("none", "✓", th.C(theme.OK), "Nothing unsaved is lost.")
	}
}

// tasks are what each of ts is doing.
func tasks(ts []core.BackgroundTask) []string {
	var out []string
	for _, t := range ts {
		out = append(out, cmp.Or(t.Description, t.Kind, t.ID))
	}
	return out
}

// previewed is the first few of names, and how many more.
func previewed(names []string) string {
	s := strings.Join(names[:min(len(names), endPreviewedFiles)], ", ")
	if k := len(names) - endPreviewedFiles; k > 0 {
		s += fmt.Sprintf(" and %d more", k)
	}
	return s
}
