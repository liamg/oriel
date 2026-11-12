package claude

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liamg/oriel/agent/claude/transcript"
)

// The test binary is also `oriel hook` (for the real claude's mod and the fake's) and, with ORIEL_FAKE_CLAUDE=1,
// a fake claude: the mod's side of the hook protocol in Go, writing a log in Claude Code's format.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "hook" {
		os.Exit(HookMain(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
	}
	if os.Getenv("ORIEL_FAKE_CLAUDE") == "1" {
		os.Exit(fakeClaude(os.Args[1:]))
	}
	os.Exit(m.Run())
}

var (
	//go:embed testdata/oauth-revoked.jsonl
	recordedRevoked []byte
	//go:embed testdata/signed-out-start.pty
	recordedSignedOut []byte
	//go:embed testdata/external-imports.pty
	recordedImports []byte
)

type fake struct {
	id      string
	mode    string
	log     string
	mu      sync.Mutex
	prompts []string // what the user said, oldest first
	replied bool     // the main loop has answered something: there's something to fork
	usage   bool     // /usage has been typed: the plan's windows are known
	cancel  context.CancelFunc
	seq     int
	typed   chan string // lines typed into its terminal, for a dialog on it to read
}

func fakeClaude(args []string) int {
	f := &fake{typed: make(chan string, 8)}
	var plugin, resume, first string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--plugin-dir":
			i++
			plugin = args[i]
		case "--session-id":
			i++
			f.id = args[i]
		case "--resume":
			i++
			resume = args[i]
		case "--permission-mode":
			i++
			f.mode = args[i]
		case "--model", "--effort", "--mcp-config", "--settings":
			i++
		case "--strict-mcp-config", "--":
		default:
			if strings.HasPrefix(a, "--") {
				fmt.Fprintln(os.Stderr, "fake claude: unknown flag", a)
				return 2
			}
			first = a
		}
	}
	for _, need := range []string{".claude-plugin/plugin.json", "hooks/hooks.json", "hooks/register.ts"} {
		if _, err := os.Stat(filepath.Join(plugin, need)); err != nil {
			fmt.Fprintln(os.Stderr, "fake claude: the mod is missing", need)
			return 2
		}
	}
	if os.Getenv("ORIEL_SPAWNED") != "1" || os.Getenv("CLAUDE_CODE_ENABLE_FUNCTION_HOOKS") != "1" {
		fmt.Fprintln(os.Stderr, "fake claude: not spawned by Oriel with function hooks")
		return 2
	}
	if resume != "" {
		f.id = resume
	}
	cwd, _ := os.Getwd()
	f.log = transcript.Path(transcript.ConfigDir(nil), cwd, f.id)
	if resume != "" {
		r := &transcript.Reader{Path: f.log}
		for _, e := range r.Read() {
			switch {
			case e.Kind == transcript.Text && e.Role == "user":
				f.prompts = append(f.prompts, e.Text)
			case e.Kind == transcript.Text && e.Role == "assistant":
				f.replied = true
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(f.log), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	// Debugging, Claude Code logs into CLAUDE_CODE_DEBUG_LOGS_DIR, under an id of its own, and links the log as latest.
	if dir := os.Getenv("CLAUDE_CODE_DEBUG_LOGS_DIR"); dir != "" && os.Getenv("DEBUG") == "1" {
		_ = os.MkdirAll(dir, 0o755)
		log := filepath.Join(dir, "debug-"+f.id+".txt")
		_ = os.WriteFile(log, []byte("[DEBUG] fake claude started\n"), 0o644)
		_ = os.Symlink(log, filepath.Join(dir, "latest"))
	}
	fmt.Printf("fake claude %s in %s\r\n", f.id, cwd)
	if os.Getenv("ORIEL_FAKE_NO_HELLO") == "1" {
		note("run")
		_, _ = io.Copy(io.Discard, os.Stdin) // until the terminal closes
		return 0
	}

	var hello struct{ Tools []toolSpec }
	if err := f.hook("hello", map[string]any{"sessionId": f.id, "cwd": cwd}, &hello); err != nil {
		fmt.Fprintln(os.Stderr, "fake claude: hello:", err)
		return 1
	}
	turns := make(chan string, 16)
	if first != "" {
		turns <- first
	}
	go f.poll(turns)
	go f.terminal()
	for text := range turns {
		f.turn(text)
	}
	return 0
}

// hook runs `$ORIEL_BIN hook op`, as the mod does.
func (f *fake) hook(op string, body, out any) error {
	in, _ := json.Marshal(body)
	cmd := exec.Command(os.Getenv("ORIEL_BIN"), "hook", op)
	cmd.Stdin = bytes.NewReader(in)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	b, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("%w: %s", err, stderr.String())
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(b, out)
}

func (f *fake) poll(turns chan<- string) {
	for {
		var w waitOut
		if err := f.hook("wait", struct{}{}, &w); err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		switch w.Kind {
		case "submit":
			if w.Urgent {
				f.abort()
			}
			turns <- w.Text
		case "abort":
			f.abort()
		case "usage":
			f.mu.Lock()
			read := f.usage
			f.mu.Unlock()
			rls := []any{}
			if read {
				rls = fakeRateLimits
			}
			fails := os.Getenv("ORIEL_FAKE_USAGE_FAILS")
			if fails != "" {
				rls = []any{}
			}
			if read && os.Getenv("ORIEL_FAKE_USAGE_ODD") == "1" { // a shape Oriel doesn't read: resetsAt in seconds
				rls = []any{map[string]any{"kind": "five_hour", "percentUsed": 12.5, "resetsAt": 1760464800}}
			}
			go func() { _ = f.hook("event", map[string]any{"kind": "usage", "rateLimits": rls, "error": fails}, nil) }()
		case "fork":
			go func() {
				f.mu.Lock()
				replied := f.replied
				f.mu.Unlock()
				a := forkAnswerIn{ID: w.ID, Answered: true, Text: "fork: " + w.Prompt}
				if !replied {
					a.Text = "complete: " + w.Prompt
				}
				_ = f.hook("fork-answer", a, nil)
			}()
		}
	}
}

// terminal answers what's typed into it: /login signs in, as claude's does once you're through its steps; /usage
// reads the plan's windows; /exit quits. ORIEL_FAKE_RUNS gets a line as it starts, as /usage is typed and as it quits;
// with ORIEL_FAKE_SIGNED_OUT=1 it starts on the signed-out screen, and with ORIEL_FAKE_IMPORTS=1 on the external
// imports dialog. ORIEL_FAKE_DIALOG starts it under a dialog that
// swallows what's typed.
func (f *fake) terminal() {
	note("run")
	if os.Getenv("ORIEL_FAKE_SIGNED_OUT") == "1" {
		os.Stdout.Write(recordedSignedOut)
	}
	if os.Getenv("ORIEL_FAKE_IMPORTS") == "1" {
		os.Stdout.Write(recordedImports)
	}
	dialog := os.Getenv("ORIEL_FAKE_DIALOG")
	if dialog != "" {
		os.Stdout.WriteString("\x1b[1mNew MCP server found in .mcp.json\x1b[0m\r\n")
	}
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		select {
		case f.typed <- sc.Text():
		default:
		}
		if strings.Trim(sc.Text(), " \x1b") == "/usage" {
			note("usage")
		}
		if dialog != "" {
			continue
		}
		switch strings.Trim(sc.Text(), " \x1b") {
		case "/login":
			os.Stdout.WriteString("\x1b[38;2;78;186;101mLogin successful. Press \x1b[1mEnter\x1b[22m to continue…\x1b[39m\r\n")
		case "/usage":
			f.mu.Lock()
			f.usage = true
			f.mu.Unlock()
		case "/exit":
			note("quit")
			os.Exit(0)
		}
	}
}

// fakeRateLimits are the plan's windows as the fake's $.session.usage() has them.
var fakeRateLimits = []any{map[string]any{"kind": "five_hour", "percentUsed": 12.5, "resetsAt": "2026-10-14T18:00:00Z"}}

func note(what string) {
	if path := os.Getenv("ORIEL_FAKE_RUNS"); path != "" {
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintln(f, what)
			f.Close()
		}
	}
}

