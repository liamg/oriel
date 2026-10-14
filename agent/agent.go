// Package agent defines the Provider interface and the shared turn and transcript types. Claude and Codex
// implement it in subpackages; the session host and the UI can't tell them apart (agents.md).
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"time"

	"github.com/liamg/oriel/core"
)

// Provider is one kind of agent: Claude, Codex, or a plugin's.
type Provider interface {
	ID() string
	Detect(ctx context.Context) (Install, error)
	Models(ctx context.Context) ([]Model, error)
	Start(ctx context.Context, s StartSpec) (Session, error)
	Resume(ctx context.Context, id string, s StartSpec) (Session, error)
	Usage(ctx context.Context) (core.ProviderUsage, error)
	SandboxCredentials() []Mount
}

// Logged is a provider that keeps its own log of a session, which the worker's stats read (worker/stats). The
// worker keeps a record of the events of a provider that doesn't.
type Logged interface {
	Logs(cwd, session string) []Log
}

// Log is a file a provider writes a session's log to, and its format: "claude" for Claude Code's.
type Log struct {
	Path   string
	Format string
}

// Install is what Detect found.
type Install struct {
	Bin      string `json:"bin"`
	Version  string `json:"version"`
	LoggedIn bool   `json:"loggedIn"`
	Account  string `json:"account,omitempty"` // "chatgpt", "apiKey", the plan's name
	Note     string `json:"note,omitempty"`    // why it can't run, or what to know first
}

// Model is a model the provider offers, with its efforts.
type Model struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Efforts []string `json:"efforts,omitempty"`
	Context int      `json:"context,omitempty"` // tokens
	Default bool     `json:"default,omitempty"`
}

// Mount is a file a sandboxed agent needs from the host: its credentials, stripped to what it uses.
type Mount struct {
	Host, Sandbox string
	ReadOnly      bool
}

// StartSpec is everything a provider needs to start a session.
type StartSpec struct {
	Cwd      string
	Model    string
	Effort   string
	Pack     Pack     // the session kind's prompt pack
	Prompt   string   // the first thing to say, after the pack
	Tools    []Tool   // offered to the agent as mcp__oriel__*
	Policy   Policy   // the agent's own gate
	Protect  []string // directories the agent must never edit: the user's checkouts
	Env      []string // extra environment, KEY=value
	Exec     Exec     // how to run a process: on the host, or in the sandbox
	Sandbox  *Sandbox
	StateDir string // where the provider may keep files for this session
}

// Policy is how much the agent's own gate asks (agents.md → Permissions).
type Policy string

const (
	PolicyAsk    Policy = "ask"    // every tool the agent would ask about
	PolicyLight  Policy = "light"  // edits go ahead, shell commands ask
	PolicyBypass Policy = "bypass" // nothing asks: only inside a sandbox
)

// HookSocketEnv names the unix socket a provider's agent calls back to it over (hooks, its MCP tools). A sandbox
// carries that socket into the container and points this at its end there.
const HookSocketEnv = "ORIEL_HOOK_SOCK"

// Sandbox is the container a session's agent runs in, and the gateway it reaches the host through.
type Sandbox struct {
	Name       string
	GatewayMCP string // an MCP config file naming the gateway server
}

// Pack is a prompt pack: what a kind of session tells its agent (plan, work, review, tour, job).
type Pack struct {
	Name  string // "plan"
	Skill string // the skill a Claude session starts with: /oriel:<Skill>
	Text  string // the pack's instructions, markdown
	Tools []string
}

// Tool is a tool Oriel offers the agent. Call runs on the session host.
type Tool struct {
	Name        string                                                          `json:"name"`
	Description string                                                          `json:"description"`
	Schema      json.RawMessage                                                 `json:"inputSchema"`
	ReadOnly    bool                                                            `json:"readOnly,omitempty"`
	Call        func(ctx context.Context, args json.RawMessage) (string, error) `json:"-"`
}

// Exec starts processes for a provider: on the host by default, in a sandbox when there is one.
type Exec interface {
	StartPTY(ctx context.Context, cmd *exec.Cmd, cols, rows int) (PTY, error)
	Start(ctx context.Context, cmd *exec.Cmd) (Process, error)
}

// PTY is an agent's terminal: the raw view of a Claude session.
type PTY interface {
	io.ReadWriteCloser
	Resize(cols, rows int) error
	Wait() error
}

// Process is a provider process with pipes: Codex's app-server.
type Process interface {
	Stdin() io.WriteCloser
	Stdout() io.ReadCloser
	Wait() error
	Kill() error
}

