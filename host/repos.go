package host

import (
	"context"

	"github.com/liamg/oriel/core"
)

// AddRepoPath registers the checkout at path, or returns it if it's already registered.
func (w *Host) AddRepoPath(ctx context.Context, path string) (_ core.Repo, err error) {
	defer w.recover("AddRepoPath", &err)
	return w.repos.AddRepoPath(ctx, path)
}

// RemoveRepo unregisters a repo, leaving its files, and keeps discovery from adding it back.
func (w *Host) RemoveRepo(id core.RepoID) (err error) {
	defer w.recover("RemoveRepo", &err)
	return w.repos.RemoveRepo(id)
}

// CloneTarget says where a clone would go and what's there.
func (w *Host) CloneTarget(ctx context.Context, r CloneReq) (_ CloneTarget, err error) {
	defer w.recover("CloneTarget", &err)
	return w.repos.CloneTarget(ctx, r)
}

// CloneRepo clones a repo with the user's own git and credentials, and registers it. A checkout already there of
// the same URL is registered as it is. Cancelling ctx stops the clone and takes away what it made.
func (w *Host) CloneRepo(ctx context.Context, r CloneReq) (_ core.Repo, err error) {
	defer w.recover("CloneRepo", &err)
	return w.repos.CloneRepo(ctx, r)
}

// ScanRepos registers the siblings of the known repos: for each folder one sits in, the git repos directly in it
// that are their own main clone. A folder read before is skipped unless all is set; removed repos stay removed.
func (w *Host) ScanRepos(ctx context.Context, all bool) (_ []core.Repo, err error) {
	defer w.recover("ScanRepos", &err)
	return w.repos.ScanRepos(ctx, all)
}

// RepoFacts reads a checkout's facts.
func (w *Host) RepoFacts(ctx context.Context, id core.RepoID) (_ RepoFacts, err error) {
	defer w.recover("RepoFacts", &err)
	return w.repos.RepoFacts(ctx, id)
}

// FetchRepo fetches a checkout's remote, pruning branches it deleted.
func (w *Host) FetchRepo(ctx context.Context, id core.RepoID) (err error) {
	defer w.recover("FetchRepo", &err)
	return w.repos.FetchRepo(ctx, id)
}

// Housekeeping reads what a checkout has to clean: local branches merged into its default, or whose upstream was
// deleted, and worktrees whose directory is gone. Never the default branch or the checkout's own.
func (w *Host) Housekeeping(ctx context.Context, req HousekeepReq) (_ HousekeepPlan, err error) {
	defer w.recover("Housekeeping", &err)
	return w.repos.Housekeeping(ctx, req)
}

// Housekeep deletes the branches asked for, each with its worktree, and prunes stale worktrees if asked.
func (w *Host) Housekeep(ctx context.Context, req HousekeepReq) (_ []Cleaned, err error) {
	defer w.recover("Housekeep", &err)
	return w.repos.Housekeep(ctx, req)
}

// TagPreview reads a checkout's branches and tags, and the commit branch ("" for the default) is at.
func (w *Host) TagPreview(ctx context.Context, id core.RepoID, branch string) (_ TagPreview, err error) {
	defer w.recover("TagPreview", &err)
	return w.repos.TagPreview(ctx, id, branch)
}

// Tag makes an annotated tag on a branch: fast-forwarded first if asked, and pushed (that tag alone) if asked.
func (w *Host) Tag(ctx context.Context, req TagReq) (_ Tagged, err error) {
	defer w.recover("Tag", &err)
	return w.repos.Tag(ctx, req)
}

// PushTag pushes one tag to a checkout's origin.
func (w *Host) PushTag(ctx context.Context, id core.RepoID, name string) (err error) {
	defer w.recover("PushTag", &err)
	return w.repos.PushTag(ctx, id, name)
}

// Repos is the registered checkouts.
func (w *Host) Repos() []core.Repo { return w.repos.Repos() }

// CheckRepoPath says what adding the checkout at path would do, without adding it.
func (w *Host) CheckRepoPath(ctx context.Context, p string) (c RepoCheck) {
	defer w.recover("CheckRepoPath", nil)
	return w.repos.CheckRepoPath(ctx, p)
}

// CloneParent is the folder clones go in by default: the one most known repos share, else the repos directory.
func (w *Host) CloneParent() string { return w.repos.CloneParent() }

// SetReposDir sets where clones and new repos go when nothing else says (repos.dir); "" puts back the default.
func (w *Host) SetReposDir(dir string) { w.repos.SetReposDir(dir) }
