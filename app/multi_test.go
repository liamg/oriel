package app_test

import (
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
)

// A :s of a whole word offers the language server's rename instead, and ⇥ takes it, showing its diff.
func TestSubstituteOffersLSPRename(t *testing.T) {
	s, _ := apptest.LSPScene(t, "go", "gopls", "main.go")
	s.EditorKeys(`:%s/\<greeting\>/salute/g`)
	s.LSPSettle()
	s.WantText("the command line", "1 match", "⇥ use LSP rename")
	s.Press("<Tab>")
	s.LSPSettle()
	if !s.Showing(kit.ReplaceView) {
		t.Fatalf("⇥ didn't show the rename:\n%s", s.Text())
	}
	s.WantText("the rename", "Rename greeting → salute")
	if _, ok := s.Editor().Cmdline(); ok {
		t.Error("the command line stayed open")
	}
}