func (f *fake) abort() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cancel != nil {
		f.cancel()
	}
}

func (f *fake) write(rec map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	rec["uuid"] = "u" + strconv.Itoa(f.seq)
	rec["sessionId"] = f.id
	rec["timestamp"] = time.Now().UTC().Format(time.RFC3339Nano)
	b, _ := json.Marshal(rec)
	file, err := os.OpenFile(f.log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = file.Write(append(b, '\n'))
}

var fakeUsage = map[string]any{"input_tokens": 10, "output_tokens": 5, "cache_read_input_tokens": 100, "cache_creation_input_tokens": 20}

func (f *fake) assistant(blocks ...map[string]any) {
	f.mu.Lock()
	msg := "msg_" + strconv.Itoa(f.seq)
	f.mu.Unlock()
	for _, b := range blocks {
		f.write(map[string]any{"type": "assistant", "message": map[string]any{
			"id": msg, "role": "assistant", "model": "claude-opus-5", "content": []any{b}, "usage": fakeUsage,
		}})
	}
}

func (f *fake) say(text string) string {
	f.assistant(map[string]any{"type": "text", "text": text})
	return text
}

func (f *fake) use(id, tool string, input any) {
	f.assistant(map[string]any{"type": "tool_use", "id": id, "name": tool, "input": input})
}

func (f *fake) result(id, text string, isError bool) {
	f.write(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "tool_result", "tool_use_id": id, "content": text, "is_error": isError},
	}}})
}

