package agentrun

import (
	"encoding/json"

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

// ConfigureReq switches a session's agent to another model or effort, keeping its conversation.
type ConfigureReq struct {
	Session core.SessionID `json:"session"`
	Model   string         `json:"model,omitempty"`  // "" keeps the agent's; a new model runs at Effort, or its default
	Effort  string         `json:"effort,omitempty"` // "" keeps the agent's, or a new model's default
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
	// Outgoing opens a comment for a pull request's author, in the review the user will submit: the agent isn't
	// asked to answer it.
	Outgoing bool `json:"outgoing,omitempty"`
}

// ThreadReq changes a thread's status: resolve, reopen, defer, dismiss.
type ThreadReq struct {
	Session core.SessionID    `json:"session"`
	Thread  core.ThreadID     `json:"thread"`
	Status  core.ThreadStatus `json:"status,omitempty"`
	Urgent  *bool             `json:"urgent,omitempty"`
	Title   string            `json:"title,omitempty"`
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
