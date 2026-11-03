package host

import (
	"slices"
	"sync"

	"github.com/liamg/oriel/core"
)

// KindSource is where the host finds the kinds of session it starts: the app's registry (registry.SessionKind).
type KindSource interface {
	Kind(k core.Kind) (core.KindSpec, bool)
}

var everything = []core.Capability{core.AddProjects, core.Ships, core.OwnsChanges, core.NextPR, core.Delete, core.OwnsBranch}

// Built-in kinds of session, as data. Their plugins register them in the app; the host reads
// these until the app gives it its registry (SetKinds).
var (
	WorkKind = core.KindSpec{Kind: core.KindWork, Title: "Work", Desc: "straight to the worktree", From: core.FromPrompt, Pack: "work",
		Worktrees: core.WorktreesNow, Gate: "hard", Launcher: true, Order: 10, Caps: everything,
		About: "Each repo gets a worktree on the session's branch, from a freshly fetched base, and the agent starts at once."}
	PlanKind = core.KindSpec{Kind: core.KindPlan, Title: "Plan", Desc: "docs first, Start work later", From: core.FromPrompt, Pack: "plan",
		Worktrees: core.WorktreesStartWork, Gate: "hard", Launcher: true, Order: 20, Caps: everything,
		About: "The agent drafts the plan in PLAN.md, you annotate it, and Start work implements it in these worktrees."}
	ReviewKind = core.KindSpec{Kind: core.KindReview, Title: "Review PR", Desc: "someone else's change", From: core.FromPR, Pack: "review",
		Worktrees: core.WorktreesPR, Gate: "off", Launcher: true, Order: 30,
		Caps:  []core.Capability{core.AddProjects, core.Delete, core.OwnsBranch, core.CommentsInDiff},
		About: "The PR's head is checked out read-only from the repo whose remote it's on, and the agent reviews it.",
		// The Explorer on what the PR changes, its diff in front and the review tab behind.
		Opens: []core.KindCall{{Command: "explorer.scope", Args: map[string]any{"scope": "changed"}}, {Command: "ship.pr.open"},
			{Command: "view.open", Args: map[string]any{"id": "explorer", "place": "dock", "edge": "left", "alone": true, "fixed": true}},
			{Command: "diff.open"}}}
	JobKind = core.KindSpec{Kind: core.KindJob, Title: "Job", Desc: "runs to done on its own", From: core.FromPrompt, Pack: "job",
		Worktrees: core.WorktreesNow, Gate: "soft", Order: 40, PromptFile: "JOB.md", Caps: everything,
		About: "The agent runs the prompt until it's done or blocked, and makes worktrees as it needs them."}
	NewRepoKind = core.KindSpec{Kind: core.KindNewRepo, Title: "New repo", Desc: "start from nothing", From: core.FromName, Pack: "newrepo",
		Worktrees: core.WorktreesInit, Gate: "hard", Order: 50, Caps: everything,
		About: "Oriel makes the repo from the template, commits and registers it, and carries on as a plan for it."}
	DirectKind = core.KindSpec{Kind: core.KindDirect, Title: "Direct", Desc: "a directory as it is", From: core.FromDir, Pack: "direct",
		Worktrees: core.WorktreesNone, Gate: "off", Order: 60, Caps: []core.Capability{core.Ships, core.OwnsChanges},
		About: "Opens the directory itself, git repo or not: no worktree, no new branch. An agent here edits your real files."}
)

// BuiltinKinds are the built-in kinds, in the launcher's order.
func BuiltinKinds() []core.KindSpec {
	return []core.KindSpec{WorkKind, PlanKind, ReviewKind, JobKind, NewRepoKind, DirectKind}
}

type builtins struct{}

func (builtins) Kind(k core.Kind) (core.KindSpec, bool) {
	all := BuiltinKinds()
	if i := slices.IndexFunc(all, func(s core.KindSpec) bool { return s.Kind == k }); i >= 0 {
		return all[i], true
	}
	return core.KindSpec{}, false
}

// kinds holds the host's KindSource.
type kinds struct {
	mu  sync.RWMutex
	src KindSource
}

func (k *kinds) Get(kind core.Kind) (core.KindSpec, bool) {
	k.mu.RLock()
	src := k.src
	k.mu.RUnlock()
	if src == nil {
		src = builtins{}
	}
	return src.Kind(kind)
}

// SetKinds sets where the host finds its kinds of session; nil is the built-ins.
func (w *Host) SetKinds(src KindSource) {
	w.kinds.mu.Lock()
	defer w.kinds.mu.Unlock()
	w.kinds.src = src
}

// Spec is se's kind's spec; a kind no longer registered can do everything a session with worktrees can.
func (k *kinds) Spec(se core.SessionState) core.KindSpec {
	if s, ok := k.Get(se.Kind); ok {
		return s
	}
	return core.KindSpec{Kind: se.Kind, Caps: everything}
}
