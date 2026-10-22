// Package acptest is a fake ACP agent for tests: over stdio it plays the conformance scenarios (agent/agenttest),
// and a few of ACP's own (files, plans, terminals, images).
package acptest

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/jsonrpc"
)

// Env set to 1 makes a test binary the fake (Main).
const Env = "ORIEL_FAKE_ACP"

// DirEnv names where it keeps its sessions, so a load finds them; the temp directory if unset.
const DirEnv = "ORIEL_FAKE_ACP_DIR"

// fake is an ACP agent: one connection, sessions it keeps in ORIEL_FAKE_ACP_DIR so a load finds them.
type fake struct {
	conn   *jsonrpc.Conn
	dir    string
	client struct {
		Terminal bool `json:"terminal"`
	}
	mu       sync.Mutex
	sessions map[string]*fakeSession
	seq      int
}

type fakeSession struct {
	id      string
	cwd     string
	mcp     map[string]any
	prompts []string
	cancel  chan struct{}
}

// Main is the fake agent on stdin and stdout, until stdin closes. It returns the exit code.
func Main() int {
	dir := os.Getenv(DirEnv)
	if dir == "" {
		dir = os.TempDir()
	}
	f := &fake{dir: dir, sessions: map[string]*fakeSession{}}
	f.conn = jsonrpc.NewConn(os.Stdin, os.Stdout, jsonrpc.Handlers{Request: f.request, Notify: f.notify}, jsonrpc.Options{Version: "2.0"})
	<-f.conn.Done()
	return 0
}

func (f *fake) notify(method string, params json.RawMessage) {
	if method != "session/cancel" {
		return
	}
	var p struct {
		SessionID string `json:"sessionId"`
	}
	json.Unmarshal(params, &p)
	f.mu.Lock()
	s := f.sessions[p.SessionID]
	f.mu.Unlock()
	if s != nil {
		select {
		case s.cancel <- struct{}{}:
		default:
		}
	}
}

func (f *fake) request(r *jsonrpc.Request) {
	go func() { r.Reply(f.answer(r.Method, r.Params)) }()
}

func (f *fake) answer(method string, params json.RawMessage) (any, error) {
	switch method {
	case "initialize":
		var p struct {
			ClientCapabilities json.RawMessage `json:"clientCapabilities"`
		}
		json.Unmarshal(params, &p)
		json.Unmarshal(p.ClientCapabilities, &f.client)
		return map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{"loadSession": true,
			"promptCapabilities": map[string]any{"image": true}}, "agentInfo": map[string]any{"name": "fake", "version": "1"},
			"authMethods": []any{}}, nil
	case "session/new", "session/load":
		var p struct {
			SessionID  string           `json:"sessionId"`
			Cwd        string           `json:"cwd"`
			MCPServers []map[string]any `json:"mcpServers"`
		}
		json.Unmarshal(params, &p)
		s := &fakeSession{id: p.SessionID, cwd: p.Cwd, cancel: make(chan struct{}, 1)}
		if len(p.MCPServers) > 0 {
			s.mcp = p.MCPServers[0]
		}
		if method == "session/new" {
			f.mu.Lock()
			f.seq++
			s.id = fmt.Sprintf("acp-%d-%d", os.Getpid(), f.seq)
			f.mu.Unlock()
		} else {
			b, err := os.ReadFile(filepath.Join(f.dir, s.id))
			if err != nil {
				return nil, &jsonrpc.Error{Code: -32002, Message: "no session " + s.id}
			}
			json.Unmarshal(b, &s.prompts)
			for _, p := range s.prompts {
				f.update(s, map[string]any{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "text", "text": p}})
				f.update(s, map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "replayed"}})
			}
		}
		f.mu.Lock()
		f.sessions[s.id] = s
		f.mu.Unlock()
		if method == "session/load" {
			return map[string]any{}, nil
		}
		return map[string]any{"sessionId": s.id, "configOptions": []any{map[string]any{"id": "model", "name": "Model",
			"category": "model", "type": "select", "currentValue": "m1", "options": []any{map[string]any{"value": "m1", "name": "M1"},
				map[string]any{"value": "m2", "name": "M2"}}}}}, nil
	case "session/set_config_option":
		return map[string]any{"configOptions": []any{}}, nil
	case "session/prompt":
		var p struct {
			SessionID string `json:"sessionId"`
			Prompt    []struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				MimeType string `json:"mimeType"`
			} `json:"prompt"`
		}
		json.Unmarshal(params, &p)
		f.mu.Lock()
		s := f.sessions[p.SessionID]
		f.mu.Unlock()
		if s == nil {
			return nil, &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: "no session"}
		}
		text, images := "", 0
		for _, b := range p.Prompt {
			switch b.Type {
			case "text":
				text = b.Text
			case "image":
				images++
			}
		}
		return f.play(s, text, images)
	}
	return nil, &jsonrpc.Error{Code: jsonrpc.MethodNotFound, Message: method}
}

