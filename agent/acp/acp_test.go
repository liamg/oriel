package acp

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/acp/acptest"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/agent/hostexec"
	"github.com/liamg/oriel/agent/rpcagent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/internal/isolate"
)

// fakeProvider runs the test binary as the fake ACP agent, its sessions kept in a directory of the test's.
func fakeProvider(t *testing.T) *Provider {
	return &Provider{Name: "fakeacp", Config: Config{Command: []string{os.Args[0]},
		Env: map[string]string{acptest.Env: "1", acptest.DirEnv: t.TempDir()}, Login: "fakeacp login"}}
}

func harness(p agent.Provider) agenttest.Harness {
	return agenttest.Harness{Provider: p, Spec: func(t *testing.T, dir string) agent.StartSpec {
		return agent.StartSpec{Exec: hostexec.Exec{}, Policy: agent.PolicyAsk, StateDir: t.TempDir()}
	}, Timeout: 20 * time.Second}
}

// ref:   acp/docs/overview.mdx
// quote: "Clients provide the interface between users and agents."
func TestConformance(t *testing.T) {
	agenttest.Run(t, harness(fakeProvider(t)))
}

// The bridge as Oriel ships it: the ACP provider served over the Oriel agent protocol.
func TestConformanceOverTheAgentProtocol(t *testing.T) {
	isolate.Test(t)
	p := fakeProvider(t)
	ar, bw := io.Pipe()
	br, aw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- rpcagent.Serve(br, bw, func(in rpcagent.Init) (agent.Provider, error) { return p, nil })
	}()
	c, err := rpcagent.Dial(context.Background(), rpcagent.Options{Name: "fakeacp", Dial: rpcagent.Pipe(ar, aw)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.(interface{ Close() error }).Close(); bw.Close(); <-done })
	agenttest.Run(t, harness(c))
}

func start(t *testing.T, spec agent.StartSpec) *agenttest.Session {
	t.Helper()
	dir := t.TempDir()
	h := harness(fakeProvider(t))
	inner := h.Spec
	h.Spec = func(t *testing.T, d string) agent.StartSpec {
		s := inner(t, d)
		s.Protect, s.Sandbox, s.Pack, s.Model = spec.Protect, spec.Sandbox, spec.Pack, spec.Model
		return s
	}
	return agenttest.StartIn(t, h, dir, "")
}

