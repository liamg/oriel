// Package mirror brings a session's pull requests' review threads, from any code host that gives them, into its
// threads: the session in front's every syncPoll, each open review session's behind it every reviewPoll (each a
// subscription to its session's manager), and on demand. Its cards are this package's ThreadSource: the reviewer and
// where they stand, and under the agent's draft reply ↗ Forward and Edit. Only a forward posts, as the user. The
// status line says where the reviewers stand, and services.PRReviews gives a PR's reviewers' verdicts.
package mirror

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/kit/prui"
	"github.com/liamg/oriel/app/kit/sessionkit"
	"github.com/liamg/oriel/app/kit/work"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostapi"
	"github.com/liamg/oriel/internal/clock"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/services"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
	"github.com/liamg/oriel/watch"
)

// syncPoll is how often the session in front is synced.
const syncPoll = 30 * time.Second

// StatusView is the status line's segment: "PR 1 changes requested".
const StatusView = "statusline.left.70-reviews"

type Mirror struct {
	kit.Disposer
	h      plugin.Host
	clock  clock.Clock
	ctx    context.Context
	cancel context.CancelFunc
	busy   work.Busy
	subs   kit.Subs   // the syncs it keeps, from the sessions' managers
	budget kit.Budget // review sessions' syncs, behind the one in front

	mu         sync.Mutex
	reads      map[core.SessionID]read
	prs        map[string]prCached // a branch's PR, by session/project/branch
	syncing    map[core.SessionID]bool
	synced     map[core.SessionID]chan struct{} // closed when the sync running ends
	again      map[core.SessionID]bool          // a sync asked for while one ran: fresh or not
	forwarding map[string]bool                  // comment IDs being posted
	resolving  map[core.ThreadID]bool           // where each thread's resolve was last sent to its host
	lastSync   map[core.SessionID]time.Time

	editing map[string]*string // drafts being edited, by comment; on the loop
}

// read is a session's last sync, and when it came back.
type read struct {
	prs []prRead
	at  time.Time
	err error
}

// prRead is a project's PR as the last sync read it: its code host, where, and its reviewers' verdicts.
type prRead struct {
	project string
	host    registry.CodeHost
	repo    registry.CodeRepo
	pr      registry.PR
	reviews map[string]string // reviewer → registry.ReviewChanges, ReviewApproved, ReviewCommented
}

// prCached is a branch's PR as its code host last answered, and when.
type prCached struct {
	pr *registry.PR
	at time.Time
}

// ended says every PR the sync read is closed or merged.
func (m read) ended() bool {
	return len(m.prs) > 0 && !slices.ContainsFunc(m.prs, func(p prRead) bool { return p.pr.State != "closed" && !p.pr.Merged })
}

func (x *Mirror) Manifest() plugin.Manifest { return plugin.Manifest{ID: "mirror"} }

func (x *Mirror) Activate(_ context.Context, h plugin.Host) error {
	x.Add(h.Contribute(registry.Busy{Name: "mirror", Busy: x.busy.Busy}))
	x.h = h
	x.reads, x.prs, x.syncing, x.forwarding, x.editing = map[core.SessionID]read{}, map[string]prCached{}, map[core.SessionID]bool{}, map[string]bool{}, map[string]*string{}
	x.again, x.resolving, x.lastSync = map[core.SessionID]bool{}, map[core.ThreadID]bool{}, map[core.SessionID]time.Time{}
	x.ctx, x.cancel = context.WithCancel(context.Background())
	x.Add(h.Contribute(registry.Service{Name: services.PRReviewsService, Impl: reviews{x}}))
	x.Add(h.DeclareEvent(registry.EventInfo{Name: "PrPushed", Fired: true, Payload: prui.PrPushed{},
		Doc: "A session's pull request's head, each time its code host is read for it; the subject is the session. { session, project, head }"}))
	for _, c := range x.commands() {
		x.Add(h.Command(c))
	}
	x.Add(h.Contribute(uireg.ThreadSource{Origin: core.OriginCodeHost, Title: "Review", Head: x.head, Comment: x.comment}))
	x.Add(h.View(StatusView, uireg.ViewFunc(func(uireg.ViewCtx) ui.Node { return &ui.Func{Key: "reviews", Fn: x.statusSegment} })))
	x.Add(h.Subscribe(core.Filter{}, func(ev core.Event) {
		switch ev := ev.(type) {
		case core.SessionFocused:
			if ev.ID != "" && x.syncedAgo(ev.ID) >= syncPoll { // one synced lately stands: each sync asks for every PR's comments
				x.sync(ev.ID, false)
			}
			x.follow()
		case core.SessionOpened, core.SessionArchived, core.SessionClosed:
			x.follow()
		case core.ThreadUpdated:
			h.Do(func() { x.resolveHost(ev.Session, ev.Thread) })
		}
	}))
	x.budget.PerHour = syncBudget
	x.follow()
	return nil
}

