package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
)

// A project's pull request, through the code host its origin is on (registry.CodeHost, hosts.md): the host process
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
}

// codeRepo is the project as its code host's funcs are asked about it.
func (s shipRead) codeRepo(id core.SessionID) registry.CodeRepo {
	return registry.CodeRepo{Session: string(id), Project: s.Project, Remote: s.Origin, Repo: s.Repo, Site: s.Site}
}

// words are how the project's host names a pull request.
func (s shipRead) words() registry.Words {
	if s.Code == nil {
		return registry.CodeHost{}.Said()
	}
	return s.Code.Said()
}

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
	mu   sync.Mutex
	read map[string]prRead
}

// withPRs is ships with each one's code host, PR and checks: the last prTTL's unless fresh.
func (x *shipPlugin) withPRs(ctx context.Context, id core.SessionID, ships []host.Ship, fresh bool) []shipRead {
	out := make([]shipRead, len(ships))
	var wg sync.WaitGroup
	for i, s := range ships {
		out[i] = shipRead{Ship: s}
		hs, repo, ok := x.sh.sites.remote(s.Origin)
		if !ok {
			hostname, _, parsed := registry.ParseRemote(s.Origin)
			if !parsed {
				continue
			}
			if _, ok = x.sh.sites.probe(ctx, hostname); ok {
				hs, repo, ok = x.sh.sites.remote(s.Origin)
			}
			if !ok {
				out[i].Unknown = hostname
				continue
			}
		}
		out[i].Code, out[i].Site, out[i].Repo = &hs.CodeHost, hs.Site, repo
		x.sh.sites.warm(ctx, hs)
		if !x.sh.sites.keyed(hs) {
			out[i].Unkeyed = true
			continue
		}
		if s.ReadOnly || s.Branch == "" || hs.ForBranch == nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			g := x.prFor(ctx, id, out[i], fresh)
			out[i].PR, out[i].Checks, out[i].FailLine, out[i].HostError = g.pr, g.checks, g.failLine, g.err
			if g.err != "" && x.sh.sites.refuse(hs, errors.New(g.err)) {
				out[i].Unkeyed = !x.sh.sites.keyed(hs)
				x.cache.mu.Lock()
				delete(x.cache.read, formKey(id, s.Project)+"/"+s.Branch)
				x.cache.mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return out
}

func (x *shipPlugin) prFor(ctx context.Context, id core.SessionID, s shipRead, fresh bool) prRead {
	key := formKey(id, s.Project) + "/" + s.Branch
	x.cache.mu.Lock()
	g, ok := x.cache.read[key]
	x.cache.mu.Unlock()
	if ok && !fresh && x.now().Sub(g.at) < prTTL {
		return g
	}
	g = prRead{at: x.now()}
	h, repo := *s.Code, s.codeRepo(id)
	pr, err := forBranch(ctx, h, repo, s.Branch)
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

// raisePR raises a project's PR from its branch, or edits the open one, asks its reviewers, and cross-links every PR
// of the session in their merge order.
func (x *shipPlugin) raisePR(ctx context.Context, se core.SessionState, s shipRead, r prReq) error {
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
		open := *pr
		err = registry.Call(func() (err error) { pr, err = h.Edit(ctx, repo, open, r.Title, r.Body); return err })
	} else {
		if s.Base == "" {
			return fmt.Errorf("%s has no base branch for the %s", s.Project, h.Said().Short)
		}
		n := registry.NewPR{Title: r.Title, Body: r.Body, Branch: s.Branch, Base: s.Base, Draft: r.Draft}
		err = registry.Call(func() (err error) { pr, err = h.Create(ctx, repo, n); return err })
	}
	if err != nil {
		return err
	}
	if pr == nil {
		return fmt.Errorf("%s didn't answer with the %s", h.Label(), h.Said().Short)
	}
	if len(r.Reviewers) > 0 && h.RequestReviewers != nil {
		if err := registry.Call(func() error { return h.RequestReviewers(ctx, repo, *pr, r.Reviewers) }); err != nil {
			return err
		}
	}
	return x.crossLink(ctx, se)
}

// Cross-linked PRs carry a block naming each other, between these markers, kept up to date as each is raised.
const (
	linkStart = "<!-- oriel:linked-prs -->"
	linkEnd   = "<!-- /oriel:linked-prs -->"
)

// crossLink writes the block into every open PR of a session with more than one, in the projects' order.
func (x *shipPlugin) crossLink(ctx context.Context, se core.SessionState) error {
	type linked struct {
		s  shipRead
		pr *registry.PR
	}
	var prs []linked
	for _, s := range x.report(se.ID).ships {
		if s.Code == nil || s.ReadOnly || s.Branch == "" || s.Code.ForBranch == nil {
			continue
		}
		pr, err := forBranch(ctx, *s.Code, s.codeRepo(se.ID), s.Branch)
		if err != nil {
			return err
		}
		if pr.Open() {
			prs = append(prs, linked{s, pr})
		}
	}
	if len(prs) < 2 {
		return nil
	}
	for i, l := range prs {
		var b strings.Builder
		b.WriteString(linkStart + "\n**Ships with** (merge in this order):\n")
		for j, o := range prs {
			fmt.Fprintf(&b, "%d. %s", j+1, o.s.Code.Ref(o.s.Repo, o.pr.Number))
			if j == i {
				b.WriteString(" (this " + l.s.words().Short + ")")
			}
			b.WriteString("\n")
		}
		b.WriteString(linkEnd)
		body := withLinks(l.pr.Body, b.String())
		if body == l.pr.Body || l.s.Code.Edit == nil {
			continue
		}
		h, repo, pr := *l.s.Code, l.s.codeRepo(se.ID), *l.pr
		if err := registry.Call(func() error { _, err := h.Edit(ctx, repo, pr, pr.Title, body); return err }); err != nil {
			return err
		}
	}
	return nil
}

// withLinks is body with its cross-linked PRs block replaced by block, or block added at its end.
func withLinks(body, block string) string {
	if i := strings.Index(body, linkStart); i >= 0 {
		if j := strings.Index(body[i:], linkEnd); j >= 0 {
			return body[:i] + block + body[i+j+len(linkEnd):]
		}
	}
	if strings.TrimSpace(body) == "" {
		return block
	}
	return strings.TrimRight(body, "\n") + "\n\n" + block
}

// markReady takes a project's draft PR out of draft.
func (x *shipPlugin) markReady(ctx context.Context, se core.SessionState, s shipRead) error {
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
func (x *shipPlugin) renameGuard(ctx context.Context, se core.SessionState, s shipRead) error {
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

// setupFirst opens the setup dialog for a project whose site isn't set up, and says so.
func (x *shipPlugin) setupFirst(ctx registry.Ctx, s shipRead) bool {
	if !s.Unkeyed && s.Unknown == "" {
		return false
	}
	x.h.Run(ctx, "code.setup", setupArgs(s))
	return true
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
func (x *shipPlugin) mirrorReviews(ctx context.Context, id core.SessionID, ships []shipRead, fresh bool) {
	x.mu.Lock()
	if x.mirrored == nil {
		x.mirrored = map[core.SessionID]time.Time{}
	}
	due := fresh || x.now().Sub(x.mirrored[id]) >= mirrorEvery
	if due {
		x.mirrored[id] = x.now()
	}
	x.mu.Unlock()
	c, err := x.client(id)
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
			x.sh.sites.refuse(registry.HostSite{CodeHost: h, Site: s.Site}, err)
			x.h.Log().Debug("ship", "threads", s.Project, "err", err)
			continue
		}
		req := host.MirrorReviewReq{Session: id, Project: s.Project, Host: h.Name, Repo: s.Repo, PR: pr.Number, URL: pr.URL}
		for _, t := range ts {
			rt := host.ReviewThread{Key: t.ID, Path: t.Path, Line: t.Line, Old: t.Old, Outdated: t.Outdated, Resolved: t.Resolved,
				Reviewer: t.Reviewer, State: t.State, URL: t.URL}
			for _, n := range t.Comments {
				rt.Comments = append(rt.Comments, host.ReviewComment{Key: n.ID, User: n.Author, Body: n.Body, At: n.At,
					Trusted: n.Member || x.sh.sites.trustedUser(n.Author)})
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
func (x *shipPlugin) forwardHost(se core.SessionState, thread core.ThreadID, comment string) error {
	c, err := x.client(se.ID)
	if err != nil {
		return err
	}
	t, body, err := c.DraftOf(host.ForwardReq{Session: se.ID, Thread: thread, Comment: comment})
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
	rt := registry.ReviewThread{ID: t.Hosted.Key, URL: t.Hosted.URL, Outdated: t.Hosted.Outdated}
	if a := t.Anchor; a != nil && rt.ID != "" {
		rt.Path, rt.Line, rt.Old = a.File, a.Line, a.Side == "old"
	}
	return x.step(se.ID, s.Project, "Forwarding", func(ctx context.Context, c *host.Host) error {
		var posted string
		if err := registry.Call(func() (err error) { posted, err = h.Reply(ctx, repo, pr, rt, body); return err }); err != nil {
			return err
		}
		_, err := c.Forwarded(host.ForwardedReq{Session: se.ID, Thread: thread, Comment: comment, HostID: posted})
		return err
	})
}
