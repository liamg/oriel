package hostapi

import (
	"slices"

	"github.com/liamg/oriel/core"
)

// WorktreeCaps are every capability a kind with worktrees has.
var WorktreeCaps = []core.Capability{core.AddProjects, core.Ships, core.OwnsChanges, core.NextPR, core.Delete, core.OwnsBranch}

// attended are the capabilities of a kind with worktrees whose agent works with you: it isn't done when it exits.
var attended = append(slices.Clone(WorktreeCaps), core.RestartsAgent)

// Built-in kinds of session, as data. Their plugins register them in the app; the host reads
// these until the app gives it its registry (SetKinds).
var (
	WorkKind = core.KindSpec{Kind: core.KindWork, Title: "Work", Desc: "straight to the worktree", From: core.FromPrompt, Pack: "work",
		Worktrees: core.WorktreesNow, Launcher: true, Order: 10, Caps: attended,
		About: "Each repo gets a worktree on the session's branch, from a freshly fetched base, and the agent starts at once."}
	PlanKind = core.KindSpec{Kind: core.KindPlan, Title: "Plan", Desc: "docs first, Start work later", From: core.FromPrompt, Pack: "plan",
		Worktrees: core.WorktreesStartWork, Launcher: true, Order: 20, Caps: attended,
		About: "The agent drafts the plan in PLAN.md, you annotate it, and Start work implements it in these worktrees."}
	ReviewKind = core.KindSpec{Kind: core.KindReview, Title: "Review PR", Desc: "someone else's change", From: core.FromPR, Pack: "review",
		Worktrees: core.WorktreesPR, Launcher: true, Order: 30,
		Caps:  []core.Capability{core.AddProjects, core.Delete, core.OwnsBranch, core.CommentsInDiff, core.RestartsAgent, core.DeletesOnEnd},
		About: "The PR's head is checked out read-only from the repo whose remote it's on, and the agent reviews it.",
		// The Explorer on what the PR changes, its diff in front and the review tab behind.
		Opens: []core.KindCall{{Command: "explorer.scope", Args: map[string]any{"scope": "changed"}}, {Command: "ship.pr.open"},
			{Command: "view.open", Args: map[string]any{"id": "explorer", "place": "dock", "edge": "left", "alone": true, "fixed": true}},
			{Command: "diff.open"}}}
	JobKind = core.KindSpec{Kind: core.KindJob, Title: "Job", Desc: "runs to done on its own", From: core.FromPrompt, Pack: "job",
		Worktrees: core.WorktreesNow, Order: 40, PromptFile: "JOB.md", AgentGate: "auto", Caps: WorktreeCaps,
		About: "The agent runs the prompt until it's done or blocked, and makes worktrees as it needs them."}
	NewRepoKind = core.KindSpec{Kind: core.KindNewRepo, Title: "New repo", Desc: "start from nothing", From: core.FromName, Pack: "newrepo",
		Worktrees: core.WorktreesInit, Order: 50, Caps: attended,
		About: "Oriel makes the repo from the template, commits and registers it, and carries on as a plan for it."}
	DirectKind = core.KindSpec{Kind: core.KindDirect, Title: "Direct", Desc: "a directory as it is", From: core.FromDir, Pack: "direct",
		Worktrees: core.WorktreesNone, Order: 60, Caps: []core.Capability{core.Ships, core.OwnsChanges, core.RestartsAgent},
		About: "Opens the directory itself, git repo or not: no worktree, no new branch. An agent here edits your real files."}
)

// BuiltinKinds are the built-in kinds, in the launcher's order.
func BuiltinKinds() []core.KindSpec {
	return []core.KindSpec{WorkKind, PlanKind, ReviewKind, JobKind, NewRepoKind, DirectKind}
}

// KindSource is where the host finds the kinds of session it starts: the app's registry (registry.SessionKind).
type KindSource interface {
	Kind(k core.Kind) (core.KindSpec, bool)
}
