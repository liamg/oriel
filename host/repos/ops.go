package repos

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
)

// What the Repos page does to a checkout: read its facts, clean up its branches, tag it. Each writes to the
// checkout only when asked.

// defaultBranch is the checkout's default branch by its local name: main, not origin/main.
func defaultBranch(ctx context.Context, r core.Repo, g gitx.Repo) string {
	b := r.BaseBranch
	if b == "" {
		b = g.DefaultBranch(ctx)
	}
	return strings.TrimPrefix(b, "origin/")
}

func (x *Registry) repoGit(id core.RepoID) (core.Repo, gitx.Repo, error) {
	r, ok := x.list.find(string(id))
	if !ok {
		return core.Repo{}, gitx.Repo{}, fmt.Errorf("no repo %q", id)
	}
	return r, gitx.Repo{Dir: r.Path}, nil
}

// RepoFacts are a checkout's numbers, as the Repos page shows them.
type RepoFacts struct {
	Branch    string        `json:"branch"`
	Default   string        `json:"default,omitempty"`
	Tracking  gitx.Tracking `json:"tracking,omitzero"`
	Branches  int           `json:"branches"`
	Worktrees int           `json:"worktrees"` // besides the checkout itself
	Tags      int           `json:"tags"`
	LatestTag string        `json:"latestTag,omitempty"`
	Last      gitx.Commit   `json:"last,omitzero"`
	SizeKiB   int64         `json:"sizeKiB"`           // the object store
	Signing   bool          `json:"signing,omitempty"` // a signing key is set up
}

// RepoFacts reads a checkout's facts.
func (x *Registry) RepoFacts(ctx context.Context, id core.RepoID) (f RepoFacts, err error) {
	r, g, err := x.repoGit(id)
	if err != nil {
		return RepoFacts{}, err
	}
	f.Branch, _ = g.Branch(ctx)
	f.Default = defaultBranch(ctx, r, g)
	f.Tracking, _ = g.BranchTracking(ctx, "HEAD")
	if bs, err := g.Branches(ctx); err == nil {
		f.Branches = len(bs)
	}
	if wts, err := g.Worktrees(ctx); err == nil {
		f.Worktrees = max(0, len(wts)-1)
	}
	if ts, err := g.Tags(ctx); err == nil {
		f.Tags = len(ts)
	}
	f.LatestTag, _ = g.LatestSemverTag(ctx)
	f.Last, _ = g.Last(ctx)
	f.Signing = g.SigningConfigured(ctx)
	f.SizeKiB = g.ObjectsKiB(ctx)
	return f, nil
}

// FetchRepo fetches a checkout's remote, pruning branches it deleted.
func (x *Registry) FetchRepo(ctx context.Context, id core.RepoID) (err error) {
	_, g, err := x.repoGit(id)
	if err != nil {
		return err
	}
	return g.FetchPrune(ctx)
}

// Why a branch is up for cleaning.
const (
	WhyMerged = "merged"
	WhyGone   = "upstream gone"
)

// StaleBranch is a local branch housekeeping offers to delete.
type StaleBranch struct {
	Name     string    `json:"name"`
	Why      string    `json:"why"` // WhyMerged or WhyGone
	When     time.Time `json:"when,omitzero"`
	Worktree string    `json:"worktree,omitempty"` // a worktree that has it checked out
	Dirty    bool      `json:"dirty,omitempty"`    // its worktree has uncommitted changes
	Session  string    `json:"session,omitempty"`  // a live session works on it: it's shown, not offered
}

// HousekeepPlan is what housekeeping would clean in a checkout.
type HousekeepPlan struct {
	Branches []StaleBranch `json:"branches,omitempty"`
	Stale    []string      `json:"stale,omitempty"` // worktrees whose directory is gone
	Base     string        `json:"base"`            // what "merged" is merged into
}

// HousekeepReq is repos.housekeep: with no Apply, the plan; with it, those branches deleted.
type HousekeepReq struct {
	Repo  core.RepoID `json:"repo"`
	Fetch bool        `json:"fetch,omitempty"` // fetch --prune first, so "upstream gone" is true
	Apply []string    `json:"apply,omitempty"` // branches to delete
	Force []string    `json:"force,omitempty"` // of those, the ones whose dirty worktree goes anyway
	Prune bool        `json:"prune,omitempty"` // git worktree prune
}

