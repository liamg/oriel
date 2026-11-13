package host

import (
	"context"

	"github.com/liamg/oriel/core"
)

// AddRepoPath is repos.Registry.AddRepoPath, with a panic recovered.
func (w *Host) AddRepoPath(ctx context.Context, path string) (_ core.Repo, err error) {
	defer w.recover("AddRepoPath", &err)
	return w.repos.AddRepoPath(ctx, path)
}

// RemoveRepo is repos.Registry.RemoveRepo, with a panic recovered.
func (w *Host) RemoveRepo(id core.RepoID) (err error) {
	defer w.recover("RemoveRepo", &err)
	return w.repos.RemoveRepo(id)
}

// CloneTarget is repos.Registry.CloneTarget, with a panic recovered.
func (w *Host) CloneTarget(ctx context.Context, r CloneReq) (_ CloneTarget, err error) {
	defer w.recover("CloneTarget", &err)
	return w.repos.CloneTarget(ctx, r)
}

// CloneRepo is repos.Registry.CloneRepo, with a panic recovered.
func (w *Host) CloneRepo(ctx context.Context, r CloneReq) (_ core.Repo, err error) {
	defer w.recover("CloneRepo", &err)
	return w.repos.CloneRepo(ctx, r)
}

// ScanRepos is repos.Registry.ScanRepos, with a panic recovered.
func (w *Host) ScanRepos(ctx context.Context, all bool) (_ []core.Repo, err error) {
	defer w.recover("ScanRepos", &err)
	return w.repos.ScanRepos(ctx, all)
}

// RepoFacts is repos.Registry.RepoFacts, with a panic recovered.
func (w *Host) RepoFacts(ctx context.Context, id core.RepoID) (_ RepoFacts, err error) {
	defer w.recover("RepoFacts", &err)
	return w.repos.RepoFacts(ctx, id)
}

// FetchRepo is repos.Registry.FetchRepo, with a panic recovered.
func (w *Host) FetchRepo(ctx context.Context, id core.RepoID) (err error) {
	defer w.recover("FetchRepo", &err)
	return w.repos.FetchRepo(ctx, id)
}

// Housekeeping is repos.Registry.Housekeeping, with a panic recovered.
func (w *Host) Housekeeping(ctx context.Context, req HousekeepReq) (_ HousekeepPlan, err error) {
	defer w.recover("Housekeeping", &err)
	return w.repos.Housekeeping(ctx, req)
}

// Housekeep is repos.Registry.Housekeep, with a panic recovered.
func (w *Host) Housekeep(ctx context.Context, req HousekeepReq) (_ []Cleaned, err error) {
	defer w.recover("Housekeep", &err)
	return w.repos.Housekeep(ctx, req)
}

// TagPreview is repos.Registry.TagPreview, with a panic recovered.
func (w *Host) TagPreview(ctx context.Context, id core.RepoID, branch string) (_ TagPreview, err error) {
	defer w.recover("TagPreview", &err)
	return w.repos.TagPreview(ctx, id, branch)
}

// Tag is repos.Registry.Tag, with a panic recovered.
func (w *Host) Tag(ctx context.Context, req TagReq) (_ Tagged, err error) {
	defer w.recover("Tag", &err)
	return w.repos.Tag(ctx, req)
}

// PushTag is repos.Registry.PushTag, with a panic recovered.
func (w *Host) PushTag(ctx context.Context, id core.RepoID, name string) (err error) {
	defer w.recover("PushTag", &err)
	return w.repos.PushTag(ctx, id, name)
}

// Repos is repos.Registry.Repos.
func (w *Host) Repos() []core.Repo { return w.repos.Repos() }

// CheckRepoPath is repos.Registry.CheckRepoPath, with a panic recovered.
func (w *Host) CheckRepoPath(ctx context.Context, p string) (c RepoCheck) {
	defer w.recover("CheckRepoPath", nil)
	return w.repos.CheckRepoPath(ctx, p)
}

// CloneParent is repos.Registry.CloneParent.
func (w *Host) CloneParent() string { return w.repos.CloneParent() }

// SetReposDir is repos.Registry.SetReposDir.
func (w *Host) SetReposDir(dir string) { w.repos.SetReposDir(dir) }
