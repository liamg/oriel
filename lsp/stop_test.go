package lsp_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/liamg/oriel/internal/waitfor"
	"github.com/liamg/oriel/lsp"
	"github.com/liamg/oriel/lsp/lsptest"
)

// processManager is a Manager whose one server is ps, warmed for a Go project of its own, answered with its root.
func processManager(t *testing.T, ps *lsptest.ProcessServer) (*lsp.Manager, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := lsp.NewManager(lsp.ManagerOpts{Servers: func() []lsp.ServerSpec { return []lsp.ServerSpec{ps.Spec} }})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := m.Warm(ctx, "go", root); err != nil {
		t.Fatal(err)
	}
	if servers, children := ps.Pids(); len(servers) != 1 || len(children) != 1 {
		t.Fatalf("started %d servers with %d children, want one with its child", len(servers), len(children))
	}
	return m, root
}

// gone waits for every process ps started to have exited, failing after d naming those left.
func gone(t *testing.T, ps *lsptest.ProcessServer, d time.Duration, what string) {
	t.Helper()
	waitfor.PollFor(t, d, what, func() bool { return len(ps.Running()) == 0 },
		func() string { return fmt.Sprintf(" (still running: %v)", ps.Running()) })
}

// A server that answers shutdown exits, and takes what it started with it.
func TestShutdownStopsTheServer(t *testing.T) {
	ps := lsptest.NewProcessServer(t, "fake", []string{"go"}, []string{"go.mod"}, false)
	m, _ := processManager(t, ps)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	gone(t, ps, 10*time.Second, "the server and its child to exit")
}

// A server that never answers shutdown is killed once the wait runs out, and everything it started with it: its
// helpers don't outlive it.
func TestShutdownKillsAStuckServerAndWhatItStarted(t *testing.T) {
	ps := lsptest.NewProcessServer(t, "fake", []string{"go"}, []string{"go.mod"}, true)
	m, _ := processManager(t, ps)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = m.Shutdown(ctx) // its shutdown request times out
	gone(t, ps, 10*time.Second, "the stuck server and its child to be killed")
}

// Stopping the servers under a directory stops those whose root is in it, and only those.
func TestStopUnderStopsTheServersInADirectory(t *testing.T) {
	ps := lsptest.NewProcessServer(t, "fake", []string{"go"}, []string{"go.mod"}, false)
	m, root := processManager(t, ps)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m.StopUnder(ctx, filepath.Join(filepath.Dir(root), "elsewhere"))
	if len(ps.Running()) != 2 {
		t.Fatalf("stopping another directory's servers stopped this one's: running %v", ps.Running())
	}
	m.StopUnder(ctx, filepath.Dir(root))
	gone(t, ps, 10*time.Second, "the server under the directory to stop")
	if cs := m.Clients(); len(cs) != 0 {
		t.Errorf("a stopped server is still listed: %d clients", len(cs))
	}
}
