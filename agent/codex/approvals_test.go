package codex

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/internal/isolate"
	"github.com/liamg/oriel/jsonrpc"
)

// wire is a session over pipes to a stand-in for codex, which sends it requests and notifications and hears what
// it steers turns with.
type wire struct {
	t     *testing.T
	s     *session
	codex *jsonrpc.Conn

	mu     sync.Mutex
	steers chan string
	reads  map[string]any // config/read's answer
}

func wired(t *testing.T, spec agent.StartSpec) *wire {
	t.Helper()
	isolate.Test(t)
	w := &wire{t: t, steers: make(chan string, 8)}
	s := newSession(&Provider{}, spec)
	s.id = "th"
	ar, aw := io.Pipe()
	br, bw := io.Pipe()
	s.rpc = jsonrpc.NewConn(ar, bw, jsonrpc.Handlers{Request: s.onRequest, Notify: s.onNotify}, jsonrpc.Options{})
	w.codex = jsonrpc.NewConn(br, aw, jsonrpc.Handlers{Request: func(r *jsonrpc.Request) {
		switch r.Method {
		case "turn/steer":
			var p struct {
				Input []struct{ Text string } `json:"input"`
			}
			json.Unmarshal(r.Params, &p)
			if len(p.Input) > 0 {
				w.steers <- p.Input[0].Text
			}
			r.Reply(map[string]any{"turnId": "tu"}, nil)
		case "config/read":
			w.mu.Lock()
			res := w.reads
			w.mu.Unlock()
			r.Reply(res, nil)
		default:
			r.Reply(nil, &jsonrpc.Error{Code: jsonrpc.MethodNotFound, Message: r.Method})
		}
	}}, jsonrpc.Options{})
	t.Cleanup(func() {
		s.rpc.Close()
		w.codex.Close()
		s.events.Close()
		for range s.events.Out() {
		}
	})
	w.s = s
	return w
}

// notify sends a notification, as JSON.
func (w *wire) notify(method, params string) {
	w.t.Helper()
	w.s.onNotify(method, json.RawMessage(params))
}

// call sends a request in the background, and gives what it's answered.
func (w *wire) call(method string, params any) <-chan json.RawMessage {
	out := make(chan json.RawMessage, 1)
	go func() {
		var res json.RawMessage
		w.codex.Call(context.Background(), method, params, &res)
		out <- res
	}()
	return out
}

func (w *wire) next() agent.Event {
	w.t.Helper()
	select {
	case ev := <-w.s.events.Out():
		return ev
	case <-time.After(5 * time.Second):
		w.t.Fatal("no event came")
		return nil
	}
}

func answered(t *testing.T, ch <-chan json.RawMessage) string {
	t.Helper()
	select {
	case b := <-ch:
		return string(b)
	case <-time.After(5 * time.Second):
		t.Fatal("the request wasn't answered")
		return ""
	}
}

func (w *wire) steer() string {
	w.t.Helper()
	select {
	case s := <-w.steers:
		return s
	case <-time.After(5 * time.Second):
		w.t.Fatal("nothing was said to the turn")
		return ""
	}
}

const twoFiles = `{"threadId":"th","turnId":"tu","item":{"type":"fileChange","id":"fc","status":"%s",
	"changes":[{"path":"a.go","kind":{"type":"update","move_path":null},"diff":"@@ -1 +1 @@\n-a\n+b\n"},
	{"path":"n.go","kind":{"type":"add"},"diff":"one\ntwo\n"}]}}`

