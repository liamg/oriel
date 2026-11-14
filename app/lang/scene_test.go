package lang_test

import (
	"os"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/lang"
)

func lspOf(s *apptest.Scene) *lang.LSP     { return apptest.PluginOf[*lang.LSP](s, "lsp") }
func testsOf(s *apptest.Scene) *lang.Tests { return apptest.PluginOf[*lang.Tests](s, "tests") }
func debugOf(s *apptest.Scene) *lang.Debug { return apptest.PluginOf[*lang.Debug](s, "debug") }

func lspScene(t *testing.T, fixture, server, file string) (*apptest.Scene, string) {
	return apptest.LSPScene(t, fixture, server, file)
}

func lspSettle(s *apptest.Scene) { s.LSPSettle() }

func cursorAt(s *apptest.Scene, line, col int) { s.CursorAt(line, col) }

func wantText(t *testing.T, s *apptest.Scene, what string, wants ...string) {
	t.Helper()
	s.WantText(what, wants...)
}

// fileLines are a file's lines: its buffer's if it's open, else from disk.
func fileLines(s *apptest.Scene, path string) []string {
	if b, ok := s.Editor().Buffer(path); ok {
		return strings.Split(string(b.Bytes()), "\n")
	}
	b, _ := os.ReadFile(path)
	return strings.Split(string(b), "\n")
}

func testsScene(t *testing.T) (*apptest.Scene, string) { return apptest.TestsScene(t) }

func runDone(t *testing.T, s *apptest.Scene) {
	t.Helper()
	s.RunDone()
}
