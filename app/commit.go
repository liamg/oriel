package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
	"github.com/liamg/oriel/worker"
)

// CommitKeyMode is the commit dialog's keys while a gate holds the commit: a commits anyway, ⏎ confirms past a
// soft gate, r reviews, Esc cancels.
const CommitKeyMode = "commit"

const commitHelp = "files/commit"

// commitWait is how long a commit waits for its gates to finish counting, checking every commitPoll.
const (
	commitWait = 15 * time.Second
	commitPoll = 50 * time.Millisecond
)

// CommitEvent is the Commit hook's payload: a commit about to be made, which a veto stops.
type CommitEvent struct {
	Project string   `json:"project"` // the session's name for the repo, else its root
	Root    string   `json:"root"`
	Message string   `json:"message"`
	Files   []string `json:"files"`
	Unread  int      `json:"unread"` // what the gates that don't pass count as left: agent-written lines unread
}

// commitAsk is the commit dialog: writing the message, or a commit the gates hold.
type commitAsk struct {
	root    string
	all     bool
	message string
	checks  []registry.GateCheck // those that don't pass; none while writing the message
	focus   bool
}

// writing says the dialog is asking for the message.
func (a *commitAsk) writing() bool { return len(a.checks) == 0 }

// hard says a gate blocks the commit, rather than only asking.
func (a *commitAsk) hard() bool {
	for _, g := range a.checks {
		if g.Blocks() {
			return true
		}
	}
	return false
}

func (g *gitPlugin) activateCommit(h plugin.Host) {
	g.add(h.DeclareEvent(registry.EventInfo{Name: "Commit", Fired: true, Payload: CommitEvent{},
		Doc: "A commit about to be made, past its gates; a veto stops it. { project, root, message, files, unread }"}))
	open := func(registry.Args) bool { return g.ask != nil }
	held := func(registry.Args) bool { return g.ask != nil && !g.ask.writing() }
	for _, c := range []registry.Command{
		{ID: "git.commit", Title: "Commit what's staged in {path}'s repo ({all}: everything uncommitted) with {message}, asking for one if none, once the commit gates pass; {anyway} commits past a hard gate with its trailer, {confirm} past a soft one",
			Run: func(_ registry.Ctx, a registry.Args) error { return g.commit(a, time.Now()) }},
		{ID: "git.commit.anyway", Title: "Commit anyway, past the gates holding the commit, recording it in a trailer and the session log", Available: held,
			Run: func(registry.Ctx, registry.Args) error { return g.resume(registry.Args{"anyway": true}) }},
		{ID: "git.commit.confirm", Title: "Commit: the message written, or past a soft gate", Available: open, Run: func(registry.Ctx, registry.Args) error {
			if g.ask.hard() {
				return errors.New("the commit is gated: commit anyway, or review first")
			}
			return g.resume(registry.Args{"confirm": !g.ask.writing()})
		}},
		{ID: "git.commit.review", Title: "Review what the gates holding the commit want read", Available: held, Run: func(ctx registry.Ctx, _ registry.Args) error {
			a := g.ask
			g.ask = nil
			for _, c := range a.checks {
				if c.Review != "" {
					return h.Run(ctx, c.Review, c.ReviewArgs)
				}
			}
			return nil
		}},
		{ID: "git.commit.cancel", Title: "Close the commit dialog without committing", Available: open,
			Run: func(registry.Ctx, registry.Args) error { g.ask = nil; return nil }},
	} {
		g.add(h.Command(c))
	}
	for _, k := range []registry.Keymap{
		{Keys: "a", Command: "git.commit.anyway", Desc: "Commit anyway"},
		{Keys: "<CR>", Command: "git.commit.confirm", Desc: "Commit"},
		{Keys: "r", Command: "git.commit.review", Desc: "Review"},
		{Keys: "<Esc>", Command: "git.commit.cancel", Desc: "Cancel"},
	} {
		k.Mode = CommitKeyMode
		g.add(h.Keymap(k))
	}
	g.add(h.View(OverlayPrefix+"commit", registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "commit", Fn: g.commitView}
	})))
}

// resume runs the dialog's commit again with more args: its message, anyway or confirm.
func (g *gitPlugin) resume(more registry.Args) error {
	a := g.ask
	if a == nil {
		return errors.New("no commit is waiting")
	}
	args := registry.Args{"path": a.root, "message": a.message, "all": a.all}
	for k, v := range more {
		args[k] = v
	}
	return g.commit(args, time.Now())
}

