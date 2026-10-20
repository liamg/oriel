package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// Ending a session (sessions.md, "Ending a session"; help shell/tabs#end): session.end archives or deletes one, and with no {how} opens the
// End session dialog, which says per project what would be lost first. The tab's menu, mission control, the
// Workspace page and Ship reach it through the command.

// SessionEndKeyMode is the End session dialog's keys: ⌘↵ ends it, Esc cancels.
const SessionEndKeyMode = "session.end"

const (
	endHelp           = "shell/tabs#end"
	archiveOnMerge    = "session.archive_on_merge"
	sessionSubject    = "session:"
	endArchive        = "archive"
	endDelete         = "delete"
	endPreviewedFiles = 3
)

var endHows = []string{"Archive", "Delete"}

type sessionEnd struct {
	disposer
	sh  *shell
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
}

func (x *sessionEnd) Manifest() plugin.Manifest { return plugin.Manifest{ID: "sessionend"} }

func (x *sessionEnd) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	has := func(a registry.Args) bool {
		return x.sh.cfg.Host != nil && (a.String("id") != "" || h.State().Focus != "")
	}
	open := func(registry.Args) bool { return x.ask != nil }
	for _, c := range []registry.Command{
		{ID: "session.end", Title: "End {id}, or the session in front: {how} archive or delete, {discard} uncommitted work, {delete_branch}; no {how} opens the dialog",
			Available: has, Run: func(ctx registry.Ctx, a registry.Args) error {
				id := core.SessionID(cmpOr(a.String("id"), string(h.State().Focus)))
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
	} {
		x.add(h.Command(c))
	}
	for _, k := range []registry.Keymap{
		{Keys: "<D-CR>", Command: "session.end.confirm", Desc: "End the session"},
		{Keys: "<Esc>", Command: "session.end.cancel", Desc: "Cancel"},
	} {
		k.Mode = SessionEndKeyMode
		x.add(h.Keymap(k))
	}
	onSession := func(run func(ctx registry.Ctx, id string) error) func(registry.Ctx, registry.MenuCtx) error {
		return func(ctx registry.Ctx, at registry.MenuCtx) error {
			return run(ctx, strings.TrimPrefix(at.Subject, sessionSubject))
		}
	}
	for _, it := range []registry.MenuItem{
		{Name: "session.close-tab", Label: "Close tab", Group: "session", Order: 10, When: registry.MenuWhen{Subject: sessionSubject, Help: "shell/tabs"},
			Source: "builtin:sessionend", Run: onSession(func(ctx registry.Ctx, id string) error {
				return h.Run(ctx, "session.close-tab", registry.Args{"id": id})
			})},
		{Name: "session.end", Label: "End session…", Group: "session", Order: 90, When: registry.MenuWhen{Subject: sessionSubject},
			Source: "builtin:sessionend", Run: onSession(func(ctx registry.Ctx, id string) error {
				return h.Run(ctx, "session.end", registry.Args{"id": id})
			})},
	} {
		x.add(h.Contribute(it))
	}
	x.add(h.Option(registry.OptionSpec{Name: archiveOnMerge, Section: "git", Type: registry.Bool, Default: false,
		Scopes: registry.ScopeUser | registry.ScopeProject,
		Help:   "Archive a session by itself once every one of its pull requests is merged and nothing in its worktrees is uncommitted."}))
	x.add(registry.On(h, registry.Hook{Kind: registry.Observe, Label: "archive the session once all its PRs are merged (session.archive_on_merge)"},
		func(ctx registry.Ctx, ev *PrMerged) (registry.Verdict, error) {
			x.archiveIfMerged(ctx, core.SessionID(ev.Session))
			return registry.Verdict{}, nil
		}))
	x.add(h.View(OverlayPrefix+"session-end", registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "session-end", Fn: x.view}
	})))
	return nil
}

// open opens the dialog on id, reading its projects afresh for what it would lose.
func (x *sessionEnd) open(id core.SessionID) error {
	if _, ok := x.h.State().Session(id); !ok {
		return fmt.Errorf("no session %q", id)
	}
	x.ask = &endAsk{id: id, focus: true}
	if s := x.sh.ship; s != nil {
		s.refresh(id, true)
	}
	return nil
}

// preview is the session's projects as Ship last read them, and how far that read is.
func (x *sessionEnd) preview(c *ui.Ctx, se core.SessionState) ([]shipRead, widget.LoadState) {
	s := x.sh.ship
	if s == nil {
		return nil, widget.LoadState{Phase: widget.Ready}
	}
	ships, _ := s.sessionShips(se)
	out := widget.LoadState{Phase: widget.Ready}
	for _, p := range se.Projects {
		switch st := s.loadState(c, se.ID, p.Name); {
		case st.Loading():
			return ships, st
		case st.Phase == widget.Failed && out.Phase != widget.Failed:
			out = st
		}
	}
	return ships, out
}

