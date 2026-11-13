package host

import (
	"context"
	"fmt"
	"slices"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gitx"
)

// SearchPRs is github.GitHub.SearchPRs, with a panic recovered.
func (w *Host) SearchPRs(ctx context.Context, r PRsReq) (_ []gh.Found, err error) {
	defer w.recover("SearchPRs", &err)
	return w.code.SearchPRs(ctx, r)
}

// ReviewQueue is github.GitHub.ReviewQueue, with a panic recovered.
func (w *Host) ReviewQueue(ctx context.Context, r ReviewQueueReq) (_ []gh.Asked, err error) {
	defer w.recover("ReviewQueue", &err)
	return w.code.ReviewQueue(ctx, r)
}

// PRReview is github.GitHub.PRReview, with a panic recovered.
func (w *Host) PRReview(ctx context.Context, r PRReviewReq) (_ PRReview, err error) {
	defer w.recover("PRReview", &err)
	return w.code.PRReview(ctx, r)
}

// SubmitPRReview is github.GitHub.SubmitPRReview, with a panic recovered.
func (w *Host) SubmitPRReview(ctx context.Context, r PRSubmitReq) (err error) {
	defer w.recover("SubmitPRReview", &err)
	return w.code.SubmitPRReview(ctx, r)
}

// ReactPR is github.GitHub.ReactPR, with a panic recovered.
func (w *Host) ReactPR(ctx context.Context, r PRReactReq) (err error) {
	defer w.recover("ReactPR", &err)
	return w.code.ReactPR(ctx, r)
}

// UnreactPR is github.GitHub.UnreactPR, with a panic recovered.
func (w *Host) UnreactPR(ctx context.Context, r PRReactReq) (err error) {
	defer w.recover("UnreactPR", &err)
	return w.code.UnreactPR(ctx, r)
}

// SyncGitHub is github.GitHub.SyncGitHub, with a panic recovered.
func (w *Host) SyncGitHub(ctx context.Context, r MirrorReq) (_ Mirror, err error) {
	defer w.recover("SyncGitHub", &err)
	return w.code.SyncGitHub(ctx, r)
}

// ForwardGitHub is github.GitHub.ForwardGitHub, with a panic recovered.
func (w *Host) ForwardGitHub(ctx context.Context, r ForwardReq) (_ core.Comment, err error) {
	defer w.recover("ForwardGitHub", &err)
	return w.code.ForwardGitHub(ctx, r)
}

// ForwardHost is github.GitHub.ForwardHost, with a panic recovered.
func (w *Host) ForwardHost(ctx context.Context, r ForwardReq, post func(ctx context.Context, t core.Thread, body string) (string, error)) (_ core.Comment, err error) {
	defer w.recover("ForwardHost", &err)
	return w.code.ForwardHost(ctx, r, post)
}

// EditDraft is github.GitHub.EditDraft, with a panic recovered.
func (w *Host) EditDraft(r DraftEditReq) (err error) {
	defer w.recover("EditDraft", &err)
	return w.code.EditDraft(r)
}

// ResolveGitHub is github.GitHub.ResolveGitHub, with a panic recovered.
func (w *Host) ResolveGitHub(ctx context.Context, r ResolveReq) (err error) {
	defer w.recover("ResolveGitHub", &err)
	return w.code.ResolveGitHub(ctx, r)
}

// MarkHostResolved is github.GitHub.MarkHostResolved, with a panic recovered.
func (w *Host) MarkHostResolved(r ResolveReq) (err error) {
	defer w.recover("MarkHostResolved", &err)
	return w.code.MarkHostResolved(r)
}

// MirrorReview is github.GitHub.MirrorReview, with a panic recovered.
func (w *Host) MirrorReview(ctx context.Context, r MirrorReviewReq) (err error) {
	defer w.recover("MirrorReview", &err)
	return w.code.MirrorReview(ctx, r)
}

// Forwarded is github.GitHub.Forwarded, with a panic recovered.
func (w *Host) Forwarded(r ForwardedReq) (_ core.Comment, err error) {
	defer w.recover("Forwarded", &err)
	return w.code.Forwarded(r)
}

// SetReviewDraft is github.GitHub.SetReviewDraft, with a panic recovered.
func (w *Host) SetReviewDraft(r core.ReviewDrafted) (err error) {
	defer w.recover("SetReviewDraft", &err)
	return w.code.SetReviewDraft(r)
}

// GitHubPRFor is github.GitHub.GitHubPRFor, with a panic recovered.
func (w *Host) GitHubPRFor(ctx context.Context, r GitHubReq) (_ *gh.PR, err error) {
	defer w.recover("GitHubPRFor", &err)
	return w.code.GitHubPRFor(ctx, r)
}

// GitHubCreate is github.GitHub.GitHubCreate, with a panic recovered.
func (w *Host) GitHubCreate(ctx context.Context, r GitHubReq) (_ *gh.PR, err error) {
	defer w.recover("GitHubCreate", &err)
	return w.code.GitHubCreate(ctx, r)
}

// GitHubEdit is github.GitHub.GitHubEdit, with a panic recovered.
func (w *Host) GitHubEdit(ctx context.Context, r GitHubReq) (_ *gh.PR, err error) {
	defer w.recover("GitHubEdit", &err)
	return w.code.GitHubEdit(ctx, r)
}

// GitHubReady is github.GitHub.GitHubReady, with a panic recovered.
func (w *Host) GitHubReady(ctx context.Context, r GitHubReq) (err error) {
	defer w.recover("GitHubReady", &err)
	return w.code.GitHubReady(ctx, r)
}