// Cleaned is what happened to one branch.
type Cleaned struct {
	Branch string `json:"branch"`
	Error  string `json:"error,omitempty"`
}

// Housekeeping reads what a checkout has to clean: local branches merged into its default, or whose upstream was
// deleted, and worktrees whose directory is gone. Never the default branch or the checkout's own.
func (x *Registry) Housekeeping(ctx context.Context, req HousekeepReq) (p HousekeepPlan, err error) {
	r, g, err := x.repoGit(req.Repo)
	if err != nil {
		return HousekeepPlan{}, err
	}
	if req.Fetch && r.Remote != "" {
		if err := g.FetchPrune(ctx); err != nil {
			return HousekeepPlan{}, err
		}
	}
	def := defaultBranch(ctx, r, g)
	p.Base = def
	if r.Remote != "" && g.Verify(ctx, "refs/remotes/origin/"+def) {
		p.Base = "origin/" + def
	}
	branches, err := g.Branches(ctx)
	if err != nil {
		return HousekeepPlan{}, err
	}
	merged, _ := g.MergedInto(ctx, p.Base)
	wts, _ := g.Worktrees(ctx)
	current, _ := g.Branch(ctx)
	sessions := x.sessionBranches(r.Path)
	for _, b := range branches {
		if b.Name == def || b.Name == current {
			continue
		}
		why := ""
		switch {
		case slices.Contains(merged, b.Name):
			why = WhyMerged
		case b.Gone:
			why = WhyGone
		default:
			continue
		}
		s := StaleBranch{Name: b.Name, Why: why, When: b.When, Session: sessions[b.Name]}
		if i := slices.IndexFunc(wts, func(t gitx.Worktree) bool { return t.Branch == b.Name && !t.Prunable }); i >= 0 {
			s.Worktree = wts[i].Dir
			dirty, err := gitx.Repo{Dir: s.Worktree}.Dirty(ctx)
			s.Dirty = dirty || err != nil // a status git can't read isn't clean: housekeeping keeps it unless forced
		}
		p.Branches = append(p.Branches, s)
	}
	for _, t := range wts {
		if t.Prunable {
			p.Stale = append(p.Stale, t.Dir)
		}
	}
	return p, nil
}

// sessionBranches are the branches live sessions work on in worktrees of the checkout at path, to the session.
func (x *Registry) sessionBranches(path string) map[string]string {
	out := map[string]string{}
	for _, se := range x.st.Snapshot().Sessions {
		if se.Archived {
			continue
		}
		for _, p := range se.Projects {
			if p.Repo == path && p.Branch != "" {
				out[p.Branch] = string(se.ID)
			}
		}
	}
	return out
}

// Housekeep deletes the branches asked for, each with its worktree, and prunes stale worktrees if asked. Only
// branches the plan offers go; a dirty worktree goes only when forced. Each has its own result.
func (x *Registry) Housekeep(ctx context.Context, req HousekeepReq) (out []Cleaned, err error) {
	_, g, err := x.repoGit(req.Repo)
	if err != nil {
		return nil, err
	}
	plan, err := x.Housekeeping(ctx, HousekeepReq{Repo: req.Repo})
	if err != nil {
		return nil, err
	}
	for _, name := range req.Apply {
		i := slices.IndexFunc(plan.Branches, func(b StaleBranch) bool { return b.Name == name })
		res := Cleaned{Branch: name}
		switch {
		case i < 0:
			res.Error = "not merged and its upstream isn't gone: kept"
		case plan.Branches[i].Session != "":
			res.Error = "in session " + plan.Branches[i].Session + ": archive it first"
		default:
			b := plan.Branches[i]
			force := slices.Contains(req.Force, name)
			if b.Worktree != "" {
				if b.Dirty && !force {
					res.Error = "its worktree has uncommitted changes: kept"
					break
				}
				if err := g.RemoveWorktree(ctx, b.Worktree, b.Dirty); err != nil {
					res.Error = err.Error()
					break
				}
			}
			if err := g.DeleteBranch(ctx, name, b.Why == WhyGone); err != nil {
				res.Error = err.Error()
			}
		}
		out = append(out, res)
	}
	if req.Prune {
		if err := g.PruneWorktrees(ctx); err != nil {
			return out, err
		}
	}
	return out, nil
}