// leftMachine is whether anything of the session was pushed or raised: archiving keeps its history.
func leftMachine(ships []shipRead) bool {
	for _, s := range ships {
		if s.Pushed || s.PR != nil {
			return true
		}
	}
	return false
}

// allMerged is whether every project with something to ship has its PR merged, and one at least has.
func allMerged(ships []shipRead) bool {
	merged := 0
	for _, s := range ships {
		switch {
		case s.ReadOnly:
		case s.PR != nil && s.PR.Merged:
			merged++
		case s.PR != nil || len(s.Files) > 0 || len(s.Unpushed) > 0:
			return false
		}
	}
	return merged > 0
}

func uncommitted(ships []shipRead) int {
	n := 0
	for _, s := range ships {
		n += len(s.Files)
	}
	return n
}

// archiveIfMerged archives id when session.archive_on_merge is on, every PR is merged and nothing is uncommitted.
func (x *sessionEnd) archiveIfMerged(ctx registry.Ctx, id core.SessionID) {
	s, ok := x.h.State().Session(id)
	if !ok || x.sh.ship == nil {
		return
	}
	se := s.Snapshot()
	primary, _ := se.Primary()
	v, _, _ := x.h.Options().Get(archiveOnMerge, registry.At{Project: primary.Dir, Session: string(id)})
	if on, _ := v.(bool); !on {
		return
	}
	ships, _ := x.sh.ship.sessionShips(se)
	if se.Archived || !allMerged(ships) || uncommitted(ships) > 0 {
		return
	}
	if err := x.end(ctx, host.EndReq{Session: id}); err != nil {
		x.h.Notify(core.Notice{ID: "session.end." + string(id), Severity: core.SevError, Session: id, Text: err.Error(), Time: time.Now()})
	}
}

// settle picks the choice the dialog starts on, until one is picked: Archive once anything left the machine, Delete
// otherwise; a direct session's directory isn't Oriel's to delete.
func (a *endAsk) settle(se core.SessionState, ships []shipRead, st widget.LoadState) {
	switch {
	case se.Kind == core.KindDirect:
		a.how = 0
	case a.picked || st.Loading():
	case leftMachine(ships):
		a.how = 0
	default:
		a.how = 1
	}
}

// confirm ends the session as the dialog says, discarding uncommitted work it has shown.
func (x *sessionEnd) confirm(ctx registry.Ctx) error {
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
	var ships []shipRead
	if x.sh.ship != nil {
		ships, _ = x.sh.ship.sessionShips(se)
	}
	x.ask = nil
	return x.end(ctx, host.EndReq{Session: a.id, Delete: a.how == 1, Discard: a.how == 0 && uncommitted(ships) > 0, DeleteBranch: a.deleteBranch})
}

// end closes the session's tab, then ends it off the loop; an archive offers Undo while its branches stay.
func (x *sessionEnd) end(ctx registry.Ctx, r host.EndReq) error {
	w := x.sh.cfg.Host
	if w == nil {
		return errors.New("there are no sessions in this window")
	}
	name := string(r.Session)
	if s, ok := x.h.State().Session(r.Session); ok {
		name = label(s.Snapshot())
	}
	for _, t := range x.h.State().Tabs {
		if t == r.Session {
			if err := x.h.Run(ctx, "session.close-tab", registry.Args{"id": string(r.Session)}); err != nil {
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

func (x *sessionEnd) view(c *ui.Ctx) {
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
	a.settle(se, ships, st)
	th := c.Theme()
	size := c.Size()
	centre := render.Point{X: size.X / 2, Y: size.Y / 2}
	w := min(520, size.X-32)
	closed := widget.Scrim(c, "session-end", widget.ScrimOpts{Z: ui.LayerDialog, Fill: th.C(tScrimOmni), Help: endHelp}, func(c *ui.Ctx) {
		box := c.Next("box")
		if a.focus {
			c.Focus(box)
			a.focus = false
		}
		c.Column("box", ui.Style{W: ui.Px(w), Fill: th.C(tOmniBg), Stroke: th.C(tOmniBorder), Border: 1, Radius: 10, Clip: true,
			Trap: true, Interactive: true, Focusable: true, KeyMode: SessionEndKeyMode, Float: &ui.Float{Point: &centre, Self: render.Point{X: .5, Y: .5}},
			Role: ui.RoleDialog, Label: "End " + label(se), Help: endHelp}, func(c *ui.Ctx) {
			c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(52), Pad: ui.PadXY(20, 0), Gap: 14, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("title", "END SESSION", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 14, Weight: typeset.Bold, Tracking: .24},
					Color: th.C(theme.Fg)})
				c.Row("t", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("t", label(se), monoFont(10.5, th.C(theme.FgFaint))) })
				if widget.Button(c, "close", "esc", widget.ButtonOpts{Ghost: true, Help: endHelp}) {
					x.ask = nil
				}
			})
			c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			c.Column("body", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 24, R: 24, T: 16, B: 18}, Gap: 12}, func(c *ui.Ctx) {
				x.body(c, a, se, ships, st, w-48)
			})
			c.Box("rule2", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			c.Row("foot", ui.Style{W: ui.Flex(1), H: ui.Px(60), Pad: ui.PadXY(24, 0), Gap: 10, Cross: ui.Center, Main: ui.End}, func(c *ui.Ctx) {
				if widget.Button(c, "cancel", "Cancel", widget.ButtonOpts{Key: "esc", Help: endHelp}) {
					x.ask = nil
					return
				}
				label := endHows[a.how]
				if a.how == 0 && uncommitted(ships) > 0 {
					label = "Discard and archive"
				}
				if widget.Button(c, "end", label, widget.ButtonOpts{Primary: true, Key: "⌘↵", Help: endHelp, Disabled: st.Loading()}) {
					x.run("session.end.confirm")
				}
			})
		})
	})
	if closed {
		x.ask = nil
	}
}

