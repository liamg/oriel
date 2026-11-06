package github

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/host/hostcore"
)

// A session's project's pull request on GitHub, for the app's GitHub code host: each call is made with the checkout's
// gh and env, so a sandboxed workspace's goes through its gateway.

// GitHubReq names a session's project, and what the call needs of its PR.
type GitHubReq struct {
	Session   core.SessionID `json:"session"`
	Project   string         `json:"project"`
	Host      string         `json:"host,omitempty"` // a GitHub Enterprise server's hostname
	Repo      string         `json:"repo,omitempty"` // owner/name, where origin's URL isn't github.com's
	Token     string         `json:"-"`              // one Oriel found, else gh's own
	Branch    string         `json:"branch,omitempty"`
	PR        *gh.PR         `json:"pr,omitempty"`
	New       gh.NewPR       `json:"new,omitzero"`
	Title     string         `json:"title,omitempty"`
	Body      string         `json:"body,omitempty"`
	Reviewers []string       `json:"reviewers,omitempty"`
	Merge     gh.Merge       `json:"merge,omitzero"`
}

// GitHubChecks are a PR's checks, and the line of the first failing Actions job's log that says why.
type GitHubChecks struct {
	Checks   []gh.Check `json:"checks,omitempty"`
	FailLine string     `json:"failLine,omitempty"`
}

// githubProject is the GitHub client and repo of a session's project.
func (x *GitHub) githubProject(ctx context.Context, r GitHubReq) (gh.Client, gh.Repo, error) {
	_, p, repo, err := hostcore.ProjectRepo(ctx, x.st, x.deps.Kinds, r.Session, r.Project)
	if err != nil {
		return nil, gh.Repo{}, err
	}
	ghRepo, ok := repoOf(r, x.remoteURL(ctx, repo))
	if !ok {
		return nil, gh.Repo{}, fmt.Errorf("%s's origin isn't on GitHub", p.Name)
	}
	return x.githubOn(repo, r.Host, r.Token), ghRepo, nil
}

// repoOf is the request's repo, else the one origin's URL names on github.com.
func repoOf(r GitHubReq, remote string) (gh.Repo, bool) {
	if owner, name, ok := strings.Cut(r.Repo, "/"); ok && owner != "" && name != "" {
		return gh.Repo{Owner: owner, Name: name}, true
	}
	return gh.ParseRepo(remote)
}

// prNumber is the request's PR's number, refusing a request without one.
func (r GitHubReq) prNumber() (int, error) {
	if r.PR == nil || r.PR.Number == 0 {
		return 0, fmt.Errorf("%s has no pull request", r.Project)
	}
	return r.PR.Number, nil
}

// GitHubPRFor is the branch's PR: its open one, else the newest; nil for none.
func (x *GitHub) GitHubPRFor(ctx context.Context, r GitHubReq) (_ *gh.PR, err error) {
	_, p, repo, err := hostcore.ProjectRepo(ctx, x.st, x.deps.Kinds, r.Session, r.Project)
	if err != nil {
		return nil, err
	}
	ghRepo, ok := repoOf(r, x.remoteURL(ctx, repo))
	if !ok {
		return nil, fmt.Errorf("%s's origin isn't on GitHub", p.Name)
	}
	g := x.githubFor(ctx, r.Session, p.Name, repo, ghRepo, r.Host, r.Token, r.Branch, true)
	if g.err != "" {
		return nil, errors.New(g.err)
	}
	return g.pr, nil
}

// githubFor is a branch's PR: the last githubTTL's reading unless fresh.
func (x *GitHub) githubFor(ctx context.Context, id core.SessionID, project string, repo gitx.Repo, ghRepo gh.Repo, hostname, token, branch string, fresh bool) githubRead {
	st := &x.ship
	key := shipKey(id, project) + "/" + branch
	st.mu.Lock()
	g, ok := st.github[key]
	st.mu.Unlock()
	if ok && !fresh && x.env.Now().Sub(g.at) < githubTTL {
		return g
	}
	g = githubRead{at: x.env.Now()}
	if pr, err := x.githubOn(repo, hostname, token).PRForBranch(ctx, ghRepo, branch); err != nil {
		g.err = err.Error()
	} else {
		g.pr = pr
	}
	st.mu.Lock()
	st.github[key] = g
	st.mu.Unlock()
	return g
}

