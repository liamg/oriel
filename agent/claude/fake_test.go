package claude

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
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
}

func fakeClaude(args []string) int {
	f := &fake{}
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
		case "--model", "--effort", "--mcp-config":
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
	fmt.Printf("fake claude %s in %s\r\n", f.id, cwd)
	if os.Getenv("ORIEL_FAKE_NO_HELLO") == "1" {
		note("run")
		time.Sleep(time.Hour)
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
// reads the plan's windows; /exit quits. ORIEL_FAKE_RUNS gets a line as it starts and as it quits; with
// ORIEL_FAKE_SIGNED_OUT=1 it starts on the signed-out screen. ORIEL_FAKE_DIALOG starts it under a dialog that
// swallows what's typed: "escape" until Escape closes it, "stuck" for good.
func (f *fake) terminal() {
	note("run")
	if os.Getenv("ORIEL_FAKE_SIGNED_OUT") == "1" {
		os.Stdout.Write(recordedSignedOut)
	}
	dialog := os.Getenv("ORIEL_FAKE_DIALOG")
	if dialog != "" {
		os.Stdout.WriteString("\x1b[1mNew MCP server found in .mcp.json\x1b[0m\r\n")
	}
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		if dialog == "escape" && strings.Contains(sc.Text(), "\x1b") {
			dialog = ""
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
	start := time.Now()
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
		var c checkOut
		_ = f.hook("check", checkIn{Tool: "mcp__oriel__echo", Input: json.RawMessage(`{"text":"hi"}`)}, &c)
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
		"durationMs": time.Since(start).Milliseconds(),
		"usage":      map[string]any{"input_tokens": 10, "output_tokens": 5, "cache_read_input_tokens": 100, "cache_creation_input_tokens": 20, "model": "claude-opus-5"},
		"context":    map[string]any{"tokens": 130, "window": 200000},
		"rateLimits": fakeRateLimits,
		"background": background,
	}, nil)
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
	return &Provider{Bin: exe, OrielBin: exe}
}
