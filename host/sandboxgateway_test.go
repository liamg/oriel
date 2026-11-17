package host

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostapi"
	"github.com/liamg/oriel/sandbox/mcp"
)

// allowed calls a gateway tool as the sandboxed agent does, allowing it once if it asks: what the agent gets back.
func (s *sandboxRig) allowed(id core.SessionID, root, tool string, args any) mcp.CallResult {
	s.t.Helper()
	ch := make(chan mcp.CallResult, 1)
	go func() { ch <- gatewayCall(s.t, root, tool, args) }()
	var got *mcp.CallResult
	until(s.t, tool+" to answer or ask", func() bool {
		select {
		case r := <-ch:
			got = &r
			return true
		default:
		}
		se := s.session(id)
		if len(se.Asks) == 1 && se.Asks[0].Kind == core.AskGateway && se.Asks[0].Tool == tool {
			if err := s.w.Answer(hostapi.AnswerReq{Session: id, Ask: se.Asks[0].ID, Decision: "allow"}); err != nil {
				s.t.Fatal(err)
			}
			r := <-ch
			got = &r
			return true
		}
		return false
	})
	return *got
}

// The agent in the sandbox keeps its branch in step with origin through the gateway: oriel__fetch and oriel__pull
// run git on the host, fast-forwarding to what someone else pushed; oriel__pr_create with no title and no saved
// draft says to save one.
func TestGatewayFetchesAndPullsForTheAgent(t *testing.T) {
	s := newSandboxRig(t)
	id, _, b := s.sandboxed()
	p, _ := s.session(id).Primary()
	if err := os.WriteFile(filepath.Join(p.Dir, "new.go"), []byte("package pricing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := s.allowed(id, b.Root(), "oriel__commit", map[string]any{"message": "Add new.go"}); r.IsError {
		t.Fatalf("commit: %s", r.Text())
	}
	if r := s.allowed(id, b.Root(), "oriel__push", map[string]any{}); r.IsError || r.Text() != "Pushed." {
		t.Fatalf("push: %s", r.Text())
	}

	// Someone else pushes to the branch.
	other := filepath.Join(t.TempDir(), "other")
	git := func(dir string, args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if out, err := exec.Command("git", "clone", "-q", "-b", p.Branch, s.remote, other).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(other, "theirs.go"), []byte("package pricing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(other, "add", "theirs.go")
	git(other, "commit", "-q", "-m", "Theirs")
	git(other, "push", "-q", "origin", p.Branch)

	if r := s.allowed(id, b.Root(), "oriel__fetch", map[string]any{}); r.IsError || r.Text() != "Fetched." {
		t.Fatalf("fetch: %s", r.Text())
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "theirs.go")); err == nil {
		t.Fatal("a fetch changed the worktree")
	}
	if r := s.allowed(id, b.Root(), "oriel__pull", map[string]any{}); r.IsError || r.Text() != "Pulled." {
		t.Fatalf("pull: %s", r.Text())
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "theirs.go")); err != nil {
		t.Fatalf("the pull didn't bring their commit: %v", err)
	}

	if r := s.allowed(id, b.Root(), "oriel__pr_create", map[string]any{}); !r.IsError || !strings.Contains(r.Text(), "draft") {
		t.Errorf("pr_create with no draft: %q", r.Text())
	}
}

// oriel__ask_user_to_run, allowed to run, runs the command on the host in the workspace (or the cwd it names) and
// gives the agent its output; oriel__ssh_agent opens the host's agent for the minutes asked, and says plainly when
// the host has none.
func TestGatewayRunsOnTheHostAndLendsTheSSHAgent(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	s := newSandboxRig(t)
	id, _, b := s.sandboxed()
	if err := os.MkdirAll(filepath.Join(b.Root(), "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	r := s.allowed(id, b.Root(), "oriel__ask_user_to_run", map[string]any{"command": "pwd", "why": "where am I", "cwd": "sub"})
	if r.IsError || !strings.Contains(r.Text(), filepath.Join(b.Root(), "sub")) {
		t.Errorf("ask_user_to_run: %q", r.Text())
	}

	if r := s.allowed(id, b.Root(), "oriel__ssh_agent", map[string]any{"why": "private deps", "minutes": 2}); !r.IsError ||
		!strings.Contains(r.Text(), "there is no ssh-agent on the host") {
		t.Errorf("ssh_agent with none on the host: %q", r.Text())
	}
	// A host agent that's listening: the grant only checks it answers a dial. The session socket's short home keeps
	// this path under the unix limit.
	sock := filepath.Join(os.Getenv("HOME"), "agent.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() }) // the test is done with it
	t.Setenv("SSH_AUTH_SOCK", sock)
	if r := s.allowed(id, b.Root(), "oriel__ssh_agent", map[string]any{"why": "private deps", "minutes": 2}); r.IsError ||
		!strings.Contains(r.Text(), "The ssh-agent is available until") {
		t.Errorf("ssh_agent: %q", r.Text())
	}
}