func (x *Mirror) commands() []registry.Command {
	inFront := func(a registry.Args) bool { _, ok := x.session(a); return ok }
	return []registry.Command{
		{ID: "review.threads.sync", Title: "Mirror the session's pull requests' review threads now", Available: inFront, Run: func(_ registry.Ctx, a registry.Args) error {
			se, ok := x.session(a)
			if !ok {
				return errors.New("no session")
			}
			x.sync(se.ID, true)
			return nil
		}},
		{ID: "review.draft.forward", Title: "Forward the agent's draft reply on a review thread to its code host, as you: {thread, comment}", Available: inFront,
			Run: func(_ registry.Ctx, a registry.Args) error { return x.forward(a) }},
		{ID: "review.draft.edit", Title: "Edit the agent's draft reply on a review thread before forwarding it: {thread, comment}", Available: inFront,
			Run: func(_ registry.Ctx, a registry.Args) error { return x.edit(a) }},
		{ID: "review.draft.save", Title: "Save an edited draft reply: {thread, comment, body}", Available: inFront,
			Run: func(_ registry.Ctx, a registry.Args) error { return x.save(a) }},
	}
}

func (x *Mirror) Deactivate(ctx context.Context) error {
	x.cancel()
	x.subs.Close()
	x.busy.Wait()
	return x.Disposer.Deactivate(ctx)
}

// reviewPoll is how often a review session not in front is synced with its pull request.
const reviewPoll = 2 * time.Minute

// syncRequests is about how many requests a sync makes; syncBudget the requests an hour review sessions' syncs may
// make together.
const (
	syncRequests = 5
	syncBudget   = 1000
)