// ref:   acp/docs/file-system.mdx#Writing Files
// quote: "The Client **MUST** create the file if it doesn't exist."
// ref:   acp/docs/tool-calls.mdx#Diffs
// quote: "File modifications shown as diffs"
// ref:   acp/docs/agent-plan.mdx#Creating Plans
// quote: "When the language model creates an execution plan, the Agent **SHOULD** report it to the Client"
// The agent reads and writes the workspace's files through Oriel, its edit comes back as a numbered diff, and its
// plan as a call of plan's.
func TestFilesDiffsAndPlans(t *testing.T) {
	s := start(t, agent.StartSpec{})
	dir := s.S.(*session).spec.Cwd
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\nthree\n"), 0o644)
	s.Send("edit a.txt")
	if got := s.Reply(); !strings.Contains(got, "line 2 was two") {
		t.Errorf("reply = %q, want the line it read", got)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(b) != "one\nTWO\nthree\n" {
		t.Errorf("a.txt = %q", b)
	}
	var diff *core.FileDiff
	plan := false
	for _, ev := range s.Events {
		switch e := ev.(type) {
		case agent.ToolResult:
			if e.Diff != nil {
				diff = e.Diff
			}
		case agent.ToolCall:
			plan = plan || (e.Tool == "plan" && strings.Contains(string(e.Input), "Edit a.txt"))
			if e.ID == "call-edit" && e.Tool != "Edit" {
				t.Errorf("an edit is called %q", e.Tool)
			}
		}
	}
	want := []core.DiffLine{{Op: "-", Old: 2, Text: "two"}, {Op: "+", New: 2, Text: "TWO"}}
	if diff == nil || diff.Added != 1 || diff.Removed != 1 || len(diff.Lines) != 2 || diff.Lines[0] != want[0] || diff.Lines[1] != want[1] {
		t.Errorf("diff = %+v", diff)
	}
	if !plan {
		t.Error("the plan didn't come through")
	}
}

// ref:   acp/docs/session-setup.mdx#Working Directory
// quote: "This root set **SHOULD** serve as a boundary for tool operations on the file system."
// The agent writes nothing outside its workspace through Oriel, nor in a directory it must never edit.
func TestWritesStayInTheWorkspace(t *testing.T) {
	s := start(t, agent.StartSpec{})
	outside := filepath.Join(t.TempDir(), "x")
	s.Send("write " + outside)
	if got := s.Reply(); !strings.Contains(got, "outside the session's workspace") {
		t.Errorf("writing outside: %q", got)
	}
	if _, err := os.Stat(outside); err == nil {
		t.Error("it wrote outside the workspace")
	}

	p := start(t, agent.StartSpec{})
	pdir := p.S.(*session).spec.Cwd
	p.S.(*session).spec.Protect = []string{pdir}
	p.Send("write " + filepath.Join(pdir, "y"))
	if got := p.Reply(); !strings.Contains(got, "must never edit") {
		t.Errorf("writing a protected directory: %q", got)
	}
}

// ref:   acp/docs/terminals.mdx#Executing Commands
// quote: "The `terminal/create` method starts a command in a new terminal"
// In a sandbox the agent may run commands through Oriel, which runs them through the session's Exec; outside one
// it isn't offered terminals.
func TestTerminalsInASandbox(t *testing.T) {
	s := start(t, agent.StartSpec{Sandbox: &agent.Sandbox{Name: "box"}})
	s.Send("run in a terminal")
	if got := s.Reply(); !strings.Contains(got, "exit 3") || !strings.Contains(got, "out") || !strings.Contains(got, "err") {
		t.Errorf("in a sandbox: %q", got)
	}
	o := start(t, agent.StartSpec{})
	o.Send("run in a terminal")
	if got := o.Reply(); !strings.Contains(got, "no terminals") {
		t.Errorf("outside one: %q", got)
	}
}

// ref:   acp/docs/content.mdx#Image Content
// quote: "Images can be included for visual context or analysis."
// An agent that takes images gets them as images.
func TestImages(t *testing.T) {
	s := start(t, agent.StartSpec{})
	img := filepath.Join(t.TempDir(), "shot.png")
	os.WriteFile(img, []byte("\x89PNG"), 0o644)
	if err := s.S.Send(context.Background(), agent.Input{Text: "look", Images: []string{img}}); err != nil {
		t.Fatal(err)
	}
	if got := s.Reply(); !strings.Contains(got, "1 images") {
		t.Errorf("reply = %q", got)
	}
}

// ref:   acp/docs/session-setup.mdx#Loading a Session
// quote: "The Agent **MUST** replay the entire conversation to the Client in the form of `session/update` notifications (like `session/prompt`)."
// A resumed session's history isn't said again: Oriel has it.
func TestLoadDoesNotRepeatTheHistory(t *testing.T) {
	dir := t.TempDir()
	h := harness(fakeProvider(t))
	s := agenttest.StartIn(t, h, dir, "")
	s.Send("say hello")
	s.Reply()
	id := s.S.ID()
	s.S.Close()
	r := agenttest.StartIn(t, h, dir, id)
	r.Send("say hello")
	r.Reply()
	for _, ev := range r.Events {
		if m, ok := ev.(agent.Message); ok && strings.Contains(m.Text, "replayed") {
			t.Errorf("the history came again: %+v", m)
		}
	}
}

// ref:   acp/docs/prompt-turn.mdx#Session Usage Updates
// quote: "`used` and `size` are required and non-null token counts for the current session context."
// A turn's end says the context the agent last reported, and its thinking streams as thinking.
func TestUsageAndThinking(t *testing.T) {
	s := start(t, agent.StartSpec{})
	s.Send("say hello")
	s.Reply()
	thought := false
	for _, ev := range s.Events {
		switch e := ev.(type) {
		case agent.TurnEnd:
			if e.Usage.Context != 1200 || e.Usage.ContextMax != 200000 || e.Usage.Cost != 0.01 {
				t.Errorf("usage = %+v", e.Usage)
			}
		case agent.Message:
			thought = thought || (e.Role == core.ItemThinking && e.Text == "greeting" && !e.Partial)
		}
	}
	if !thought {
		t.Error("no thinking")
	}
}

// ref:   acp/docs/tool-calls.mdx#Permission Options
// quote: "`allow_always` - Allow this operation and remember the choice"
// Always is allow_always; bypass asks no one.
func TestPermissionOptions(t *testing.T) {
	s := start(t, agent.StartSpec{})
	s.Send("run a command")
	p := s.Wait("the permission", func(ev agent.Event) bool { _, ok := ev.(agent.Permission); return ok }).(agent.Permission)
	if p.Tool != "Bash" || !strings.Contains(string(p.Input), "go test") {
		t.Errorf("permission = %+v", p)
	}
	s.S.Answer(context.Background(), p.ID, agent.Decision{Allow: true, Always: true})
	if got := s.Reply(); !strings.Contains(got, "ran: ok") {
		t.Errorf("reply = %q", got)
	}

	b := start(t, agent.StartSpec{})
	b.S.(*session).spec.Policy = agent.PolicyBypass
	b.Send("run a command")
	if got := b.Reply(); !strings.Contains(got, "ran: ok") {
		t.Errorf("bypassed: %q", got)
	}
	for _, ev := range b.Events {
		if _, ok := ev.(agent.Permission); ok {
			t.Error("bypass asked")
		}
	}
}

// ref:   acp/docs/tool-calls.mdx#Permission Options
// quote: "`reject_once` - Reject this operation only this time"
// Refuse asks no one either: what it doesn't allow ahead is rejected at once, what it does goes ahead.
func TestRefuseAsksNobody(t *testing.T) {
	for _, allow := range []bool{false, true} {
		s := start(t, agent.StartSpec{})
		s.S.(*session).spec.Policy = agent.PolicyRefuse
		if allow {
			s.S.(*session).spec.Allow = []string{"Bash"}
		}
		s.Send("run a command")
		got := s.Reply()
		if want := map[bool]string{false: "denied", true: "ran: ok"}[allow]; !strings.Contains(got, want) {
			t.Errorf("allowed %v: %q, want %q", allow, got, want)
		}
		for _, ev := range s.Events {
			if _, ok := ev.(agent.Permission); ok {
				t.Errorf("allowed %v: refuse asked", allow)
			}
		}
	}
}
