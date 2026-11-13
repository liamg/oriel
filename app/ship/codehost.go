package ship

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
)

// A project's pull request, through the code host its origin is on (registry.CodeHost): the host process
// reads git, the app asks the code host for the rest.

// shipRead is a project as the Ship page shows it: the host's reading of its git, and its pull request.
type shipRead struct {
	host.Ship
	Code      *registry.CodeHost // the host origin is on; nil for none Oriel knows
	Site      registry.Site      // which of its servers
	Repo      string             // its name there: "acme/api"
	Unknown   string             // origin's hostname, when no code host is known to be there
	Unkeyed   bool               // the site's token isn't set yet
	PR        *registry.PR
	Checks    []registry.Check
	FailLine  string // what the first failing check's log says
	HostError string
	GitRead   time.Time // when git was read
	PRRead    time.Time // when the code host was last asked; zero if never
}

// codeRepo is the project as its code host's funcs are asked about it.
func (s shipRead) codeRepo(id core.SessionID) registry.CodeRepo {
	return registry.CodeRepo{Session: string(id), Project: s.Project, Remote: s.Origin, Repo: s.Repo, Site: s.Site}
}

// words are how the project's host names a pull request.
func (s shipRead) words() registry.Words { return kit.WordsOf(s.Code) }

// prTTL is how long a branch's PR and checks answer before the code host is asked again.
const prTTL = 30 * time.Second

type prRead struct {
	at       time.Time
	pr       *registry.PR
	checks   []registry.Check
	failLine string
	err      string
}

// prCache is the code hosts' last answers, by session/project/branch.
type prCache struct {
	mu     sync.Mutex
	read   map[string]prRead
	raised map[string]raisedPR // the PR raising answered with, until the code host's lookup finds it too
}

// raisedPR is a PR as its code host answered raising it, and when.
type raisedPR struct {
	pr registry.PR
	at time.Time
}

// raisedHold is how long a PR just raised stands for a lookup that doesn't find it yet: a code host's search by
// branch can lag the PR it has just made.
const raisedHold = 2 * time.Minute

// withPRs is ships with each one's code host, PR and checks: the last prTTL's unless fresh.
func (x *Ship) withPRs(ctx context.Context, id core.SessionID, ships []host.Ship, fresh bool) []shipRead {
	out := make([]shipRead, len(ships))
	var wg sync.WaitGroup
	for i, s := range ships {
		out[i] = shipRead{Ship: s, GitRead: x.Clock()}
		hs, repo, ok := kit.SitesOf(x.h).Remote(s.Origin)
		if !ok {
			hostname, _, parsed := registry.ParseRemote(s.Origin)
			if !parsed {
				continue
			}
			if _, ok = kit.SitesOf(x.h).Probe(ctx, hostname); ok {
				hs, repo, ok = kit.SitesOf(x.h).Remote(s.Origin)
			}
			if !ok {
				out[i].Unknown = hostname
				continue
			}
		}
		out[i].Code, out[i].Site, out[i].Repo = &hs.CodeHost, hs.Site, repo
		kit.SitesOf(x.h).Warm(ctx, hs)
		if !kit.SitesOf(x.h).Keyed(hs) {
			out[i].Unkeyed = true
			continue
		}
		if s.ReadOnly || s.Branch == "" || hs.ForBranch == nil {
			continue
		}
		wg.Go(func() {
			g := x.prFor(ctx, id, out[i], fresh)
			out[i].PR, out[i].Checks, out[i].FailLine, out[i].HostError, out[i].PRRead = g.pr, g.checks, g.failLine, g.err, g.at
			if g.err != "" && kit.SitesOf(x.h).Refuse(hs, errors.New(g.err)) {
				out[i].Unkeyed = !kit.SitesOf(x.h).Keyed(hs)
				x.cache.mu.Lock()
				delete(x.cache.read, formKey(id, s.Project)+"/"+s.Branch)
				x.cache.mu.Unlock()
			}
		})
	}
	wg.Wait()
	return out
}

