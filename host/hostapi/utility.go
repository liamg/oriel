package hostapi

import (
	"context"
	"encoding/json"
)

// UtilitySpec is a utility agent to start.
type UtilitySpec struct {
	Name     string `json:"name"`               // what it's for ("assistant"): its folder, and who the app's tools are for
	Provider string `json:"provider,omitempty"` // "" for the first installed and signed in
	Model    string `json:"model,omitempty"`    // "" or "small" for the provider's small model, "default" for its default
	Pack     string `json:"pack"`               // what it's told: a Claude skill, Codex's first message
	Prompt   string `json:"prompt"`             // the first turn
	// Allow are the provider's own tools it may use without asking: WebSearch, WebFetch.
	Allow []string `json:"allow,omitempty"`
	// Web lets it reach the web with the provider's own tools for that (agent.WebTooled) without asking.
	Web bool `json:"web,omitempty"`
}

// UtilityEvent is what a utility agent did.
type UtilityEvent struct {
	Type     string          `json:"type"`               // ready, turn-start, message, tool-call, tool-result, turn-end, failure, exited
	ID       string          `json:"id,omitempty"`       // ready: the agent's, for send; a tool call's
	Provider string          `json:"provider,omitempty"` // ready
	Model    string          `json:"model,omitempty"`    // ready
	Text     string          `json:"text,omitempty"`     // a message, a failure, a tool's result
	Tool     string          `json:"tool,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
	Error    bool            `json:"error,omitempty"` // a tool result that failed
}

// UtilityReq is a turn for a utility agent, or (with no Text) the one to interrupt.
type UtilityReq struct {
	ID   string `json:"id"`
	Text string `json:"text,omitempty"`
}

// Utility is a utility agent as the app holds it.
type Utility interface {
	Ready() UtilityEvent         // what the agent said as it started: its id, provider and model
	Events() <-chan UtilityEvent // what it does, until it's gone
	Send(ctx context.Context, text string) error
	Interrupt(ctx context.Context) error
	Close() error
}
