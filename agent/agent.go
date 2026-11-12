// Package agent defines the Provider interface and the shared turn and transcript types. Claude and Codex
// implement it in subpackages; the session host and the UI can't tell them apart.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/liamg/oriel/agent/price"
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
}

// Logged is a provider that keeps its own log of a session, which the worker's stats read (worker/stats). The
// worker keeps a record of the events of a provider that doesn't.
type Logged interface {
	Logs(cwd, session string) []Log
}

// Discovered is a provider that can find every session log it keeps on this machine, whoever started the session (a
// terminal, an IDE, Oriel): the worker's stats count those no Oriel session owns as outside Oriel.
type Discovered interface {
	Discover() []Transcript
}

// DiscoverStamped is a Discovered that can say, far more cheaply than Discover, that it may find logs it didn't
// before: its stamp changes when one is added. A list of logs is kept while the stamp holds, for a while.
type DiscoverStamped interface {
	DiscoverStamp() string
}

// Transcript is one session's logs as its provider keeps them.
type Transcript struct {
	Session  string    // the provider's own id for it
	Logs     []Log     // its own log first, then its subagents'
	Modified time.Time // the latest change to any of them
}

// Log is a file a provider writes a session's log to, and its format: "claude" for Claude Code's.
type Log struct {
	Path   string
	Format string
}

// Featured is a provider that says what its sessions can do past what every provider's must. One that isn't has
// none of the Features.
type Featured interface {
	Features() Features
}

// Features are what a provider's sessions can do past Session's least.
type Features struct {
	Terminal bool // Session.Terminal is the agent's own terminal, which the user may watch and type in
	Fork     bool // Fork answers from the session's context with no tools
	Images   bool // Input.Images reach the agent as images, not as their paths
	Sandbox  bool // it runs in a sandbox: it's Sandboxed
	Login    bool // it signs in from Oriel: it's a LoginStarter
	// StopProcesses says its sessions are ProcessStoppers: what a turn left Running can be stopped.
	StopProcesses bool
	// Notes is the file it reads standing instructions from at the root of where it starts ("CLAUDE.md"), which a
	// workspace's notes are written to; AGENTS.md, which most agents read, if "".
	Notes string
}

// FeaturesOf are p's Features.
func FeaturesOf(p Provider) Features {
	if f, ok := p.(Featured); ok {
		return f.Features()
	}
	return Features{}
}

// LoginStarter is a provider that signs in from Oriel: in a browser it opens, with no terminal.
type LoginStarter interface {
	// StartLogin begins a sign-in. Its URL is where the user signs in; Done says how it ended.
	StartLogin(ctx context.Context) (Login, error)
}

// Login is a sign-in under way.
type Login struct {
	URL  string
	Done <-chan error // nil when it succeeded; closed or sent once
}

// Sandboxed is a provider whose agent can run in a sandbox: what it needs there past the workspace, which the
// sandbox gives it wherever the backend puts it (a container, the OS's sandbox).
type Sandboxed interface {
	SandboxNeeds(ctx context.Context, at SandboxPlace) (SandboxNeeds, error)
	MCPInstalled
}

// MCPInstalled is a provider that says which MCP servers the user installed for it, which its agent reaches on this
// machine and a sandbox's gateway carries in their place.
type MCPInstalled interface {
	MCPServers() []MCPServer
}

// MCPServer is an MCP server the user installed for a provider.
type MCPServer struct {
	Name   string
	Source string            // where it was found: "user", "plugin:<name>"
	Config json.RawMessage   // in Claude's mcpServers form: { type, command, args, env, url, headers }
	Vars   map[string]string // what its ${VAR}s stand for, past the environment
}

// SandboxPlace is where a sandbox runs a provider's agent.
type SandboxPlace struct {
	Root     string   // the workspace
	Cwd      string   // where the agent starts
	Dir      string   // the provider's own directory in this sandbox, kept between runs, which the agent may write
	Home     string   // the agent's $HOME, as the agent sees it
	Tmp      string   // the sandbox's temp directory on the host, which the session reads
	OS, Arch string   // what the agent runs on: linux in a container, the host's under the OS's sandbox
	Shared   bool     // the agent shares the host's filesystem layout and network (the OS's sandbox), not a container's
	UID      int      // the user the agent runs as
	StateDir string   // the session's StartSpec.StateDir
	Environ  []string // the host's environment
	Cache    string   // a directory the provider may keep downloads in, shared by every sandbox
	// Report says what a slow step is doing ("Downloading Claude Code 2.1.300"), and why it's slow when that's worth
	// saying: it happens once.
	Report func(step, note string)
	// Env is a host variable as the sandbox passes it on (its env settings): "" for one it doesn't. A provider
	// reads the credentials it's handed outright here (an API key), which come before the host's login.
	Env func(name string) string
}

