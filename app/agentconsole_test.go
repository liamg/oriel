package app

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/headless"
)

// latest waits for the frame on screen, settled (no other drawn for a moment, so nothing's still moving), to satisfy
// ok, and returns it: unlike waitFrame, not one drawn before.
func (r *running) latest(what string, ok func(headless.Frame) bool) headless.Frame {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	seen := -1
	for {
		fs := r.win.Frames()
		if len(fs) > 0 && len(fs) == seen && ok(fs[len(fs)-1]) {
			return fs[len(fs)-1]
		}
		seen = len(fs)
		if time.Now().After(deadline) {
			last := ""
			if fs := r.win.Frames(); len(fs) > 0 {
				last = fs[len(fs)-1].Text()
			}
			r.t.Fatalf("the frame on screen never had %s; it is:\n%s", what, last)
		}
		time.Sleep(40 * time.Millisecond)
	}
}

// clickText clicks the middle of the first run of text in the frame on screen.
func (r *running) clickText(text string) {
	r.t.Helper()
	var at render.Point
	r.latest(`"`+text+`" to click`, func(f headless.Frame) bool {
		for _, op := range f.Ops {
			if op.Kind == render.OpGlyphs && op.Run.Text == text {
				at = render.Point{X: op.At.X + op.Run.Advance/2, Y: op.At.Y - op.Run.Ascent/2}
				return true
			}
		}
		return false
	})
	r.win.Send(render.PointerEvent{Kind: render.PointerMove, Pos: at})
	r.win.Send(render.PointerEvent{Kind: render.PointerPress, Pos: at, Buttons: render.ButtonPrimary})
	r.win.Send(render.PointerEvent{Kind: render.PointerRelease, Pos: at, Buttons: render.ButtonPrimary})
}

func (r *running) typeText(s string) {
	for _, ch := range s {
		r.win.Send(render.KeyEvent{Key: string(ch)})
	}
}

func (r *running) waitGone(s string) {
	r.t.Helper()
	r.latest("no "+s, func(f headless.Frame) bool { return !strings.Contains(f.Text(), s) })
}

// consoleSession starts a session with the fake agent, waits for the window to reopen on it, and opens its console
// with ⌘⇧A.
func consoleSession(t *testing.T) (*running, *agenttest.FakeSession, *localWorker) {
	t.Helper()
	r, a, lw := agentSession(t)
	r.typeKeys("<D-A>")
	r.waitText("SESSION STATS")
	return r, a, lw
}

// agentSession starts a session with the fake agent and waits for the window to reopen on it.
func agentSession(t *testing.T) (*running, *agenttest.FakeSession, *localWorker) {
	t.Helper()
	lw := newLocalWorker(t)
	r := startAppWith(t, t.TempDir(), "", nil, lw.connect(t))
	waitState(t, r.store, "the worker", func(st core.State) bool { return len(st.Workers) == 1 })
	err := r.do(func() error {
		return r.host.Run(context.Background(), "session.start", registry.Args{"kind": "work", "name": "w", "provider": "fake", "repos": lw.repo})
	})
	if err != nil {
		t.Fatal(err)
	}
	a := lw.fake.WaitStarted(t)
	st := waitState(t, r.store, "the session in front", func(st core.State) bool { return st.Focus == "w" })
	se, _ := st.Session("w")
	p, _ := se.Snapshot().Primary()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var dir string
		r.do(func() error { dir = r.host.Here().Project; return nil })
		if filepath.Clean(dir) == filepath.Clean(p.Dir) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the window never reopened on %s", p.Dir)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return r, a, lw
}

func toolInput(m map[string]string) json.RawMessage {
	b, _ := json.Marshal(m)
	return b
}