// follow subscribes to the syncs it keeps: the session in front's every syncPoll, and each open review session's
// behind it every reviewPoll, so what its author pushes and says is seen while the user is elsewhere.
func (x *Mirror) follow() {
	st := x.h.State()
	keep := map[string]bool{}
	for _, se := range st.Sessions {
		front := se.ID == st.Focus
		if se.Archived {
			continue
		}
		if !front {
			if m, ok := x.read(se.ID); services.ReviewedPR(x.h, se) == "" || ok && m.ended() {
				continue
			}
		}
		m := kit.ManagerFor(x.h, se.ID)
		if m == nil {
			continue
		}
		id := se.ID
		key := "mirror.sync:" + string(id)
		need := watch.Need{MaxAge: syncPoll, Background: watch.Slow, Slow: work.UnfocusedPoll}
		if !front {
			need = watch.Need{MaxAge: reviewPoll, Background: watch.Slow, Slow: work.UnfocusedPoll}
		}
		keep[key] = true
		x.subs.Hold(key, m, need, func() kit.Closer {
			src := kit.Every(key, func(ctx context.Context) error { return x.syncWait(ctx, id) }, func(maxAge time.Duration) time.Duration {
				if maxAge < reviewPoll {
					return maxAge
				}
				return x.budget.Interval(maxAge)
			})
			return budgetedSync{watch.Subscribe(m, src, need, "review mirror"), x.budget.Use(key, syncRequests)}
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
func (x *Mirror) syncWait(ctx context.Context, id core.SessionID) error {
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
	if m, ok := x.read(id); ok && m.err != nil {
		return m.err
	}
	return nil
}

// resolveHost takes a thread's resolving or reopening in Oriel to the review thread it mirrors on its code host,
// once for each change.
func (x *Mirror) resolveHost(id core.SessionID, t core.Thread) {
	g := t.Hosted
	if g == nil || g.Key == "" || t.Status == core.ThreadDismissed || t.Status == core.ThreadDeferred {
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
		x.h.Notify(core.Notice{ID: "review.resolve", Severity: core.SevWarning, Session: id, Text: fmt.Sprintf("%s the thread on its code host: %v", word, err)})
	}
	p, err := x.prOf(id, g)
	if err != nil {
		fail(err)
		return
	}
	if p.host.Resolve == nil {
		fail(fmt.Errorf("%s can't resolve threads from Oriel", p.host.Label()))
		return
	}
	c, err := sessionkit.CodeHostCalls(x.h)
	if err != nil {
		return
	}
	rt := hostThread(t)
	x.busy.Go(func() {
		ctx, cancel := context.WithTimeout(x.ctx, time.Minute)
		defer cancel()
		err := registry.Call(func() error { return p.host.Resolve(ctx, p.repo, p.pr, rt, want) })
		if err == nil {
			err = c.MarkHostResolved(hostapi.ResolveReq{Session: id, Thread: t.ID, Resolved: want})
		}
		if err != nil {
			fail(err)
		}
	})
}

// prOf is the PR a mirrored thread is on, as the session's last sync read it.
func (x *Mirror) prOf(id core.SessionID, g *core.HostThread) (prRead, error) {
	m, _ := x.read(id)
	for _, p := range m.prs {
		if p.project == g.Project && p.host.Name == g.Host && p.pr.Number == g.PR {
			return p, nil
		}
	}
	return prRead{}, fmt.Errorf("%s's pull request hasn't been read yet", g.Project)
}

// hostThread is a mirrored thread as its code host's calls take it.
func hostThread(t core.Thread) registry.ReviewThread {
	rt := registry.ReviewThread{ID: t.Hosted.Key, URL: t.Hosted.URL, Outdated: t.Hosted.Outdated}
	if a := t.Anchor; a != nil && rt.ID != "" {
		rt.Path, rt.Line, rt.Old = a.File, a.Line, a.Side == "old"
	}
	return rt
}

// wait is until every sync and forward started has finished.
func (x *Mirror) wait() { x.busy.Wait() }

func (x *Mirror) session(a registry.Args) (core.SessionState, bool) {
	se, err := sessionkit.SessionFrom(x.h, a)
	return se, err == nil
}

// sync mirrors the session's PRs' review threads, off the loop.
func (x *Mirror) sync(id core.SessionID, fresh bool) {
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
	se, ok := x.h.Session(id)
	var state core.SessionState
	if ok {
		state = se.Snapshot()
	}
	reviewed := services.ReviewedPR(x.h, state)
	x.busy.Go(func() {
		var prs []prRead
		err := errors.New("no session")
		if ok {
			ctx, cancel := context.WithTimeout(x.ctx, 2*time.Minute)
			prs, err = x.mirrorSession(ctx, state, reviewed, fresh)
			cancel()
		}
		x.mu.Lock()
		delete(x.syncing, id)
		delete(x.synced, id)
		close(done)
		x.lastSync[id] = clock.Or(x.clock).Now()
		m := read{prs: prs, at: clock.Or(x.clock).Now()}
		if err != nil {
			m = x.reads[id]
			m.err = err
		}
		x.reads[id] = m
		again, queued := x.again[id]
		delete(x.again, id)
		x.mu.Unlock()
		if err == nil {
			for _, p := range prs {
				if p.pr.Head != "" {
					ev := prui.PrPushed{Session: string(id), Project: p.project, Head: p.pr.Head}
					x.h.Do(func() { x.h.Fire(context.Background(), "PrPushed", ev.Session, &ev) })
				}
			}
		}
		x.h.Redraw()
		if queued {
			x.sync(id, again)
		} else if x.ctx.Err() == nil && m.ended() {
			x.follow() // its PRs are all closed: stop syncing it
		}
	})
}

// mirrorSession brings each of the session's projects' PR review threads, from its code host, into the session: the
// PR its branch has, or the one a review session reads. A project whose host gives no threads is passed over.
func (x *Mirror) mirrorSession(ctx context.Context, se core.SessionState, reviewed string, fresh bool) ([]prRead, error) {
	c, err := sessionkit.CodeHostCalls(x.h)
	if err != nil {
		return nil, err
	}
	var out []prRead
	var errs []error
	for _, p := range se.Projects {
		review := reviewed != "" && p.Detached && strings.HasPrefix(p.Base, "refs/oriel/pr/")
		if !review && (p.ReadOnly || p.Detached || p.Branch == "") {
			continue
		}
		remote, err := c.ProjectRemote(ctx, hostapi.ShipRef{Session: se.ID, Project: p.Name})
		if err != nil {
			continue
		}
		sites := kit.Sites(x.h)
		hs, name, ok := sites.Remote(remote)
		if review {
			hs, ok = sites.URL(reviewed)
		}
		if !ok || hs.Threads == nil || !sites.Keyed(hs) && !hs.Tokens.Optional {
			continue
		}
		h := hs.CodeHost
		var pr *registry.PR
		if review {
			pr, name, err = x.reviewed(ctx, hs, reviewed)
		}
		repo := registry.CodeRepo{Session: string(se.ID), Project: p.Name, Remote: remote, Repo: name, Site: hs.Site}
		if !review {
			pr, err = x.branchPR(ctx, se.ID, h, repo, p.Branch, fresh)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p.Name, err))
			continue
		}
		if pr == nil {
			continue
		}
		var ts []registry.ReviewThread
		if err := registry.Call(func() (err error) { ts, err = h.Threads(ctx, repo, *pr); return err }); err != nil {
			sites.Refuse(hs, err)
			errs = append(errs, fmt.Errorf("%s: %w", p.Name, err))
			continue
		}
		req := hostapi.MirrorReviewReq{Session: se.ID, Project: p.Name, Host: h.Name, Repo: name, PR: pr.Number, URL: pr.URL, Author: pr.Author}
		for _, t := range ts {
			rt := hostapi.ReviewThread{Key: t.ID, Path: t.Path, Line: t.Line, Old: t.Old, Outdated: t.Outdated, Resolved: t.Resolved,
				Reviewer: t.Reviewer, State: t.State, URL: t.URL}
			for _, n := range t.Comments {
				rt.Comments = append(rt.Comments, hostapi.ReviewComment{Key: n.ID, User: n.Author, Body: n.Body, At: n.At,
					Trusted: n.Member || sites.TrustedUser(n.Author), Mine: n.Mine, Written: n.Written})
			}
			req.Threads = append(req.Threads, rt)
		}
		if err := c.MirrorReview(ctx, req); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p.Name, err))
			continue
		}
		out = append(out, prRead{project: p.Name, host: h, repo: repo, pr: *pr, reviews: verdicts(ts)})
	}
	if len(out) == 0 && len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return out, nil
}