// commitFiles are the files a commit takes: what's staged, or with all every uncommitted file.
func commitFiles(st *gitx.State, all bool) []string {
	var out []string
	for _, f := range st.Files(gitx.Uncommitted) {
		if (all || f.Area == gitx.Staged) && !slices.Contains(out, f.Path) {
			out = append(out, f.Path)
		}
	}
	return out
}

// commit runs the commit gates and the Commit hooks, then commits off the loop. Without a message it asks for one;
// a hard gate holds it until anyway, a soft one until confirm, and either shows the dialog. A gate still counting is
// asked again, until commitWait from started.
func (g *gitPlugin) commit(a registry.Args, started time.Time) error {
	root, _, err := repoTarget(g.sh, g.h, cmpOr(a.String("path"), g.h.Here().Project))
	if err != nil {
		return err
	}
	t := g.tree(root)
	st := t.state()
	later := func() error {
		if time.Since(started) > commitWait {
			return fmt.Errorf("%s's status or gates didn't settle; try again", filepath.Base(root))
		}
		g.busy.Add(1)
		go func() {
			defer g.busy.Done()
			time.Sleep(commitPoll)
			g.h.Do(func() {
				if err := g.commit(a, started); err != nil {
					g.h.Notify(core.Notice{ID: "git.commit", Severity: core.SevError, Text: err.Error()})
				}
			})
		}()
		return nil
	}
	if st == nil {
		return later()
	}
	all, _ := a["all"].(bool)
	files := commitFiles(st, all)
	if len(files) == 0 {
		return fmt.Errorf("nothing to commit in %s: stage something first, or commit all", filepath.Base(root))
	}
	message := a.String("message")
	if strings.TrimSpace(message) == "" {
		g.ask = &commitAsk{root: root, all: all, message: message, focus: true}
		return nil
	}
	checks := registry.CheckCommit(g.h, registry.CommitCheck{Root: root, Files: files, Message: message})
	var holding []registry.GateCheck
	pending, blocked, asking := false, false, false
	for _, c := range checks {
		pending = pending || c.Pending
		if c.Blocks() || c.Asks() {
			holding = append(holding, c)
			blocked, asking = blocked || c.Blocks(), asking || c.Asks()
		}
	}
	if pending {
		return later()
	}
	anyway, _ := a["anyway"].(bool)
	confirm, _ := a["confirm"].(bool)
	if blocked && !anyway || asking && !anyway && !confirm {
		g.ask = &commitAsk{root: root, all: all, message: message, checks: holding, focus: true}
		return nil
	}
	ev := CommitEvent{Project: root, Root: root, Message: message, Files: files}
	se, project, inSession := projectSession(g.h, root)
	if inSession {
		ev.Project = project
	}
	var trailers []string
	for _, c := range holding {
		ev.Unread += c.Unread
		if c.Blocks() && c.Trailer != "" {
			trailers = append(trailers, c.Trailer)
		}
	}
	if v, _ := g.h.Fire(context.Background(), "Commit", root, ev); v.Veto {
		g.ask = nil
		return fmt.Errorf("the commit was stopped: %s", cmpOr(v.Reason, "a Commit hook vetoed it"))
	}
	g.ask = nil
	repo := t.status.Repo()
	g.async(func(ctx context.Context) error {
		if all {
			if err := repo.Stage(ctx, files...); err != nil {
				return err
			}
		}
		hash, err := repo.CommitStaged(ctx, message, trailers...)
		if err != nil {
			return err
		}
		if len(holding) > 0 {
			g.h.Do(func() { g.recordOverrides(se, project, inSession, hash, message, holding) })
		}
		return nil
	})
	return nil
}

