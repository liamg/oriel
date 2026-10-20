package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
)

// endScene is a work session on the host's repo, in front, its tab drawn.
func endScene(t *testing.T) (*errScene, core.SessionState) {
	s, lw := hostScene(t)
	s.run("session.start", registry.Args{"kind": "work", "name": "rounding", "repos": []string{lw.repo}})
	s.untilState(t, "the session in front", func(st core.State) bool { return st.Focus == "rounding" })
	s.until(t, "its tab", func() bool { return s.access("rounding") != nil })
	se, _ := s.store.State().Session("rounding")
	return s, se.Snapshot()
}

// sessions.md, "Ending a session": the session tab's right-click menu has End session…, which opens the dialog;
// with nothing pushed it starts on Delete, and ⌘↵ deletes it.
func TestEndSessionFromItsTab(t *testing.T) {
	s, _ := endScene(t)
	text := s.rightClick(centre(s.access("rounding").Bounds))
	if !strings.Contains(text, "End session…") || !strings.Contains(text, "Close tab") {
		t.Fatalf("the tab's menu:\n%s", text)
	}
	s.clickText(t, "End session…")
	s.waitFor("Nothing unsaved is lost.")
	s.wantFrame(t, "END SESSION", "Its worktrees, workspace and log go")
	s.lint("the End session dialog")
	s.keyCommand(t, "<D-CR>")
	s.untilState(t, "the session gone", func(st core.State) bool { _, ok := st.Session("rounding"); return !ok })
}

// Uncommitted work is shown, file by file, and Archive offers to discard it rather than refusing.
func TestEndSessionDiscardsAndArchives(t *testing.T) {
	s, se := endScene(t)
	if err := os.WriteFile(filepath.Join(se.Projects[0].Dir, "notes.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.run("session.end", registry.Args{"id": "rounding"})
	s.waitFor("1 file uncommitted, lost: notes.txt")
	s.clickText(t, "Archive")
	s.wantFrame(t, "Discard and archive")
	s.keyCommand(t, "<D-CR>")
	s.untilState(t, "the session archived", func(st core.State) bool {
		se, ok := st.Session("rounding")
		return ok && se.Snapshot().Archived
	})
	if _, err := os.Stat(se.Projects[0].Dir); err == nil {
		t.Error("the worktree is still there")
	}
	s.waitFor("Archived rounding.")
	s.wantFrame(t, "Undo")
}