// GitHubReviewers is github.GitHub.GitHubReviewers, with a panic recovered.
func (w *Host) GitHubReviewers(ctx context.Context, r GitHubReq) (err error) {
	defer w.recover("GitHubReviewers", &err)
	return w.code.GitHubReviewers(ctx, r)
}

// GitHubChecks is github.GitHub.GitHubChecks, with a panic recovered.
func (w *Host) GitHubChecks(ctx context.Context, r GitHubReq) (_ GitHubChecks, err error) {
	defer w.recover("GitHubChecks", &err)
	return w.code.GitHubChecks(ctx, r)
}

// GitHubMergeState is github.GitHub.GitHubMergeState, with a panic recovered.
func (w *Host) GitHubMergeState(ctx context.Context, r GitHubReq) (_ gh.MergeState, err error) {
	defer w.recover("GitHubMergeState", &err)
	return w.code.GitHubMergeState(ctx, r)
}

// GitHubMerge is github.GitHub.GitHubMerge, with a panic recovered.
func (w *Host) GitHubMerge(ctx context.Context, r GitHubReq) (err error) {
	defer w.recover("GitHubMerge", &err)
	return w.code.GitHubMerge(ctx, r)
}

// GitHubDeleteBranch is github.GitHub.GitHubDeleteBranch, with a panic recovered.
func (w *Host) GitHubDeleteBranch(ctx context.Context, r GitHubReq) (err error) {
	defer w.recover("GitHubDeleteBranch", &err)
	return w.code.GitHubDeleteBranch(ctx, r)
}

// ShipRead is github.GitHub.ShipRead, with a panic recovered.
func (w *Host) ShipRead(ctx context.Context, r ShipReq) (_ []Ship, err error) {
	defer w.recover("ShipRead", &err)
	return w.code.ShipRead(ctx, r)
}

// ShipCommit is github.GitHub.ShipCommit, with a panic recovered.
func (w *Host) ShipCommit(ctx context.Context, r ShipCommitReq) (_ string, err error) {
	defer w.recover("ShipCommit", &err)
	return w.code.ShipCommit(ctx, r)
}

// ShipPush is github.GitHub.ShipPush, with a panic recovered.
func (w *Host) ShipPush(ctx context.Context, r ShipRef) (err error) {
	defer w.recover("ShipPush", &err)
	return w.code.ShipPush(ctx, r)
}

// ShipSync is github.GitHub.ShipSync, with a panic recovered.
func (w *Host) ShipSync(ctx context.Context, r ShipRef) (err error) {
	defer w.recover("ShipSync", &err)
	return w.code.ShipSync(ctx, r)
}

// ShipMergeDefault is github.GitHub.ShipMergeDefault, with a panic recovered.
func (w *Host) ShipMergeDefault(ctx context.Context, r ShipRef) (_ gitx.Merge, err error) {
	defer w.recover("ShipMergeDefault", &err)
	return w.code.ShipMergeDefault(ctx, r)
}

// ShipRename renames a project's branch. The app refuses first when its PR is open on it.
func (w *Host) ShipRename(ctx context.Context, r RenameReq) (err error) {
	defer w.recover("ShipRename", &err)
	if err := w.code.ShipRename(ctx, r); err != nil {
		return err
	}
	w.run.WriteNotes(r.Session)
	return nil
}

// NewBranch moves a project onto a fresh branch cut from its repo's default branch, freshly fetched, which becomes
// the session's branch for it; the one it leaves is kept. It answers the new branch's name.
func (w *Host) NewBranch(ctx context.Context, r NewBranchReq) (_ string, err error) {
	defer w.recover("NewBranch", &err)
	name, err := w.work.NewBranch(ctx, r)
	if err != nil {
		return "", err
	}
	w.run.WriteNotes(r.Session)
	return name, nil
}

// ShipSwitchBack puts a project's worktree back on the session's branch for it.
func (w *Host) ShipSwitchBack(ctx context.Context, r ShipRef) (err error) {
	defer w.recover("ShipSwitchBack", &err)
	if err := w.work.SwitchBack(ctx, r.Session, r.Project); err != nil {
		return err
	}
	if p, ok := w.projectOf(r); ok {
		w.run.Note(r.Session, fmt.Sprintf("The user switched %s back to this session's branch %s. Stay on it.", p.Name, p.Branch))
	}
	return nil
}

// ShipAdopt makes the branch a project's worktree is on the session's branch for it, when that's safe.
func (w *Host) ShipAdopt(ctx context.Context, r ShipRef) (err error) {
	defer w.recover("ShipAdopt", &err)
	if err := w.work.Adopt(ctx, r.Session, r.Project); err != nil {
		return err
	}
	w.run.WriteNotes(r.Session)
	if p, ok := w.projectOf(r); ok {
		w.run.Note(r.Session, fmt.Sprintf("The user made %s this session's branch for %s: Oriel pushes it now.", p.Branch, p.Name))
	}
	return nil
}

// projectOf is the project r names.
func (w *Host) projectOf(r ShipRef) (core.Project, bool) {
	se, err := w.session(r.Session)
	if err != nil {
		return core.Project{}, false
	}
	i := slices.IndexFunc(se.Projects, func(p core.Project) bool { return p.Name == r.Project })
	if i < 0 {
		return core.Project{}, false
	}
	return se.Projects[i], true
}

// ShipDraft is github.GitHub.ShipDraft, with a panic recovered.
func (w *Host) ShipDraft(ctx context.Context, r DraftReq) (err error) {
	defer w.recover("ShipDraft", &err)
	return w.code.ShipDraft(ctx, r)
}

// DraftOf is github.GitHub.DraftOf.
func (w *Host) DraftOf(r ForwardReq) (core.Thread, string, error) { return w.code.DraftOf(r) }