// recordOverrides puts a commit made past gates in the session's log (a notice) and its state; outside a session,
// in Oriel's log.
func (g *gitPlugin) recordOverrides(se core.SessionState, project string, inSession bool, hash, message string, held []registry.GateCheck) {
	subject, _, _ := strings.Cut(strings.TrimSpace(message), "\n")
	short := hash[:min(len(hash), 7)]
	for _, c := range held {
		how := "confirmed past the soft " + c.Gate.Name + " gate"
		trailer := ""
		if c.Blocks() {
			how, trailer = "committed anyway past the "+c.Gate.Name+" gate", c.Trailer
		}
		text := fmt.Sprintf("%s %q %s: %s", short, subject, how, c.Reason)
		if trailer != "" {
			text += " (" + trailer + ")"
		}
		n := core.Notice{ID: "git.commit.gate", Severity: core.SevWarning, Source: "git", Title: "Committed past the commit gate", Text: text}
		if inSession {
			n.Session = se.ID
		}
		g.h.Notify(n)
		if !inSession || g.sh.sessions == nil {
			continue
		}
		cl, err := g.sh.sessions.client(se.ID)
		if err != nil {
			continue
		}
		ev := core.CommitGateOverridden{Session: se.ID, Override: core.GateOverride{Project: project, Gate: c.Gate.Name, Mode: c.Mode, Unread: c.Unread,
			Trailer: trailer, Commit: hash, Subject: subject, Time: time.Now().UTC()}}
		g.busy.Add(1)
		go func(cl worker.Client) {
			defer g.busy.Done()
			if err := cl.RecordOverride(context.Background(), ev); err != nil {
				g.h.Log().Warn("commit gate", "err", err)
			}
		}(cl)
	}
}

// commitView is the commit dialog while it's open: the message, or the gates holding the commit.
func (g *gitPlugin) commitView(c *ui.Ctx) {
	a := g.ask
	if a == nil {
		return
	}
	th := c.Theme()
	size := c.Size()
	centre := render.Point{X: size.X / 2, Y: size.Y / 2}
	w := min(560, size.X-32)
	closed := widget.Scrim(c, "commit", widget.ScrimOpts{Z: ui.LayerDialog, Fill: th.C(tScrimOmni), Help: commitHelp}, func(c *ui.Ctx) {
		box := c.Next("box")
		if a.focus && !a.writing() {
			c.Focus(box)
			a.focus = false
		}
		title, label := "COMMIT", "Commit"
		switch {
		case a.hard():
			title, label = "COMMIT GATED", "Commit gated"
		case !a.writing():
			title, label = "COMMIT WITH UNREAD LINES?", "Commit with unread lines"
		}
		c.Column("box", ui.Style{W: ui.Px(w), Fill: th.C(tOmniBg), Stroke: th.C(tOmniBorder), Border: 1, Radius: 10, Clip: true,
			Trap: true, Interactive: true, Focusable: true, KeyMode: CommitKeyMode, Float: &ui.Float{Point: &centre, Self: render.Point{X: .5, Y: .5}},
			Role: ui.RoleDialog, Label: label, Help: commitHelp}, func(c *ui.Ctx) {
			c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(52), Pad: ui.PadXY(20, 0), Gap: 14, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("title", title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 14, Weight: typeset.Bold, Tracking: .24},
					Color: th.C(theme.Fg)})
				c.Text("repo", filepath.Base(a.root), monoFont(10.5, th.C(theme.FgFaint)))
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				if widget.Button(c, "close", "esc", widget.ButtonOpts{Ghost: true, Help: commitHelp}) {
					g.ask = nil
				}
			})
			c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			c.Column("body", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 24, R: 24, T: 16, B: 18}, Gap: 12}, func(c *ui.Ctx) {
				if a.writing() {
					g.commitMessage(c, a, w-48)
				} else {
					g.commitGates(c, a, w-48)
				}
			})
			c.Box("rule2", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			c.Row("foot", ui.Style{W: ui.Flex(1), H: ui.Px(60), Pad: ui.PadXY(24, 0), Gap: 10, Cross: ui.Center, Main: ui.End}, func(c *ui.Ctx) {
				g.commitButtons(c, a)
			})
		})
	})
	if closed {
		g.ask = nil
	}
}

func (g *gitPlugin) commitMessage(c *ui.Ctx, a *commitAsk, width float32) {
	th := c.Theme()
	what := "what's staged"
	if a.all {
		what = "everything uncommitted"
	}
	c.Text("what", "Commits "+what+".", uiFont(12, th.C(theme.FgMuted)))
	id := c.Next("msg")
	if a.focus {
		c.Focus(id)
		a.focus = false
	}
	r := widget.Input(c, "msg", &a.message, widget.InputOpts{Label: "Commit message", Placeholder: "feat: what it does", Lines: 6, MinLines: 3,
		Help: commitHelp, Width: ui.Px(width)})
	if r.Cancelled {
		g.ask = nil
	}
	if r.Submitted {
		g.submit()
	}
	if gs := g.gatesFor(a); len(gs) > 0 {
		for i, gc := range gs {
			widget.Wrap(c, i, gateLine(gc), width, monoFont(10.5, th.C(tLedgerUnread)))
		}
	}
}

