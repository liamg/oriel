package host

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sandbox/egress"
)

// imageBackend is a fake backend with an image: it describes itself and rebuilds.
type imageBackend struct {
	fakeBackend
	rebuilt chan string
}

func (imageBackend) Describe(_ context.Context, root string, _ sandbox.Config) sandbox.Description {
	return sandbox.Description{Container: "oriel-x", Image: "img:1", Running: true, Places: map[string]string{sandbox.TmpDir(root): "/tmp/claude-1"}}
}

func (imageBackend) Shell(_ context.Context, root, cwd string, _ sandbox.LaunchOptions) (sandbox.Command, error) {
	return sandbox.Command{Argv: []string{"shell-in", root, cwd}}, nil
}

func (b imageBackend) Rebuild(_ context.Context, root string, _ sandbox.Config) error {
	b.rebuilt <- root
	return nil
}

// A sandboxed session's workspace has its projects, the sandbox's directories with where the agent finds them, and
// the backend's description; a rebuild builds the image and starts the agent again on it.
func TestWorkspaceDescribesTheSandbox(t *testing.T) {
	s := newSandboxRig(t)
	ib := imageBackend{fakeBackend{s}, make(chan string, 1)}
	s.f.Backend = func(sandbox.Config) (sandbox.Backend, error) { return ib, nil }
	id, _, b := s.sandboxed()
	ws, err := s.w.Workspace(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !ws.Sandboxed || !ws.Running || ws.Backend != "fake" || ws.Image != "img:1" || !ws.Rebuild || ws.Root != b.root {
		t.Errorf("workspace = %+v", ws)
	}
	if c, err := s.w.SandboxShell(context.Background(), id, ""); err != nil || len(c.Argv) != 3 || c.Argv[2] != b.root {
		t.Errorf("the sandbox's shell = %+v, %v", c, err)
	}
	i := slices.IndexFunc(ws.Paths, func(p WorkspacePath) bool { return p.Name == "Scratchpad" })
	if i < 0 || ws.Paths[i].Host != sandbox.TmpDir(b.root) || ws.Paths[i].Agent != "/tmp/claude-1" {
		t.Errorf("paths = %+v", ws.Paths)
	}
	if len(ws.Paths) == 0 || ws.Paths[0].Host == "" || ws.Paths[0].Name == "Scratchpad" {
		t.Errorf("the project doesn't lead the paths: %+v", ws.Paths)
	}
	if err := s.w.RebuildSandbox(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if got := <-ib.rebuilt; got != b.root {
		t.Errorf("rebuilt %q", got)
	}
	for deadline := time.Now().Add(5 * time.Second); s.w.boxOf(id) == b || s.w.boxOf(id) == nil; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the agent didn't start again on the rebuilt image")
		}
	}
}

// A session whose agent runs on the host has its directories, and no sandbox.
func TestWorkspaceUnsandboxed(t *testing.T) {
	r := newRig(t)
	id := r.start(core.SessionSpec{Kind: core.KindDirect, Dir: r.repo.Dir})
	ws, err := r.w.Workspace(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if ws.Sandboxed || ws.Root == "" {
		t.Errorf("workspace = %+v", ws)
	}
	if _, err := r.w.SandboxShell(context.Background(), id, ""); err == nil {
		t.Error("a shell in a sandbox that isn't there")
	}
	if err := r.w.RestartSandbox(id); err == nil {
		t.Error("restarted a sandbox that isn't there")
	}
}

// A session started with its network open still carries its connections through the host's egress session, which
// allows them all, private addresses too, asking nothing.
func TestOpenNetworkStillGoesThroughTheHost(t *testing.T) {
	s := newSandboxRig(t)
	id := s.start(core.SessionSpec{Kind: core.KindWork, Prompt: "Work", Provider: "fake", Repos: []core.RepoID{core.RepoID(s.repo.Dir)},
		Sandbox: core.SandboxPolicy{Container: true, Network: "open"}})
	s.agent()
	b := s.keptBox(id)
	if !b.egress.Open {
		t.Fatal("an open session's egress isn't open")
	}
	if b.egress.Decide(context.Background(), "litellm.internal.example.io", 443) != egress.Allow {
		t.Error("an open session refused a host")
	}
	if se := s.session(id); len(se.Asks) != 0 {
		t.Errorf("an open session asked: %+v", se.Asks)
	}
	if p, _ := s.w.Permissions(context.Background(), id); p.Filtered {
		t.Error("an open session reads as filtered")
	}
}

// A plan session reads its projects' checkouts in place until Start work: the sandbox's manifest has them, so the
// agent finds them at their own paths, read-only.
func TestPlanSessionReadsItsCheckouts(t *testing.T) {
	s := newSandboxRig(t)
	id := s.start(core.SessionSpec{Kind: core.KindPlan, Prompt: "Plan", Provider: "fake", Repos: []core.RepoID{core.RepoID(s.repo.Dir)},
		Sandbox: core.SandboxPolicy{Container: true, Network: "ask"}})
	s.agent()
	b := s.keptBox(id)
	m, ok := sandbox.LoadManifest(b.root)
	if !ok || !slices.Contains(m.Reads, s.repo.Dir) {
		t.Errorf("the manifest reads %v, want the checkout %s (projects %+v)", m.Reads, s.repo.Dir, m.Projects)
	}
}
