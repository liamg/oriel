package ship

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/kit/prui"
	"github.com/liamg/oriel/app/kit/sessionkit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostapi"
	"github.com/liamg/oriel/registry"
)

// A project's pull request, through the code host its origin is on (registry.CodeHost): the host process
// reads git, the app asks the code host for the rest.

// shipRead is a project as the Ship page shows it: the host's reading of its git, and its pull request.
type shipRead struct {
	hostapi.Ship
	CodeHost    *registry.CodeHost  // the host origin is on; nil for none Oriel knows
	Site        registry.Site       // which of its servers
	Repo        string              // its name there: "acme/api"
	UnknownHost string              // origin's hostname, when no code host is known to be there
	Template    registry.PRTemplate // the repo's pull request template, as its code host reads it
	Owners      []string            // the code owners of what the branch changes
	Unkeyed     bool                // the site's token isn't set yet
	PR          *registry.PR
	Checks      []registry.Check
	FailLine    string // what the first failing check's log says
	HostError   string
	GitRead     time.Time // when git was read
	PRRead      time.Time // when the code host was last asked; zero if never
}

// codeRepo is the project as its code host's funcs are asked about it.
func (s shipRead) codeRepo(id core.SessionID) registry.CodeRepo {
	return registry.CodeRepo{Session: string(id), Project: s.Project, Remote: s.Origin, Repo: s.Repo, Site: s.Site}
}

// words are how the project's host names a pull request.
func (s shipRead) words() registry.Words { return prui.WordsOf(s.CodeHost) }

// prTTL is how long a branch's PR and checks answer before the code host is asked again.
const prTTL = 30 * time.Second

type prRead struct {
	at       time.Time
	pr       *registry.PR
	checks   []registry.Check
	failLine string
	err      error
}

// errText is err as a page says it; "" for none.
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
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
func (x *Ship) withPRs(ctx context.Context, id core.SessionID, ships []hostapi.Ship, fresh bool) []shipRead {
	out := make([]shipRead, len(ships))
	var wg sync.WaitGroup
	for i, s := range ships {
		out[i] = shipRead{Ship: s, GitRead: x.now()}
		hs, repo, ok := kit.Sites(x.h).Remote(s.Origin)
		if !ok {
			hostname, _, parsed := registry.ParseRemote(s.Origin)
			if !parsed {
				continue
			}
			if _, ok = kit.Sites(x.h).Probe(ctx, hostname); ok {
				hs, repo, ok = kit.Sites(x.h).Remote(s.Origin)
			}
			if !ok {
				out[i].UnknownHost = hostname
				continue
			}
		}
		out[i].CodeHost, out[i].Site, out[i].Repo = &hs.CodeHost, hs.Site, repo
		if !s.ReadOnly {
			out[i].Template, out[i].Owners = conventions(ctx, hs.CodeHost, s.Dir, s.Changed)
		}
		kit.Sites(x.h).Warm(ctx, hs)
		if !kit.Sites(x.h).Keyed(hs) {
			out[i].Unkeyed = true
			continue
		}
		if s.ReadOnly || s.Branch == "" || hs.ForBranch == nil {
			continue
		}
		wg.Go(func() {
			g := x.prFor(ctx, id, out[i], fresh)
			out[i].PR, out[i].Checks, out[i].FailLine, out[i].HostError, out[i].PRRead = g.pr, g.checks, g.failLine, errText(g.err), g.at
			if g.err != nil && kit.Sites(x.h).Refuse(hs, g.err) {
				out[i].Unkeyed = !kit.Sites(x.h).Keyed(hs)
				x.cache.mu.Lock()
				delete(x.cache.read, formKey(id, s.Project)+"/"+s.Branch)
				x.cache.mu.Unlock()
			}
		})
	}
	wg.Wait()
	return out
}

// conventions are the template and code owners the project's checkout at dir gives, as its code host reads them.
func conventions(ctx context.Context, h registry.CodeHost, dir string, changed []string) (t registry.PRTemplate, owners []string) {
	if h.Template != nil {
		_ = registry.RecoverAsError(func() (err error) { t, err = h.Template(ctx, dir); return err })
	}
	if h.Owners != nil && len(changed) > 0 {
		_ = registry.RecoverAsError(func() (err error) { owners, err = h.Owners(ctx, dir, changed); return err })
	}
	return t, owners
}

