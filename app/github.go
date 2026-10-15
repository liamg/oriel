package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
	"github.com/liamg/oriel/worker"
)

// GitHub review mirroring (ui.md 06, RV-4): the session's PRs' reviews are synced into threads by its worker
// (github.sync), every githubPoll for the session in front and on demand. Their cards are this ThreadSource's: the
// GitHub header, and under the agent's draft reply ↗ Forward to GitHub and Edit. Only a forward posts, from the
// worker with the user's own gh. The status line says where the reviewers stand.

// githubPoll is how often the session in front is synced.
const githubPoll = 30 * time.Second

// GitHubStatusView is the status line's segment: "GH 1 changes requested".
const GitHubStatusView = "statusline.left.70-github"

var (
	tGitHub       = theme.Declare("github.accent", "violet")
	tGitHubDanger = theme.Declare("github.changes", "warn")
	tGitHubOK     = theme.Declare("github.approved", "ok")
)

type githubExt struct {
	disposer
	sh     *shell
	h      ext.Host
	now    func() time.Time
	ctx    context.Context
	cancel context.CancelFunc
	busy   sync.WaitGroup
	poller sync.WaitGroup

	mu         sync.Mutex
	mirrors    map[core.SessionID]githubMirror
	syncing    map[core.SessionID]bool
	forwarding map[string]bool // comment IDs being posted

	editing map[string]*string // drafts being edited, by comment; on the loop
}

// githubMirror is a session's last sync, and when it came back.
type githubMirror struct {
	worker.Mirror
	at  time.Time
	err string
}

func (x *githubExt) Manifest() ext.Manifest { return ext.Manifest{ID: "github"} }

func (x *githubExt) Activate(_ context.Context, h ext.Host) error {
	x.h = h
	if x.now == nil {
		x.now = time.Now
	}
	x.mirrors, x.syncing, x.forwarding, x.editing = map[core.SessionID]githubMirror{}, map[core.SessionID]bool{}, map[string]bool{}, map[string]*string{}
	x.ctx, x.cancel = context.WithCancel(context.Background())
	x.add(h.Option(registry.OptionSpec{Name: "github.trusted_orgs", Section: "git", Type: registry.List, Default: []string{}, Scopes: registry.ScopeUser,
		Help: "GitHub orgs whose members' review comments the agent may read. Text from anyone else is hidden from it. Empty: the repository's owner."}))
	x.add(h.Option(registry.OptionSpec{Name: "github.trusted_users", Section: "git", Type: registry.List, Default: []string{}, Scopes: registry.ScopeUser,
		Help: "GitHub logins whose review comments the agent may read, beside the trusted orgs' members."}))
	inFront := func(a registry.Args) bool { _, ok := x.session(a); return ok }
	for _, c := range []registry.Command{
		{ID: "github.sync", Title: "Sync the session's GitHub reviews into its threads now", Available: inFront, Run: func(_ registry.Ctx, a registry.Args) error {
			se, ok := x.session(a)
			if !ok {
				return errors.New("no session")
			}
			x.sync(se.ID, true)
			return nil
		}},
		{ID: "github.forward", Title: "Forward the agent's draft reply on a GitHub thread to GitHub, as you: {thread, comment}", Available: inFront,
			Run: func(_ registry.Ctx, a registry.Args) error { return x.forward(a) }},
		{ID: "github.edit", Title: "Edit the agent's draft reply on a GitHub thread before forwarding it: {thread, comment}", Available: inFront,
			Run: func(_ registry.Ctx, a registry.Args) error { return x.edit(a) }},
		{ID: "github.draft.save", Title: "Save an edited draft reply: {thread, comment, body}", Available: inFront,
			Run: func(_ registry.Ctx, a registry.Args) error { return x.save(a) }},
	} {
		x.add(h.Command(c))
	}
	x.add(h.Contribute(registry.ThreadSource{Origin: core.OriginGitHub, Title: "GitHub review", Head: x.head, Comment: x.comment}))
	x.add(h.View(GitHubStatusView, registry.ViewFunc(func(registry.ViewCtx) ui.Node { return &ui.Func{Key: "github", Fn: x.statusSegment} })))
	x.add(onDispose(h.Subscribe(core.Filter{}, func(ev core.Event) {
		if ev, ok := ev.(core.SessionFocused); ok && ev.ID != "" {
			x.sync(ev.ID, false)
		}
	})))
	x.poller.Add(1)
	go x.poll()
	return nil
}