// AI-6: the console shows each kind of block: prose, a prompt, thinking (folded), tool calls with their summaries,
// an edit as a mini-diff, shell output folded, an error, and the subagents box; ⏎ and a click unfold.
func TestAgentConsoleShowsEachBlock(t *testing.T) {
	r, a, _ := consoleSession(t)
	diff := &core.FileDiff{Path: "internal/api/usage/handler.go", Added: 1, Removed: 1, Lines: []core.DiffLine{
		{Op: " ", Old: 87, New: 87, Text: "if !res.OK() {"},
		{Op: "-", Old: 88, Text: `http.Error(w, "rate limited", 429)`},
		{Op: "+", New: 88, Text: "secs := int(time.Until(res.ResetAt()).Seconds())"},
	}}
	for _, ev := range []agent.Event{
		agent.TurnStart{},
		agent.Message{ID: "m0", Role: core.ItemUser, Text: "Add Retry-After to the 429 path"},
		agent.Message{ID: "m1", Role: core.ItemAssistant, Text: "Reading the limiter to see what it exposes."},
		agent.Message{ID: "th", Role: core.ItemThinking, Text: "The limiter has a ResetAt already."},
		agent.ToolCall{ID: "t1", Tool: "Read", Input: toolInput(map[string]string{"file_path": "internal/ratelimit/limiter.go"})},
		agent.ToolResult{ID: "t1", Output: "package ratelimit\n\nfunc New() {}"},
		agent.ToolCall{ID: "t2", Tool: "Edit", Input: toolInput(map[string]string{"file_path": "internal/api/usage/handler.go"})},
		agent.ToolResult{ID: "t2", Diff: diff},
		agent.ToolCall{ID: "t3", Tool: "Bash", Input: toolInput(map[string]string{"command": "go test ./..."})},
		agent.ToolResult{ID: "t3", Output: "ok  \tpricing\t0.2s"},
		agent.Subagent{ID: "s1", Description: "Find every 429 path", Type: "explorer"},
		agent.Failure{Err: "the API is overloaded"},
	} {
		a.Emit(ev)
	}
	r.waitFrame("every block", func(s string) bool {
		return containsAll(s, "Reading the limiter to see what it exposes.", "Read", "internal/ratelimit/limiter.go", "3 lines",
			"Edit", "+1", "-1", "view in diff ↗", "secs := int(time.Until(res.ResetAt()).Seconds())", "Bash", "go test ./...", "SUBAGENTS", "1 running",
			"explorer", "Find every 429 path", "the API is overloaded", "WORKING")
	})
	if strings.Contains(r.waitText("Bash"), "0.2s") {
		t.Error("shell output shows before it's unfolded")
	}
	r.clickText("Bash")
	r.waitText("ok      pricing    0.2s")
	r.typeKeys("kkk<CR>") // from the shell call clicked: the read, the edit, then the thinking
	r.waitText("The limiter has a ResetAt already.")
	r.typeKeys("kk") // the prose, then the prompt, scrolled into view
	r.waitFrame("the prompt", func(s string) bool { return containsAll(s, "YOU", "Add Retry-After to the 429 path") })
}

// AI-7: a permission's card answers the agent: Allow once on a click, Always on a, and ⇧n's note goes back with the
// denial; each card goes once it's answered.
func TestPermissionCardsAnswerTheAgent(t *testing.T) {
	r, a, _ := consoleSession(t)
	allow := a.Ask(agent.Permission{ID: "p1", Tool: "Bash", Input: toolInput(map[string]string{"command": "go test ./internal/api/... -run TestUsage"})})
	r.waitFrame("the permission card", func(s string) bool {
		return containsAll(s, "Fake wants to run a command", "$ go test ./internal/api/... -run TestUsage", "rule: Bash(go test:*)", "WANTS PERMISSION")
	})
	r.clickText("Allow once")
	if d := waitDecision(t, allow); !d.Allow || d.Always {
		t.Errorf("Allow once answered %+v", d)
	}
	r.waitGone("Fake wants to run a command")

	always := a.Ask(agent.Permission{ID: "p2", Tool: "Bash", Input: toolInput(map[string]string{"command": "go vet ./..."})})
	r.waitText("$ go vet ./...")
	r.typeKeys("a")
	if d := waitDecision(t, always); !d.Allow || !d.Always {
		t.Errorf("a answered %+v", d)
	}
	r.waitText("BASH(GO VET:*)") // the rule it made, in force

	deny := a.Ask(agent.Permission{ID: "p3", Tool: "Bash", Input: toolInput(map[string]string{"command": "rm -rf build"})})
	r.waitText("$ rm -rf build")
	r.typeKeys("N")
	r.waitText("Deny with this note")
	r.typeText("use make clean")
	r.typeKeys("<CR>")
	if d := waitDecision(t, deny); d.Allow || !strings.Contains(d.Note, "use make clean") {
		t.Errorf("the note's denial answered %+v", d)
	}
	r.waitGone("$ rm -rf build")
}

