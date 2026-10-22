package host

import (
	"context"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gitx"
)

// SearchPRs finds pull requests across GitHub as the user's gh is signed in.
func (w *Host) SearchPRs(ctx context.Context, r PRsReq) (_ []gh.Found, err error) {
	defer w.recover("SearchPRs", &err)
	return w.code.SearchPRs(ctx, r)
}

// ReviewQueue is the user's review queue on GitHub, as their gh is signed in (gh.API.ReviewQueue).
func (w *Host) ReviewQueue(ctx context.Context, r ReviewQueueReq) (_ []gh.Asked, err error) {
	defer w.recover("ReviewQueue", &err)
	return w.code.ReviewQueue(ctx, r)
}

// PRReview reads a pull request in full: its details, files, reviews and the checks on its head.
func (w *Host) PRReview(ctx context.Context, r PRReviewReq) (_ PRReview, err error) {
	defer w.recover("PRReview", &err)
	return w.code.PRReview(ctx, r)
}

// SubmitPRReview submits the user's review of a pull request: never an agent's (NoPosting).
func (w *Host) SubmitPRReview(ctx context.Context, r PRSubmitReq) (err error) {
	defer w.recover("SubmitPRReview", &err)
	return w.code.SubmitPRReview(ctx, r)
}

// SyncGitHub mirrors the review of each of a session's projects' PRs into its threads.
func (w *Host) SyncGitHub(ctx context.Context, r MirrorReq) (_ Mirror, err error) {
	defer w.recover("SyncGitHub", &err)
	return w.code.SyncGitHub(ctx, r)
}

// ForwardGitHub posts the agent's draft reply to GitHub as the user, once, and marks it sent.
func (w *Host) ForwardGitHub(ctx context.Context, r ForwardReq) (_ core.Comment, err error) {
	defer w.recover("ForwardGitHub", &err)
	return w.code.ForwardGitHub(ctx, r)
}

// ForwardHost posts a draft reply on a thread mirrored from another code host with post, which returns the host's
// id for it, and marks it sent. A mirror meanwhile waits for the mark, so the reply's echo is recognised.
func (w *Host) ForwardHost(ctx context.Context, r ForwardReq, post func(ctx context.Context, t core.Thread, body string) (string, error)) (_ core.Comment, err error) {
	defer w.recover("ForwardHost", &err)
	return w.code.ForwardHost(ctx, r, post)
}

// EditDraft rewrites a draft reply before it's forwarded.
func (w *Host) EditDraft(r DraftEditReq) (err error) {
	defer w.recover("EditDraft", &err)
	return w.code.EditDraft(r)
}

// ResolveGitHub resolves or reopens a review thread on GitHub, and records that it is.
func (w *Host) ResolveGitHub(ctx context.Context, r ResolveReq) (err error) {
	defer w.recover("ResolveGitHub", &err)
	return w.code.ResolveGitHub(ctx, r)
}

// MarkHostResolved records that a mirrored thread is resolved, or open, on its code host: once the user's resolve has
// reached it.
func (w *Host) MarkHostResolved(r ResolveReq) (err error) {
	defer w.recover("MarkHostResolved", &err)
	return w.code.MarkHostResolved(r)
}

// MirrorReview brings a PR's review threads into the session, and hands the agent what's new.
func (w *Host) MirrorReview(ctx context.Context, r MirrorReviewReq) (err error) {
	defer w.recover("MirrorReview", &err)
	return w.code.MirrorReview(ctx, r)
}

// Forwarded marks a draft reply sent, once its code host has it. A mirror can meanwhile take its echo for a new
// comment: ForwardHost posts and marks with mirroring held off.
func (w *Host) Forwarded(r ForwardedReq) (_ core.Comment, err error) {
	defer w.recover("Forwarded", &err)
	return w.code.Forwarded(r)
}

// SetReviewDraft keeps the review the user is writing of a review session's pull request.
func (w *Host) SetReviewDraft(r core.ReviewDrafted) (err error) {
	defer w.recover("SetReviewDraft", &err)
	return w.code.SetReviewDraft(r)
}

// GitHubPRFor is the branch's PR: its open one, else the newest; nil for none.
func (w *Host) GitHubPRFor(ctx context.Context, r GitHubReq) (_ *gh.PR, err error) {
	defer w.recover("GitHubPRFor", &err)
	return w.code.GitHubPRFor(ctx, r)
}