func (x *Ship) prFor(ctx context.Context, id core.SessionID, s shipRead, fresh bool) prRead {
	key := formKey(id, s.Project) + "/" + s.Branch
	x.cache.mu.Lock()
	g, ok := x.cache.read[key]
	x.cache.mu.Unlock()
	if ok && !fresh && x.Clock().Sub(g.at) < prTTL {
		return g
	}
	g = prRead{at: x.Clock()}
	h, repo := *s.Code, s.codeRepo(id)
	pr, err := forBranch(ctx, h, repo, s.Branch)
	x.cache.mu.Lock()
	if r, ok := x.cache.raised[key]; ok {
		switch {
		case pr != nil || x.Clock().Sub(r.at) >= raisedHold:
			delete(x.cache.raised, key)
		case err == nil:
			raised := r.pr
			pr = &raised // raised a moment ago: the lookup hasn't caught up
		}
	}
	x.cache.mu.Unlock()
	switch {
	case err != nil:
		g.err = err.Error()
	case pr != nil:
		g.pr = pr
		if h.Checks != nil {
			if err := registry.Call(func() (err error) { g.checks, err = h.Checks(ctx, repo, *pr); return err }); err != nil {
				g.err = err.Error()
			}
		}
		for _, c := range g.checks {
			if c.State == registry.CheckFailed && c.Detail != "" {
				g.failLine = c.Detail
				break
			}
		}
	}
	x.cache.mu.Lock()
	if x.cache.read == nil {
		x.cache.read = map[string]prRead{}
	}
	x.cache.read[key] = g
	x.cache.mu.Unlock()
	return g
}

// forBranch is a branch's PR on h.
func forBranch(ctx context.Context, h registry.CodeHost, repo registry.CodeRepo, branch string) (*registry.PR, error) {
	if h.ForBranch == nil {
		return nil, registry.ErrNotOffered
	}
	var pr *registry.PR
	err := registry.Call(func() (err error) { pr, err = h.ForBranch(ctx, repo, branch); return err })
	return pr, err
}

// prReq is a PR to raise, or the open one's new title and body.
type prReq struct {
	Project   string
	Title     string
	Body      string
	Draft     bool
	Reviewers []string
}

// hosted is a project's code host, refusing one whose origin is on none or whose host can't do what's asked.
func hosted(s shipRead, can bool) (registry.CodeHost, error) {
	switch {
	case s.Code == nil && !s.Remote:
		return registry.CodeHost{}, fmt.Errorf("%s has no remote", s.Project)
	case s.Code == nil:
		return registry.CodeHost{}, fmt.Errorf("%s's origin isn't on a code host Oriel knows", s.Project)
	case !can:
		return registry.CodeHost{}, fmt.Errorf("%s: %w", s.Code.Label(), registry.ErrNotOffered)
	}
	return *s.Code, nil
}

// raisePR raises a project's PR from its branch, or edits the open one, and asks its reviewers.
func (x *Ship) raisePR(ctx context.Context, se core.SessionState, s shipRead, r prReq) error {
	h, err := hosted(s, s.Code != nil && s.Code.ForBranch != nil && s.Code.Create != nil)
	if err != nil {
		return err
	}
	if s.Branch == "" {
		return fmt.Errorf("%s has no branch to raise a %s from", s.Project, h.Said().Short)
	}
	repo := s.codeRepo(se.ID)
	pr, err := forBranch(ctx, h, repo, s.Branch)
	if err != nil {
		return err
	}
	if pr.Open() {
		if h.Edit == nil {
			return fmt.Errorf("%s can't edit a %s: %w", h.Label(), h.Said().Short, registry.ErrNotOffered)
		}
		open, body := *pr, withoutLinkedPRs(r.Body)
		err = registry.Call(func() (err error) { pr, err = h.Edit(ctx, repo, open, r.Title, body); return err })
	} else {
		if s.Base == "" {
			return fmt.Errorf("%s has no base branch for the %s", s.Project, h.Said().Short)
		}
		n := registry.NewPR{Title: r.Title, Body: withoutLinkedPRs(r.Body), Branch: s.Branch, Base: s.Base, Draft: r.Draft}
		err = registry.Call(func() (err error) { pr, err = h.Create(ctx, repo, n); return err })
	}
	if err != nil {
		return err
	}
	if pr == nil {
		return fmt.Errorf("%s didn't answer with the %s", h.Label(), h.Said().Short)
	}
	x.cache.mu.Lock()
	if x.cache.raised == nil {
		x.cache.raised = map[string]raisedPR{}
	}
	x.cache.raised[formKey(se.ID, s.Project)+"/"+s.Branch] = raisedPR{pr: *pr, at: x.Clock()}
	x.cache.mu.Unlock()
	if len(r.Reviewers) > 0 && h.RequestReviewers != nil {
		if err := registry.Call(func() error { return h.RequestReviewers(ctx, repo, *pr, r.Reviewers) }); err != nil {
			return err
		}
	}
	return nil
}