// Session is one running conversation.
type Session interface {
	ID() string // the provider's own id, to resume by
	Send(ctx context.Context, in Input) error
	Interrupt(ctx context.Context) error
	Fork(ctx context.Context, f ForkSpec) (string, error) // a tool-less side question; the answer
	Events() <-chan Event
	Answer(ctx context.Context, id string, d Decision) error // a Permission or Question
	Terminal() Terminal                                      // the raw terminal, nil when there is none
	Close() error
}

// Terminal is a session's raw terminal, which any number of views may watch.
type Terminal interface {
	Write(p []byte) (int, error)
	Resize(cols, rows int) error
	Attach() (backlog []byte, live <-chan []byte, detach func()) // what it has shown, then what it shows
}

// Input is a user turn.
type Input struct {
	Text   string
	Images []string // paths
	Urgent bool     // in front of whatever is running
}

// ForkSpec is a side question answered from the session's context, outside the main conversation.
type ForkSpec struct {
	Prompt string
}

// Decision answers a Permission or a Question.
type Decision struct {
	Allow   bool
	Always  bool              // and make a rule of it
	Note    string            // a denial's reason, read by the agent
	Answers map[string]string // a Question's answers, by question
}

// Event is one of the types below.
type Event interface{ isEvent() }

type (
	// Ready says the session is up, with the provider's id for it.
	Ready struct{ ID string }
	// TurnStart begins a turn.
	TurnStart struct{}
	// Message is the agent's prose, or the user's as the agent got it. Partial while it streams.
	Message struct {
		ID      string
		Role    core.ItemKind // assistant, user, thinking
		Text    string
		Partial bool
		Agent   string // a subagent's id
	}
	// ToolCall is the agent calling a tool.
	ToolCall struct {
		ID, Tool string
		Input    json.RawMessage
		Agent    string
	}
	// ToolResult is a tool call's outcome.
	ToolResult struct {
		ID      string
		Output  string
		IsError bool
		Diff    *core.FileDiff
	}
	// Permission is the agent's own gate asking (layer 1): it waits for Answer.
	Permission struct {
		ID, Tool, Summary string
		Input             json.RawMessage
		Rule              string // what "Always" would allow
	}
	// Question is the agent asking the user something: it waits for Answer.
	Question struct {
		ID        string
		Questions []core.Question
	}
	// Subagent is a helper the agent started, ran or finished.
	Subagent struct {
		ID, Description, Type, Prompt string
		Done                          bool
		Result                        string
	}
	// TurnEnd ends a turn.
	TurnEnd struct {
		Usage Usage
	}
	// Failure is a turn that failed after the provider's own retries.
	Failure struct {
		Err       string
		Retryable bool
	}
	// UsageUpdate is the plan's windows as the provider last said.
	UsageUpdate struct{ Usage core.ProviderUsage }
	// AuthExpired is the provider's login on this machine no longer working: every session on it waits for a new
	// one. Start returns it as an error when a session can't start for it.
	AuthExpired struct {
		Provider string
		Account  string // ProviderUsage.Account, when known
		Detail   string // what the provider said: "OAuth token revoked · Please run /login"
		Terminal string // typed into the session's terminal, signs in again: "/login"; "" when that can't
		Shell    string // run in a shell on the agent's machine, signs in again: "claude, then /login"
	}
	// AuthRestored is a login seen to succeed, so whoever waits on one checks again.
	AuthRestored struct{ Provider, Account string }
	// Exited is the provider process gone.
	Exited struct{ Err string }
)

// Usage is what a turn used.
type Usage struct {
	Input, Output, CacheRead, CacheWrite int64
	Context, ContextMax                  int
	Cost                                 float64
	Duration                             time.Duration
}

func (Ready) isEvent()        {}
func (TurnStart) isEvent()    {}
func (Message) isEvent()      {}
func (ToolCall) isEvent()     {}
func (ToolResult) isEvent()   {}
func (Permission) isEvent()   {}
func (Question) isEvent()     {}
func (Subagent) isEvent()     {}
func (TurnEnd) isEvent()      {}
func (Failure) isEvent()      {}
func (UsageUpdate) isEvent()  {}
func (Exited) isEvent()       {}
func (AuthExpired) isEvent()  {}
func (AuthRestored) isEvent() {}

func (e AuthExpired) Error() string {
	if e.Detail != "" {
		return e.Detail
	}
	return e.Provider + " is signed out"
}

// ErrNoFork is returned by Fork when there is nothing to fork from yet.
var ErrNoFork = errors.New("the agent has said nothing yet: there is no context to answer from")