// SandboxNeeds are what a provider's agent needs in a sandbox. Paths are the host's; a sandbox gives the agent each
// at the same path unless a Mount says where.
type SandboxNeeds struct {
	Bins map[string]string // executables on the agent's PATH by name, each built for SandboxPlace.OS and Arch
	// Tools are what a container installs for it with mise, as the projects' toolchains are, and puts on its PATH
	// after theirs: "node@22", "npm:@google/gemini-cli@0.9.0". The OS's sandbox runs the host's.
	Tools    []string
	Mounts   []Mount           // host paths the agent sees (Sandbox the path it sees each at; Host's own if "")
	Env      map[string]string // set for the agent
	PassEnv  []string          // host variables it inherits by default, as names or NAME_* globs: OPENAI_*
	SkipEnv  []string          // of those, ones it never inherits by default: a parent session's own
	URLEnv   []string          // of its variables, the ones holding a URL whose localhost means the host
	URLs     []string          // endpoints it reaches that its environment doesn't say (its own settings file)
	Hosts    []string          // what it reaches on the network, allowed by default: api.openai.com, *.chatgpt.com
	Problems []SandboxProblem  // what stops this one; a blocking one refuses the launch
}

// SandboxProblem is something stopping a provider's sandboxed agents, with what fixes it.
type SandboxProblem struct {
	Code, Message, Fix string
	Blocking           bool
	SignIn             []string // a blocking one that signing in on the host fixes: the HostCommand that does
}

// Install is what Detect found.
type Install struct {
	Bin      string `json:"bin"`
	Version  string `json:"version"`
	LoggedIn bool   `json:"loggedIn"`
	// Login marks the sign-in found, changing when it's renewed (a hash of its token); "" when it can't be told.
	Login   string `json:"-"`
	Account string `json:"account,omitempty"` // "chatgpt", "apiKey", the plan's name
	Note    string `json:"note,omitempty"`    // why it can't run, or what to know first
}

// Model is a model the provider offers, with its efforts.
type Model struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Efforts []string `json:"efforts,omitempty"`
	Context int      `json:"context,omitempty"` // tokens
	Default bool     `json:"default,omitempty"`
	// Small is a quick, cheap model for utility agents, such as the resident utility session.
	Small bool `json:"small,omitempty"`
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
	Pack     Pack          // the session kind's prompt pack
	Prompt   string        // the first thing to say, after the pack
	Tools    []Tool        // offered to the agent as mcp__oriel__*
	ToolsNow func() []Tool // the tools offered now, which change as plugins load; nil: Tools
	Policy   Policy        // the agent's own gate
	Mode     string        // the permission mode a resumed conversation was last in (ModeChanged): over Policy where it can start in it
	Protect  []string      // directories the agent must never edit: the user's checkouts
	Env      []string      // extra environment, KEY=value
	Exec     Exec          // how to run a process: on the host, or in the sandbox
	Sandbox  *Sandbox
	StateDir string // where the provider may keep files for this session
	Memory   string // the agent's memory directory, "" for the provider's own
	NoMCP    bool   // start without the user's MCP servers
	// NoWeb turns off the agent's own web tools that ask nobody (a search its model's host runs); those its gate
	// asks about are the caller's to answer.
	NoWeb bool
	// Prices are what its turns' tokens are priced with; the vendored snapshot if nil.
	Prices price.Books
	// Unattended is an agent nobody watches: a permission or question nobody answers soon is refused with a reason,
	// and its own terminal never opens a dialog to wait on someone at it.
	Unattended bool
}

// Offered are the tools offered to the agent now.
func (s StartSpec) Offered() []Tool {
	if s.ToolsNow != nil {
		return s.ToolsNow()
	}
	return s.Tools
}

// ToolNames are tools' names, in order.
func ToolNames(tools []Tool) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name
	}
	return out
}

// UnattendedRefusal is what an unattended agent is told of an ask nobody answered.
const UnattendedRefusal = "Nobody is here to answer this: carry on without it, or say in your reply what the user should do."

// Policy is how much the agent's own gate asks.
type Policy string

const (
	PolicyAsk    Policy = "ask"    // every tool the agent would ask about
	PolicyLight  Policy = "light"  // edits go ahead, shell commands ask
	PolicyBypass Policy = "bypass" // nothing asks: only inside a sandbox
	PolicyAuto   Policy = "auto"   // the agent's own judgement asks only of what's risky (Claude's auto mode); light elsewhere
)