// branchPR is a branch's PR on its code host: the last syncPoll's answer unless fresh.
func (x *Mirror) branchPR(ctx context.Context, id core.SessionID, h registry.CodeHost, repo registry.CodeRepo, branch string, fresh bool) (*registry.PR, error) {
	if h.ForBranch == nil {
		return nil, nil
	}
	key := string(id) + "/" + repo.Project + "/" + branch
	now := clock.Or(x.clock).Now()
	x.mu.Lock()
	c, ok := x.prs[key]
	x.mu.Unlock()
	if ok && !fresh && now.Sub(c.at) < syncPoll {
		return c.pr, nil
	}
	var pr *registry.PR
	if err := registry.Call(func() (err error) { pr, err = h.ForBranch(ctx, repo, branch); return err }); err != nil {
		return nil, err
	}
	x.mu.Lock()
	x.prs[key] = prCached{pr: pr, at: now}
	x.mu.Unlock()
	return pr, nil
}

// reviewed is the pull request a review session reads, as its code host has it now, and its repo there.
func (x *Mirror) reviewed(ctx context.Context, hs registry.HostSite, link string) (*registry.PR, string, error) {
	h := hs.CodeHost
	if h.Lookup == nil {
		return nil, "", fmt.Errorf("%s can't look a pull request up", h.Label())
	}
	var d registry.ReviewDetail
	if err := registry.Call(func() (err error) { d, err = h.Lookup(ctx, hs.Site, link); return err }); err != nil {
		return nil, "", err
	}
	n := d.Request.Number
	if u, err := url.Parse(link); n == 0 && err == nil {
		n, _ = h.PRNumber(u.Path)
	}
	if n == 0 || d.Request.Repo == "" {
		return nil, "", fmt.Errorf("%s isn't one of %s's pull requests", link, h.Label())
	}
	pr := &registry.PR{Number: n, URL: link, Author: d.Request.Author, Head: d.Commit, Merged: d.State == "merged", State: "open"}
	if d.State == "closed" || d.State == "merged" {
		pr.State = "closed"
	}
	return pr, d.Request.Repo, nil
}