// TagPreview is what the New tag form starts from.
type TagPreview struct {
	Branches []string `json:"branches"`
	Default  string   `json:"default"`
	Latest   string   `json:"latest,omitempty"` // the highest semver tag
	Commit   string   `json:"commit,omitempty"` // what the branch points at now
	Subject  string   `json:"subject,omitempty"`
	Signing  bool     `json:"signing,omitempty"`
	Remote   bool     `json:"remote,omitempty"`
}

// TagPreview reads a checkout's branches and tags, and the commit branch ("" for the default) is at.
func (x *Registry) TagPreview(ctx context.Context, id core.RepoID, branch string) (p TagPreview, err error) {
	r, g, err := x.repoGit(id)
	if err != nil {
		return TagPreview{}, err
	}
	bs, err := g.Branches(ctx)
	if err != nil {
		return TagPreview{}, err
	}
	for _, b := range bs {
		p.Branches = append(p.Branches, b.Name)
	}
	p.Default = defaultBranch(ctx, r, g)
	if branch == "" {
		branch = p.Default
	}
	p.Latest, _ = g.LatestSemverTag(ctx)
	p.Commit, p.Subject, _ = g.CommitOf(ctx, "refs/heads/"+branch)
	p.Signing = g.SigningConfigured(ctx)
	p.Remote = r.Remote != ""
	return p, nil
}

// TagReq is repos.tag: an annotated tag on a branch, pulled first, signed and pushed if asked.
type TagReq struct {
	Repo    core.RepoID `json:"repo"`
	Branch  string      `json:"branch,omitempty"` // the default branch if ""
	Name    string      `json:"name"`
	Message string      `json:"message"`
	Pull    bool        `json:"pull,omitempty"`
	Sign    bool        `json:"sign,omitempty"`
	Push    bool        `json:"push,omitempty"`
}

// Tagged is a tag made: the commit, and how its push went. A failed push keeps the tag.
type Tagged struct {
	Name    string `json:"name"`
	Commit  string `json:"commit"`
	Subject string `json:"subject,omitempty"`
	Pushed  bool   `json:"pushed,omitempty"`
	PushErr string `json:"pushErr,omitempty"`
}

// Tag makes an annotated tag on a branch: fast-forwarded first if asked, and pushed (that tag alone) if asked.
func (x *Registry) Tag(ctx context.Context, req TagReq) (t Tagged, err error) {
	r, g, err := x.repoGit(req.Repo)
	if err != nil {
		return Tagged{}, err
	}
	branch := cmp.Or(req.Branch, defaultBranch(ctx, r, g))
	if err := g.ValidTagName(ctx, req.Name); err != nil {
		return Tagged{}, err
	}
	if g.TagExists(ctx, req.Name) {
		return Tagged{}, fmt.Errorf("tag %s already exists", req.Name)
	}
	if req.Push && r.Remote == "" {
		return Tagged{}, fmt.Errorf("%s has no remote to push to", r.ID)
	}
	if req.Pull && r.Remote != "" {
		if err := g.FastForward(ctx, branch); err != nil {
			return Tagged{}, err
		}
	}
	ref := "refs/heads/" + branch
	if err := g.CreateTag(ctx, gitx.TagReq{Name: req.Name, Ref: ref, Message: req.Message, Sign: req.Sign}); err != nil {
		return Tagged{}, err
	}
	t = Tagged{Name: req.Name}
	t.Commit, t.Subject, _ = g.CommitOf(ctx, ref)
	if req.Push {
		if err := g.PushTag(ctx, "origin", req.Name); err != nil {
			t.PushErr = err.Error()
		} else {
			t.Pushed = true
		}
	}
	return t, nil
}

// PushTag pushes one tag to a checkout's origin.
func (x *Registry) PushTag(ctx context.Context, id core.RepoID, name string) (err error) {
	_, g, err := x.repoGit(id)
	if err != nil {
		return err
	}
	return g.PushTag(ctx, "origin", name)
}
