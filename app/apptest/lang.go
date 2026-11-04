package apptest

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/lang"
	"github.com/liamg/oriel/lsp"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/text"
)

// LSPScene is the lsp module's fixture open in the editor, its server answering from the recorded session; it
// answers the fixture's directory.
func LSPScene(t *testing.T, fixture, server, file string) (*Scene, string) {
	t.Helper()
	root := rig.LSPProject(t, fixture)
	s := New(t, Options{WorkDir: root})
	s.Registry().Contribute(rig.RecordedServer(t, fixture, server))
	s.Run("editor.open", registry.Args{"path": filepath.Join(root, file)})
	s.LSPSettle()
	return s, root
}

// LSPSettle frames until the language server's answers are in.
func (s *Scene) LSPSettle() {
	x := PluginOf[*lang.LSP](s, "lsp")
	for range 4 {
		s.Frame()
		x.Wait()
		s.RunQueued()
	}
	s.Frame()
	s.Frame()
}

// CursorAt puts the editor's cursor at line and col, 0-based, and lets the servers answer.
func (s *Scene) CursorAt(line, col int) {
	s.Editor().SetCursor(text.Pos{Line: line, Col: col})
	s.LSPSettle()
}

// WantText fails the test for each of wants the frame lacks, saying what it's checking.
func (s *Scene) WantText(what string, wants ...string) {
	s.T.Helper()
	text := s.Text()
	for _, w := range wants {
		if !strings.Contains(text, w) {
			s.T.Errorf("%s: no %q in\n%s", what, w, text)
		}
	}
}

// TestsScene is rig's pricing package open in the editor; it answers the package's directory.
func TestsScene(t *testing.T) (*Scene, string) {
	dir := rig.PricingPackage(t)
	s := New(t, Options{WorkDir: dir})
	s.Run("editor.open", registry.Args{"path": filepath.Join(dir, "round_test.go")})
	s.WaitSyntax()
	s.Frame()
	return s, dir
}

// RunDone frames until the test run in progress has finished.
func (s *Scene) RunDone() {
	s.T.Helper()
	x := PluginOf[*lang.Tests](s, "tests")
	deadline := time.Now().Add(60 * time.Second)
	for x.Running() {
		if time.Now().After(deadline) {
			s.T.Fatal("the run didn't finish")
		}
		s.RunQueued()
		s.Frame()
		time.Sleep(10 * time.Millisecond)
	}
	s.RunQueued()
	s.Frame()
	s.Frame()
}

// WaitTerm frames until the frame has want, as a terminal's shell answers off the loop, and answers the frame's text.
func (s *Scene) WaitTerm(want string) string {
	s.T.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		s.RunQueued()
		s.Frame()
		text := s.Text()
		if strings.Contains(text, want) {
			return text
		}
		if time.Now().After(deadline) {
			s.T.Fatalf("no %q in:\n%s", want, text)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TypeTerm types text at the focused terminal, a key at a time, as the window would.
func (s *Scene) TypeTerm(keys ...string) {
	s.T.Helper()
	for _, k := range keys {
		for _, r := range k {
			s.Press(string(r))
		}
	}
}

// TermScene is a terminal opened with args (term.open) in a directory of the test's.
func TermScene(t *testing.T, args registry.Args) *Scene {
	s := New(t, Options{WorkDir: t.TempDir()})
	s.Frame()
	s.Run("term.open", args)
	s.Frame()
	s.Frame()
	return s
}

// TestServers are the language servers a test's app may start: those the test registers (a recorded server), never
// the machine's own built-in ones, which are slow and differ from one machine to the next.
func TestServers(s lsp.ServerSpec) bool { return s.Source != "builtin:lsp" }

// WaitDiagnostics waits for the language servers' diagnostics on path, and gives them: a server publishes them when
// it has them, not in answer to anything the app waits on.
func (s *Scene) WaitDiagnostics(path string) []registry.Diagnostic {
	s.T.Helper()
	var ds []registry.Diagnostic
	s.Until("the diagnostics on "+filepath.Base(path), func() bool {
		ds = s.Registry().DiagnosticsFor(path)
		return len(ds) > 0
	})
	return ds
}
