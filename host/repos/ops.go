package repos

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/liamg/oriel/host/hostapi"

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

// RepoFacts reads a checkout's facts.
func (x *Registry) RepoFacts(ctx context.Context, id core.RepoID) (f hostapi.RepoFacts, err error) {
	defer x.env.Recover("RepoFacts", &err)
	r, g, err := x.repoGit(id)
	if err != nil {
		return hostapi.RepoFacts{}, err
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
	defer x.env.Recover("FetchRepo", &err)
	_, g, err := x.repoGit(id)
	if err != nil {
		return err
	}
	return g.FetchPrune(ctx)
}

// Housekeeping reads what a checkout has to clean: local branches merged into its default, or whose upstream was
// deleted, and worktrees whose directory is gone. Never the default branch or the checkout's own.
func (x *Registry) Housekeeping(ctx context.Context, req hostapi.HousekeepReq) (p hostapi.HousekeepPlan, err error) {
	defer x.env.Recover("Housekeeping", &err)
	r, g, err := x.repoGit(req.Repo)
	if err != nil {
		return hostapi.HousekeepPlan{}, err
	}
	if req.Fetch && r.Remote != "" {
		if err := g.FetchPrune(ctx); err != nil {
			return hostapi.HousekeepPlan{}, err
		}
	}
	def := defaultBranch(ctx, r, g)
	p.Base = def
	if r.Remote != "" && g.Verify(ctx, "refs/remotes/origin/"+def) {
		p.Base = "origin/" + def
	}
	branches, err := g.Branches(ctx)
	if err != nil {
		return hostapi.HousekeepPlan{}, err
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
			why = hostapi.WhyMerged
		case b.Gone:
			why = hostapi.WhyGone
		default:
			continue
		}
		s := hostapi.StaleBranch{Name: b.Name, Why: why, When: b.When, Session: sessions[b.Name]}
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
func (x *Registry) Housekeep(ctx context.Context, req hostapi.HousekeepReq) (out []hostapi.Cleaned, err error) {
	defer x.env.Recover("Housekeep", &err)
	_, g, err := x.repoGit(req.Repo)
	if err != nil {
		return nil, err
	}
	plan, err := x.Housekeeping(ctx, hostapi.HousekeepReq{Repo: req.Repo})
	if err != nil {
		return nil, err
	}
	for _, name := range req.Apply {
		i := slices.IndexFunc(plan.Branches, func(b hostapi.StaleBranch) bool { return b.Name == name })
		res := hostapi.Cleaned{Branch: name}
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
			if err := g.DeleteBranch(ctx, name, b.Why == hostapi.WhyGone); err != nil {
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

// TagPreview reads a checkout's branches and tags, and the commit branch ("" for the default) is at.
func (x *Registry) TagPreview(ctx context.Context, id core.RepoID, branch string) (p hostapi.TagPreview, err error) {
	defer x.env.Recover("TagPreview", &err)
	r, g, err := x.repoGit(id)
	if err != nil {
		return hostapi.TagPreview{}, err
	}
	bs, err := g.Branches(ctx)
	if err != nil {
		return hostapi.TagPreview{}, err
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

// Tag makes an annotated tag on a branch: fast-forwarded first if asked, and pushed (that tag alone) if asked.
func (x *Registry) Tag(ctx context.Context, req hostapi.TagReq) (t hostapi.Tagged, err error) {
	defer x.env.Recover("Tag", &err)
	r, g, err := x.repoGit(req.Repo)
	if err != nil {
		return hostapi.Tagged{}, err
	}
	branch := cmp.Or(req.Branch, defaultBranch(ctx, r, g))
	if err := g.ValidTagName(ctx, req.Name); err != nil {
		return hostapi.Tagged{}, err
	}
	if g.TagExists(ctx, req.Name) {
		return hostapi.Tagged{}, fmt.Errorf("tag %s already exists", req.Name)
	}
	if req.Push && r.Remote == "" {
		return hostapi.Tagged{}, fmt.Errorf("%s has no remote to push to", r.ID)
	}
	if req.Pull && r.Remote != "" {
		if err := g.FastForward(ctx, branch); err != nil {
			return hostapi.Tagged{}, err
		}
	}
	ref := "refs/heads/" + branch
	if err := g.CreateTag(ctx, gitx.TagReq{Name: req.Name, Ref: ref, Message: req.Message, Sign: req.Sign}); err != nil {
		return hostapi.Tagged{}, err
	}
	t = hostapi.Tagged{Name: req.Name}
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
	defer x.env.Recover("PushTag", &err)
	_, g, err := x.repoGit(id)
	if err != nil {
		return err
	}
	return g.PushTag(ctx, "origin", name)
}