// AI-7: with the console hidden, an ask's card stacks over whatever shows, with its session's name, and answers from
// there; its session's tab counts it.
func TestAskCardsInterruptAnyView(t *testing.T) {
	r, a, _ := consoleSession(t)
	r.typeKeys("<D-A>")
	r.waitGone("SESSION STATS")
	deny := a.Ask(agent.Permission{ID: "p1", Tool: "Bash", Input: toolInput(map[string]string{"command": "curl example.com"})})
	r.waitFrame("the interrupt card", func(s string) bool { return containsAll(s, "W · PERMISSION", "$ curl example.com") })
	r.clickText("Deny")
	if d := waitDecision(t, deny); d.Allow {
		t.Errorf("Deny answered %+v", d)
	}
	r.waitGone("$ curl example.com")
}

// AI-7: a question's options are buttons; a click on one answers it.
func TestQuestionCardAnswers(t *testing.T) {
	r, a, _ := consoleSession(t)
	ch := a.Expect("q1")
	q := "Is RoundMinor safe to call for JPY amounts?"
	a.Emit(agent.Question{ID: "q1", Questions: []core.Question{{Question: q, Options: []string{"Safe: precision comes from the currency table", "Not yet"}}}})
	r.waitFrame("the question", func(s string) bool { return containsAll(s, "Fake asks", q, "NEEDS YOU") })
	r.clickText("Not yet")
	if d := waitDecision(t, ch); d.Answers[q] != "Not yet" {
		t.Errorf("answered %+v", d)
	}
	r.waitGone(q)
}

// AI-6: Interrupt in the console's header interrupts the agent's turn.
func TestConsoleInterrupts(t *testing.T) {
	r, a, _ := consoleSession(t)
	a.Emit(agent.TurnStart{})
	r.waitText("WORKING")
	r.clickText("Interrupt")
	deadline := time.Now().Add(5 * time.Second)
	for a.Interrupts() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the agent was never interrupted")
		}
		time.Sleep(5 * time.Millisecond)
	}
	r.waitText("IDLE")
}

// AI-6: a message written while the agent works waits as QUEUED, and goes to the agent when its turn ends.
func TestComposerQueuesWhileWorking(t *testing.T) {
	r, a, _ := consoleSession(t)
	a.Emit(agent.TurnStart{})
	r.waitText("WORKING")
	r.typeKeys("i")
	r.typeText("also check routes.go")
	r.typeKeys("<CR>")
	r.waitText("QUEUED")
	r.waitText("also check routes.go")
	a.NoneSent(t)
	a.Emit(agent.TurnEnd{})
	if in := a.WaitSent(t); in.Text != "also check routes.go" {
		t.Errorf("sent %q", in.Text)
	}
	r.waitGone("QUEUED")
}

func waitDecision(t *testing.T, ch chan agent.Decision) agent.Decision {
	t.Helper()
	select {
	case d := <-ch:
		return d
	case <-time.After(10 * time.Second):
		t.Fatal("the agent never got an answer")
	}
	return agent.Decision{}
}

// pipeSource is a terminal source a test writes into, and reads what was typed from.
type pipeSource struct {
	out   *io.PipeReader
	in    *io.PipeWriter
	typed chan []byte
}

func (p *pipeSource) Read(b []byte) (int, error) { return p.out.Read(b) }
func (p *pipeSource) Write(b []byte) (int, error) {
	p.typed <- append([]byte(nil), b...)
	return len(b), nil
}
func (p *pipeSource) Resize(int, int) error { return nil }
func (p *pipeSource) Close() error          { return p.out.Close() }

// A terminal view shows any source of bytes, not only a program it starts: the agent's raw terminal is one.
func TestTerminalShowsASource(t *testing.T) {
	r := runEditor(t, map[string]string{"a.txt": "hi\n"}, []string{"a.txt"})
	pr, pw := io.Pipe()
	src := &pipeSource{out: pr, in: pw, typed: make(chan []byte, 16)}
	t.Cleanup(func() { pw.Close() })
	if err := r.do(func() error {
		return r.host.Run(context.Background(), "term.open", registry.Args{"source": TermSource(src), "title": "agent · w"})
	}); err != nil {
		t.Fatal(err)
	}
	go pw.Write([]byte("hello from elsewhere\r\n"))
	r.waitText("hello from elsewhere")
	r.typeKeys("x")
	select {
	case b := <-src.typed:
		if string(b) != "x" {
			t.Errorf("typed %q", b)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("what was typed never reached the source")
	}
}