func (x *githubExt) Deactivate(ctx context.Context) error {
	x.cancel()
	x.poller.Wait()
	x.busy.Wait()
	return x.disposer.Deactivate(ctx)
}

func (x *githubExt) poll() {
	defer x.poller.Done()
	t := time.NewTicker(githubPoll)
	defer t.Stop()
	for {
		select {
		case <-x.ctx.Done():
			return
		case <-t.C:
			if id := x.sh.cfg.Store.Focus(); id != "" {
				x.sync(id, false)
			}
		}
	}
}

// wait is until every sync and forward started has finished.
func (x *githubExt) wait() { x.busy.Wait() }

func (x *githubExt) session(a registry.Args) (core.SessionState, bool) {
	id := core.SessionID(a.String("id"))
	if id == "" {
		id = x.sh.cfg.Store.Focus()
	}
	se, ok := x.h.Session(id)
	if !ok {
		return core.SessionState{}, false
	}
	return se.Snapshot(), true
}

func (x *githubExt) client(id core.SessionID) (worker.Client, error) {
	if x.sh.sessions == nil {
		return worker.Client{}, errors.New("no worker is connected")
	}
	return x.sh.sessions.client(id)
}

// trust is the trusted orgs and users, from the options.
func (x *githubExt) trust() worker.Trust {
	list := func(name string) []string {
		v, _, _ := x.h.Options().Get(name, registry.At{})
		l, _ := v.([]string)
		return l
	}
	return worker.Trust{Orgs: list("github.trusted_orgs"), Users: list("github.trusted_users")}
}

// sync asks the session's worker to mirror its PRs' reviews, off the loop.
func (x *githubExt) sync(id core.SessionID, fresh bool) {
	x.mu.Lock()
	if x.syncing[id] {
		x.mu.Unlock()
		return
	}
	x.syncing[id] = true
	x.mu.Unlock()
	trust := x.trust()
	x.busy.Add(1)
	go func() {
		defer x.busy.Done()
		var m worker.Mirror
		c, err := x.client(id)
		if err == nil {
			ctx, cancel := context.WithTimeout(x.ctx, 2*time.Minute)
			m, err = c.SyncGitHub(ctx, worker.MirrorReq{Session: id, Fresh: fresh, Trust: trust})
			cancel()
		}
		x.mu.Lock()
		delete(x.syncing, id)
		g := githubMirror{Mirror: m, at: x.now()}
		if err != nil {
			g = x.mirrors[id]
			g.err = err.Error()
		}
		x.mirrors[id] = g
		x.mu.Unlock()
		x.h.Redraw()
	}()
}

func (x *githubExt) mirror(id core.SessionID) (githubMirror, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	m, ok := x.mirrors[id]
	return m, ok
}

// draft is the session in front's thread and draft comment that a command names.
func (x *githubExt) draft(a registry.Args) (core.SessionState, core.Thread, core.Comment, error) {
	se, ok := x.session(a)
	if !ok {
		return se, core.Thread{}, core.Comment{}, errors.New("no session")
	}
	t, ok := se.Thread(core.ThreadID(a.String("thread")))
	if !ok {
		return se, t, core.Comment{}, fmt.Errorf("no thread %q", a.String("thread"))
	}
	ref := a.String("comment")
	for _, c := range t.Comments {
		if c.ID == ref || ref == "" && c.Draft {
			if !c.Draft {
				return se, t, c, errors.New("that reply isn't a draft")
			}
			return se, t, c, nil
		}
	}
	return se, t, core.Comment{}, errors.New("no draft reply in the thread")
}

