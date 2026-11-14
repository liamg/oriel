// Package usage is what an agent's log says it used, in the terms every provider shares: the Events host/stats counts.
// A provider whose log has a format of its own reads it with a Format of its own (agent.UsageLogs); one with none
// writes Events, the Events format.
package usage

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/liamg/oriel/agent/price"
)

// Event is one thing a log says happened: a turn the user started, a request with its tokens, a tool call or its
// result. One of no kind only moves the log's clock (and gives its directory, Cwd).
type Event struct {
	At     time.Time       `json:"at"`
	Kind   string          `json:"kind"` // turn, place, request, tool, result
	Model  string          `json:"model,omitempty"`
	Effort string          `json:"effort,omitempty"`
	Agent  string          `json:"agent,omitempty"` // a subagent's id
	Window int64           `json:"window,omitempty"`
	Tokens price.Tokens    `json:"tokens,omitzero"`
	Fast   bool            `json:"fast,omitempty"` // a request at the faster, dearer speed
	Msg    string          `json:"msg,omitempty"`  // a request's message: a later request of the same replaces it
	Text   string          `json:"text,omitempty"` // a turn's prompt
	ID     string          `json:"id,omitempty"`
	Name   string          `json:"name,omitempty"`
	Input  json.RawMessage `json:"input,omitempty"`
	Error  bool            `json:"error,omitempty"`
	Cwd    string          `json:"cwd,omitempty"`
}

// The kinds of Event.
const (
	Turn    = "turn"
	Place   = "place" // a file tool call that's coming: where the requests from then on are charged
	Request = "request"
	Tool    = "tool"
	Result  = "result"
)

// Reader reads a log a line at a time, emitting what each line says as Events.
type Reader interface {
	Line(line []byte, emit func(Event))
	// State is what a reader carries from one line to the next, for the next read of the log to start from.
	State() json.RawMessage
}

// Format is a kind of log, by agent.Log's Format: how it's read.
type Format struct {
	Name string
	// Reader starts reading, from a State a reader gave, or nil at the log's start.
	Reader func(state json.RawMessage) Reader
}

// Events is the log of Events, a JSON one a line: Oriel's record of a provider's events, and the format of a
// provider with none of its own.
var Events = Format{Name: "events", Reader: func(json.RawMessage) Reader { return events{} }}

type events struct{}

func (events) Line(line []byte, emit func(Event)) {
	var ev Event
	if json.Unmarshal(line, &ev) == nil {
		emit(ev)
	}
}

func (events) State() json.RawMessage { return nil }

// Prompt says whether a user message's text is something the user said, not what the system wrapped in tags.
func Prompt(text string) bool {
	t := strings.TrimSpace(text)
	return t != "" && !strings.HasPrefix(t, "<")
}

// ModelName is the model id with any deployment suffix cut off: the million-token variant is the same model.
func ModelName(id string) string {
	if id == "" {
		return "unknown"
	}
	if i := strings.IndexByte(id, '['); i > 0 {
		id = id[:i]
	}
	return strings.TrimSpace(id)
}
