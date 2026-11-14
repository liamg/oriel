package ship

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/app/kit/prui"

	"github.com/liamg/oriel/app/kit/work"

	"github.com/liamg/oriel/registry/services"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/internal/clock"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
	"github.com/liamg/oriel/watch"
)

// GitHub review mirroring: the session's PRs' reviews are synced into threads by the session host
// (SyncGitHub), every githubPoll for the session in front, every reviewPoll for open review sessions behind it (each a
// subscription to its session's manager), and on demand. Their cards are this ThreadSource's: the
// GitHub header, and under the agent's draft reply ↗ Forward to GitHub and Edit. Only a forward posts, with the
// user's own gh. The status line says where the reviewers stand.

// githubPoll is how often the session in front is synced.
const githubPoll = 30 * time.Second

// GitHubStatusView is the status line's segment: "GH 1 changes requested".
const GitHubStatusView = "statusline.left.70-github"

var (
	tGitHub       = theme.Declare("github.accent", "violet")
	tGitHubDanger = theme.Declare("github.changes", "warn")
	tGitHubOK     = theme.Declare("github.approved", "ok")
)

type GitHub struct {
	kit.Disposer
	h      plugin.Host
	clock  clock.Clock
	ctx    context.Context
	cancel context.CancelFunc
	busy   work.Busy
	subs   kit.Subs   // the syncs it keeps, from the sessions' managers
	budget kit.Budget // review sessions' syncs, behind the one in front

	mu         sync.Mutex
	mirrors    map[core.SessionID]githubMirror
	syncing    map[core.SessionID]bool
	synced     map[core.SessionID]chan struct{} // closed when the sync running ends
	again      map[core.SessionID]bool          // a sync asked for while one ran: fresh or not
	forwarding map[string]bool                  // comment IDs being posted
	resolving  map[core.ThreadID]bool           // where each thread's resolve was last sent to its host
	lastSync   map[core.SessionID]time.Time

	editing map[string]*string // drafts being edited, by comment; on the loop
}

// githubMirror is a session's last sync, and when it came back.
type githubMirror struct {
	host.Mirror
	at  time.Time
	err string
}

// ended says every PR the mirror read is closed or merged.
func (m githubMirror) ended() bool {
	return len(m.PRs) > 0 && !slices.ContainsFunc(m.PRs, func(p host.MirrorPR) bool { return p.Open })
}

func (x *GitHub) Manifest() plugin.Manifest { return plugin.Manifest{ID: "github"} }

func (x *GitHub) Activate(_ context.Context, h plugin.Host) error {
	x.Add(h.Contribute(registry.Busy{Name: "github", Busy: x.busy.Busy}))
	x.h = h
	x.mirrors, x.syncing, x.forwarding, x.editing = map[core.SessionID]githubMirror{}, map[core.SessionID]bool{}, map[string]bool{}, map[string]*string{}
	x.again, x.resolving, x.lastSync = map[core.SessionID]bool{}, map[core.ThreadID]bool{}, map[core.SessionID]time.Time{}
	x.ctx, x.cancel = context.WithCancel(context.Background())
	x.Add(h.Contribute(registry.Service{Name: prui.PRReviewsService, Impl: githubReviews{x}}))
	x.Add(h.DeclareEvent(registry.EventInfo{Name: "PrPushed", Fired: true, Payload: kit.PrPushed{},
		Doc: "A session's pull request's head, each time GitHub is read for it; the subject is the session. { session, project, head }"}))
	x.Add(h.Option(registry.OptionSpec{Name: "github.trusted_orgs", Section: "git", Type: registry.List, Default: []string{}, Scopes: registry.ScopeUser,
		Help: "GitHub orgs whose members' review comments the agent may read. Text from anyone else is hidden from it. Empty: the repository's owner."}))
	x.Add(h.Option(registry.OptionSpec{Name: "github.trusted_users", Section: "git", Type: registry.List, Default: []string{}, Scopes: registry.ScopeUser,
		Help: "GitHub logins whose review comments the agent may read, beside the trusted orgs' members."}))
	for _, c := range x.commands() {
		x.Add(h.Command(c))
	}
	x.Add(h.Contribute(uireg.ThreadSource{Origin: core.OriginGitHub, Title: "GitHub review", Head: x.head, Comment: x.comment}))
	x.Add(h.Contribute(uireg.ThreadSource{Origin: core.OriginCodeHost, Title: "Review", Head: x.head, Comment: x.comment}))
	x.Add(h.View(GitHubStatusView, uireg.ViewFunc(func(uireg.ViewCtx) ui.Node { return &ui.Func{Key: "github", Fn: x.statusSegment} })))
	x.Add(h.Subscribe(core.Filter{}, func(ev core.Event) {
		switch ev := ev.(type) {
		case core.SessionFocused:
			if ev.ID != "" && x.syncedAgo(ev.ID) >= githubPoll { // one synced lately stands: each sync asks for every PR's comments
				x.sync(ev.ID, false)
			}
			x.follow()
		case core.SessionOpened, core.SessionArchived, core.SessionClosed:
			x.follow()
		case core.ThreadUpdated:
			h.Do(func() { x.resolveHost(ev.Session, ev.Thread) })
		}
	}))
	x.budget.PerHour = githubBudget
	x.follow()
	return nil
}

