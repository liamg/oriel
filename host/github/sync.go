package github

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/host/hostcore"
	"github.com/liamg/oriel/sandbox"
)

// SyncGitHub mirrors the review of each of a session's projects' PRs into its threads.
func (x *GitHub) SyncGitHub(ctx context.Context, r MirrorReq) (_ Mirror, err error) {
	se, err := x.st.Session(r.Session)
	if err != nil {
		return Mirror{}, err
	}
	out := Mirror{Synced: x.env.Now().UTC()}
	kick := false
	for _, p := range se.Projects {
		reviewed := reviewedPR(se) != "" && p.Detached && strings.HasPrefix(p.Base, "refs/oriel/pr/")
		if !reviewed && (p.ReadOnly || p.Detached || p.Branch == "") {
			continue
		}
		env, err := sandbox.GitEnv(p.Dir)
		if err != nil {
			continue
		}
		repo := gitx.Repo{Dir: p.Dir, Env: env}
		var ghRepo gh.Repo
		var pr *gh.PR
		if reviewed {
			if pr, ghRepo, err = x.reviewedPR(ctx, repo, reviewedPR(se)); err != nil {
				out.Errors = append(out.Errors, p.Name+": "+err.Error())
				continue
			}
		} else {
			var ok bool
			if ghRepo, ok = gh.ParseRepo(x.remoteURL(ctx, repo, false)); !ok {
				continue
			}
			g := x.githubFor(ctx, se.ID, p.Name, repo, ghRepo, "", "", p.Branch, r.Fresh)
			if g.err != "" {
				out.Errors = append(out.Errors, p.Name+": "+g.err)
				continue
			}
			if pr = g.pr; pr == nil {
				continue
			}
		}
		mp, k, err := x.mirrorPR(ctx, se.ID, p, repo, ghRepo, pr, r.Trust)
		if err != nil {
			out.Errors = append(out.Errors, p.Name+": "+err.Error())
			continue
		}
		out.PRs = append(out.PRs, mp)
		kick = kick || k
	}
	if kick {
		se, _ = x.st.Session(r.Session)
		if se.Agent.Provider != "" && !se.Archived {
			x.deps.Wake(r.Session)
		}
	}
	hostcore.Restatus(x.st, r.Session)
	return out, nil
}

// reviewedPR is the pull request a review session reads, as GitHub has it now.
func (x *GitHub) reviewedPR(ctx context.Context, repo gitx.Repo, url string) (*gh.PR, gh.Repo, error) {
	ghRepo, n, err := gh.ParsePR(url)
	if err != nil {
		return nil, ghRepo, err
	}
	d, err := x.github(repo).PRDetails(ctx, ghRepo, n)
	if err != nil {
		return nil, ghRepo, err
	}
	return &gh.PR{Number: n, URL: url, Author: d.Author, SHA: d.HeadSHA}, ghRepo, nil
}