// The block earlier versions wrote into each of a session's PR descriptions, naming the others: taken out when a
// description is next written.
const (
	linkStart = "<!-- oriel:linked-prs -->"
	linkEnd   = "<!-- /oriel:linked-prs -->"
)

// withoutLinkedPRs is body with the linked-PRs block taken out, and the blank lines around it.
func withoutLinkedPRs(body string) string {
	i := strings.Index(body, linkStart)
	if i < 0 {
		return body
	}
	j := strings.Index(body[i:], linkEnd)
	if j < 0 {
		return body
	}
	before, after := strings.TrimRight(body[:i], "\n"), strings.TrimLeft(body[i+j+len(linkEnd):], "\n")
	if before == "" || after == "" {
		return before + after
	}
	return before + "\n\n" + after
}

// markReady takes a project's draft PR out of draft.
func (x *Ship) markReady(ctx context.Context, se core.SessionState, s shipRead) error {
	h, err := hosted(s, s.Code != nil && s.Code.Ready != nil)
	if err != nil {
		return err
	}
	pr, err := forBranch(ctx, h, s.codeRepo(se.ID), s.Branch)
	if err != nil {
		return err
	}
	if !pr.Open() {
		return fmt.Errorf("%s has no open %s", s.Project, h.Said().Long)
	}
	return registry.Call(func() error { return h.Ready(ctx, s.codeRepo(se.ID), *pr) })
}

// renameGuard refuses to rename a branch its PR is open on: the PR would be left on the old one.
func (x *Ship) renameGuard(ctx context.Context, se core.SessionState, s shipRead) error {
	if s.Code == nil || s.Code.ForBranch == nil || s.Branch == "" {
		return nil
	}
	pr, err := forBranch(ctx, *s.Code, s.codeRepo(se.ID), s.Branch)
	if err != nil && !errors.Is(err, registry.ErrNotOffered) {
		return err
	}
	if pr.Open() {
		return fmt.Errorf("%s's %s %s is open on %s: renaming would leave it behind", s.Project, s.words().Short,
			s.Code.Ref(s.Repo, pr.Number), s.Branch)
	}
	return nil
}

// setupArgs set up the project's code host's site, or ask which host its server is.
func setupArgs(s shipRead) registry.Args {
	if s.Code != nil {
		return registry.Args{"hostname": s.Site.Host, "code_host": s.Code.Name}
	}
	return registry.Args{"hostname": s.Unknown}
}

// setupFirst opens the setup dialog for a project whose site isn't set up, and says so: with the error when it
// can't be opened.
func (x *Ship) setupFirst(ctx registry.Ctx, s shipRead) (bool, error) {
	if !s.Unkeyed && s.Unknown == "" {
		return false, nil
	}
	if err := x.h.Run(ctx, "code.setup", setupArgs(s)); err != nil {
		return true, fmt.Errorf("%s's code host isn't set up: %w", s.Project, err)
	}
	return true, nil
}