func (x *GitHub) commands() []registry.Command {
	inFront := func(a registry.Args) bool { _, ok := x.session(a); return ok }
	return []registry.Command{
		{ID: "github.sync", Title: "Sync the session's GitHub reviews into its threads now", Available: inFront, Run: func(_ registry.Ctx, a registry.Args) error {
			se, ok := x.session(a)
			if !ok {
				return errors.New("no session")
			}
			x.sync(se.ID, true)
			return nil
		}},
		{ID: "github.forward", Title: "Forward the agent's draft reply on a review thread to its code host, as you: {thread, comment}", Available: inFront,
			Run: func(_ registry.Ctx, a registry.Args) error { return x.forward(a) }},
		{ID: "github.edit", Title: "Edit the agent's draft reply on a GitHub thread before forwarding it: {thread, comment}", Available: inFront,
			Run: func(_ registry.Ctx, a registry.Args) error { return x.edit(a) }},
		{ID: "github.draft.save", Title: "Save an edited draft reply: {thread, comment, body}", Available: inFront,
			Run: func(_ registry.Ctx, a registry.Args) error { return x.save(a) }},
	}
}

func (x *GitHub) Deactivate(ctx context.Context) error {
	x.cancel()
	x.subs.Close()
	x.busy.Wait()
	return x.Disposer.Deactivate(ctx)
}

// reviewPoll is how often a review session not in front is synced with its pull request.
const reviewPoll = 2 * time.Minute

// githubRequests is about how many requests a sync makes; githubBudget the requests an hour review sessions' syncs
// may make together.
const (
	githubRequests = 5
	githubBudget   = 1000
)

// follow subscribes to the syncs it keeps: the session in front's every githubPoll, and each open review session's
// behind it every reviewPoll, so what its author pushes and says is seen while the user is elsewhere.
func (x *GitHub) follow() {
	st := x.h.State()
	keep := map[string]bool{}
	for _, se := range st.Sessions {
		front := se.ID == st.Focus
		if se.Archived {
			continue
		}
		if !front {
			if m, ok := x.mirror(se.ID); kit.ReviewedPR(x.h, se) == "" || ok && m.ended() {
				continue
			}
		}
		m := kit.ManagerFor(x.h, se.ID)
		if m == nil {
			continue
		}
		id := se.ID
		key := "github.sync:" + string(id)
		need := watch.Need{MaxAge: githubPoll, Background: watch.Slow, Slow: kit.UnfocusedPoll}
		if !front {
			need = watch.Need{MaxAge: reviewPoll, Background: watch.Slow, Slow: kit.UnfocusedPoll}
		}
		keep[key] = true
		x.subs.Hold(key, m, need, func() kit.Closer {
			src := kit.Every(key, func(ctx context.Context) error { return x.syncWait(ctx, id) }, func(maxAge time.Duration) time.Duration {
				if maxAge < reviewPoll {
					return maxAge
				}
				return x.budget.Interval(maxAge)
			})
			return budgetedSync{watch.Subscribe(m, src, need, "github: review sync"), x.budget.Use(key, githubRequests)}
		})
	}
	x.subs.KeepOnly(keep)
}

