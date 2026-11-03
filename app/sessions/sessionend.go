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
// End session dialog, which says per project what would be lost first. The tab's menu, mission control, the
// Workspace page and Ship reach it through the command; closing the tab of a session that's done
// (registry.SessionDone) opens it too.

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
	h      plugin.Host
	ask    *endAsk
	ending map[core.SessionID]bool // sessions whose tab end closes, so closing it asks nothing
}

// endAsk is the dialog while it's open.
type endAsk struct {
	id           core.SessionID
	how          int // into endHows
	picked       bool
	deleteBranch bool
	focus        bool
	done         string // why it looks finished, when closing its tab opened the dialog
}

func (x *End) Manifest() plugin.Manifest { return plugin.Manifest{ID: "sessionend"} }

func (x *End) Activate(_ context.Context, h plugin.Host) error {
	x.h, x.ending = h, map[core.SessionID]bool{}
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
				return h.Run(ctx, "session.close-tab", registry.Args{"id": id})
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
	x.Add(kit.OnDispose(h.Subscribe(core.Filter{}, func(ev core.Event) {
		if e, ok := ev.(core.TabClosed); ok {
			h.Do(func() { x.offer(e.ID) })
		}
	})))
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

// offer opens the dialog on id once its tab closed, if it looks done, its local branches ticked to go.
func (x *End) offer(id core.SessionID) {
	if x.ending[id] {
		delete(x.ending, id)
		return
	}
	s, ok := x.h.State().Session(id)
	if !ok || x.ask != nil || slices.Contains(x.h.State().Tabs, id) {
		return
	}
	se := s.Snapshot()
	if se.Archived {
		return
	}
	why := registry.Done(x.h, se)
	if why == "" || x.open(id) != nil {
		return
	}
	x.ask.done, x.ask.deleteBranch = why, true
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

// settle picks the choice the dialog starts on, until one is picked: Archive once anything left the machine, Delete
// otherwise; a session whose kind can't be deleted (a direct one's directory isn't Oriel's) archives.
func (a *endAsk) settle(canDelete bool, ships []registry.ProjectShip, st widget.LoadState) {
	switch {
	case !canDelete:
		a.how = 0
	case a.picked || st.Loading():
	case leftMachine(ships):
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
	w, _ := kit.SessionHost(x.h)
	if w == nil {
		return errors.New("there are no sessions in this window")
	}
	name := string(r.Session)
	if s, ok := x.h.State().Session(r.Session); ok {
		name = kit.Label(s.Snapshot())
	}
	for _, t := range x.h.State().Tabs {
		if t == r.Session {
			x.ending[r.Session] = true
			if err := x.h.Run(ctx, "session.close-tab", registry.Args{"id": string(r.Session)}); err != nil {
				delete(x.ending, r.Session)
				return err
			}
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
	a.settle(registry.Can(x.h, se.Kind, core.Delete), ships, st)
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
	if err := x.h.Run(context.Background(), id, nil); err != nil {
		x.h.Notify(core.Notice{ID: id, Severity: core.SevError, Source: "sessionend", Key: true, Text: err.Error(), Time: time.Now()})
	}
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

// losses are, per project, what ending it this way would lose or leave behind; then the agent and its sandbox.
func (x *End) losses(c *ui.Ctx, a *endAsk, se core.SessionState, ships []registry.ProjectShip, width float32) {
	th := c.Theme()
	line := func(key, glyph string, tone render.Color, text string) {
		c.Row(key, ui.Style{W: ui.Flex(1), Gap: 10, Role: ui.RoleText, Label: text}, func(c *ui.Ctx) {
			c.Box("g", ui.Style{W: ui.Px(14)}, func(c *ui.Ctx) { c.Text("g", glyph, kit.UIFont(13, tone)) })
			widget.Wrap(c, "t", text, width-24, kit.UIFont(12.5, th.C(theme.FgMuted)))
		})
	}
	warn, info := th.C(theme.Warn), th.C(theme.FgFaint)
	n := 0
	for _, s := range ships {
		if s.ReadOnly {
			continue
		}
		if k := len(s.Files); k > 0 {
			var names []string
			names = append(names, s.Files[:min(k, endPreviewedFiles)]...)
			more := ""
			if k > endPreviewedFiles {
				more = fmt.Sprintf(" and %d more", k-endPreviewedFiles)
			}
			line(s.Project+"/files", "!", warn, fmt.Sprintf("%s: %s uncommitted, lost: %s%s", s.Project, kit.Counted(k, "file"), strings.Join(names, ", "), more))
			n++
		}
		if k := s.Unpushed; k > 0 {
			fate := "they stay on its branch"
			tone := info
			if a.deleteBranch {
				fate, tone = "lost with its branch", warn
			}
			line(s.Project+"/unpushed", "!", tone, fmt.Sprintf("%s: %s not pushed: %s", s.Project, kit.Counted(k, "commit"), fate))
			n++
		}
		if s.PR.Open() {
			w := kit.WordsOf(s.Code)
			line(s.Project+"/pr", "·", info, fmt.Sprintf("%s: %s%d stays open on %s", s.Project, w.Sigil, s.PR.Number, s.Code.Label()))
			n++
		}
	}
	if se.Agent.Working {
		line("agent", "!", warn, "The agent is mid-turn: it's stopped where it is.")
		n++
	}
	if se.Agent.Sandboxed && se.Agent.Running {
		line("sandbox", "·", info, "Its sandbox stops.")
		n++
	}
	if n == 0 {
		line("none", "✓", th.C(theme.OK), "Nothing unsaved is lost.")
	}
}