// verdicts are each reviewer's latest among a PR's review threads and its conversation's reviewers: approved or
// changes over commented.
func verdicts(ts []registry.ReviewThread) map[string]string {
	out := map[string]string{}
	add := func(who, state string) {
		if who == "" || state == "" || state == registry.ReviewCommented && out[who] != "" {
			return
		}
		out[who] = state
	}
	for _, t := range ts {
		add(t.Reviewer, t.State)
		for _, r := range t.Reviewers {
			add(r.Login, r.State)
		}
	}
	return out
}

// syncedAgo is how long ago session id last synced; forever if it hasn't.
func (x *Mirror) syncedAgo(id core.SessionID) time.Duration {
	x.mu.Lock()
	defer x.mu.Unlock()
	at, ok := x.lastSync[id]
	if !ok {
		return time.Duration(1<<63 - 1)
	}
	return clock.Or(x.clock).Now().Sub(at)
}

func (x *Mirror) read(id core.SessionID) (read, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	m, ok := x.reads[id]
	return m, ok
}

// draft is the session in front's thread and draft comment that a command names.
func (x *Mirror) draft(a registry.Args) (core.SessionState, core.Thread, core.Comment, error) {
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

// forward posts a draft to its thread's code host, as the user, off the loop.
func (x *Mirror) forward(a registry.Args) error {
	se, t, cm, err := x.draft(a)
	if err != nil {
		return err
	}
	if t.Hosted == nil {
		return errors.New("this thread isn't from a code host")
	}
	p, err := x.prOf(se.ID, t.Hosted)
	if err != nil {
		return err
	}
	if p.host.Reply == nil {
		return fmt.Errorf("%s can't post replies from Oriel", p.host.Label())
	}
	c, err := sessionkit.CodeHostCalls(x.h)
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
		_, err := c.ForwardHost(ctx, hostapi.ForwardReq{Session: se.ID, Thread: t.ID, Comment: cm.ID}, func(ctx context.Context, t core.Thread, body string) (posted string, err error) {
			err = registry.Call(func() (err error) { posted, err = p.host.Reply(ctx, p.repo, p.pr, hostThread(t), body); return err })
			return posted, err
		})
		cancel()
		x.mu.Lock()
		delete(x.forwarding, cm.ID)
		x.mu.Unlock()
		if err != nil {
			kit.NotifyFailure(x.h, core.Notice{ID: "review.forward", Source: "mirror", Title: "Forward to " + p.host.Label()}, err)
		}
		x.h.Redraw()
	})
	return nil
}