// budgetedSync is a sync's subscription, counted against the budget until it's closed.
type budgetedSync struct {
	watch.Sub[time.Time]
	release func()
}

func (b budgetedSync) Close() {
	b.Sub.Close()
	b.release()
}

// syncWait syncs the session, or joins the sync running, and waits for it.
func (x *GitHub) syncWait(ctx context.Context, id core.SessionID) error {
	x.sync(id, false)
	x.mu.Lock()
	done := x.synced[id]
	x.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
	case <-ctx.Done():
	}
	if m, ok := x.mirror(id); ok && m.err != "" {
		return errors.New(m.err)
	}
	return nil
}

// resolveHost takes a thread's resolving or reopening in Oriel to the review thread it mirrors on its code host,
// once for each change.
func (x *GitHub) resolveHost(id core.SessionID, t core.Thread) {
	g := t.Hosted
	if g == nil || g.Root == 0 && g.Key == "" || t.Status == core.ThreadDismissed || t.Status == core.ThreadDeferred {
		x.mu.Lock()
		delete(x.resolving, t.ID)
		x.mu.Unlock()
		return
	}
	want := t.Status == core.ThreadResolved
	x.mu.Lock()
	sent, ok := x.resolving[t.ID]
	if want == g.Resolved || ok && sent == want {
		if want == g.Resolved {
			delete(x.resolving, t.ID)
		}
		x.mu.Unlock()
		return
	}
	x.resolving[t.ID] = want
	x.mu.Unlock()
	word := map[bool]string{true: "Resolving", false: "Reopening"}[want]
	fail := func(err error) {
		x.h.Notify(core.Notice{ID: "github.resolve", Severity: core.SevWarning, Session: id, Text: fmt.Sprintf("%s the thread on its code host: %v", word, err)})
	}
	if g.Host != "" {
		s, ok := registry.ShipsOf(x.h)
		if !ok {
			return
		}
		if se, ok := x.h.Session(id); ok {
			if err := s.ResolveThread(se.Snapshot(), t, want); err != nil {
				fail(err)
			}
		}
		return
	}
	c, err := x.client(id)
	if err != nil {
		return
	}
	x.busy.Go(func() {
		ctx, cancel := context.WithTimeout(x.ctx, time.Minute)
		defer cancel()
		if err := c.ResolveGitHub(ctx, host.ResolveReq{Session: id, Thread: t.ID, Resolved: want}); err != nil {
			fail(err)
		}
	})
}

// wait is until every sync and forward started has finished.
func (x *GitHub) wait() { x.busy.Wait() }

func (x *GitHub) session(a registry.Args) (core.SessionState, bool) {
	se, err := kit.SessionFrom(x.h, a)
	return se, err == nil
}

func (x *GitHub) client(id core.SessionID) (host.CodeHostCalls, error) {
	return kit.CodeHostCalls(x.h)
}

// trust is the trusted orgs and users, from the options.
func (x *GitHub) trust() host.Trust {
	list := func(name string) []string {
		l, _ := registry.Option[[]string](x.h.Options(), name, registry.At{})
		return l
	}
	return host.Trust{Orgs: list("github.trusted_orgs"), Users: list("github.trusted_users")}
}

