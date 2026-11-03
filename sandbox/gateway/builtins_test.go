package gateway

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/sandbox/mcp"
)

// fakeHost is a workspace with one project, "demo", whose gh is the one on
// PATH. It records what it was asked to do.
type fakeHost struct {
	dir     string
	commits []string
	ran     []string
}

func newFakeHost(t *testing.T) *fakeHost { return &fakeHost{dir: t.TempDir()} }

func (f *fakeHost) project(name string) error {
	if name != "" && name != "demo" {
		return fmt.Errorf("%w %q", ErrNoProject, name)
	}
	return nil
}

func (f *fakeHost) Approve(context.Context, Request) (Answer, error) {
	return Answer{Decision: Once}, nil
}

func (f *fakeHost) Commit(_ context.Context, project, message string, files []string) (int, error) {
	if err := f.project(project); err != nil {
		return 0, err
	}
	if len(files) == 0 {
		files = []string{"b.txt"}
	}
	f.commits = append(f.commits, message)
	return len(files), nil
}

func (f *fakeHost) Push(_ context.Context, project string) error  { return f.project(project) }
func (f *fakeHost) Pull(_ context.Context, project string) error  { return f.project(project) }
func (f *fakeHost) Fetch(_ context.Context, project string) error { return f.project(project) }

func (f *fakeHost) CreatePR(_ context.Context, project, title, _ string) (string, error) {
	if err := f.project(project); err != nil {
		return "", err
	}
	if title == "" {
		return "", ErrNoDraft
	}
	return "https://github.com/o/r/pull/1", nil
}

func (f *fakeHost) GH(ctx context.Context, project string, args ...string) (string, error) {
	if err := f.project(project); err != nil {
		return "", err
	}
	return RunGH(ctx, f.dir, nil, args...)
}

func (f *fakeHost) Repos(context.Context) ([]Repo, error) { return nil, nil }

func (f *fakeHost) AddProject(_ context.Context, repo string) (Added, error) {
	return Added{Name: "other", Dir: "other", Tools: []string{"go@1.25"}}, nil
}

func (f *fakeHost) RunOnHost(ctx context.Context, command, _ string) mcp.CallResult {
	f.ran = append(f.ran, command)
	return HostRun(ctx, f.dir, command)
}

func (f *fakeHost) RunTail() string { return "$ make up\nready" }

func (f *fakeHost) SSHAgent(context.Context, time.Duration) (time.Time, error) {
	return time.Date(2026, 1, 1, 12, 0, 0, 0, time.Local), nil
}

func (f *fakeHost) RequestHost(context.Context, string) (bool, string) {
	return false, "the user refused; do not ask again this session"
}

func runTool(t *testing.T, h Host, tool, args string) mcp.CallResult {
	t.Helper()
	b, ok := Builtins(h)[tool]
	if !ok {
		t.Fatalf("no builtin %s", tool)
	}
	res, err := b.Run(t.Context(), []byte(args))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestCommitToolCommits(t *testing.T) {
	h := newFakeHost(t)
	res := runTool(t, h, "oriel__commit", `{"message":"add b"}`)
	if res.IsError || res.Text() != "Committed 1 file(s)." {
		t.Fatalf("commit: %q", res.Text())
	}
	if len(h.commits) != 1 || h.commits[0] != "add b" {
		t.Fatalf("commits = %q", h.commits)
	}
}

func TestHostToolsNameTheirProject(t *testing.T) {
	h := newFakeHost(t)
	for _, tool := range []string{"oriel__fetch", "oriel__commit", "oriel__pr_create", "oriel__ci"} {
		res := runTool(t, h, tool, `{"project":"nope","message":"m","files":["a"],"title":"t"}`)
		if !res.IsError || !strings.Contains(res.Text(), `no such project "nope"`) {
			t.Errorf("%s: %q", tool, res.Text())
		}
	}
}

func TestAskUserToRun(t *testing.T) {
	h := newFakeHost(t)
	g := &Gateway{Builtins: Builtins(h), Approve: func(context.Context, Request) (Answer, error) {
		return Answer{Decision: Once, Note: "done it"}, nil
	}}
	res, _ := g.Call(t.Context(), "oriel__ask_user_to_run", []byte(`{"command":"make up","why":"docker"}`))
	if res.IsError || !strings.Contains(res.Text(), "The user says they ran it. Their note: done it") || !strings.Contains(res.Text(), "ready") {
		t.Fatalf("ran it themselves: %q", res.Text())
	}
	if len(h.ran) != 0 {
		t.Fatalf("ran on the host without Run: %q", h.ran)
	}
	g.Approve = func(context.Context, Request) (Answer, error) { return Answer{Decision: Once, Run: true}, nil }
	res, _ = g.Call(t.Context(), "oriel__ask_user_to_run", []byte(`{"command":"echo hi","why":"docker"}`))
	if res.IsError || res.Text() != "Ran on the host.\n\nhi\n" {
		t.Fatalf("run on host: %q", res.Text())
	}
}

func TestHostRunFails(t *testing.T) {
	res := HostRun(t.Context(), t.TempDir(), "echo no; exit 3")
	if !res.IsError || !strings.Contains(res.Text(), "Ran on the host and failed") || !strings.Contains(res.Text(), "no") {
		t.Fatalf("failed run: %q", res.Text())
	}
	missing := HostRun(t.Context(), "/nonexistent/dir", "true")
	if !missing.IsError || !strings.Contains(missing.Text(), "no such directory") {
		t.Fatalf("missing dir: %q", missing.Text())
	}
}

func TestAddProjectSaysToEndTheTurn(t *testing.T) {
	res := runTool(t, newFakeHost(t), "oriel__add_project", `{"repo":"/src/other"}`)
	if res.IsError || !strings.Contains(res.Text(), "Added other at other. Installed go@1.25.") || !strings.Contains(res.Text(), "end your turn now") {
		t.Fatalf("add_project: %q", res.Text())
	}
}

func TestRequestHostIsDirect(t *testing.T) {
	h := newFakeHost(t)
	asked := false
	g := &Gateway{Builtins: Builtins(h), Approve: func(context.Context, Request) (Answer, error) {
		asked = true
		return Answer{Decision: Once}, nil
	}}
	res, _ := g.Call(t.Context(), "oriel__request_host", []byte(`{"why":"needs docker"}`))
	if asked || !res.IsError || !strings.Contains(res.Text(), "refused") {
		t.Fatalf("request_host asked=%v %q", asked, res.Text())
	}
}

func TestSSHAgentNamesTheSocket(t *testing.T) {
	res := runTool(t, newFakeHost(t), "oriel__ssh_agent", `{"why":"private module","minutes":2}`)
	if res.IsError || !strings.Contains(res.Text(), "until 12:00:00") || !strings.Contains(res.Text(), "$SSH_AUTH_SOCK") {
		t.Fatalf("ssh_agent: %q", res.Text())
	}
}

func TestFailedRunIDs(t *testing.T) {
	checks := "build\tfail\t1m\thttps://github.com/o/r/actions/runs/11/job/1\nlint\tpass\t1m\thttps://github.com/o/r/actions/runs/12/job/2\ntest\tfail\t1m\thttps://github.com/o/r/actions/runs/11/job/3\n"
	if got := failedRunIDs(checks); len(got) != 1 || got[0] != "11" {
		t.Fatalf("failed runs %v", got)
	}
}