func (f *fake) update(s *fakeSession, u map[string]any) {
	f.conn.Notify("session/update", map[string]any{"sessionId": s.id, "update": u})
}

func (f *fake) say(s *fakeSession, text string) {
	id := fmt.Sprintf("msg-%d", time.Now().UnixNano())
	half := len(text) / 2
	for _, part := range []string{text[:half], text[half:]} {
		f.update(s, map[string]any{"sessionUpdate": "agent_message_chunk", "messageId": id, "content": map[string]any{"type": "text", "text": part}})
	}
}

func (f *fake) play(s *fakeSession, text string, images int) (any, error) {
	before := ""
	if len(s.prompts) > 0 {
		before = s.prompts[len(s.prompts)-1]
	}
	s.prompts = append(s.prompts, text)
	if b, err := json.Marshal(s.prompts); err == nil {
		os.WriteFile(filepath.Join(f.dir, s.id), b, 0o600)
	}
	ctx := context.Background()
	switch {
	case text == "say hello":
		f.update(s, map[string]any{"sessionUpdate": "agent_thought_chunk", "content": map[string]any{"type": "text", "text": "greeting"}})
		f.update(s, map[string]any{"sessionUpdate": "usage_update", "used": 1200, "size": 200000, "cost": map[string]any{"amount": 0.01, "currency": "USD"}})
		f.say(s, "hello")
	case text == "call echo":
		f.update(s, map[string]any{"sessionUpdate": "tool_call", "toolCallId": "call-echo", "name": "mcp__oriel__echo", "title": "echo",
			"kind": "other", "status": "pending", "rawInput": map[string]any{"text": "hi"}})
		out, err := f.callEcho(s)
		if err != nil {
			out = err.Error()
		}
		f.update(s, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "call-echo", "status": "completed",
			"content": []any{map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": out}}}})
		f.say(s, out)
	case text == "run a command":
		f.update(s, map[string]any{"sessionUpdate": "tool_call", "toolCallId": "call-cmd", "title": "go test ./...", "kind": "execute",
			"status": "pending", "rawInput": map[string]any{"command": "go test ./..."}})
		var r struct {
			Outcome struct {
				Outcome  string `json:"outcome"`
				OptionID string `json:"optionId"`
			} `json:"outcome"`
		}
		err := f.conn.Call(ctx, "session/request_permission", map[string]any{"sessionId": s.id, "toolCall": map[string]any{"toolCallId": "call-cmd"},
			"options": []any{map[string]any{"optionId": "yes", "name": "Allow once", "kind": "allow_once"},
				map[string]any{"optionId": "always", "name": "Always allow go test", "kind": "allow_always"},
				map[string]any{"optionId": "no", "name": "Reject", "kind": "reject_once"}}}, &r)
		if err != nil {
			return nil, err
		}
		if r.Outcome.OptionID == "no" {
			f.update(s, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "call-cmd", "status": "failed"})
			f.say(s, "denied")
			return map[string]any{"stopReason": "end_turn"}, nil
		}
		f.update(s, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "call-cmd", "status": "completed",
			"content": []any{map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": "ok"}}}})
		f.say(s, "ran: ok")
	case strings.HasPrefix(text, "The user denied "):
		_, note, _ := strings.Cut(text, ": ")
		f.say(s, "denied: "+note)
	case text == "ask me":
		var r struct {
			Action  string            `json:"action"`
			Content map[string]string `json:"content"`
		}
		err := f.conn.Call(ctx, "elicitation/create", map[string]any{"sessionId": s.id, "mode": "form", "message": "Which colour?",
			"requestedSchema": map[string]any{"type": "object", "properties": map[string]any{"colour": map[string]any{"type": "string",
				"enum": []string{"red", "blue"}}}, "required": []string{"colour"}}}, &r)
		if err != nil {
			return nil, err
		}
		f.say(s, "you chose "+r.Content["colour"])
	case text == "work slowly":
		<-s.cancel
		return map[string]any{"stopReason": "cancelled"}, nil
	case text == "what did I say":
		f.say(s, "you said: "+before)
	case text == "lose the login":
		return nil, &jsonrpc.Error{Code: -32000, Message: "Authentication required"}
	case text == "edit a.txt":
		path := filepath.Join(s.cwd, "a.txt")
		var r struct {
			Content string `json:"content"`
		}
		if err := f.conn.Call(ctx, "fs/read_text_file", map[string]any{"sessionId": s.id, "path": path, "line": 2, "limit": 1}, &r); err != nil {
			return nil, err
		}
		if err := f.conn.Call(ctx, "fs/write_text_file", map[string]any{"sessionId": s.id, "path": path, "content": "one\nTWO\nthree\n"}, nil); err != nil {
			return nil, err
		}
		f.update(s, map[string]any{"sessionUpdate": "plan", "entries": []any{map[string]any{"content": "Edit a.txt", "priority": "high", "status": "completed"}}})
		f.update(s, map[string]any{"sessionUpdate": "tool_call", "toolCallId": "call-edit", "title": "Edit a.txt", "kind": "edit",
			"status": "completed", "content": []any{map[string]any{"type": "diff", "path": path, "oldText": "one\ntwo\nthree\n", "newText": "one\nTWO\nthree\n"}}})
		f.say(s, "line 2 was "+strings.TrimSpace(r.Content))
	case strings.HasPrefix(text, "write "):
		err := f.conn.Call(ctx, "fs/write_text_file", map[string]any{"sessionId": s.id, "path": strings.TrimPrefix(text, "write "), "content": "x"}, nil)
		f.say(s, fmt.Sprintf("wrote: %v", err))
	case text == "run in a terminal":
		if !f.client.Terminal {
			f.say(s, "no terminals")
			return map[string]any{"stopReason": "end_turn"}, nil
		}
		var c struct {
			TerminalID string `json:"terminalId"`
		}
		if err := f.conn.Call(ctx, "terminal/create", map[string]any{"sessionId": s.id, "command": "sh", "args": []string{"-c", "echo out; echo err >&2; exit 3"}}, &c); err != nil {
			return nil, err
		}
		var w struct {
			ExitCode int `json:"exitCode"`
		}
		f.conn.Call(ctx, "terminal/wait_for_exit", map[string]any{"sessionId": s.id, "terminalId": c.TerminalID}, &w)
		var o struct {
			Output string `json:"output"`
		}
		f.conn.Call(ctx, "terminal/output", map[string]any{"sessionId": s.id, "terminalId": c.TerminalID}, &o)
		f.conn.Call(ctx, "terminal/release", map[string]any{"sessionId": s.id, "terminalId": c.TerminalID}, nil)
		f.say(s, fmt.Sprintf("exit %d: %s", w.ExitCode, strings.Join(strings.Fields(o.Output), " ")))
	case text == "look":
		f.say(s, fmt.Sprintf("%d images", images))
	default:
		f.say(s, "I don't know "+text)
	}
	return map[string]any{"stopReason": "end_turn"}, nil
}

// callEcho calls Oriel's echo tool through the MCP server the client named, as an agent would.
func (f *fake) callEcho(s *fakeSession) (string, error) {
	if s.mcp == nil {
		return "", fmt.Errorf("no MCP server")
	}
	b, _ := json.Marshal(s.mcp)
	var m struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
		Env     []struct {
			Name, Value string
		} `json:"env"`
	}
	json.Unmarshal(b, &m)
	cmd := exec.Command(m.Command, m.Args...)
	cmd.Env = os.Environ()
	for _, e := range m.Env {
		cmd.Env = append(cmd.Env, e.Name+"="+e.Value)
	}
	in, _ := cmd.StdinPipe()
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		return "", err
	}
	defer cmd.Wait()
	defer in.Close()
	rd := bufio.NewReader(out)
	io.WriteString(in, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`+"\n")
	rd.ReadString('\n')
	io.WriteString(in, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hi"}}}`+"\n")
	line, err := rd.ReadString('\n')
	if err != nil {
		return "", err
	}
	var r struct {
		Result struct {
			Content []struct{ Text string } `json:"content"`
		} `json:"result"`
	}
	json.Unmarshal([]byte(line), &r)
	if len(r.Result.Content) == 0 {
		return "", fmt.Errorf("tools/call: %s", line)
	}
	return r.Result.Content[0].Text, nil
}