// forward posts a draft to GitHub through the worker, as the user, off the loop.
func (x *githubExt) forward(a registry.Args) error {
	se, t, cm, err := x.draft(a)
	if err != nil {
		return err
	}
	c, err := x.client(se.ID)
	if err != nil {
		return err
	}
	x.mu.Lock()
	if x.forwarding[cm.ID] {
		x.mu.Unlock()
		return errors.New("that reply is being forwarded")
	}
	x.forwarding[cm.ID] = true
	x.mu.Unlock()
	delete(x.editing, cm.ID)
	x.busy.Add(1)
	go func() {
		defer x.busy.Done()
		ctx, cancel := context.WithTimeout(x.ctx, 2*time.Minute)
		_, err := c.ForwardGitHub(ctx, worker.ForwardReq{Session: se.ID, Thread: t.ID, Comment: cm.ID})
		cancel()
		x.mu.Lock()
		delete(x.forwarding, cm.ID)
		x.mu.Unlock()
		if err != nil {
			x.h.Notify(core.Notice{ID: "github.forward", Severity: core.SevError, Source: "github", Title: "Forward to GitHub", Text: err.Error()})
		}
		x.h.Redraw()
	}()
	return nil
}

// edit opens a draft for editing in its card.
func (x *githubExt) edit(a registry.Args) error {
	_, _, cm, err := x.draft(a)
	if err != nil {
		return err
	}
	if x.editing[cm.ID] == nil {
		body := cm.Body
		x.editing[cm.ID] = &body
	}
	return nil
}