// edit opens a draft for editing in its card.
func (x *Mirror) edit(a registry.Args) error {
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
func (x *Mirror) save(a registry.Args) error {
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
	c, err := sessionkit.CodeHostCalls(x.h)
	if err != nil {
		return err
	}
	if err := c.EditDraft(hostapi.DraftEditReq{Session: se.ID, Thread: t.ID, Comment: cm.ID, Body: body}); err != nil {
		return err
	}
	delete(x.editing, cm.ID)
	return nil
}

func (x *Mirror) run(cmd string, a registry.Args) {
	kit.RunOrNotify(x.h, "mirror", cmd, a)
}

// head is a mirrored thread's card head after its title: its line, the reviewer, where they stand, the sync.
func (x *Mirror) head(c *ui.Ctx, session, thread string) {
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
	c.Text("l", place, widget.MonoFont(10.5, th.C(theme.FgFaint)))
	if g.Reviewer != "" {
		widget.Chip(c, "who", "@"+g.Reviewer, th.C(prui.TReviewAccent), "git/github#threads")
	}
	if word, tok := prui.ReviewWord(g.State); word != "" {
		widget.Chip(c, "state", word, th.C(tok), "git/github#threads")
	}
	if !t.Open() {
		widget.Chip(c, "status", string(t.Status), th.C(theme.StatusQuiet), "git/github#threads")
	}
	if g.Outdated {
		widget.Chip(c, "outdated", "outdated", th.C(theme.FgFaint), "git/github#threads")
	}
	c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
	if m, ok := x.read(core.SessionID(session)); ok && !m.at.IsZero() {
		c.Text("synced", "synced "+syncedAgo(c.Now(), m.at), widget.MonoFont(10, th.C(theme.FgGhost)))
	}
}

// hostWords are how a mirrored thread's code host names a PR.
func hostWords(r registry.Registry, host string) registry.Words {
	h, _ := registry.HostByName(r, host)
	return h.Said()
}

// hostLabel is a mirrored thread's code host's name: "GitHub", "GitLab".
func hostLabel(r registry.Registry, g *core.HostThread) string {
	if g != nil {
		if h, ok := registry.HostByName(r, g.Host); ok {
			return h.Label()
		}
		return g.Host
	}
	return "its code host"
}

func syncedAgo(now, t time.Time) string {
	if d := now.Sub(t); d < time.Minute {
		return fmt.Sprintf("%ds ago", max(int(d.Seconds()), 0))
	}
	return widget.AgoLabel(now, t) + " ago"
}

// comment is what goes under a draft reply: ↗ Forward, Edit, and when it's sent; or the draft being edited.
func (x *Mirror) comment(c *ui.Ctx, session, thread, comment string, width float32) {
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
				save := widget.Button(c, "save", "Save draft", widget.ButtonOpts{Primary: true, Tone: th.C(prui.TReviewAccent), Help: "git/github#forward"})
				if save || r.Submitted {
					a := registry.Args{"body": *e}
					for k, v := range args {
						a[k] = v
					}
					x.run("review.draft.save", a)
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
		if widget.Button(c, "forward", "↗ Forward to "+hostLabel(x.h, t.Hosted), widget.ButtonOpts{Primary: true, Tone: th.C(prui.TReviewAccent), Busy: busy,
			BusyLabel: "Forwarding", Help: "git/github#forward"}) {
			x.run("review.draft.forward", args)
		}
		if widget.Button(c, "edit", "Edit", widget.ButtonOpts{Ghost: true, Disabled: busy, Help: "git/github#forward"}) {
			x.run("review.draft.edit", args)
		}
		c.Text("draft", services.ProviderTitle(x.h, cmp.Or(cm.Name, se.Snapshot().Agent.Provider))+"'s draft reaches "+hostLabel(x.h, t.Hosted)+" when you forward it.",
			widget.UIFont(11, th.C(theme.FgFaint)))
	})
}

// reviews is the PRReviewsService from what the last sync of each session's PRs found.
type reviews struct{ x *Mirror }

func (r reviews) Reviews(id core.SessionID, url string) map[string]string {
	m, ok := r.x.read(id)
	if !ok {
		return nil
	}
	for _, p := range m.prs {
		if p.pr.URL == url {
			return maps.Clone(p.reviews)
		}
	}
	return nil
}

// statusSegment is "PR 1 changes requested": where the reviewers of the session in front's PRs stand.
func (x *Mirror) statusSegment(c *ui.Ctx) {
	id := x.h.State().Focus
	m, ok := x.read(id)
	if !ok || len(m.prs) == 0 {
		return
	}
	counts := map[string]int{}
	for _, p := range m.prs {
		for _, s := range p.reviews {
			counts[s]++
		}
	}
	var parts []string
	for _, s := range []string{registry.ReviewChanges, registry.ReviewApproved} {
		if n := counts[s]; n > 0 {
			word, _ := prui.ReviewWord(s)
			parts = append(parts, fmt.Sprintf("%d %s", n, word))
		}
	}
	if len(parts) == 0 {
		return
	}
	text := m.prs[0].host.Said().Short + " " + strings.Join(parts, " · ")
	c.Row("reviews", ui.Style{H: ui.Flex(1), Gap: 6, Cross: ui.Center, Help: "git/github#status", Role: ui.RoleStatus, Label: text}, func(c *ui.Ctx) {
		c.Text("t", text, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10.5, Weight: typeset.Medium}, Color: c.Theme().C(prui.TReviewAccent)})
	})
}
