package host

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/sandbox"
)

func fill(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func there(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// An agent ending empties its sandbox's temp dir; its home and caches stay for the next run.
func TestAgentExitEmptiesSandboxTmp(t *testing.T) {
	s := newSandboxRig(t)
	id, _, b := s.sandboxed()
	fill(t, filepath.Join(sandbox.TmpDir(b.Root()), "scratchpad", "notes.md"))
	fill(t, filepath.Join(sandbox.HomeDir(b.Root()), ".cache", "go-build", "x"))
	if err := s.w.Stop(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(sandbox.TmpDir(b.Root())); len(entries) != 0 {
		t.Errorf("tmp still holds %v", entries)
	}
	if !there(filepath.Join(sandbox.HomeDir(b.Root()), ".cache", "go-build", "x")) {
		t.Error("stopping took the home's caches")
	}
}

// Archiving a session empties the sandbox's temp and home caches and keeps the rest, so a
// reopen works.
func TestArchiveCleansTheSandbox(t *testing.T) {
	s := newSandboxRig(t)
	id, _, b := s.sandboxed()
	fill(t, filepath.Join(sandbox.TmpDir(b.Root()), "scratchpad", "notes.md"))
	fill(t, filepath.Join(sandbox.HomeDir(b.Root()), ".npm", "x"))
	fill(t, filepath.Join(sandbox.HomeDir(b.Root()), ".gitconfig"))
	if err := s.w.End(context.Background(), EndReq{Session: id, Discard: true}); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(sandbox.TmpDir(b.Root())); len(entries) != 0 {
		t.Errorf("tmp still holds %v", entries)
	}
	if there(filepath.Join(sandbox.HomeDir(b.Root()), ".npm")) {
		t.Error("the home's caches stayed")
	}
	if !there(filepath.Join(sandbox.HomeDir(b.Root()), ".gitconfig")) || !sandbox.Managed(b.Root()) {
		t.Error("archiving took what a reopen needs")
	}
}

// launch runs the agent's command in its sandbox, as a provider does, and waits for the backend's launch.
func (s *sandboxRig) launch(spec *agent.StartSpec) {
	s.t.Helper()
	p, err := spec.Exec.Start(context.Background(), exec.Command("claude"))
	if err != nil {
		s.t.Fatal(err)
	}
	_, _ = io.ReadAll(p.Stdout())
	_ = p.Wait()
	<-s.launched
	<-s.opts
}

// Archiving a session takes its sandbox down (the backend's containers, network and volume), and reopening it
// launches the sandbox again, in the same place.
func TestArchiveStopsTheSandboxAndReopeningLaunchesItAgain(t *testing.T) {
	s := newSandboxRig(t)
	s.stopped = make(chan string, 4)
	id, spec, b := s.sandboxed()
	s.launch(spec)
	if err := s.w.End(context.Background(), EndReq{Session: id, Discard: true}); err != nil {
		t.Fatal(err)
	}
	select {
	case root := <-s.stopped:
		if root != b.Root() {
			t.Errorf("stopped %s, not the session's sandbox %s", root, b.Root())
		}
	default:
		t.Fatal("archiving didn't stop the sandbox")
	}
	if err := s.w.Resume(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	a := s.agent()
	again := s.keptBox(id)
	if again == b || again.Root() != b.Root() {
		t.Errorf("reopened in a new sandbox %v at %s, want one at %s", again != b, again.Root(), b.Root())
	}
	s.launch(&a.Spec)
}

// Deleting a session removes the sandbox's directory with the workspace.
func TestDeleteRemovesTheSandbox(t *testing.T) {
	s := newSandboxRig(t)
	id, _, b := s.sandboxed()
	if !there(sandbox.Dir(b.Root())) {
		t.Fatal("the session has no sandbox dir")
	}
	if err := s.w.End(context.Background(), EndReq{Session: id, Delete: true}); err != nil {
		t.Fatal(err)
	}
	if there(sandbox.Dir(b.Root())) {
		t.Errorf("%s is still there", sandbox.Dir(b.Root()))
	}
}

// The sweep runs at once and then on its timer, asks every backend, and removes a sandbox whose session is gone
// while keeping one a session still has.
func TestSweepRunsOnATimer(t *testing.T) {
	s := newSandboxRig(t)
	_, _, b := s.sandboxed()
	s.root = b.Root()
	orphan := filepath.Join(t.TempDir(), "gone")
	if _, err := sandbox.Create(orphan, sandbox.ModeSandbox); err != nil {
		t.Fatal(err)
	}
	s.swept = make(chan bool, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.w.SweepSandboxes(ctx, 20*time.Millisecond)
	for range 2 {
		select {
		case attached := <-s.swept:
			if !attached {
				t.Error("the sweep didn't see the running session's sandbox as attached")
			}
		case <-time.After(waitTimeout):
			t.Fatal("the sweep never ran")
		}
	}
	if there(sandbox.Dir(orphan)) {
		t.Error("the orphan sandbox stayed")
	}
	if !there(sandbox.Dir(b.Root())) {
		t.Error("the sweep took a live session's sandbox")
	}
}