// save keeps an edited draft, through the worker.
func (x *githubExt) save(a registry.Args) error {
	se, t, cm, err := x.draft(a)
	if err != nil {
		return err
	}
	body := a.String("body")
	if body == "" {
		if e := x.editing[cm.ID]; e != nil {
			body = *e
		}
	}
	if strings.TrimSpace(body) == "" {
		return errors.New("the draft is empty")
	}
	c, err := x.client(se.ID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(x.ctx, 30*time.Second)
	defer cancel()
	if err := c.EditDraft(ctx, worker.DraftEditReq{Session: se.ID, Thread: t.ID, Comment: cm.ID, Body: body}); err != nil {
		return err
	}
	delete(x.editing, cm.ID)
	return nil
}

func (x *githubExt) run(cmd string, a registry.Args) {
	if err := x.h.Run(context.Background(), cmd, a); err != nil {
		x.h.Notify(core.Notice{ID: "github", Severity: core.SevError, Source: "github", Text: err.Error()})
	}
}

// head is a GitHub thread's card head after "GitHub review": its line, the reviewer, where they stand, the sync.
func (x *githubExt) head(c *ui.Ctx, session, thread string) {
	th := c.Theme()
	se, ok := x.h.Session(core.SessionID(session))
	if !ok {
		return
	}
	t, ok := se.Snapshot().Thread(core.ThreadID(thread))
	if !ok || t.GitHub == nil {
		return
	}
	g := t.GitHub
	place := fmt.Sprintf("· #%d ·", g.PR)
	if t.Anchor != nil {
		place = fmt.Sprintf("· L%d ·", t.Anchor.Line)
	}
	c.Text("l", place, monoFont(10.5, th.C(theme.FgFaint)))
	if g.Reviewer != "" {
		widget.Chip(c, "who", "@"+g.Reviewer, th.C(tGitHub), "git/github#threads")
	}
	if word, tok := githubReviewWord(g.State); word != "" {
		widget.Chip(c, "state", word, th.C(tok), "git/github#threads")
	}
	if !t.Open() {
		widget.Chip(c, "status", string(t.Status), th.C(theme.StatusQuiet), "git/github#threads")
	}
	if g.Outdated {
		widget.Chip(c, "outdated", "outdated", th.C(theme.FgFaint), "git/github#threads")
	}
	c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
	if m, ok := x.mirror(core.SessionID(session)); ok && !m.at.IsZero() {
		c.Text("synced", "synced "+syncedAgo(c.Now(), m.at), monoFont(10, th.C(theme.FgGhost)))
	}
}

func githubReviewWord(s string) (string, theme.TokenID) {
	switch s {
	case gh.ReviewChangesRequested:
		return "changes requested", tGitHubDanger
	case gh.ReviewApproved:
		return "approved", tGitHubOK
	case gh.ReviewCommented:
		return "commented", theme.FgFaint
	}
	return "", theme.FgFaint
}

func syncedAgo(now, t time.Time) string {
	if d := now.Sub(t); d < time.Minute {
		return fmt.Sprintf("%ds ago", max(int(d.Seconds()), 0))
	}
	return ago(now, t) + " ago"
}

// comment is what goes under a draft reply: ↗ Forward to GitHub, Edit, and the promise; or the draft being edited.
func (x *githubExt) comment(c *ui.Ctx, session, thread, comment string, width float32) {
	th := c.Theme()
	se, ok := x.h.Session(core.SessionID(session))
	if !ok {
		return
	}
	t, ok := se.Snapshot().Thread(core.ThreadID(thread))
	if !ok {
		return
	}
	var cm core.Comment
	for _, x := range t.Comments {
		if x.ID == comment {
			cm = x
		}
	}
	if !cm.Draft {
		return
	}
	args := registry.Args{"id": session, "thread": thread, "comment": comment}
	if e := x.editing[comment]; e != nil {
		c.Column("edit", ui.Style{W: ui.Px(width), Gap: 6, Pad: ui.Insets{T: 6}}, func(c *ui.Ctx) {
			r := widget.Input(c, "draft", e, widget.InputOpts{Label: "Edit the draft reply", Lines: 8, MinLines: 3, Help: "git/github#forward"})
			c.Row("acts", ui.Style{Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				save := widget.Button(c, "save", "Save draft", widget.ButtonOpts{Primary: true, Tone: th.C(tGitHub), Help: "git/github#forward"})
				if save || r.Submitted {
					a := registry.Args{"body": *e}
					for k, v := range args {
						a[k] = v
					}
					x.run("github.draft.save", a)
				}
				if widget.Button(c, "cancel", "Cancel", widget.ButtonOpts{Ghost: true, Help: "git/github#forward"}) || r.Cancelled {
					delete(x.editing, comment)
				}
			})
		})
		return
	}
	x.mu.Lock()
	busy := x.forwarding[comment]
	x.mu.Unlock()
	c.Row("acts", ui.Style{Gap: 8, Cross: ui.Center, Pad: ui.Insets{T: 6}}, func(c *ui.Ctx) {
		label := "↗ Forward to GitHub"
		if busy {
			label = "Forwarding…"
		}
		if widget.Button(c, "forward", label, widget.ButtonOpts{Primary: true, Tone: th.C(tGitHub), Disabled: busy, Help: "git/github#forward"}) {
			x.run("github.forward", args)
		}
		if widget.Button(c, "edit", "Edit", widget.ButtonOpts{Ghost: true, Disabled: busy, Help: "git/github#forward"}) {
			x.run("github.edit", args)
		}
		c.Text("never", providerName(cmpOr(cm.Name, se.Snapshot().Agent.Provider))+" never posts to GitHub. You do.", uiFont(11, th.C(theme.FgFaint)))
	})
}

// statusSegment is "GH 1 changes requested": where the reviewers of the session in front's PRs stand.
func (x *githubExt) statusSegment(c *ui.Ctx) {
	id := x.sh.cfg.Store.Focus()
	m, ok := x.mirror(id)
	if !ok || len(m.PRs) == 0 {
		return
	}
	counts := map[string]int{}
	for _, pr := range m.PRs {
		for _, s := range pr.Reviews {
			counts[s]++
		}
	}
	var parts []string
	for _, s := range []string{gh.ReviewChangesRequested, gh.ReviewApproved} {
		if n := counts[s]; n > 0 {
			word, _ := githubReviewWord(s)
			parts = append(parts, fmt.Sprintf("%d %s", n, word))
		}
	}
	if len(parts) == 0 {
		return
	}
	text := "GH " + strings.Join(parts, " · ")
	c.Row("github", ui.Style{H: ui.Flex(1), Gap: 6, Cross: ui.Center, Help: "git/github#status", Role: ui.RoleStatus, Label: text}, func(c *ui.Ctx) {
		c.Text("t", text, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10.5, Weight: typeset.Medium}, Color: c.Theme().C(tGitHub)})
	})
}
