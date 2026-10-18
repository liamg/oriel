package core

import "time"

// ItemKind is what a transcript item is.
type ItemKind string

const (
	ItemUser      ItemKind = "user"      // a prompt the agent was given
	ItemAssistant ItemKind = "assistant" // the agent's prose
	ItemThinking  ItemKind = "thinking"
	ItemTool      ItemKind = "tool" // a tool call, and its result once it has one
	ItemSystem    ItemKind = "system"
	ItemError     ItemKind = "error"
)

// DiffLine is one line of a mini-diff: ' ', '+' or '-', and the line numbers it had.
type DiffLine struct {
	Op   string `json:"op"`
	Old  int    `json:"old,omitempty"`
	New  int    `json:"new,omitempty"`
	Text string `json:"text"`
}

// FileDiff is what an edit did to a file.
type FileDiff struct {
	Path    string     `json:"path"`
	Added   int        `json:"added"`
	Removed int        `json:"removed"`
	Lines   []DiffLine `json:"lines,omitempty"`
}

// Item is one block of the agent's transcript: prose, a tool call and its result, an error.
type Item struct {
	ID       string    `json:"id"`
	Kind     ItemKind  `json:"kind"`
	Text     string    `json:"text,omitempty"`
	Tool     string    `json:"tool,omitempty"`
	Input    string    `json:"input,omitempty"`  // a tool's input, JSON
	Title    string    `json:"title,omitempty"`  // a tool call in a line: "Read internal/ratelimit/limiter.go"
	Output   string    `json:"output,omitempty"` // a tool's result, clipped
	IsError  bool      `json:"isError,omitempty"`
	Done     bool      `json:"done,omitempty"`    // a tool call that has its result
	Partial  bool      `json:"partial,omitempty"` // prose still streaming
	Diff     *FileDiff `json:"diff,omitempty"`
	Agent    string    `json:"agent,omitempty"` // the subagent it belongs to; "" for the main loop
	Time     time.Time `json:"time,omitzero"`
	Duration int64     `json:"duration,omitempty"` // milliseconds a tool took
}

// MaxTranscript is how many items a session's state keeps; its log keeps them all.
const MaxTranscript = 2000
