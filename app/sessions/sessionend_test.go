package sessions_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// endScene is a work session on the host's repo, in front, its tab drawn.
func endScene(t *testing.T) (*apptest.Scene, core.SessionState) {
	s, lw := apptest.HostScene(t)
	s.Run("session.start", registry.Args{"kind": "work", "name": "rounding", "repos": []string{lw.Repo}})
	s.UntilState("the session in front", func(st core.State) bool { return st.Focus == "rounding" })
	s.Until("its tab", func() bool { return s.Node("rounding") != nil })
	se, _ := s.Store().State().Session("rounding")
	return s, se.Snapshot()
}

// Ending a session: the session tab's right-click menu has End session…, which opens the dialog;
// with nothing pushed it starts on Delete, and ⌘↵ deletes it.
func TestEndSessionFromItsTab(t *testing.T) {
	s, _ := endScene(t)
	text := s.RightClick(apptest.Centre(s.Node("rounding").Bounds))
	if !strings.Contains(text, "End session…") || !strings.Contains(text, "Close tab") {
		t.Fatalf("the tab's menu:\n%s", text)
	}
	s.ClickText("End session…")
	s.WaitFor("Nothing unsaved is lost.")
	s.WantFrame("END SESSION", "Its worktrees, workspace and log go")
	s.Lint("the End session dialog")
	s.Press("<D-CR>")
	s.UntilState("the session gone", func(st core.State) bool { _, ok := st.Session("rounding"); return !ok })
}

// Uncommitted work is shown, file by file, and Archive offers to discard it rather than refusing.
func TestEndSessionDiscardsAndArchives(t *testing.T) {
	s, se := endScene(t)
	if err := os.WriteFile(filepath.Join(se.Projects[0].Dir, "notes.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.Run("session.end", registry.Args{"id": "rounding"})
	s.WaitFor("1 file uncommitted, lost: notes.txt")
	s.ClickText("Archive")
	s.WantFrame("Discard and archive")
	s.Press("<D-CR>")
	s.UntilState("the session archived", func(st core.State) bool {
		se, ok := st.Session("rounding")
		return ok && se.Snapshot().Archived
	})
	if _, err := os.Stat(se.Projects[0].Dir); err == nil {
		t.Error("the worktree is still there")
	}
	s.WaitFor("Archived rounding.")
	s.WantFrame("Undo")
}

// Closing the tab of a session that isn't done just closes it; one a SessionDone contribution calls done asks first,
// and Cancel leaves it, its tab closed.
func TestClosingADoneSessionsTabOffersToEndIt(t *testing.T) {
	s, se := endScene(t)
	closeTab := func() {
		t.Helper()
		s.RightClick(apptest.Centre(s.NodeRole(render.RoleTab, se.Name).Bounds))
		s.ClickText("Close tab")
		s.UntilState("the tab closed", func(st core.State) bool { return len(st.Tabs) == 0 })
	}
	closeTab()
	s.Frames(3)
	if s.Has("END SESSION") {
		t.Fatal("closing an unfinished session's tab offered to end it")
	}
	s.Registry().Contribute(registry.SessionDone{Name: "test", Done: func(core.SessionState) string { return "It's done." }})
	s.Run("session.focus", registry.Args{"id": string(se.ID)})
	s.Until("its tab", func() bool { return s.FindRole(render.RoleTab, se.Name) != nil })
	closeTab()
	s.Until("the End session dialog", func() bool { return s.Has("END SESSION", "It's done.") })
	s.ClickText("Cancel")
	s.Until("the dialog closed", func() bool { return !s.Has("END SESSION") })
	if sess, ok := s.Store().State().Session(se.ID); !ok || sess.Snapshot().Archived {
		t.Error("cancelling ended the session")
	}
}
