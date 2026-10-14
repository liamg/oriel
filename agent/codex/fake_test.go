package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/agent/jsonrpc"
)

// fakeCodex is `codex` for the tests: --version, and an app-server speaking protocol v2 that plays the
// conformance scenarios (agenttest). FAKE_CODEX_ACCOUNT picks the login ("chatgpt" unless set; "none" for none).
func fakeCodex(args []string) int {
	if len(args) > 0 && args[0] == "--version" {
		fmt.Println("codex-cli 0.99.0-fake")
		return 0
	}
	f := &fake{home: os.Getenv("CODEX_HOME"), threads: map[string]*fakeThread{}}
	f.rpc = jsonrpc.NewConn(os.Stdin, os.Stdout, jsonrpc.Handlers{Request: f.handle}, jsonrpc.Options{})
	<-f.rpc.Done()
	f.mu.Lock()
	for _, t := range f.threads {
		t.stopMCP()
	}
	f.mu.Unlock()
	return 0
}

type fake struct {
	home string
	rpc  *jsonrpc.Conn

	mu      sync.Mutex
	next    int
	threads map[string]*fakeThread
}

type fakeThread struct {
	id        string
	ephemeral bool
	history   []string

	mcp    *jsonrpc.Conn
	mcpCmd *exec.Cmd

	mu        sync.Mutex
	turn      string
	interrupt chan struct{}
	steer     chan string
}

func (t *fakeThread) stopMCP() {
	if t.mcp != nil {
		t.mcp.Close()
	}
	if t.mcpCmd != nil {
		t.mcpCmd.Process.Kill()
		t.mcpCmd.Wait()
	}
}

func (f *fake) id(prefix string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	return fmt.Sprintf("%s-%d-%d", prefix, os.Getpid(), f.next)
}

func (f *fake) thread(id string) *fakeThread {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.threads[id]
}

func (f *fake) path(id string) string { return filepath.Join(f.home, "threads", id+".json") }

func (f *fake) save(t *fakeThread) {
	if t.ephemeral {
		return
	}
	os.MkdirAll(filepath.Join(f.home, "threads"), 0o755)
	b, _ := json.Marshal(t.history)
	os.WriteFile(f.path(t.id), b, 0o644)
}

