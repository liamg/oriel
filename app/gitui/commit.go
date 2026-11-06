package gitui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// CommitKeyMode is the commit dialog's keys: ⏎ commits, Esc cancels.
const CommitKeyMode = "commit"

// commitWait is how long a commit waits for its work tree's status to settle, checking every commitPoll.
const (
	commitWait = 15 * time.Second
	commitPoll = 50 * time.Millisecond
)

const commitHelp = "files/commit"

// CommitEvent is the Commit hook's payload: a commit about to be made, which a veto stops.
type CommitEvent struct {
	Project string   `json:"project"` // the session's name for the repo, else its root
	Root    string   `json:"root"`
	Message string   `json:"message"`
	Files   []string `json:"files"`
}

// commitAsk is the commit dialog, asking for the message.
type commitAsk struct {
	root    string
	all     bool
	message string
	focus   bool
}

func (g *Git) activateCommit(h plugin.Host) {
	g.Add(h.DeclareEvent(registry.EventInfo{Name: "Commit", Fired: true, Payload: CommitEvent{},
		Doc: "A commit about to be made; a veto stops it. { project, root, message, files }"}))
	open := func(registry.Args) bool { return g.ask != nil }
	for _, c := range []registry.Command{
		{ID: "git.commit", Title: "Commit what's staged in {path}'s repo ({all}: everything uncommitted) with {message}, asking for one if none",
			Run: func(ctx registry.Ctx, a registry.Args) error {
				return atTarget(ctx, g.h, cmp.Or(a.String("path"), g.h.Here().Project), func(root, _ string) error {
					return g.commit(root, a, time.Now())
				})
			}},
		{ID: "git.commit.confirm", Title: "Commit with the message written", Available: open,
			Run: func(registry.Ctx, registry.Args) error { return g.resume(nil) }},
		{ID: "git.commit.cancel", Title: "Close the commit dialog without committing", Available: open,
			Run: func(registry.Ctx, registry.Args) error { g.ask = nil; return nil }},
	} {
		g.Add(h.Command(c))
	}
	for _, k := range []registry.Keymap{
		{Keys: "<CR>", Command: "git.commit.confirm", Desc: "Commit"},
		{Keys: "<Esc>", Command: "git.commit.cancel", Desc: "Cancel"},
	} {
		k.Mode = CommitKeyMode
		g.Add(h.Keymap(k))
	}
	g.Add(h.View(kit.OverlayPrefix+"commit", uireg.ViewFunc(func(uireg.ViewCtx) ui.Node {
		return &ui.Func{Key: "commit", Fn: g.commitView}
	})))
}

