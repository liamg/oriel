package host

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/tour"
)

// Worktrees is when and how a kind of session gets its projects' worktrees.
type Worktrees string

const (
	WorktreesNow       Worktrees = "now"        // on a fresh branch from the fetched base, at once
	WorktreesStartWork Worktrees = "start-work" // at Start work; until then the agent reads the checkouts
	WorktreesPR        Worktrees = "pr"         // the pull request's head, detached and read-only
	WorktreesPinned    Worktrees = "pinned"     // detached at the base, read-only: tours
	WorktreesNone      Worktrees = "none"       // the directory itself (direct), or no repo at all (scratch)
	WorktreesInit      Worktrees = "init"       // a new repo, made in place
)

// From is what a kind of session starts from, beside its prompt: what the launcher asks for.
type From string

const (
	FromPrompt  From = "prompt" // a prompt or a ticket
	FromPR      From = "pr"     // a pull request's URL
	FromName    From = "name"   // a new repo's name, template and parent directory
	FromDir     From = "dir"    // a directory, opened as it is
	FromNothing From = "nothing"
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
	// AutoReview runs an AI review of the change as the session starts (AIReview): a review of a pull request.
	AutoReview bool `json:"autoReview,omitempty"`
	// PromptFile is a file at the workspace root the prompt is written to as the session starts: a job's JOB.md.
	PromptFile string `json:"promptFile,omitempty"`
	// Variants are the sorts of this kind a session can be (a tour's walkthrough, demo…), the first the default; the
	// launcher offers them, and SessionSpec.Variant picks one.
	Variants []Variant `json:"variants,omitempty"`

	reg int // which registration it is
}

// Variant is one sort of a kind of session.
type Variant struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Desc  string `json:"desc,omitempty"`
}

// variant is the variant a spec asks for: its own, else the kind's first; "" for a kind with none.
func (k KindSpec) variant(asked string) (string, error) {
	if len(k.Variants) == 0 {
		if asked != "" {
			return "", fmt.Errorf("a %s session has no variant %q", strings.ToLower(k.Title), asked)
		}
		return "", nil
	}
	if asked == "" {
		return k.Variants[0].ID, nil
	}
	if !slices.ContainsFunc(k.Variants, func(v Variant) bool { return v.ID == asked }) {
		var ids []string
		for _, v := range k.Variants {
			ids = append(ids, v.ID)
		}
		return "", fmt.Errorf("a %s session is one of %s, not %q", strings.ToLower(k.Title), strings.Join(ids, ", "), asked)
	}
	return asked, nil
}

// tourVariants are the kinds of tour.
func tourVariants() []Variant {
	var out []Variant
	for _, k := range tour.Kinds {
		out = append(out, Variant{ID: k.ID, Title: k.Title, Desc: k.Desc})
	}
	return out
}

// Kinds is the set of session kinds, a later one shadowing an earlier with the same Kind.
type Kinds struct {
	mu   sync.RWMutex
	list []KindSpec
	regs int
}

func builtinKinds() *Kinds {
	return &Kinds{list: []KindSpec{
		{Kind: core.KindPlan, Title: "Plan", Desc: "docs first, Start work later", From: FromPrompt, Pack: "plan", Worktrees: WorktreesStartWork,
			Gate: "hard", About: "The agent drafts the plan in PLAN.md, you annotate it, and Start work implements it in these worktrees."},
		{Kind: core.KindWork, Title: "Work", Desc: "straight to the worktree", From: FromPrompt, Pack: "work", Worktrees: WorktreesNow, Gate: "hard",
			NeedsRepo: true, About: "Each repo gets a worktree on the session's branch, from a freshly fetched base, and the agent starts at once."},
		{Kind: core.KindReview, Title: "Review PR", Desc: "someone else's change", From: FromPR, Pack: "review", Worktrees: WorktreesPR, Gate: "off",
			AutoReview: true, About: "The PR's head is checked out read-only from the repo whose remote it's on, and the agent reviews it."},
		{Kind: core.KindTour, Title: "Tour", Desc: "explain how it works", From: FromPrompt, Pack: "tour", Worktrees: WorktreesPinned, Gate: "off",
			Variants: tourVariants(), About: "The agent answers with a guided tour through the code, in a pinned, read-only worktree."},
		{Kind: core.KindJob, Title: "Job", Desc: "runs to done on its own", From: FromPrompt, Pack: "job", Worktrees: WorktreesNow, Gate: "soft",
			PromptFile: "JOB.md", About: "The agent runs the prompt until it's done or blocked, and makes worktrees as it needs them."},
		{Kind: core.KindNewRepo, Title: "New repo", Desc: "start from nothing", From: FromName, Pack: "newrepo", Worktrees: WorktreesInit, Gate: "hard",
			About: "Oriel makes the repo from the template, commits and registers it, and carries on as a plan for it."},
		{Kind: core.KindDirect, Title: "Direct", Desc: "a directory as it is", From: FromDir, Pack: "direct", Worktrees: WorktreesNone, Gate: "off",
			About: "Opens the directory itself, git repo or not: no worktree, no new branch. An agent here edits your real files."},
		{Kind: core.KindScratch, Title: "Scratch", Desc: "notes, no repo", From: FromNothing, Pack: "scratch", Worktrees: WorktreesNone, Gate: "off",
			About: "Notes, the assistant and terminals, with no repo."},
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