func (f *fake) turn(text string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.mu.Lock()
	f.cancel = cancel
	before := append([]string(nil), f.prompts...)
	f.prompts = append(f.prompts, text)
	f.seq++
	turnID := "turn-" + strconv.Itoa(f.seq)
	f.mu.Unlock()
	f.write(map[string]any{"type": "user", "origin": map[string]any{"kind": "plugin", "name": "oriel", "asUser": true},
		"message": map[string]any{"role": "user", "content": text}})
	_ = f.hook("event", map[string]any{"kind": "turn-start", "turnId": turnID}, nil)

	reason, answer := "answer", ""
	var background []any
	switch text {
	case "say hello":
		answer = f.say("hello")
	case "call echo":
		f.use("toolu_echo", "mcp__oriel__echo", map[string]any{"text": "hi"})
		c := f.check("mcp__oriel__echo", json.RawMessage(`{"text":"hi"}`))
		if c.Decision != "allow" {
			answer = f.say("the echo call was not allowed: " + c.Reason)
			break
		}
		var out toolOut
		if err := f.hook("tool", toolIn{Name: "echo", ID: "toolu_echo", Input: json.RawMessage(`{"text":"hi"}`)}, &out); err != nil {
			out = toolOut{Result: err.Error(), IsError: true}
		}
		f.result("toolu_echo", out.Result, out.IsError)
		answer = f.say("the tool said " + out.Result)
	case "run a command":
		input := json.RawMessage(`{"command":"go test ./...","description":"Run the tests"}`)
		f.use("toolu_bash", "Bash", input)
		var c checkOut
		_ = f.hook("check", checkIn{Tool: "Bash", Input: input}, &c)
		decision, message := c.Decision, c.Reason
		if decision == "" && f.mode == "default" {
			var p permissionOut
			_ = f.hook("permission", permissionIn{ID: "perm-" + turnID, Tool: "Bash", Input: input,
				Suggestions: json.RawMessage(`[{"type":"addRules","rules":[{"toolName":"Bash","ruleContent":"go test:*"}],"behavior":"allow","destination":"localSettings"}]`)}, &p)
			decision, message = p.Decision, p.Message
		}
		if decision == "deny" {
			f.result("toolu_bash", message, true)
			answer = f.say("denied: " + message)
		} else {
			f.result("toolu_bash", "ok", false)
			answer = f.say("ran: ok")
		}
	case "call a connector":
		answer = f.connector(turnID)
	case "call a connector, its dialog told first":
		_ = f.hook("event", map[string]any{"kind": "dialog", "tool": fakeConnector, "input": json.RawMessage(`{"items":["topic.index"]}`)}, nil)
		answer = f.connector(turnID)
	case "call a connector past the org's plugin":
		answer = f.skippedConnector()
	case "ask me":
		qs := map[string]any{"questions": []any{map[string]any{"question": "Which colour?", "header": "Colour", "multiSelect": false,
			"options": []any{map[string]any{"label": "red", "description": "warm"}, map[string]any{"label": "blue", "description": "cool"}}}}}
		f.use("toolu_ask", "AskUserQuestion", qs)
		in, _ := json.Marshal(qs)
		var q questionOut
		_ = f.hook("question", questionIn{ID: "toolu_ask", Input: in}, &q)
		if q.Deny != "" {
			f.result("toolu_ask", q.Deny, true)
			answer = f.say("no answer: " + q.Deny)
			break
		}
		f.result("toolu_ask", "User has answered: "+q.Answers["Which colour?"], false)
		answer = f.say("you chose " + q.Answers["Which colour?"])
	case "lose the login":
		var rec map[string]any
		_ = json.Unmarshal(recordedRevoked, &rec)
		f.write(rec)
	case "show the login prompt":
		os.Stdout.Write(recordedSignedOut)
		answer = f.say("shown")
	case "work slowly":
		<-ctx.Done()
		reason = "aborted"
	case "test in the background":
		answer = f.say("the suite is running")
		background = []any{map[string]any{"id": "b1", "type": "shell", "status": "running", "description": "Run the suite", "command": "go test ./..."}}
	case "switch to auto": // Shift+Tab: the mod's Stop hook sees the mode change and says so
		f.mode = "auto"
		_ = f.hook("event", map[string]any{"kind": "mode", "mode": f.mode}, nil)
		answer = f.say("switched")
	case "what mode":
		answer = f.say("mode " + f.mode)
	case "what did I say":
		if len(before) == 0 {
			answer = f.say("nothing yet")
		} else {
			answer = f.say("you said: " + before[len(before)-1])
		}
	default:
		answer = f.say("I don't know how to " + text)
	}
	f.mu.Lock()
	f.replied = f.replied || answer != ""
	f.cancel = nil
	f.mu.Unlock()
	_ = f.hook("event", map[string]any{
		"kind": "turn-complete", "turnId": turnID, "reason": reason, "answer": answer,
		"durationMs": 1200, // a fixed turn time, so nothing shown from it moves with the machine
		"usage":      map[string]any{"input_tokens": 10, "output_tokens": 5, "cache_read_input_tokens": 100, "cache_creation_input_tokens": 20, "model": "claude-opus-5"},
		"context":    map[string]any{"tokens": 130, "window": 200000},
		"rateLimits": fakeRateLimits,
		"background": background,
	}, nil)
}