// gatesFor are the gates that wouldn't pass the dialog's commit now, for a preview while the message is written.
func (g *gitPlugin) gatesFor(a *commitAsk) []registry.GateCheck {
	st := g.tree(a.root).state()
	if st == nil {
		return nil
	}
	var out []registry.GateCheck
	for _, c := range registry.CheckCommit(g.h, registry.CommitCheck{Root: a.root, Files: commitFiles(st, a.all)}) {
		if c.Blocks() || c.Asks() {
			out = append(out, c)
		}
	}
	return out
}

// gateLine is a gate holding a commit, in a line: "⏻ gated (hard): 21 agent-written lines unread".
func gateLine(c registry.GateCheck) string {
	return fmt.Sprintf("⏻ %s gate (%s): %s", cmpOr(c.Gate.Label, c.Gate.Name), c.Mode, c.Reason)
}

func (g *gitPlugin) commitGates(c *ui.Ctx, a *commitAsk, width float32) {
	th := c.Theme()
	for i, gc := range a.checks {
		c.Column(i, ui.Style{W: ui.Flex(1), Gap: 4, Role: ui.RoleGroup, Label: gc.Gate.Label}, func(c *ui.Ctx) {
			c.Row("head", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("l", cmpOr(gc.Gate.Label, gc.Gate.Name), uiFont(13, th.C(theme.Fg)))
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				if gc.Total > 0 {
					c.Text("n", fmt.Sprintf("%d / %d", gc.Done, gc.Total), monoFont(11, th.C(tLedgerUnread)))
				}
			})
			widget.Wrap(c, "why", gc.Reason, width, uiFont(12, th.C(theme.FgMuted)))
		})
	}
	note := "Commit asks first while these are unread; it goes in the session log."
	if a.hard() {
		var ts []string
		for _, gc := range a.checks {
			if gc.Blocks() && gc.Trailer != "" {
				ts = append(ts, gc.Trailer)
			}
		}
		note = "Commit anyway records it in the commit (" + strings.Join(ts, ", ") + ") and in the session log."
	}
	widget.Wrap(c, "note", note, width, monoFont(10.5, th.C(theme.FgFaint)))
}

func (g *gitPlugin) commitButtons(c *ui.Ctx, a *commitAsk) {
	run := func(cmd string) {
		if err := g.h.Run(context.Background(), cmd, nil); err != nil {
			g.h.Notify(core.Notice{ID: "git.commit", Severity: core.SevError, Key: true, Text: err.Error()})
		}
	}
	if widget.Button(c, "cancel", "Cancel", widget.ButtonOpts{Key: "esc", Help: commitHelp}) {
		g.ask = nil
		return
	}
	if a.writing() {
		if widget.Button(c, "commit", "Commit", widget.ButtonOpts{Primary: true, Key: "⌘↵", Disabled: strings.TrimSpace(a.message) == "", Help: commitHelp}) {
			g.submit()
		}
		return
	}
	for _, gc := range a.checks {
		if gc.Review != "" {
			if widget.Button(c, "review", cmpOr(gc.ReviewLabel, "Review"), widget.ButtonOpts{Primary: true, Key: "r", Help: commitHelp}) {
				run("git.commit.review")
				return
			}
			break
		}
	}
	if a.hard() {
		if widget.Button(c, "anyway", "Commit anyway", widget.ButtonOpts{Key: "a", Help: commitHelp}) {
			run("git.commit.anyway")
		}
		return
	}
	if widget.Button(c, "confirm", "Commit", widget.ButtonOpts{Key: "↵", Help: commitHelp}) {
		run("git.commit.confirm")
	}
}

// submit commits the message written.
func (g *gitPlugin) submit() {
	if g.ask == nil || strings.TrimSpace(g.ask.message) == "" {
		return
	}
	if err := g.resume(nil); err != nil {
		g.h.Notify(core.Notice{ID: "git.commit", Severity: core.SevError, Key: true, Text: err.Error()})
	}
}