// sync asks the session host to mirror the session's PRs' reviews, off the loop.
func (x *GitHub) sync(id core.SessionID, fresh bool) {
	x.mu.Lock()
	if x.syncing[id] {
		x.again[id] = x.again[id] || fresh
		x.mu.Unlock()
		return
	}
	x.syncing[id] = true
	if x.synced == nil {
		x.synced = map[core.SessionID]chan struct{}{}
	}
	done := make(chan struct{})
	x.synced[id] = done
	x.mu.Unlock()
	trust := x.trust()
	x.busy.Go(func() {
		var m host.Mirror
		c, err := x.client(id)
		if err == nil {
			ctx, cancel := context.WithTimeout(x.ctx, 2*time.Minute)
			m, err = c.SyncGitHub(ctx, host.MirrorReq{Session: id, Fresh: fresh, Trust: trust})
			cancel()
		}
		x.mu.Lock()
		delete(x.syncing, id)
		delete(x.synced, id)
		close(done)
		x.lastSync[id] = clock.Or(x.clock).Now()
		g := githubMirror{Mirror: m, at: clock.Or(x.clock).Now()}
		if err != nil {
			g = x.mirrors[id]
			g.err = err.Error()
		}
		x.mirrors[id] = g
		again, queued := x.again[id]
		delete(x.again, id)
		x.mu.Unlock()
		if err == nil {
			for _, pr := range m.PRs {
				if pr.Head != "" {
					ev := kit.PrPushed{Session: string(id), Project: pr.Project, Head: pr.Head}
					x.h.Do(func() { x.h.Fire(context.Background(), "PrPushed", ev.Session, &ev) })
				}
			}
		}
		x.h.Redraw()
		if queued {
			x.sync(id, again)
		} else if x.ctx.Err() == nil && g.ended() {
			x.follow() // its PRs are all closed: stop syncing it
		}
	})
}

// syncedAgo is how long ago session id last synced; forever if it hasn't.
func (x *GitHub) syncedAgo(id core.SessionID) time.Duration {
	x.mu.Lock()
	defer x.mu.Unlock()
	at, ok := x.lastSync[id]
	if !ok {
		return time.Duration(1<<63 - 1)
	}
	return clock.Or(x.clock).Now().Sub(at)
}

func (x *GitHub) mirror(id core.SessionID) (githubMirror, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	m, ok := x.mirrors[id]
	return m, ok
}