// check asks Oriel about a tool call as the mod's tool.check does. With no answer, Oriel's own tools go ahead (the
// mod registered them), an MCP call other than the gateway's is refused, and the rest is the mode's.
func (f *fake) check(tool string, input json.RawMessage) checkOut {
	var c checkOut
	if err := f.hook("check", checkIn{Tool: tool, Input: input}, &c); err == nil {
		return c
	}
	switch {
	case strings.HasPrefix(tool, "mcp__oriel__"):
		return checkOut{Decision: "allow"}
	case strings.HasPrefix(tool, "mcp__oriel-gw__"), !strings.HasPrefix(tool, "mcp__"):
		return checkOut{}
	}
	return checkOut{Decision: "deny", Reason: CheckFailed}
}

// fakeConnector is the claude.ai connector tool the fake calls.
const fakeConnector = "mcp__claude_ai_Claude_Docs__guide"

// connector calls a claude.ai connector's tool as Claude Code 2.1.322 decides it: the mod's check first; its ask
// fires PermissionRequest, which the mod hands to Oriel, in every mode (bypassPermissions and dontAsk included, as
// the real claude did); with no word from the check, the mode decides.
func (f *fake) connector(turnID string) string {
	input := json.RawMessage(`{"items":["topic.index"]}`)
	f.use("toolu_conn", fakeConnector, input)
	c := f.check(fakeConnector, input)
	decision, message := c.Decision, c.Reason
	switch {
	case decision == "ask", decision == "" && f.mode == "default":
		var p permissionOut
		_ = f.hook("permission", permissionIn{ID: "perm-" + turnID, Tool: fakeConnector, Input: input,
			Suggestions: json.RawMessage(`[{"type":"addRules","rules":[{"toolName":"` + fakeConnector + `"}],"behavior":"allow","destination":"localSettings"}]`)}, &p)
		decision, message = p.Decision, p.Message
	case decision == "" && f.mode == "dontAsk":
		decision, message = "deny", "Permission to use "+fakeConnector+" has been denied because Claude Code is running in don't ask mode."
	case decision == "":
		decision = "allow"
	}
	if decision != "allow" {
		f.result("toolu_conn", message, true)
		return f.say("denied: " + message)
	}
	f.result("toolu_conn", "the index", false)
	return f.say("ran: the index")
}

// skippedConnector calls a connector's tool where a managed plugin continues past the mod's PermissionRequest hook,
// as the mod meets it once it's learnt that: its check asks, the mod tells Oriel the engine's dialog is up, and the
// call runs once 1 is typed into the terminal, which the mod's tool.call hook tells Oriel.
func (f *fake) skippedConnector() string {
	input := json.RawMessage(`{"items":["topic.index"]}`)
	f.use("toolu_conn", fakeConnector, input)
	if c := f.check(fakeConnector, input); c.Decision != "ask" {
		return f.say("not asked: " + c.Decision)
	}
	_ = f.hook("event", map[string]any{"kind": "dialog", "tool": fakeConnector, "input": input}, nil)
	for l := range f.typed { // 1: the dialog's first choice, Yes
		if l == "1" {
			break
		}
	}
	_ = f.hook("event", map[string]any{"kind": "dialog-gone"}, nil)
	f.result("toolu_conn", "the index", false)
	return f.say("ran: the index")
}

// fakeHarness starts the provider against the fake, in a HOME of its own.
func fakeProvider(t *testing.T) *Provider {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p := &Provider{Bin: exe, OrielBin: exe}
	p.box.Keychain = func(context.Context) ([]byte, error) { return nil, errors.New("no keychain in tests") }
	return p
}
