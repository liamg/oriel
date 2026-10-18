package host

import (
	"encoding/json"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
)

// StartWorkReq is Start work on a plan, and how; its zero value is the Start work dialog's defaults.
type StartWorkReq struct {
	Session core.SessionID `json:"session"`
	// KeepThreads leaves the plan's threads as they are; otherwise every one but the plan's own goes, the open and
	// deferred ones carried into the work prompt as a note.
	KeepThreads bool `json:"keepThreads,omitempty"`
	// KeepConversation keeps the plan's agent and its context; otherwise a fresh one starts on the work pack.
	KeepConversation bool              `json:"keepConversation,omitempty"`
	Branches         map[string]string `json:"branches,omitempty"` // a project's branch, by its name; its planned one if absent
	Sandbox          *bool             `json:"sandbox,omitempty"`  // nil keeps the session's
	Network          string            `json:"network,omitempty"`  // ask or open; "" keeps the session's
	Model            string            `json:"model,omitempty"`    // a new model runs at Effort, or its default
	Effort           string            `json:"effort,omitempty"`
}

// CommentReq is a comment from the user: on a thread, or opening one (with Anchor, or none for the whole change).
type CommentReq struct {
	Session core.SessionID `json:"session"`
	Thread  core.ThreadID  `json:"thread,omitempty"`
	Anchor  *core.Anchor   `json:"anchor,omitempty"`
	Title   string         `json:"title,omitempty"`
	Body    string         `json:"body"`
	Images  []string       `json:"images,omitempty"`
	Urgent  bool           `json:"urgent,omitempty"` // interrupts the turn
	Btw     bool           `json:"btw,omitempty"`    // a side question, answered by a fork
	// Status opens a new thread in this status rather than open; one that isn't open stays with the user, as a
	// note pinned to its anchor.
	Status   core.ThreadStatus `json:"status,omitempty"`
	Severity string            `json:"severity,omitempty"` // a new thread's rating, as a review finding's (core.Severities)
	Finding  string            `json:"finding,omitempty"`  // the AI review finding a new thread is opened from
}

// AIReviewReq asks for an AI review of a session's change (agents.md, Guardrails: cross-model review).
type AIReviewReq struct {
	Session  core.SessionID `json:"session"`
	Provider string         `json:"provider,omitempty"` // the reviewer's; "" for one other than the author's
	Model    string         `json:"model,omitempty"`    // "" for the provider's default
	// Full reviews the whole change since the base; otherwise a session reviewed before has what changed since.
	Full bool `json:"full,omitempty"`
	// Pack is the reviewer's prompt, when it isn't the built-in ai-review pack: the user's own.
	Pack string `json:"pack,omitempty"`
}

// ThreadReq changes a thread's status: resolve, reopen, defer, dismiss.
type ThreadReq struct {
	Session core.SessionID    `json:"session"`
	Thread  core.ThreadID     `json:"thread"`
	Status  core.ThreadStatus `json:"status,omitempty"`
	Urgent  *bool             `json:"urgent,omitempty"`
}

// AnswerReq answers an ask: allow, always, deny (with a note for the agent), or a question's answers.
type AnswerReq struct {
	Session  core.SessionID    `json:"session"`
	Ask      string            `json:"ask"`
	Decision string            `json:"decision"` // allow, always, deny, answer; recheck for a login
	Note     string            `json:"note,omitempty"`
	Answers  map[string]string `json:"answers,omitempty"`
	Scope    string            `json:"scope,omitempty"` // a network rule's host pattern
}

// AskReq is something the app asks the user for a session, as its agent's own gate would: an agent tool that asks
// first. The session's rules and the policy hooks answer before the user sees a card; "always" adds Ask.Rule.
type AskReq struct {
	Session core.SessionID `json:"session"`
	Ask     core.Ask       `json:"ask"`
}

// SendReq is a message to the agent outside any thread: the console's composer.
type SendReq struct {
	Session core.SessionID `json:"session"`
	Text    string         `json:"text"`
	Images  []string       `json:"images,omitempty"`
	Urgent  bool           `json:"urgent,omitempty"`
}

type TermReq struct {
	Session core.SessionID `json:"session"`
	Data    []byte         `json:"data,omitempty"`
	Cols    int            `json:"cols,omitempty"`
	Rows    int            `json:"rows,omitempty"`
}

type SearchReq struct {
	Query    string `json:"query"`
	Archived bool   `json:"archived,omitempty"` // archived sessions too
}

// Hit is a match in a session's threads or transcript.
type Hit struct {
	Session core.SessionID `json:"session"`
	Thread  core.ThreadID  `json:"thread,omitempty"`
	Item    string         `json:"item,omitempty"`
	Text    string         `json:"text"`
}

// ProviderInfo is a provider as the launcher shows it.
type ProviderInfo struct {
	ID      string             `json:"id"`
	Install agent.Install      `json:"install"`
	Models  []agent.Model      `json:"models,omitempty"`
	Usage   core.ProviderUsage `json:"usage,omitzero"`
	Error   string             `json:"error,omitempty"`
}

// ToolsReq asks the app for the tools it offers an agent: a session's (Agent is its kind), or a utility agent's (no
// Session; Agent is its name).
type ToolsReq struct {
	Session core.SessionID `json:"session,omitempty"`
	Agent   string         `json:"agent,omitempty"`
}

// ToolCall is an agent calling a tool the app offers.
type ToolCall struct {
	Session core.SessionID  `json:"session,omitempty"`
	Agent   string          `json:"agent,omitempty"`
	Tool    string          `json:"tool"`
	Args    json.RawMessage `json:"args"`
}

// Decide asks the app's policy hooks about an ask before it reaches the user (layer 2).
type Decide struct {
	Session core.SessionID `json:"session"`
	Ask     core.Ask       `json:"ask"`
}

// Verdict is what the policy hooks said: allow, deny, or "" to ask the user.
type Verdict struct {
	Decision string `json:"decision,omitempty"`
	Note     string `json:"note,omitempty"`
	By       string `json:"by,omitempty"`
}
