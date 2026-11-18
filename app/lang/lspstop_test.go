package lang_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/internal/waitfor"
	"github.com/liamg/oriel/lsp/lsptest"
	"github.com/liamg/oriel/registry"
	reglang "github.com/liamg/oriel/registry/lang"
)

// goServer is a fake Go language server run as a real process with a child of its own, ahead of any gopls installed.
func goServer(t *testing.T, stuck bool) *lsptest.ProcessServer {
	t.Helper()
	ps := lsptest.NewProcessServer(t, "fake-go", []string{"go"}, []string{"go.mod"}, stuck)
	ps.Spec.Order = -1
	return ps
}

// goFile writes a Go module with a main.go in dir, and answers main.go.
func goFile(t *testing.T, dir string) string {
	t.Helper()
	for name, body := range map[string]string{"go.mod": "module x\n\ngo 1.22\n", "main.go": "package main\n\nfunc main() {}\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "main.go")
}

// started waits for ps to have started one server, with its child.
func started(s *apptest.Scene, ps *lsptest.ProcessServer) {
	s.T.Helper()
	s.Until("the language server started", func() bool { return len(ps.Running()) == 2 })
}

// A language server started for a session's worktree stops, with what it started, when the session ends: it doesn't
// run on for a workspace nothing uses any more.
func TestASessionsLanguageServerStopsWhenItEnds(t *testing.T) {
	ps := goServer(t, false)
	s, lw := apptest.HostScene(t)
	s.Registry().Contribute(reglang.LSPServer(ps.Spec))
	s.Run("session.start", registry.Args{"kind": "work", "name": "rounding", "repos": []string{lw.Repo}})
	s.UntilState("the session in front", func(st core.State) bool { return apptest.FocusedOn(st, "rounding") })
	st := s.Store().State()
	id := apptest.Named(st, "rounding")
	se, _ := st.Session(id)
	s.Run("editor.open", registry.Args{"path": goFile(t, se.Snapshot().Projects[0].Dir)})
	started(s, ps)

	s.Run("session.end", registry.Args{"id": string(id), "how": "archive", "discard": true})
	s.UntilState("the session archived", func(st core.State) bool {
		se, ok := st.Session(id)
		return ok && se.Snapshot().Archived
	})
	s.Until("its language server stopped", func() bool { return len(ps.Running()) == 0 })
}

// Quitting stops every language server, one that won't answer shutdown included, and what each started.
func TestQuittingStopsTheLanguageServers(t *testing.T) {
	ps := goServer(t, true)
	t.Cleanup(func() { // after the scene has closed, as Oriel quitting
		waitfor.PollFor(t, 10*time.Second, "the language servers gone after quitting", func() bool { return len(ps.Running()) == 0 },
			func() string { return fmt.Sprintf(" (still running: %v)", ps.Running()) })
	})
	dir := t.TempDir()
	s := apptest.New(t, apptest.Options{WorkDir: dir})
	s.Registry().Contribute(reglang.LSPServer(ps.Spec))
	s.Run("editor.open", registry.Args{"path": goFile(t, dir)})
	started(s, ps)
}