// A file change is a call for each of its files, each with its own diff; its approval names them all, and Always
// accepts it for the session, where codex keeps it.
//
// ref:   codex/codex-rs/app-server-protocol/src/protocol/v2/item.rs
// quote: "User approved the file changes and future changes to the same files should run without prompting. AcceptForSession,"
func TestFileChangeApproval(t *testing.T) {
	w := wired(t, agent.StartSpec{Cwd: "/w", Policy: agent.PolicyAsk})
	w.notify("item/started", strings.ReplaceAll(twoFiles, "%s", "inProgress"))
	for _, want := range []string{"a.go", "n.go"} {
		c := w.next().(agent.ToolCall)
		var in struct {
			File string `json:"file_path"`
		}
		json.Unmarshal(c.Input, &in)
		if c.Tool != "Edit" || in.File != want {
			t.Errorf("call = %+v, want an Edit of %s", c, want)
		}
	}
	answer := w.call("item/fileChange/requestApproval", map[string]any{"threadId": "th", "turnId": "tu", "itemId": "fc", "startedAtMs": 1})
	p := w.next().(agent.Permission)
	if p.Tool != "Edit" || p.Summary != "a.go, n.go" || p.Rule != "" {
		t.Errorf("permission = %+v", p)
	}
	if err := w.s.Answer(context.Background(), p.ID, agent.Decision{Allow: true, Always: true}); err != nil {
		t.Fatal(err)
	}
	if got := answered(t, answer); !strings.Contains(got, `"acceptForSession"`) {
		t.Errorf("decision = %s", got)
	}
	if err := w.s.Answer(context.Background(), p.ID, agent.Decision{Allow: true}); err == nil {
		t.Error("answered twice")
	}
	w.notify("item/completed", strings.ReplaceAll(twoFiles, "%s", "completed"))
	a, n := w.next().(agent.ToolResult), w.next().(agent.ToolResult)
	if a.Diff == nil || a.Diff.Path != "a.go" || a.Diff.Added != 1 || a.Diff.Removed != 1 || a.IsError {
		t.Errorf("a.go's result = %+v", a)
	}
	if n.Diff == nil || n.Diff.Path != "n.go" || n.Diff.Added != 2 || n.ID == a.ID {
		t.Errorf("n.go's result = %+v", n)
	}
}

// A light session's edits go ahead; its commands still ask.
func TestLightPolicy(t *testing.T) {
	w := wired(t, agent.StartSpec{Cwd: "/w", Policy: agent.PolicyLight})
	w.notify("item/started", strings.ReplaceAll(twoFiles, "%s", "inProgress"))
	w.next()
	w.next()
	edit := w.call("item/fileChange/requestApproval", map[string]any{"threadId": "th", "turnId": "tu", "itemId": "fc", "startedAtMs": 1})
	if got := answered(t, edit); !strings.Contains(got, `"accept"`) {
		t.Errorf("the edit's decision = %s", got)
	}
	w.call("item/commandExecution/requestApproval", map[string]any{"threadId": "th", "turnId": "tu", "itemId": "c", "startedAtMs": 1, "command": "make", "cwd": "/w"})
	if p, ok := w.next().(agent.Permission); !ok || p.Tool != "Bash" || p.Summary != "make" {
		t.Errorf("the command's ask = %+v", p)
	}
}

// An edit of the user's checkout is declined, and the turn told why.
func TestProtectedCheckout(t *testing.T) {
	w := wired(t, agent.StartSpec{Cwd: "/w/ws", Protect: []string{"/src/repo"}, Policy: agent.PolicyLight})
	w.notify("item/started", `{"threadId":"th","turnId":"tu","item":{"type":"fileChange","id":"fc","status":"inProgress",
		"changes":[{"path":"/src/repo/a.go","kind":{"type":"update"},"diff":""}]}}`)
	w.next()
	got := w.call("item/fileChange/requestApproval", map[string]any{"threadId": "th", "turnId": "tu", "itemId": "fc", "startedAtMs": 1})
	if d := answered(t, got); !strings.Contains(d, `"decline"`) {
		t.Errorf("decision = %s", d)
	}
	if said := w.steer(); !strings.Contains(said, "original checkout") || !strings.Contains(said, "/w/ws") {
		t.Errorf("the turn was told %q", said)
	}
}

// A command that would post to a review is declined before anyone is asked.
func TestNoPosting(t *testing.T) {
	w := wired(t, agent.StartSpec{Cwd: "/w", Policy: agent.PolicyAsk})
	got := w.call("item/commandExecution/requestApproval", map[string]any{"threadId": "th", "turnId": "tu", "itemId": "c", "startedAtMs": 1,
		"command": "gh pr review 7 --approve", "cwd": "/w"})
	if d := answered(t, got); !strings.Contains(d, `"decline"`) {
		t.Errorf("decision = %s", d)
	}
	if said := w.steer(); !strings.Contains(said, "never post") {
		t.Errorf("the turn was told %q", said)
	}
}

