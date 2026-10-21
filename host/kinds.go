package host

import (
	"slices"
	"sync"

	"github.com/liamg/oriel/core"
)

// Worktrees is when and how a kind of session gets its projects' worktrees.
type Worktrees string

const (
	WorktreesNow       Worktrees = "now"        // on a fresh branch from the fetched base, at once
	WorktreesStartWork Worktrees = "start-work" // at Start work; until then the agent reads the checkouts
	WorktreesPR        Worktrees = "pr"         // the pull request's head, detached and read-only
	WorktreesNone      Worktrees = "none"       // the directory itself (direct)
	WorktreesInit      Worktrees = "init"       // a new repo, made in place
)

// From is what a kind of session starts from, beside its prompt: what the launcher asks for.
type From string

const (
	FromPrompt From = "prompt" // a prompt or a ticket
	FromPR     From = "pr"     // a pull request's URL
	FromName   From = "name"   // a new repo's name, template and parent directory
	FromDir    From = "dir"    // a directory, opened as it is
)

// KindSpec is a kind of session: what it starts from and what its agent is told. Plugins add kinds
// (plugins.md: session kinds are a contribution point).
type KindSpec struct {
	Kind      core.Kind `json:"kind"`
	Title     string    `json:"title"`
	Desc      string    `json:"desc,omitempty"`  // a few words, under the title in the launcher
	About     string    `json:"about,omitempty"` // what starting it does, a sentence
	From      From      `json:"from,omitempty"`  // FromPrompt if ""
	Pack      string    `json:"pack"`            // the prompt pack
	Worktrees Worktrees `json:"worktrees"`
	Gate      string    `json:"gate,omitempty"` // the commit gate: hard, soft or off (agents.md)
	NeedsRepo bool      `json:"needsRepo,omitempty"`
	// Launcher offers the kind as a mode in the new-session launcher; the rest start from their own pages or
	// session.start.
	Launcher bool `json:"launcher,omitempty"`
	// AutoReview runs an AI review of the change as the session starts (AIReview): a review of a pull request.
	AutoReview bool `json:"autoReview,omitempty"`
	// PromptFile is a file at the workspace root the prompt is written to as the session starts: a job's JOB.md.
	PromptFile string `json:"promptFile,omitempty"`
	// Opens are commands run once the window is on a new session of the kind, before session.start's own then.
	Opens []KindCall `json:"opens,omitempty"`

	reg int // which registration it is
}

// KindCall is a command and its args.
type KindCall struct {
	Command string         `json:"command"`
	Args    map[string]any `json:"args,omitempty"`
}

// Kinds is the set of session kinds, a later one shadowing an earlier with the same Kind.
type Kinds struct {
	mu   sync.RWMutex
	list []KindSpec
	regs int
}

func builtinKinds() *Kinds {
	return &Kinds{list: []KindSpec{
		{Kind: core.KindWork, Title: "Work", Desc: "straight to the worktree", From: FromPrompt, Pack: "work", Worktrees: WorktreesNow, Gate: "hard",
			Launcher: true, About: "Each repo gets a worktree on the session's branch, from a freshly fetched base, and the agent starts at once."},
		{Kind: core.KindPlan, Title: "Plan", Desc: "docs first, Start work later", From: FromPrompt, Pack: "plan", Worktrees: WorktreesStartWork,
			Gate: "hard", Launcher: true, About: "The agent drafts the plan in PLAN.md, you annotate it, and Start work implements it in these worktrees."},
		{Kind: core.KindReview, Title: "Review PR", Desc: "someone else's change", From: FromPR, Pack: "review", Worktrees: WorktreesPR, Gate: "off",
			Launcher: true, AutoReview: true, About: "The PR's head is checked out read-only from the repo whose remote it's on, and the agent reviews it.",
			Opens: []KindCall{{Command: "explorer.scope", Args: map[string]any{"scope": "changed"}}, {Command: "ship.pr.open"}}},
		{Kind: core.KindJob, Title: "Job", Desc: "runs to done on its own", From: FromPrompt, Pack: "job", Worktrees: WorktreesNow, Gate: "soft",
			PromptFile: "JOB.md", About: "The agent runs the prompt until it's done or blocked, and makes worktrees as it needs them."},
		{Kind: core.KindNewRepo, Title: "New repo", Desc: "start from nothing", From: FromName, Pack: "newrepo", Worktrees: WorktreesInit, Gate: "hard",
			About: "Oriel makes the repo from the template, commits and registers it, and carries on as a plan for it."},
		{Kind: core.KindDirect, Title: "Direct", Desc: "a directory as it is", From: FromDir, Pack: "direct", Worktrees: WorktreesNone, Gate: "off",
			About: "Opens the directory itself, git repo or not: no worktree, no new branch. An agent here edits your real files."},
	}}
}

// Register adds a kind; unregister takes it away again, and the one it shadowed is back.
func (k *Kinds) Register(s KindSpec) (unregister func()) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.regs++
	s.reg = k.regs
	k.list = append(k.list, s)
	return func() {
		k.mu.Lock()
		defer k.mu.Unlock()
		for i := len(k.list) - 1; i >= 0; i-- {
			if k.list[i].reg == s.reg {
				k.list = slices.Delete(k.list, i, i+1)
				return
			}
		}
	}
}

func (k *Kinds) Get(kind core.Kind) (KindSpec, bool) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	for _, s := range slices.Backward(k.list) {
		if s.Kind == kind {
			return s, true
		}
	}
	return KindSpec{}, false
}

// All is the winning spec for each kind, in the order they were first registered.
func (k *Kinds) All() []KindSpec {
	k.mu.RLock()
	defer k.mu.RUnlock()
	var out []KindSpec
	for _, s := range k.list {
		if !slices.ContainsFunc(out, func(o KindSpec) bool { return o.Kind == s.Kind }) {
			for _, w := range slices.Backward(k.list) {
				if w.Kind == s.Kind {
					out = append(out, w)
					break
				}
			}
		}
	}
	return out
}