// resume runs the dialog's commit again with its message, and more args.
func (g *Git) resume(more registry.Args) error {
	a := g.ask
	if a == nil {
		return errors.New("no commit is waiting")
	}
	args := registry.Args{"path": a.root, "message": a.message, "all": a.all}
	for k, v := range more {
		args[k] = v
	}
	return g.commit(a.root, args, time.Now())
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

// commit runs the Commit hooks in the work tree root, then commits off the loop. Without a message it asks for one.
// A status still being read is waited for, until commitWait from started.
func (g *Git) commit(root string, a registry.Args, started time.Time) error {
	t := g.tree(root)
	st := t.state()
	later := func() error {
		if time.Since(started) > commitWait {
			return fmt.Errorf("%s's status didn't settle; try again", filepath.Base(root))
		}
		g.busy.Add(1)
		go func() {
			defer g.busy.Done()
			time.Sleep(commitPoll)
			g.h.Do(func() {
				if err := g.commit(root, a, started); err != nil {
					g.h.Notify(core.Notice{ID: "git.commit", Severity: core.SevError, Text: err.Error()})
				}
			})
		}()
		return nil
	}
	if st == nil || t.busy() {
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
	ev := CommitEvent{Project: root, Root: root, Message: message, Files: files}
	if _, project, inSession := projectSession(g.h, root); inSession {
		ev.Project = project
	}
	if v, _ := g.h.Fire(context.Background(), "Commit", root, ev); v.Veto {
		g.ask = nil
		return fmt.Errorf("the commit was stopped: %s", cmp.Or(v.Reason, "a Commit hook vetoed it"))
	}
	g.ask = nil
	repo := t.status.Repo()
	g.async(func(ctx context.Context) error {
		if all {
			if err := repo.Stage(ctx, files...); err != nil {
				return err
			}
		}
		_, err := repo.CommitStaged(ctx, message)
		return err
	})
	return nil
}

// commitView is the commit dialog while it's open, asking for the message.
func (g *Git) commitView(c *ui.Ctx) {
	a := g.ask
	if a == nil {
		return
	}
	th := c.Theme()
	size := c.Size()
	centre := render.Point{X: size.X / 2, Y: size.Y / 2}
	w := min(560, size.X-32)
	closed := widget.Scrim(c, "commit", widget.ScrimOpts{Z: ui.LayerDialog, Fill: th.C(kit.TScrimOmni), Help: commitHelp}, func(c *ui.Ctx) {
		title, label := "COMMIT", "Commit"
		c.Column("box", ui.Style{W: ui.Px(w), Fill: th.C(kit.TOmniBg), Stroke: th.C(kit.TOmniBorder), Border: 1, Radius: 10, Clip: true,
			Trap: true, Interactive: true, Focusable: true, KeyMode: CommitKeyMode, Float: &ui.Float{Point: &centre, Self: render.Point{X: .5, Y: .5}},
			Role: ui.RoleDialog, Label: label, Help: commitHelp}, func(c *ui.Ctx) {
			c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(52), Pad: ui.PadXY(20, 0), Gap: 14, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("title", title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 14, Weight: typeset.Bold, Tracking: .24},
					Color: th.C(theme.Fg)})
				c.Text("repo", filepath.Base(a.root), kit.MonoFont(10.5, th.C(theme.FgFaint)))
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				if widget.Button(c, "close", "esc", widget.ButtonOpts{Ghost: true, Help: commitHelp}) {
					g.ask = nil
				}
			})
			c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			c.Column("body", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 24, R: 24, T: 16, B: 18}, Gap: 12}, func(c *ui.Ctx) {
				g.commitMessage(c, a, w-48)
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

func (g *Git) commitMessage(c *ui.Ctx, a *commitAsk, width float32) {
	th := c.Theme()
	what := "what's staged"
	if a.all {
		what = "everything uncommitted"
	}
	c.Text("what", "Commits "+what+".", kit.UIFont(12, th.C(theme.FgMuted)))
	id := c.Next("msg")
	if a.focus {
		c.RequestFocus(id)
		a.focus = false
	}
	r := widget.Prose(c, "msg", &a.message, widget.ProseOpts{Label: "Commit message", Placeholder: "feat: what it does", Lines: 6, MinLines: 3,
		Help: commitHelp, Width: ui.Px(width)})
	if r.Cancelled {
		g.ask = nil
	}
	if r.Submitted {
		g.submit()
	}
}

func (g *Git) commitButtons(c *ui.Ctx, a *commitAsk) {
	if widget.Button(c, "cancel", "Cancel", widget.ButtonOpts{Key: "esc", Help: commitHelp}) {
		g.ask = nil
		return
	}
	if widget.Button(c, "commit", "Commit", widget.ButtonOpts{Primary: true, Key: "⌘↵", Disabled: strings.TrimSpace(a.message) == "", Help: commitHelp}) {
		g.submit()
	}
}

// submit commits the message written.
func (g *Git) submit() {
	if g.ask == nil || strings.TrimSpace(g.ask.message) == "" {
		return
	}
	if err := g.resume(nil); err != nil {
		g.h.Notify(core.Notice{ID: "git.commit", Severity: core.SevError, Key: true, Text: err.Error()})
	}
}