func (x *sessionEnd) run(id string) {
	if err := x.h.Run(context.Background(), id, nil); err != nil {
		x.h.Notify(core.Notice{ID: id, Severity: core.SevError, Source: "sessionend", Key: true, Text: err.Error(), Time: time.Now()})
	}
}

// body is the choice, what it does, and what would be lost, project by project.
func (x *sessionEnd) body(c *ui.Ctx, a *endAsk, se core.SessionState, ships []shipRead, st widget.LoadState, width float32) {
	th := c.Theme()
	if se.Kind != core.KindDirect {
		was := a.how
		if widget.Segmented(c, "how", endHows, &a.how, widget.SegmentedOpts{Label: "How to end it", Help: endHelp}) && a.how != was {
			a.picked = true
		}
	}
	what := "Its worktrees go; its threads, transcript and test runs stay, searchable, and it can be reopened."
	if a.how == 1 {
		what = "Its worktrees, workspace and log go, with everything in them. The repos themselves stay."
	}
	widget.Wrap(c, "what", what, width, uiFont(12.5, th.C(theme.FgMuted)))
	widget.Async(c, "read", st, widget.AsyncOpts{Policy: widget.Gate, Label: "Reading what would be lost", Help: endHelp,
		Retry: func() {
			if x.sh.ship != nil {
				x.sh.ship.refresh(se.ID, true)
			}
		}}, func(c *ui.Ctx) {
		c.Column("losses", ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) { x.losses(c, a, se, ships, width) })
	})
	st2 := widget.CheckOff
	if a.deleteBranch {
		st2 = widget.CheckOn
	}
	if se.Kind != core.KindDirect && widget.Check(c, "branch", st2, widget.CheckOpts{Text: "Delete the branches too", Help: endHelp,
		Tooltip: "Each project's local branch; the remote's stays"}) {
		a.deleteBranch = !a.deleteBranch
	}
}

// losses are, per project, what ending it this way would lose or leave behind; then the agent and its sandbox.
func (x *sessionEnd) losses(c *ui.Ctx, a *endAsk, se core.SessionState, ships []shipRead, width float32) {
	th := c.Theme()
	line := func(key, glyph string, tone render.Color, text string) {
		c.Row(key, ui.Style{W: ui.Flex(1), Gap: 10, Role: ui.RoleText, Label: text}, func(c *ui.Ctx) {
			c.Box("g", ui.Style{W: ui.Px(14)}, func(c *ui.Ctx) { c.Text("g", glyph, uiFont(13, tone)) })
			widget.Wrap(c, "t", text, width-24, uiFont(12.5, th.C(theme.FgMuted)))
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
			for _, f := range s.Files[:min(k, endPreviewedFiles)] {
				names = append(names, f.Path)
			}
			more := ""
			if k > endPreviewedFiles {
				more = fmt.Sprintf(" and %d more", k-endPreviewedFiles)
			}
			line(s.Project+"/files", "!", warn, fmt.Sprintf("%s: %s uncommitted, lost: %s%s", s.Project, counted(k, "file"), strings.Join(names, ", "), more))
			n++
		}
		if k := len(s.Unpushed); k > 0 {
			fate := "they stay on its branch"
			tone := info
			if a.deleteBranch {
				fate, tone = "lost with its branch", warn
			}
			line(s.Project+"/unpushed", "!", tone, fmt.Sprintf("%s: %s not pushed: %s", s.Project, counted(k, "commit"), fate))
			n++
		}
		if s.PR.Open() {
			w := s.words()
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