// HookSocketEnv names the unix socket a provider's agent calls back to it over (hooks, its MCP tools). A sandbox
// carries that socket into the container and points this at its end there.
const HookSocketEnv = "ORIEL_HOOK_SOCK"

// Sandbox is the container a session's agent runs in, and the gateway it reaches the host through.
type Sandbox struct {
	Name       string
	GatewayMCP string // an MCP config file naming the gateway server
	// HookSocket is where the agent finds HookSocketEnv's socket inside: a provider that names it in a config the
	// agent reads (an MCP server's env) names this, and puts its own in the process's environment, which the
	// sandbox carries in. oriel is on the agent's PATH.
	HookSocket string
}

// Pack is a prompt pack: what a kind of session tells its agent (work, plan, review, job).
type Pack struct {
	Name  string // "plan"
	Skill string // the skill a Claude session starts with: /oriel:<Skill>
	Text  string // the pack's instructions, markdown
	Tools []string
}

// Tool is a tool Oriel offers the agent. Call runs on the session host.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"inputSchema"`
	ReadOnly    bool            `json:"readOnly,omitempty"`
	// CoveredBy are MCP servers that already do what it does, each by its name or part of where it runs (an
	// address: mcp.linear.app). An agent that reaches one isn't offered the tool.
	CoveredBy []string                                                        `json:"coveredBy,omitempty"`
	Call      func(ctx context.Context, args json.RawMessage) (string, error) `json:"-"`
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

// Responsive is a session that can say whether it still hears from its agent: one that has gone quiet is restarted
// when the user resumes it.
type Responsive interface {
	Responsive() bool
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
	// Ready says the session is up, with the provider's id for it, and the model it runs when it chose one itself.
	Ready struct{ ID, Model string }
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
		Model    string // the model that made the call, when the provider says: "claude-opus-5-5"
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
		// Background is the work the turn left running that will wake the agent again (shells, subagents), by what
		// each is; while there is some, the work isn't done.
		Background []core.BackgroundTask
		// Running is what the turn left running that won't wake the agent: processes the user may want to stop (a
		// dev server, a watcher), by ID when the session is a ProcessStopper.
		Running []core.BackgroundTask
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
		Shell    string // run in a shell, signs in again: "claude, then /login"
		// HostCommand runs on the host, in a terminal, and signs it in again, and so every sandbox sharing its
		// sign-in: ["codex", "login"]. A sandboxed agent signs in only this way, never in its own terminal.
		HostCommand []string
	}
	// AuthRestored is a login seen to succeed, so whoever waits on one checks again.
	AuthRestored struct{ Provider, Account string }
	// Exited is the provider process gone.
	Exited struct{ Err string }
	// Dialog is the agent's terminal showing a dialog no hook carries, so only someone at the terminal answers it:
	// Title is what it asks. Shown is false once it's answered.
	Dialog struct {
		Title string
		Shown bool
	}
	// Stale is a session that has lost what only a restart brings back (an MCP server its CLI dropped): it's
	// restarted, its conversation resumed, once its turn ends.
	Stale struct{ Why string }
	// ModeChanged is the agent's own permission mode, in its provider's terms, now something else: the user or the
	// agent switched it (Claude's Shift+Tab, its plan mode's exit). A restart resumes the conversation in it.
	ModeChanged struct{ Mode string }
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
func (Stale) isEvent()        {}
func (ModeChanged) isEvent()  {}
func (Dialog) isEvent()       {}
func (AuthExpired) isEvent()  {}
func (AuthRestored) isEvent() {}

func (e AuthExpired) Error() string {
	if e.Detail != "" {
		return e.Detail
	}
	return e.Provider + " is signed out"
}

// UsageProbeFailed is an error from Usage whose probe read no windows. Started says the agent came up and was asked;
// not started, it never got that far.
type UsageProbeFailed interface {
	error
	Provider() string
	Started() bool
}

// ErrNoFork is returned by Fork when there is nothing to fork from yet.
var ErrNoFork = errors.New("the agent has said nothing yet: there is no context to answer from")

// Covered says one of servers is one of covers: named so, or running at or from it (its config holds the cover: its
// URL, or a command line with the address in it).
func Covered(servers []MCPServer, covers []string) bool {
	for _, c := range covers {
		if c == "" {
			continue
		}
		for _, m := range servers {
			if m.Name == c || strings.Contains(string(m.Config), c) {
				return true
			}
		}
	}
	return false
}
