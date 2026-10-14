package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"

	"github.com/liamg/oriel/agent"
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

type request struct {
	Op   string          `json:"op"`
	Body json.RawMessage `json:"body,omitempty"`
}

type response struct {
	Body  json.RawMessage `json:"body,omitempty"`
	Error string          `json:"error,omitempty"`
}

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
	Kind   string `json:"kind"` // submit, abort, fork, timeout
	Text   string `json:"text,omitempty"`
	Urgent bool   `json:"urgent,omitempty"`
	ID     string `json:"id,omitempty"`
	Prompt string `json:"prompt,omitempty"`
}

type eventIn struct {
	Kind string `json:"kind"`
	// turn-complete
	TurnID     string      `json:"turnId,omitempty"`
	Reason     string      `json:"reason,omitempty"`
	Answer     string      `json:"answer,omitempty"`
	DurationMs int64       `json:"durationMs,omitempty"`
	Usage      *turnUsage  `json:"usage,omitempty"`
	Context    *ctxUsage   `json:"context,omitempty"`
	Cost       *float64    `json:"cost,omitempty"`
	RateLimits []rateLimit `json:"rateLimits,omitempty"`
	// subagent-start, -run, -end
	ID          string `json:"id,omitempty"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type,omitempty"`
	Prompt      string `json:"prompt,omitempty"`
	Text        string `json:"text,omitempty"`
	// api-error
	Error string `json:"error,omitempty"`
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
	Decision string `json:"decision"` // allow, deny, or "" for the mode to decide
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

// HookMain is `oriel hook <op>`: it sends stdin's JSON to the session in ORIEL_HOOK_SOCK as op, and prints the
// answer. It exits 0 with the answer, 1 when the session couldn't answer, 2 on bad usage.
func HookMain(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: oriel hook <op>  (run by Oriel's Claude Code mod, with JSON on stdin)")
		return 2
	}
	sock := os.Getenv(agent.HookSocketEnv)
	if sock == "" {
		fmt.Fprintln(stderr, "oriel hook: ORIEL_HOOK_SOCK is not set: this runs inside a session Oriel started")
		return 1
	}
	body, err := io.ReadAll(stdin)
	if err != nil {
		fmt.Fprintln(stderr, "oriel hook:", err)
		return 1
	}
	out, err := callHook(sock, args[0], body)
	if err != nil {
		fmt.Fprintln(stderr, "oriel hook:", err)
		return 1
	}
	if len(out) == 0 {
		out = []byte("{}")
	}
	_, _ = stdout.Write(append(out, '\n'))
	return 0
}

// callHook sends one request and waits for its answer. The connection stays open while it waits: the host reads
// its closing as this process gone.
func callHook(sock, op string, body []byte) (json.RawMessage, error) {
	req := request{Op: op}
	if len(body) > 0 {
		if !json.Valid(body) {
			return nil, fmt.Errorf("stdin is not JSON")
		}
		req.Body = body
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return nil, err
	}
	var res response
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&res); err != nil {
		return nil, fmt.Errorf("the session closed without answering: %w", err)
	}
	if res.Error != "" {
		return nil, fmt.Errorf("%s", res.Error)
	}
	return res.Body, nil
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
