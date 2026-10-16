package core

import "context"

// RepoID names a checkout a worker knows about.
type RepoID string

// Repo is a checkout registered on a worker. It is never written to, except in a direct session.
type Repo struct {
	ID         RepoID   `json:"id"`
	Worker     WorkerID `json:"worker,omitempty"`
	Path       string   `json:"path"`
	Remote     string   `json:"remote,omitempty"`
	BaseBranch string   `json:"baseBranch,omitempty"`
}

// SandboxPolicy is where a session's agent runs and what it may reach.
type SandboxPolicy struct {
	Container bool   `json:"container,omitempty"` // run the agent in a container, with the gateway for git and gh
	Network   string `json:"network,omitempty"`   // "ask" (per new host) or "open"
	Gate      string `json:"gate,omitempty"`      // the agent's own gate: ask, light or bypass
	Backend   string `json:"backend,omitempty"`   // the sandbox backend, one of the worker's Sandboxes; "" is its sandbox.backend
}

// SessionSpec is what a new session starts from (sessions.md).
type SessionSpec struct {
	Kind   Kind   `json:"kind"`
	Name   string `json:"name,omitempty"`  // derived from the prompt if empty
	Title  string `json:"title,omitempty"` // the name it shows, when not its id: a job's run, "job: sweep stale deps"
	Prompt string `json:"prompt,omitempty"`
	Ticket string `json:"ticket,omitempty"`
	// Context comes with the prompt to the agent but isn't part of it: the ticket's text, comments and attachments.
	Context  string        `json:"context,omitempty"`
	PR       string        `json:"pr,omitempty"`    // review kind: the PR's URL
	Repos    []RepoID      `json:"repos,omitempty"` // 0..n
	Dir      string        `json:"dir,omitempty"`   // direct kind: the directory itself; new repo: where it goes
	Branch   string        `json:"branch,omitempty"`
	Base     string        `json:"base,omitempty"` // the branch to cut from; each repo's default if empty
	Provider string        `json:"provider,omitempty"`
	Model    string        `json:"model,omitempty"`
	Effort   string        `json:"effort,omitempty"`
	Worker   WorkerID      `json:"worker,omitempty"`
	Sandbox  SandboxPolicy `json:"sandbox,omitzero"`
	Template string        `json:"template,omitempty"` // new repo kind
	Variant  string        `json:"variant,omitempty"`  // which of its kind's variants: a tour's walkthrough, onboarding…
	Job      string        `json:"job,omitempty"`      // the saved job this is a run of
}

// Sessions starts and moves sessions; the worker implements it, and the app reaches it over the wire.
type Sessions interface {
	Start(ctx context.Context, s SessionSpec) (SessionID, error)
	AddRepo(ctx context.Context, id SessionID, r RepoID) error
	NextPR(ctx context.Context, id SessionID) error
	Archive(ctx context.Context, id SessionID) error
	Resume(ctx context.Context, id SessionID) error
}