type fakeParams struct {
	ThreadID       string                     `json:"threadId"`
	TurnID         string                     `json:"turnId"`
	ExpectedTurnID string                     `json:"expectedTurnId"`
	Ephemeral      bool                       `json:"ephemeral"`
	Config         map[string]json.RawMessage `json:"config"`
	Input          []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"input"`
}

func (f *fake) handle(r *jsonrpc.Request) {
	var p fakeParams
	json.Unmarshal(r.Params, &p)
	switch r.Method {
	case "initialize":
		r.Reply(map[string]any{"userAgent": "fake", "codexHome": f.home, "platformFamily": "unix", "platformOs": "linux"}, nil)
	case "account/read":
		switch a := os.Getenv("FAKE_CODEX_ACCOUNT"); a {
		case "none":
			r.Reply(map[string]any{"account": nil, "requiresOpenaiAuth": true}, nil)
		case "apiKey":
			r.Reply(map[string]any{"account": map[string]any{"type": "apiKey"}, "requiresOpenaiAuth": true}, nil)
		default:
			r.Reply(map[string]any{"account": map[string]any{"type": "chatgpt", "email": "you@example.com", "planType": "pro"}, "requiresOpenaiAuth": true}, nil)
		}
	case "account/rateLimits/read":
		r.Reply(map[string]any{"rateLimits": map[string]any{
			"limitId": "codex", "planType": "pro",
			"primary":   map[string]any{"usedPercent": 12, "windowDurationMins": 300, "resetsAt": 1700000000},
			"secondary": map[string]any{"usedPercent": 40, "windowDurationMins": 10080, "resetsAt": 1700500000},
		}}, nil)
	case "model/list":
		r.Reply(map[string]any{"data": []map[string]any{{
			"id": "m", "model": "gpt-fake-codex", "displayName": "GPT Fake", "hidden": false, "isDefault": true,
			"supportedReasoningEfforts": []map[string]any{{"reasoningEffort": "low", "description": ""}, {"reasoningEffort": "high", "description": ""}},
			"defaultReasoningEffort":    "low",
		}}, "nextCursor": nil}, nil)
	case "thread/start", "thread/resume":
		t := &fakeThread{id: p.ThreadID}
		if r.Method == "thread/start" {
			t.id = f.id("thread")
		} else {
			b, err := os.ReadFile(f.path(p.ThreadID))
			if err != nil {
				r.Reply(nil, &jsonrpc.Error{Code: jsonrpc.InvalidRequest, Message: "no rollout for " + p.ThreadID})
				return
			}
			json.Unmarshal(b, &t.history)
		}
		go func() {
			if err := f.startMCP(t, p.Config["mcp_servers.oriel"]); err != nil {
				r.Reply(nil, err)
				return
			}
			f.mu.Lock()
			f.threads[t.id] = t
			f.mu.Unlock()
			r.Reply(map[string]any{"thread": map[string]any{"id": t.id}}, nil)
			f.rpc.Notify("thread/started", map[string]any{"thread": map[string]any{"id": t.id}})
		}()
	case "thread/fork":
		src := f.thread(p.ThreadID)
		if src == nil {
			r.Reply(nil, &jsonrpc.Error{Code: jsonrpc.InvalidRequest, Message: "no thread"})
			return
		}
		src.mu.Lock()
		t := &fakeThread{id: f.id("fork"), ephemeral: p.Ephemeral, history: append([]string(nil), src.history...)}
		src.mu.Unlock()
		f.mu.Lock()
		f.threads[t.id] = t
		f.mu.Unlock()
		r.Reply(map[string]any{"thread": map[string]any{"id": t.id, "forkedFromId": src.id}}, nil)
	case "thread/unsubscribe":
		r.Reply(map[string]any{}, nil)
	case "turn/start":
		t := f.thread(p.ThreadID)
		if t == nil || len(p.Input) == 0 {
			r.Reply(nil, &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: "no thread or no input"})
			return
		}
		id := f.id("turn")
		t.mu.Lock()
		t.turn, t.interrupt, t.steer = id, make(chan struct{}), make(chan string, 1)
		t.mu.Unlock()
		r.Reply(map[string]any{"turn": map[string]any{"id": id, "status": "inProgress", "items": []any{}}}, nil)
		go f.run(t, id, p.Input[0].Text)
	case "turn/interrupt":
		t := f.thread(p.ThreadID)
		if t == nil {
			r.Reply(nil, &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: "no thread"})
			return
		}
		t.mu.Lock()
		if t.turn == p.TurnID && t.interrupt != nil {
			close(t.interrupt)
			t.interrupt = nil
		}
		t.mu.Unlock()
		r.Reply(map[string]any{}, nil)
	case "turn/steer":
		t := f.thread(p.ThreadID)
		if t == nil || len(p.Input) == 0 {
			r.Reply(nil, &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: "no thread or no input"})
			return
		}
		t.mu.Lock()
		ok := t.turn == p.ExpectedTurnID && t.turn != ""
		steer := t.steer
		t.mu.Unlock()
		if !ok {
			r.Reply(nil, &jsonrpc.Error{Code: jsonrpc.InvalidRequest, Message: "no active turn to steer"})
			return
		}
		select {
		case steer <- p.Input[0].Text:
		default:
		}
		r.Reply(map[string]any{"turnId": p.ExpectedTurnID}, nil)
	default:
		r.Reply(nil, &jsonrpc.Error{Code: jsonrpc.MethodNotFound, Message: "fake codex: " + r.Method})
	}
}

// startMCP launches the configured oriel MCP server, as codex does, with only the environment it names.
func (f *fake) startMCP(t *fakeThread, raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var c struct {
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return err
	}
	cmd := exec.Command(c.Command, c.Args...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
	for k, v := range c.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stderr = os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	t.mcpCmd = cmd
	t.mcp = jsonrpc.NewConn(out, in, jsonrpc.Handlers{}, jsonrpc.Options{Version: "2.0"})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := t.mcp.Call(ctx, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "fake-codex", "version": "0"}}, nil); err != nil {
		return err
	}
	t.mcp.Notify("notifications/initialized", nil)
	return t.mcp.Call(ctx, "tools/list", map[string]any{}, nil)
}

// run plays one turn.
func (f *fake) run(t *fakeThread, turn, text string) {
	note := func(method string, params map[string]any) {
		params["threadId"], params["turnId"] = t.id, turn
		f.rpc.Notify(method, params)
	}
	item := func(method string, it map[string]any) { note(method, map[string]any{"item": it}) }
	say := func(text string) {
		id := f.id("msg")
		item("item/started", map[string]any{"type": "agentMessage", "id": id, "text": ""})
		half := len(text) / 2
		for _, d := range []string{text[:half], text[half:]} {
			note("item/agentMessage/delta", map[string]any{"itemId": id, "delta": d})
		}
		item("item/completed", map[string]any{"type": "agentMessage", "id": id, "text": text})
	}
	t.mu.Lock()
	t.history = append(t.history, text)
	history := append([]string(nil), t.history...)
	t.mu.Unlock()
	f.rpc.Notify("turn/started", map[string]any{"threadId": t.id, "turn": map[string]any{"id": turn, "status": "inProgress", "items": []any{}}})
	item("item/completed", map[string]any{"type": "userMessage", "id": f.id("user"), "content": []map[string]any{{"type": "text", "text": text}}})
	status := "completed"
	ctx := context.Background()
	switch {
	case strings.HasPrefix(t.id, "fork"):
		say("fork: " + text)
	case strings.HasSuffix(text, "say hello"):
		item("item/completed", map[string]any{"type": "reasoning", "id": f.id("think"), "summary": []string{"greeting"}, "content": []string{}})
		say("hello")
	case strings.HasSuffix(text, "call echo"):
		id := f.id("mcp")
		args := map[string]any{"text": "hi"}
		item("item/started", map[string]any{"type": "mcpToolCall", "id": id, "server": "oriel", "tool": "echo", "status": "inProgress", "arguments": args})
		var res struct {
			Content []struct{ Type, Text string } `json:"content"`
			IsError bool                          `json:"isError"`
		}
		err := t.mcp.Call(ctx, "tools/call", map[string]any{"name": "echo", "arguments": args}, &res)
		done := map[string]any{"type": "mcpToolCall", "id": id, "server": "oriel", "tool": "echo", "status": "completed", "arguments": args,
			"result": map[string]any{"content": res.Content}}
		if err != nil {
			done["status"], done["error"] = "failed", map[string]any{"message": err.Error()}
		}
		item("item/completed", done)
		said := ""
		if len(res.Content) > 0 {
			said = res.Content[0].Text
		}
		say("the tool said " + said)
	case strings.HasSuffix(text, "run a command"):
		id := f.id("cmd")
		cmd := map[string]any{"type": "commandExecution", "id": id, "command": "go test ./...", "cwd": "/w", "status": "inProgress", "commandActions": []any{}}
		item("item/started", cmd)
		var r struct{ Decision string }
		err := f.rpc.Call(ctx, "item/commandExecution/requestApproval", map[string]any{
			"threadId": t.id, "turnId": turn, "itemId": id, "startedAtMs": time.Now().UnixMilli(), "command": "go test ./...", "cwd": "/w",
		}, &r)
		if err == nil && (r.Decision == "accept" || r.Decision == "acceptForSession") {
			cmd["status"], cmd["aggregatedOutput"], cmd["exitCode"] = "completed", "ok\n", 0
			item("item/completed", cmd)
			say("ran: ok")
			break
		}
		cmd["status"] = "declined"
		item("item/completed", cmd)
		t.mu.Lock()
		steer := t.steer
		t.mu.Unlock()
		select {
		case why := <-steer:
			say("denied: " + why)
		case <-time.After(5 * time.Second):
			say("denied: no reason given")
		}
	case strings.HasSuffix(text, "ask me"):
		var r struct {
			Answers map[string]struct{ Answers []string } `json:"answers"`
		}
		err := f.rpc.Call(ctx, "item/tool/requestUserInput", map[string]any{
			"threadId": t.id, "turnId": turn, "itemId": f.id("ask"), "isBlocking": true,
			"questions": []map[string]any{{"id": "colour", "header": "Colour", "question": "Which colour?", "isOther": false, "isSecret": false,
				"options": []map[string]any{{"label": "red", "description": ""}, {"label": "blue", "description": ""}}}},
		}, &r)
		if a := r.Answers["colour"].Answers; err == nil && len(a) > 0 {
			say("you chose " + a[0])
		} else {
			say(fmt.Sprintf("no answer: %v", err))
		}
	case strings.HasSuffix(text, "lose the login"):
		// The shape is app-server-protocol v2's TurnError; the message is the fake's own: no signed-out error has
		// been recorded from codex.
		e := map[string]any{"message": "unexpected status 401 Unauthorized", "codexErrorInfo": "unauthorized"}
		note("error", map[string]any{"error": e, "willRetry": false})
		status = "failed"
	case strings.HasSuffix(text, "work slowly"):
		t.mu.Lock()
		stop := t.interrupt
		t.mu.Unlock()
		select {
		case <-stop:
		case <-time.After(time.Minute):
		}
		status = "interrupted"
	case strings.HasSuffix(text, "what did I say"):
		if n := len(history); n >= 2 {
			say("you said: " + history[n-2])
		} else {
			say("you said nothing")
		}
	default:
		say("the fake doesn't know " + text)
	}
	t.mu.Lock()
	f.save(t)
	t.mu.Unlock()
	note("thread/tokenUsage/updated", map[string]any{"tokenUsage": map[string]any{
		"total":              map[string]any{"totalTokens": 30 * len(history), "inputTokens": 20 * len(history), "cachedInputTokens": 5, "outputTokens": 10 * len(history), "reasoningOutputTokens": 0},
		"last":               map[string]any{"totalTokens": 30, "inputTokens": 20, "cachedInputTokens": 5, "outputTokens": 10, "reasoningOutputTokens": 0},
		"modelContextWindow": 272000,
	}})
	f.rpc.Notify("account/rateLimits/updated", map[string]any{"rateLimits": map[string]any{
		"primary": map[string]any{"usedPercent": 13, "windowDurationMins": 300, "resetsAt": 1700000000},
	}})
	t.mu.Lock()
	t.turn, t.interrupt = "", nil
	t.mu.Unlock()
	f.rpc.Notify("turn/completed", map[string]any{"threadId": t.id, "turn": map[string]any{"id": turn, "status": status, "items": []any{}, "durationMs": 12}})
}