// draft is the session in front's thread and draft comment that a command names.
func (x *GitHub) draft(a registry.Args) (core.SessionState, core.Thread, core.Comment, error) {
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

// forward posts a draft to its thread's code host, as the user, off the loop: GitHub through the session host, another
// through its plugin (the Ship page's).
func (x *GitHub) forward(a registry.Args) error {
	se, t, cm, err := x.draft(a)
	if err != nil {
		return err
	}
	if t.Hosted != nil && t.Hosted.Host != "" {
		s, ok := registry.ShipsOf(x.h)
		if !ok {
			return errors.New("the Ship page isn't running")
		}
		delete(x.editing, cm.ID)
		return s.ForwardThread(se, t.ID, cm.ID)
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
	x.busy.Go(func() {
		ctx, cancel := context.WithTimeout(x.ctx, 2*time.Minute)
		_, err := c.ForwardGitHub(ctx, host.ForwardReq{Session: se.ID, Thread: t.ID, Comment: cm.ID})
		cancel()
		x.mu.Lock()
		delete(x.forwarding, cm.ID)
		x.mu.Unlock()
		if err != nil {
			x.h.Notify(core.Notice{ID: "github.forward", Severity: core.SevError, Source: "github", Title: "Forward to GitHub", Text: err.Error()})
		}
		x.h.Redraw()
	})
	return nil
}

// edit opens a draft for editing in its card.
func (x *GitHub) edit(a registry.Args) error {
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

// save keeps an edited draft.
func (x *GitHub) save(a registry.Args) error {
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
	if err := c.EditDraft(host.DraftEditReq{Session: se.ID, Thread: t.ID, Comment: cm.ID, Body: body}); err != nil {
		return err
	}
	delete(x.editing, cm.ID)
	return nil
}

func (x *GitHub) run(cmd string, a registry.Args) {
	kit.RunOrNotify(x.h, "github", cmd, a)
}

// head is a GitHub thread's card head after "GitHub review": its line, the reviewer, where they stand, the sync.
func (x *GitHub) head(c *ui.Ctx, session, thread string) {
	th := c.Theme()
	se, ok := x.h.Session(core.SessionID(session))
	if !ok {
		return
	}
	t, ok := se.Snapshot().Thread(core.ThreadID(thread))
	if !ok || t.Hosted == nil {
		return
	}
	g := t.Hosted
	place := fmt.Sprintf("· %s%d ·", hostWords(x.h, g.Host).Sigil, g.PR)
	if t.Anchor != nil {
		place = fmt.Sprintf("· L%d ·", t.Anchor.Line)
	}
	c.Text("l", place, kit.MonoFont(10.5, th.C(theme.FgFaint)))
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
		c.Text("synced", "synced "+syncedAgo(c.Now(), m.at), kit.MonoFont(10, th.C(theme.FgGhost)))
	}
}

// githubReviewWord is a reviewer's state, GitHub's or another code host's (registry.ReviewChanges…), in words.
func githubReviewWord(s string) (string, theme.TokenID) {
	switch s {
	case gh.ReviewChangesRequested, registry.ReviewChanges:
		return "changes requested", tGitHubDanger
	case gh.ReviewApproved, registry.ReviewApproved:
		return "approved", tGitHubOK
	case gh.ReviewCommented, registry.ReviewCommented:
		return "commented", theme.FgFaint
	}
	return "", theme.FgFaint
}

// hostWords are how a mirrored thread's code host names a PR; GitHub's when host is "".
func hostWords(r registry.Registry, host string) registry.Words {
	h, _ := registry.HostByName(r, cmp.Or(host, "github"))
	return h.Said()
}

// hostLabel is a mirrored thread's code host's name: "GitHub", "GitLab".
func hostLabel(r registry.Registry, g *core.HostThread) string {
	name := "github"
	if g != nil && g.Host != "" {
		name = g.Host
	}
	if h, ok := registry.HostByName(r, name); ok {
		return h.Label()
	}
	return "GitHub"
}

func syncedAgo(now, t time.Time) string {
	if d := now.Sub(t); d < time.Minute {
		return fmt.Sprintf("%ds ago", max(int(d.Seconds()), 0))
	}
	return widget.AgoLabel(now, t) + " ago"
}

// comment is what goes under a draft reply: ↗ Forward to GitHub, Edit, and the promise; or the draft being edited.
func (x *GitHub) comment(c *ui.Ctx, session, thread, comment string, width float32) {
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
			r := widget.Prose(c, "draft", e, widget.ProseOpts{Label: "Edit the draft reply", Lines: 8, MinLines: 3, Help: "git/github#forward"})
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
		if widget.Button(c, "forward", "↗ Forward to "+hostLabel(x.h, t.Hosted), widget.ButtonOpts{Primary: true, Tone: th.C(tGitHub), Busy: busy,
			BusyLabel: "Forwarding", Help: "git/github#forward"}) {
			x.run("github.forward", args)
		}
		if widget.Button(c, "edit", "Edit", widget.ButtonOpts{Ghost: true, Disabled: busy, Help: "git/github#forward"}) {
			x.run("github.edit", args)
		}
		c.Text("never", services.ProviderTitle(x.h, cmp.Or(cm.Name, se.Snapshot().Agent.Provider))+" never posts to "+hostLabel(x.h, t.Hosted)+". You do.",
			kit.UIFont(11, th.C(theme.FgFaint)))
	})
}

// githubReviews is the PRReviewsService from what the last read of each session's PRs found.
type githubReviews struct{ x *GitHub }

func (g githubReviews) Reviews(id core.SessionID, url string) []string {
	m, ok := g.x.mirror(id)
	if !ok {
		return nil
	}
	for _, pr := range m.PRs {
		if pr.URL == url {
			return slices.Collect(maps.Values(pr.Reviews))
		}
	}
	return nil
}

// statusSegment is "GH 1 changes requested": where the reviewers of the session in front's PRs stand.
func (x *GitHub) statusSegment(c *ui.Ctx) {
	id := x.h.State().Focus
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
