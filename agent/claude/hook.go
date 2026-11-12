package claude

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"github.com/liamg/oriel/sandbox/hookcall"
)

// The mod hands every decision to the session host by running `oriel hook <op>` with JSON on stdin. HookMain is
// that command: it carries one request over the unix socket in ORIEL_HOOK_SOCK and prints the answer.

// Hook ops.
const (
	opHello      = "hello"       // helloIn → helloOut
	opWait       = "wait"        // {} → waitOut, a long poll
	opEvent      = "event"       // eventIn → {}
	opCheck      = "check"       // checkIn → checkOut
	opPermission = "permission"  // permissionIn → permissionOut, once answered
	opQuestion   = "question"    // questionIn → questionOut, once answered
	opTool       = "tool"        // toolIn → toolOut
	opForkAnswer = "fork-answer" // forkAnswerIn → {}
)

type (
	request  = hookcall.Request
	response = hookcall.Response
)

type helloIn struct {
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
}

type helloOut struct {
	Tools []toolSpec `json:"tools"`
}

type toolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// waitOut is what the mod does next.
type waitOut struct {
	Kind   string `json:"kind"` // submit, abort, fork, usage, tools, timeout
	Text   string `json:"text,omitempty"`
	Urgent bool   `json:"urgent,omitempty"`
	ID     string `json:"id,omitempty"`
	Prompt string `json:"prompt,omitempty"`
}

type eventIn struct {
	Kind string `json:"kind"`
	// turn-complete
	TurnID     string           `json:"turnId,omitempty"`
	Reason     string           `json:"reason,omitempty"`
	Answer     string           `json:"answer,omitempty"`
	DurationMs int64            `json:"durationMs,omitempty"`
	Usage      *turnUsage       `json:"usage,omitempty"`
	Context    *ctxUsage        `json:"context,omitempty"`
	Cost       *float64         `json:"cost,omitempty"`
	RateLimits []rateLimit      `json:"rateLimits,omitempty"`
	Background []backgroundTask `json:"background,omitempty"` // what the turn left running: the Stop hook's background_tasks
	// subagent-start, -run, -end
	ID          string `json:"id,omitempty"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type,omitempty"`
	Prompt      string `json:"prompt,omitempty"`
	Text        string `json:"text,omitempty"`
	// api-error; usage: why $.session.usage() failed
	Error string `json:"error,omitempty"`
	// tools-lost: Oriel's MCP servers the CLI has dropped
	Servers []string `json:"servers,omitempty"`
	// mode: the permission mode the session is in now, from a classic hook's permission_mode
	Mode string `json:"mode,omitempty"`
	// dialog: the call the engine's own dialog asks about
	Tool  string          `json:"tool,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

// backgroundTask is one of the Stop hook's background_tasks: a shell, subagent, monitor or workflow still running.
type backgroundTask struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	Command     string `json:"command,omitempty"`
}

// turnUsage is turn.complete's usage: the turn's tokens and its model.
type turnUsage struct {
	Input       int64  `json:"input_tokens"`
	Output      int64  `json:"output_tokens"`
	CacheRead   int64  `json:"cache_read_input_tokens"`
	CacheCreate int64  `json:"cache_creation_input_tokens"`
	Model       string `json:"model"`
}

type ctxUsage struct {
	Tokens int64 `json:"tokens"`
	Window int64 `json:"window"`
}

// rateLimit is one of the plan's windows as $.session.usage() reports it.
type rateLimit struct {
	Kind        string  `json:"kind"`
	PercentUsed float64 `json:"percentUsed"`
	ResetsAt    string  `json:"resetsAt,omitempty"`
}

type checkIn struct {
	Tool    string          `json:"tool"`
	Input   json.RawMessage `json:"input"`
	AgentID string          `json:"agentId,omitempty"`
}

type checkOut struct {
	Decision string `json:"decision"` // allow, deny, ask for Oriel's permission flow, or "" for the mode to decide
	Reason   string `json:"reason,omitempty"`
}

type permissionIn struct {
	ID          string          `json:"id"`
	Tool        string          `json:"tool"`
	Input       json.RawMessage `json:"input"`
	Suggestions json.RawMessage `json:"suggestions,omitempty"`
}

type permissionOut struct {
	Decision string `json:"decision,omitempty"` // allow, deny, or "" for the terminal's dialog
	Message  string `json:"message,omitempty"`
	Always   bool   `json:"always,omitempty"`
}

type questionIn struct {
	ID    string          `json:"id"`
	Input json.RawMessage `json:"input"`
}

type questionOut struct {
	Answers map[string]string `json:"answers,omitempty"`
	Deny    string            `json:"deny,omitempty"`
}

type toolIn struct {
	Name  string          `json:"name"`
	ID    string          `json:"id,omitempty"`
	Input json.RawMessage `json:"input"`
}

type toolOut struct {
	Result  string `json:"result"`
	IsError bool   `json:"isError,omitempty"`
}

type forkAnswerIn struct {
	ID       string `json:"id"`
	Answered bool   `json:"answered"`
	Text     string `json:"text,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// HookMain is `oriel hook <op>` (hookcall.Main).
func HookMain(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return hookcall.Main(args, stdin, stdout, stderr)
}

// sockDir makes a private directory for a session's socket, short enough for sun_path (104 bytes on macOS).
func sockDir() (string, error) {
	base := os.TempDir()
	if len(base) > 60 {
		base = "/tmp"
	}
	return os.MkdirTemp(base, "oriel-")
}

func sockPath(dir string) string { return filepath.Join(dir, "hook.sock") }