// GitHubCreate raises a PR, from a branch of the session's only.
func (w *Host) GitHubCreate(ctx context.Context, r GitHubReq) (_ *gh.PR, err error) {
	defer w.recover("GitHubCreate", &err)
	return w.code.GitHubCreate(ctx, r)
}

// GitHubEdit sets a PR's title and body.
func (w *Host) GitHubEdit(ctx context.Context, r GitHubReq) (_ *gh.PR, err error) {
	defer w.recover("GitHubEdit", &err)
	return w.code.GitHubEdit(ctx, r)
}

// GitHubReady takes a draft PR out of draft.
func (w *Host) GitHubReady(ctx context.Context, r GitHubReq) (err error) {
	defer w.recover("GitHubReady", &err)
	return w.code.GitHubReady(ctx, r)
}

// GitHubReviewers asks people and teams to review a PR.
func (w *Host) GitHubReviewers(ctx context.Context, r GitHubReq) (err error) {
	defer w.recover("GitHubReviewers", &err)
	return w.code.GitHubReviewers(ctx, r)
}

// GitHubChecks are the checks on a PR's branch.
func (w *Host) GitHubChecks(ctx context.Context, r GitHubReq) (_ GitHubChecks, err error) {
	defer w.recover("GitHubChecks", &err)
	return w.code.GitHubChecks(ctx, r)
}

// GitHubMergeState is what merging a PR now would meet.
func (w *Host) GitHubMergeState(ctx context.Context, r GitHubReq) (_ gh.MergeState, err error) {
	defer w.recover("GitHubMergeState", &err)
	return w.code.GitHubMergeState(ctx, r)
}

// GitHubMerge merges a PR at the head the user saw.
func (w *Host) GitHubMerge(ctx context.Context, r GitHubReq) (err error) {
	defer w.recover("GitHubMerge", &err)
	return w.code.GitHubMerge(ctx, r)
}

// GitHubDeleteBranch deletes a merged PR's branch on GitHub.
func (w *Host) GitHubDeleteBranch(ctx context.Context, r GitHubReq) (err error) {
	defer w.recover("GitHubDeleteBranch", &err)
	return w.code.GitHubDeleteBranch(ctx, r)
}

// ShipRead is every project of a session on its way to a PR.
func (w *Host) ShipRead(ctx context.Context, r ShipReq) (_ []Ship, err error) {
	defer w.recover("ShipRead", &err)
	return w.code.ShipRead(ctx, r)
}

// ShipCommit commits a project's files with the user's git identity, drops the commit draft it used, and returns the
// commit's hash.
func (w *Host) ShipCommit(ctx context.Context, r ShipCommitReq) (_ string, err error) {
	defer w.recover("ShipCommit", &err)
	return w.code.ShipCommit(ctx, r)
}

// ShipPush pushes a project's branch to origin: the session's own branch only.
func (w *Host) ShipPush(ctx context.Context, r ShipRef) (err error) {
	defer w.recover("ShipPush", &err)
	return w.code.ShipPush(ctx, r)
}

// ShipSync fetches origin and rebases a project's branch onto its base, carrying uncommitted work over. One that
// conflicts is undone and says where.
func (w *Host) ShipSync(ctx context.Context, r ShipRef) (err error) {
	defer w.recover("ShipSync", &err)
	return w.code.ShipSync(ctx, r)
}

// ShipMergeDefault merges a project's branch into the repo's default branch, here and not pushed (gitx.MergeInto):
// the session's own branch only, and its committed work only.
func (w *Host) ShipMergeDefault(ctx context.Context, r ShipRef) (_ gitx.Merge, err error) {
	defer w.recover("ShipMergeDefault", &err)
	return w.code.ShipMergeDefault(ctx, r)
}

// ShipRename renames a project's branch. The app refuses first when its PR is open on it.
func (w *Host) ShipRename(ctx context.Context, r RenameReq) (err error) {
	defer w.recover("ShipRename", &err)
	return w.code.ShipRename(ctx, r)
}

// ShipDraft saves a draft the user wrote, or drops it.
func (w *Host) ShipDraft(ctx context.Context, r DraftReq) (err error) {
	defer w.recover("ShipDraft", &err)
	return w.code.ShipDraft(ctx, r)
}

// DraftOf is a thread's draft reply to forward: its body, refusing one that isn't a draft or has gone already.
func (w *Host) DraftOf(r ForwardReq) (core.Thread, string, error) { return w.code.DraftOf(r) }