// sentence is s with its first letter capitalised: "Merge request".
func sentence(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// mirrorEvery is how often a session's PRs' reviews on a code host other than GitHub are mirrored (GitHub's are
// SyncGitHub's).
const mirrorEvery = 30 * time.Second

// mirrorReviews brings each project's PR review threads, from a code host that gives them, into the session.
func (x *Ship) mirrorReviews(ctx context.Context, id core.SessionID, ships []shipRead, fresh bool) {
	x.mu.Lock()
	if x.mirrored == nil {
		x.mirrored = map[core.SessionID]time.Time{}
	}
	due := fresh || x.Clock().Sub(x.mirrored[id]) >= mirrorEvery
	if due {
		x.mirrored[id] = x.Clock()
	}
	x.mu.Unlock()
	c, err := kit.CodeHostCalls(x.h)
	if !due || err != nil {
		return
	}
	for _, s := range ships {
		if s.Code == nil || s.Code.Threads == nil || s.PR == nil || s.Unkeyed {
			continue
		}
		h, repo, pr := *s.Code, s.codeRepo(id), *s.PR
		var ts []registry.ReviewThread
		if err := registry.Call(func() (err error) { ts, err = h.Threads(ctx, repo, pr); return err }); err != nil {
			kit.SitesOf(x.h).Refuse(registry.HostSite{CodeHost: h, Site: s.Site}, err)
			x.h.Log().Debug("ship", "threads", s.Project, "err", err)
			continue
		}
		x.mu.Lock()
		if x.reviewed == nil {
			x.reviewed = map[string]map[string]string{}
		}
		x.reviewed[pr.URL] = reviewStates(ts)
		x.mu.Unlock()
		req := host.MirrorReviewReq{Session: id, Project: s.Project, Host: h.Name, Repo: s.Repo, PR: pr.Number, URL: pr.URL, Author: pr.Author}
		for _, t := range ts {
			rt := host.ReviewThread{Key: t.ID, Path: t.Path, Line: t.Line, Old: t.Old, Outdated: t.Outdated, Resolved: t.Resolved,
				Reviewer: t.Reviewer, State: t.State, URL: t.URL}
			for _, n := range t.Comments {
				rt.Comments = append(rt.Comments, host.ReviewComment{Key: n.ID, User: n.Author, Body: n.Body, At: n.At,
					Trusted: n.Member || kit.SitesOf(x.h).TrustedUser(n.Author)})
			}
			req.Threads = append(req.Threads, rt)
		}
		if err := c.MirrorReview(ctx, req); err != nil {
			x.h.Log().Debug("ship", "mirror", s.Project, "err", err)
		}
	}
}

// forwardHost posts a draft reply on a thread mirrored from a code host other than GitHub, as the user, and marks it
// sent.
func (x *Ship) forwardHost(se core.SessionState, thread core.ThreadID, comment string) error {
	c, err := kit.CodeHostCalls(x.h)
	if err != nil {
		return err
	}
	req := host.ForwardReq{Session: se.ID, Thread: thread, Comment: comment}
	t, _, err := c.DraftOf(req)
	if err != nil {
		return err
	}
	s, ok := x.ship(se.ID, t.Hosted.Project)
	if !ok || s.Code == nil || s.Code.Name != t.Hosted.Host || s.PR == nil {
		return fmt.Errorf("%s's %s hasn't been read yet", t.Hosted.Project, t.Hosted.Host)
	}
	h, err := hosted(s, s.Code.Reply != nil)
	if err != nil {
		return err
	}
	repo, pr := s.codeRepo(se.ID), *s.PR
	return stepWith(x, kit.CodeHostCalls, se.ID, s.Project, "Forwarding", func(ctx context.Context, c host.CodeHostCalls) error {
		_, err := c.ForwardHost(ctx, req, func(ctx context.Context, t core.Thread, body string) (posted string, err error) {
			rt := registry.ReviewThread{ID: t.Hosted.Key, URL: t.Hosted.URL, Outdated: t.Hosted.Outdated}
			if a := t.Anchor; a != nil && rt.ID != "" {
				rt.Path, rt.Line, rt.Old = a.File, a.Line, a.Side == "old"
			}
			err = registry.Call(func() (err error) { posted, err = h.Reply(ctx, repo, pr, rt, body); return err })
			return posted, err
		})
		return err
	})
}

// resolveHost resolves or reopens a review thread mirrored from a code host other than GitHub, on that host, as the
// user did in Oriel.
func (x *Ship) resolveHost(se core.SessionState, t core.Thread, resolved bool) error {
	s, ok := x.ship(se.ID, t.Hosted.Project)
	if !ok || s.Code == nil || s.Code.Name != t.Hosted.Host || s.PR == nil {
		return fmt.Errorf("%s's %s hasn't been read yet", t.Hosted.Project, t.Hosted.Host)
	}
	if s.Code.Resolve == nil {
		return fmt.Errorf("%s can't resolve threads from Oriel", s.Code.Label())
	}
	h, repo, pr := *s.Code, s.codeRepo(se.ID), *s.PR
	rt := registry.ReviewThread{ID: t.Hosted.Key, URL: t.Hosted.URL, Outdated: t.Hosted.Outdated}
	if a := t.Anchor; a != nil {
		rt.Path, rt.Line, rt.Old = a.File, a.Line, a.Side == "old"
	}
	return stepWith(x, kit.CodeHostCalls, se.ID, s.Project, map[bool]string{true: "Resolving", false: "Reopening"}[resolved], func(ctx context.Context, c host.CodeHostCalls) error {
		if err := registry.Call(func() error { return h.Resolve(ctx, repo, pr, rt, resolved) }); err != nil {
			return err
		}
		return c.MarkHostResolved(host.ResolveReq{Session: se.ID, Thread: t.ID, Resolved: resolved})
	})
}