func (x *Ship) prFor(ctx context.Context, id core.SessionID, s shipRead, fresh bool) prRead {
	key := formKey(id, s.Project) + "/" + s.Branch
	x.cache.mu.Lock()
	g, ok := x.cache.read[key]
	x.cache.mu.Unlock()
	if ok && !fresh && x.now().Sub(g.at) < prTTL {
		return g
	}
	g = prRead{at: x.now()}
	h, repo := *s.CodeHost, s.codeRepo(id)
	pr, err := x.forBranch(ctx, h, repo, s.Branch)
	x.cache.mu.Lock()
	last := x.cache.read[key]
	if r, ok := x.cache.raised[key]; ok {
		switch {
		case pr != nil || x.now().Sub(r.at) >= raisedHold:
			delete(x.cache.raised, key)
		default:
			raised := r.pr
			pr = &raised // raised a moment ago: the lookup hasn't caught up, or failed
		}
	}
	if err != nil && pr == nil {
		pr = last.pr // a failed lookup doesn't forget the PR the last one found
	}
	x.cache.mu.Unlock()
	switch {
	case err != nil:
		g.err, g.pr = err, pr
		if pr != nil && last.pr != nil && last.pr.Number == pr.Number {
			g.checks, g.failLine = last.checks, last.failLine
		}
	case pr != nil:
		g.pr = pr
		if h.Checks != nil {
			if err := registry.RecoverAsError(func() (err error) { g.checks, err = h.Checks(ctx, repo, *pr); return err }); err != nil {
				g.err = err
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

// forBranch is a branch's PR on h, which the session host is told of: the agent asks it whether the PR is open.
func (x *Ship) forBranch(ctx context.Context, h registry.CodeHost, repo registry.CodeRepo, branch string) (*registry.PR, error) {
	if h.ForBranch == nil {
		return nil, registry.ErrNotOffered
	}
	var pr *registry.PR
	err := registry.RecoverAsError(func() (err error) { pr, err = h.ForBranch(ctx, repo, branch); return err })
	if err == nil {
		x.sawPR(repo, branch, pr.Open())
	}
	return pr, err
}

// sawPR tells the session host whether a project's branch's PR is open.
func (x *Ship) sawPR(repo registry.CodeRepo, branch string, open bool) {
	if c, err := sessionkit.ShipCalls(x.h); err == nil {
		_ = c.SawPR(hostapi.PRSeenReq{Session: core.SessionID(repo.Session), Project: repo.Project, Branch: branch, Open: open})
	}
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
	case s.CodeHost == nil && !s.HasOrigin:
		return registry.CodeHost{}, fmt.Errorf("%s has no remote", s.Project)
	case s.CodeHost == nil:
		return registry.CodeHost{}, fmt.Errorf("%s's origin isn't on a code host Oriel knows", s.Project)
	case !can:
		return registry.CodeHost{}, fmt.Errorf("%s: %w", s.CodeHost.Label(), registry.ErrNotOffered)
	}
	return *s.CodeHost, nil
}

// raisePR raises a project's PR from its branch, or edits the open one, and asks its reviewers.
func (x *Ship) raisePR(ctx context.Context, se core.SessionState, s shipRead, r prReq) error {
	h, err := hosted(s, s.CodeHost != nil && s.CodeHost.ForBranch != nil && s.CodeHost.Create != nil)
	if err != nil {
		return err
	}
	if s.Branch == "" {
		return fmt.Errorf("%s has no branch to raise a %s from", s.Project, h.Said().Short)
	}
	repo := s.codeRepo(se.ID)
	pr, err := x.forBranch(ctx, h, repo, s.Branch)
	if err != nil {
		return err
	}
	if pr.Open() {
		if h.Edit == nil {
			return fmt.Errorf("%s can't edit a %s: %w", h.Label(), h.Said().Short, registry.ErrNotOffered)
		}
		open, body := *pr, withoutLinkedPRs(r.Body)
		err = registry.RecoverAsError(func() (err error) { pr, err = h.Edit(ctx, repo, open, r.Title, body); return err })
	} else {
		if s.Base == "" {
			return fmt.Errorf("%s has no base branch for the %s", s.Project, h.Said().Short)
		}
		if err := x.ownBranch(ctx, se.ID, s); err != nil {
			return err
		}
		n := registry.NewPR{Title: r.Title, Body: withoutLinkedPRs(r.Body), Branch: s.Branch, Base: s.Base, Draft: r.Draft}
		err = registry.RecoverAsError(func() (err error) { pr, err = h.Create(ctx, repo, n); return err })
	}
	if err != nil {
		return err
	}
	if pr == nil {
		return fmt.Errorf("%s didn't answer with the %s", h.Label(), h.Said().Short)
	}
	x.sawPR(repo, s.Branch, true)
	x.cache.mu.Lock()
	if x.cache.raised == nil {
		x.cache.raised = map[string]raisedPR{}
	}
	x.cache.raised[formKey(se.ID, s.Project)+"/"+s.Branch] = raisedPR{pr: *pr, at: x.now()}
	x.cache.mu.Unlock()
	x.showRaised(se.ID, s.Project, *pr)
	if len(r.Reviewers) > 0 && h.RequestReviewers != nil {
		if err := registry.RecoverAsError(func() error { return h.RequestReviewers(ctx, repo, *pr, r.Reviewers) }); err != nil {
			return err
		}
	}
	return nil
}

// showRaised puts a PR just raised or edited on its project's reading at once, so its tab shows it as soon as the code
// host answers rather than after the read that follows.
func (x *Ship) showRaised(id core.SessionID, project string, pr registry.PR) {
	x.mu.Lock()
	r := x.reports[id]
	if r == nil || r.fixed {
		x.mu.Unlock()
		return
	}
	if i := slices.IndexFunc(r.ships, func(s shipRead) bool { return s.Project == project }); i >= 0 {
		r.ships = slices.Clone(r.ships)
		r.ships[i].PR = &pr
	}
	x.mu.Unlock()
	x.h.Do(func() { x.editPR(id, project, false) })
	x.h.Redraw()
}

// ownBranch refuses a PR from any branch but the one the session host raises the project's from: the worktree may
// have been switched off it since it was read.
func (x *Ship) ownBranch(ctx context.Context, id core.SessionID, s shipRead) error {
	c, err := sessionkit.ShipCalls(x.h)
	if err != nil {
		return err
	}
	branch, err := c.ShipBranch(ctx, hostapi.ShipRef{Session: id, Project: s.Project})
	if err != nil {
		return err
	}
	if branch != s.Branch {
		return fmt.Errorf("%s raises its %s from %q, not %q", s.Project, s.words().Short, branch, s.Branch)
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
	h, err := hosted(s, s.CodeHost != nil && s.CodeHost.Ready != nil)
	if err != nil {
		return err
	}
	pr, err := x.forBranch(ctx, h, s.codeRepo(se.ID), s.Branch)
	if err != nil {
		return err
	}
	if !pr.Open() {
		return fmt.Errorf("%s has no open %s", s.Project, h.Said().Long)
	}
	return registry.RecoverAsError(func() error { return h.Ready(ctx, s.codeRepo(se.ID), *pr) })
}

// renameGuard refuses to rename a branch its PR is open on: the PR would be left on the old one.
func (x *Ship) renameGuard(ctx context.Context, se core.SessionState, s shipRead) error {
	if s.CodeHost == nil || s.CodeHost.ForBranch == nil || s.Branch == "" {
		return nil
	}
	pr, err := x.forBranch(ctx, *s.CodeHost, s.codeRepo(se.ID), s.Branch)
	if err != nil && !errors.Is(err, registry.ErrNotOffered) {
		return err
	}
	if pr.Open() {
		return fmt.Errorf("%s's %s %s is open on %s: renaming would leave it behind", s.Project, s.words().Short,
			s.CodeHost.Ref(s.Repo, pr.Number), s.Branch)
	}
	return nil
}

// setupArgs set up the project's code host's site, or ask which host its server is.
func setupArgs(s shipRead) registry.Args {
	if s.CodeHost != nil {
		return registry.Args{"hostname": s.Site.Host, "code_host": s.CodeHost.Name}
	}
	return registry.Args{"hostname": s.UnknownHost}
}

// setupFirst opens the setup dialog for a project whose site isn't set up, and says so: with the error when it
// can't be opened.
func (x *Ship) setupFirst(ctx registry.Ctx, s shipRead) (bool, error) {
	if !s.Unkeyed && s.UnknownHost == "" {
		return false, nil
	}
	if err := x.h.Run(ctx, "code.setup.open", setupArgs(s)); err != nil {
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