// GitHubCreate raises a PR, from a branch of the session's only: ownBranch.
func (x *GitHub) GitHubCreate(ctx context.Context, r GitHubReq) (_ *gh.PR, err error) {
	_, p, _, err := hostcore.ProjectRepo(ctx, x.st, x.deps.Kinds, r.Session, r.Project)
	if err != nil {
		return nil, err
	}
	branch, err := hostcore.OwnBranch(p)
	if err != nil {
		return nil, err
	}
	if r.New.Head != branch {
		return nil, fmt.Errorf("%s raises PRs from %q only, not %q", p.Name, branch, r.New.Head)
	}
	c, repo, err := x.githubProject(ctx, r)
	if err != nil {
		return nil, err
	}
	return c.CreatePR(ctx, repo, r.New)
}

// GitHubEdit sets a PR's title and body.
func (x *GitHub) GitHubEdit(ctx context.Context, r GitHubReq) (_ *gh.PR, err error) {
	n, err := r.prNumber()
	if err != nil {
		return nil, err
	}
	c, repo, err := x.githubProject(ctx, r)
	if err != nil {
		return nil, err
	}
	return c.EditPR(ctx, repo, n, r.Title, r.Body)
}

// GitHubReady takes a draft PR out of draft.
func (x *GitHub) GitHubReady(ctx context.Context, r GitHubReq) (err error) {
	if _, err := r.prNumber(); err != nil {
		return err
	}
	c, repo, err := x.githubProject(ctx, r)
	if err != nil {
		return err
	}
	return c.MarkReady(ctx, repo, r.PR)
}

// GitHubReviewers asks people and teams to review a PR.
func (x *GitHub) GitHubReviewers(ctx context.Context, r GitHubReq) (err error) {
	n, err := r.prNumber()
	if err != nil {
		return err
	}
	c, repo, err := x.githubProject(ctx, r)
	if err != nil {
		return err
	}
	return c.RequestReviewers(ctx, repo, n, r.Reviewers)
}

// GitHubChecks are the checks on a PR's branch.
func (x *GitHub) GitHubChecks(ctx context.Context, r GitHubReq) (_ GitHubChecks, err error) {
	c, repo, err := x.githubProject(ctx, r)
	if err != nil {
		return GitHubChecks{}, err
	}
	var out GitHubChecks
	if out.Checks, err = c.Checks(ctx, repo, r.Branch); err != nil {
		return out, err
	}
	for _, ch := range out.Checks {
		if ch.State() == gh.CheckFail && ch.Actions {
			if log, err := c.Log(ctx, repo, ch); err == nil {
				out.FailLine = gh.FailLine(log)
			}
			break
		}
	}
	return out, nil
}

// GitHubMergeState is what merging a PR now would meet.
func (x *GitHub) GitHubMergeState(ctx context.Context, r GitHubReq) (_ gh.MergeState, err error) {
	n, err := r.prNumber()
	if err != nil {
		return gh.MergeState{}, err
	}
	c, repo, err := x.githubProject(ctx, r)
	if err != nil {
		return gh.MergeState{}, err
	}
	return c.MergeState(ctx, repo, n)
}

// GitHubMerge merges a PR at the head the user saw.
func (x *GitHub) GitHubMerge(ctx context.Context, r GitHubReq) (err error) {
	n, err := r.prNumber()
	if err != nil {
		return err
	}
	c, repo, err := x.githubProject(ctx, r)
	if err != nil {
		return err
	}
	return c.MergePR(ctx, repo, n, r.Merge)
}

// GitHubDeleteBranch deletes a merged PR's branch on GitHub.
func (x *GitHub) GitHubDeleteBranch(ctx context.Context, r GitHubReq) (err error) {
	c, repo, err := x.githubProject(ctx, r)
	if err != nil {
		return err
	}
	return c.DeleteBranch(ctx, repo, r.Branch)
}