// mirrorPR brings one PR's review threads and conversation into the session; kick says the agent has something new.
func (x *GitHub) mirrorPR(ctx context.Context, id core.SessionID, p core.Project, repo gitx.Repo, ghRepo gh.Repo, pr *gh.PR, trust Trust) (MirrorPR, bool, error) {
	c := x.github(repo)
	comments, err := c.ReviewComments(ctx, ghRepo, pr.Number)
	if err != nil {
		return MirrorPR{}, false, err
	}
	reviews, err := c.Reviews(ctx, ghRepo, pr.Number)
	if err != nil {
		return MirrorPR{}, false, err
	}
	issue, err := c.IssueComments(ctx, ghRepo, pr.Number)
	if err != nil {
		return MirrorPR{}, false, err
	}
	resolved, err := c.ResolvedThreads(ctx, ghRepo, pr.Number)
	if err != nil {
		resolved = nil // unknown: leave each thread's status as it is
	}
	states := gh.ReviewStates(reviews)
	mp := MirrorPR{Project: p.Name, Repo: ghRepo.String(), Number: pr.Number, URL: pr.URL, Reviews: states, Head: pr.SHA, Open: pr.Open()}
	trusted := x.truster(ctx, c, ghRepo, trust)
	se, err := x.st.Session(id)
	if err != nil {
		return mp, false, err
	}
	me, author := "", ""
	if reviewedPR(se) != "" {
		me, author = x.githubMe(ctx, c), pr.Author
	}
	msg := func(id int64, user, assoc, body string, at time.Time) mirrorMsg {
		return mirrorMsg{num: id, user: user, body: body, at: at, trusted: trusted(user, assoc), own: me != "" && user == me}
	}

	byRoot := map[int64][]gh.ReviewComment{}
	var roots []int64
	for _, rc := range comments {
		root := cmp.Or(rc.InReplyTo, rc.ID)
		if byRoot[root] == nil {
			roots = append(roots, root)
		}
		byRoot[root] = append(byRoot[root], rc)
	}
	var threads []mirrored
	for _, root := range roots {
		cs := byRoot[root]
		first := cs[0]
		if first.ID != root {
			continue // its first comment was deleted
		}
		var msgs []mirrorMsg
		for _, rc := range cs {
			msgs = append(msgs, msg(rc.ID, rc.User, rc.Association, rc.Body, rc.Created))
		}
		asides(msgs, author, false)
		line, side := first.Line, ""
		if line == 0 {
			line = first.OriginalLine
		}
		if first.Side == "LEFT" {
			side = "old"
		}
		a := &core.Anchor{Project: p.Name, File: first.Path, Line: max(line, 1), Side: side}
		if side == "" {
			a.Text = hostcore.AnchorText(se, a)
		}
		var res *bool
		if resolved != nil {
			v := resolved[root]
			res = &v
		}
		threads = append(threads, mirrored{ref: core.HostThread{Project: p.Name, Repo: mp.Repo, PR: pr.Number, Root: root, Reviewer: first.User,
			State: states[first.User], URL: first.URL, Outdated: first.Line == 0}, anchor: a, msgs: msgs, res: res, into: sentFrom(se, first)})
	}

	var convo []mirrorMsg
	for _, ic := range issue {
		convo = append(convo, msg(ic.ID, ic.User, ic.Association, ic.Body, ic.Created))
	}
	for _, rv := range reviews {
		if strings.TrimSpace(rv.Body) != "" {
			convo = append(convo, msg(rv.ID, rv.User, rv.Association, rv.Body, rv.Submitted))
		}
	}
	slices.SortStableFunc(convo, func(a, b mirrorMsg) int { return a.at.Compare(b.at) })
	asides(convo, author, true)
	ref := core.HostThread{Project: p.Name, Repo: mp.Repo, PR: pr.Number, URL: pr.URL}
	if len(convo) > 0 {
		ref.Reviewer = convo[0].user
		ref.State = states[ref.Reviewer]
	}
	threads = append(threads, mirrored{ref: ref, msgs: convo})
	x.mirror.mu.Lock()
	defer x.mirror.mu.Unlock()
	kick, err := x.applyMirror(id, "", mp.Repo, pr.Number, threads)
	return mp, kick, err
}

// githubMe is who the user is on GitHub, asked once.
func (x *GitHub) githubMe(ctx context.Context, c gh.Client) string {
	x.mirror.mu2.Lock()
	defer x.mirror.mu2.Unlock()
	if x.mirror.me == "" {
		x.mirror.me, _ = c.Me(ctx)
	}
	return x.mirror.me
}

// truster says whose text the agent may read: a trusted user, or a member of a trusted org (the repo's owner when
// none is set).
func (x *GitHub) truster(ctx context.Context, c gh.Client, repo gh.Repo, trust Trust) func(user, assoc string) bool {
	orgs := trust.Orgs
	if len(orgs) == 0 && len(trust.Users) == 0 {
		orgs = []string{repo.Owner}
	}
	return func(user, assoc string) bool {
		if slices.ContainsFunc(trust.Users, func(u string) bool { return strings.EqualFold(strings.TrimPrefix(u, "@"), user) }) {
			return true
		}
		for _, org := range orgs {
			if strings.EqualFold(org, repo.Owner) && (assoc == "OWNER" || assoc == "MEMBER") {
				return true
			}
			key := strings.ToLower(org + "/" + user)
			x.mirror.membersMu.Lock()
			in, ok := x.mirror.members[key]
			x.mirror.membersMu.Unlock()
			if !ok {
				var err error
				if in, err = c.OrgMember(ctx, org, user); err != nil {
					continue // not known: not trusted, and asked again next time
				}
				x.mirror.membersMu.Lock()
				if x.mirror.members == nil {
					x.mirror.members = map[string]bool{}
				}
				x.mirror.members[key] = in
				x.mirror.membersMu.Unlock()
			}
			if in {
				return true
			}
		}
		return false
	}
}