// "Always" on a command is a rule in Oriel's form: the prefix codex proposes allowing, else the session host's.
//
// ref:   codex/codex-rs/app-server-protocol/src/protocol/v2/item.rs
// quote: "Optional proposed execpolicy amendment to allow similar commands without prompting."
// ref:   codex/codex-rs/protocol/src/approvals.rs
// quote: "commands that start with this token sequence."
func TestCommandRule(t *testing.T) {
	w := wired(t, agent.StartSpec{Cwd: "/w", Policy: agent.PolicyAsk})
	w.call("item/commandExecution/requestApproval", map[string]any{"threadId": "th", "turnId": "tu", "itemId": "c", "startedAtMs": 1,
		"command": "go test ./...", "cwd": "/w", "proposedExecpolicyAmendment": []string{"go", "test"}})
	if p := w.next().(agent.Permission); p.Rule != "Bash(go test:*)" || !strings.Contains(string(p.Input), `"command":"go test ./..."`) {
		t.Errorf("permission = %+v", p)
	}
	w.call("item/commandExecution/requestApproval", map[string]any{"threadId": "th", "turnId": "tu", "itemId": "d", "startedAtMs": 1,
		"command": "make", "cwd": "/w"})
	if p := w.next().(agent.Permission); p.Rule != "" {
		t.Errorf("permission = %+v", p)
	}
}

// Options picked together are each an answer; a question not answered says the user's note to the turn.
//
// ref:   codex/codex-rs/app-server-protocol/src/protocol/v2/item.rs
// quote: "pub struct ToolRequestUserInputAnswer { pub answers: Vec<String>, }"
func TestQuestionAnswers(t *testing.T) {
	w := wired(t, agent.StartSpec{Cwd: "/w"})
	ask := map[string]any{"threadId": "th", "turnId": "tu", "itemId": "q", "isBlocking": true,
		"questions": []map[string]any{{"id": "colour", "header": "Colour", "question": "Which colours?", "isOther": false, "isSecret": false,
			"options": []map[string]any{{"label": "red", "description": ""}, {"label": "blue", "description": ""}}}}}
	got := w.call("item/tool/requestUserInput", ask)
	q := w.next().(agent.Question)
	w.s.Answer(context.Background(), q.ID, agent.Decision{Allow: true, Answers: map[string]string{"Which colours?": "red, blue"}})
	if a := answered(t, got); !strings.Contains(a, `"colour":{"answers":["red","blue"]}`) {
		t.Errorf("answers = %s", a)
	}
	got = w.call("item/tool/requestUserInput", ask)
	q = w.next().(agent.Question)
	w.s.Answer(context.Background(), q.ID, agent.Decision{Note: "decide yourself"})
	if a := answered(t, got); !strings.Contains(a, `"answers":{}`) {
		t.Errorf("answers = %s", a)
	}
	if said := w.steer(); said != "decide yourself" {
		t.Errorf("the turn was told %q", said)
	}
}

// An MCP server's elicitation is taken as approval for its call under way, which the permission names.
//
// ref:   codex/codex-rs/app-server-protocol/src/protocol/v2/mcp.rs
// quote: "TODO: When core can correlate an elicitation with an MCP tool call, expose the associated"
func TestElicitationNamesTheTool(t *testing.T) {
	w := wired(t, agent.StartSpec{Cwd: "/w"})
	w.notify("item/started", `{"threadId":"th","turnId":"tu","item":{"type":"mcpToolCall","id":"m1","server":"linear","tool":"create_issue",
		"status":"inProgress","arguments":{"title":"x"}}}`)
	w.next()
	w.call("mcpServer/elicitation/request", map[string]any{"threadId": "th", "turnId": "tu", "serverName": "linear", "mode": "form",
		"message": "Allow create_issue?", "requestedSchema": map[string]any{"type": "object", "properties": map[string]any{}}})
	if p := w.next().(agent.Permission); p.Tool != "mcp__linear__create_issue" || p.Summary != "Allow create_issue?" {
		t.Errorf("permission = %+v", p)
	}
	w.notify("item/started", `{"threadId":"th","turnId":"tu","item":{"type":"mcpToolCall","id":"m2","server":"github",
		"tool":"add_reply_to_pull_request_comment","status":"inProgress","arguments":{}}}`)
	w.next()
	got := w.call("mcpServer/elicitation/request", map[string]any{"threadId": "th", "turnId": "tu", "serverName": "github", "mode": "form",
		"message": "Allow?", "requestedSchema": map[string]any{"type": "object", "properties": map[string]any{}}})
	if a := answered(t, got); !strings.Contains(a, `"decline"`) {
		t.Errorf("a review reply = %s", a)
	}
}
