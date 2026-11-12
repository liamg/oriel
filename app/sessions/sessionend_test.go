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

// closeByTab hovers the session's tab and clicks its ✕.
func closeByTab(s *apptest.Scene, se core.SessionState) {
	s.T.Helper()
	s.Until("its tab", func() bool { return s.FindRole(render.RoleTab, se.Name) != nil })
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: apptest.Centre(s.NodeRole(render.RoleTab, se.Name).Bounds)})
	s.ClickOn("Close " + se.Name)
}

// archived is whether the session is archived.
func archived(st core.State, id core.SessionID) bool {
	se, ok := st.Session(id)
	return ok && se.Snapshot().Archived
}

// A session tab's ✕ ends the session: with nothing to lose and the work not done, it's archived without asking, its
// agent stopped, and its notice offers Undo.
func TestClosingASessionsTabArchivesIt(t *testing.T) {
	s, se := endScene(t)
	closeByTab(s, se)
	s.UntilState("the session archived", func(st core.State) bool { return archived(st, se.ID) })
	if st := s.Store().State(); len(st.Tabs) != 0 {
		t.Errorf("tabs open: %v", st.Tabs)
	}
	if sess, _ := s.Store().State().Session(se.ID); sess.Snapshot().Agent.Running {
		t.Error("its agent is still running")
	}
	s.WaitFor("Archived rounding.")
	s.WantFrame("Undo")
	if s.Has("END SESSION") {
		t.Error("closing it asked first, with nothing to lose")
	}
}

// With something to lose, closing the tab asks first and says what; Cancel leaves the session, its tab open.
func TestClosingASessionsTabWarnsFirst(t *testing.T) {
	s, se := endScene(t)
	if err := os.WriteFile(filepath.Join(se.Projects[0].Dir, "notes.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	closeByTab(s, se)
	s.WaitFor("1 file uncommitted, lost: notes.txt")
	s.WantFrame("END SESSION", "Discard and archive")
	s.ClickText("Cancel")
	s.Until("the dialog closed", func() bool { return !s.Has("END SESSION") })
	st := s.Store().State()
	if archived(st, se.ID) || len(st.Tabs) != 1 {
		t.Errorf("cancelling closed it: archived %v, tabs %v", archived(st, se.ID), st.Tabs)
	}
}

// Closing the tab of a session a SessionDone contribution calls done asks whether to archive or delete it, its local
// branches ticked to go; Cancel leaves it.
func TestClosingADoneSessionsTabOffersArchiveOrDelete(t *testing.T) {
	s, se := endScene(t)
	s.Registry().Contribute(registry.SessionDone{Name: "test", Done: func(core.SessionState) string { return "It's done." }})
	closeByTab(s, se)
	s.Until("the End session dialog", func() bool { return s.Has("END SESSION", "It's done.", "Nothing unsaved is lost.") })
	s.WantFrame("Archive", "Delete")
	if b := s.Node("Delete the local branches too (remote branches stay)"); b.Checked == nil || !*b.Checked {
		t.Error("deleting the local branches isn't ticked")
	}
	s.ClickText("Cancel")
	s.Until("the dialog closed", func() bool { return !s.Has("END SESSION") })
	if archived(s.Store().State(), se.ID) || len(s.Store().State().Tabs) != 1 {
		t.Error("cancelling ended the session")
	}
}

// What the agent is still running is listed too: its turn, the subagents it started, the background tasks that will
// wake it and the processes it left running all stop with it.
func TestEndSessionListsWhatTheAgentIsStillRunning(t *testing.T) {
	s, se := endScene(t)
	s.Quiet()
	a := se.Agent
	a.Working = true
	a.Background = []core.BackgroundTask{{Kind: "shell", Description: "go test ./... -count=5"}}
	a.Processes = []core.BackgroundTask{{Kind: "terminal", Description: "npm run dev"}, {Kind: "terminal", Description: "make watch"}}
	for _, ev := range []core.Event{
		core.AgentChanged{ID: se.ID, Agent: a},
		core.SubagentChanged{Session: se.ID, Subagent: core.Subagent{ID: "a1", Description: "sweep the flaky tests", Running: true}},
		core.SubagentChanged{Session: se.ID, Subagent: core.Subagent{ID: "a2", Description: "read the old parser", Running: false}},
	} {
		if err := s.Store().Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	s.Run("session.end", registry.Args{"id": "rounding"})
	s.WaitFor("The agent is mid-turn")
	// Each wraps in the dialog, so its count and what it's running are looked for apart.
	s.WantFrame("1 subagent still running, stopped with the agent: sweep the flaky tests",
		"1 background task still running, stopped with the agent", "go test ./...",
		"2 processes still running, stopped with the agent", "npm run dev, make")
	if strings.Contains(s.Text(), "read the old parser") {
		t.Error("a subagent that's finished is listed")
	}
	if strings.Contains(s.Text(), "Nothing unsaved is lost.") {
		t.Error("the dialog says nothing is lost")
	}
}
