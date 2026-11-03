//go:build showcase

package app_test

import (
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
)

// showSession puts the scene in a work session on dir, in front, as the window has it while an agent works there.
func showSession(t *testing.T, s *apptest.Scene, name, project, dir string) {
	t.Helper()
	se := core.SessionState{ID: core.SessionID(name), Name: name, Kind: core.KindWork, Dir: dir, Status: core.StatusNeedsYou,
		Projects: []core.Project{{Name: project, Repo: dir, Dir: dir, Branch: "rethink/rounding", Primary: true}},
		Agent:    core.AgentState{Provider: "claude"}}
	for _, ev := range []core.Event{core.SessionSynced{Session: se}, core.SessionFocused{ID: se.ID}} {
		if err := s.Store().Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	s.Frame()
}

func TestShowcaseP6Twins(t *testing.T) {
	s, dir := apptest.TestsScene(t)
	showSession(t, s, "pricing-rounding", "pricing", dir)
	s.Registry().Contribute(registry.Explainer{Name: "show", Explain: rig.ShowExplainer, Label: func() string { return "fork of claude · knows this session" }})
	s.Run("editor.open", registry.Args{"path": filepath.Join(dir, "round_test.go")})
	s.WaitHighlights()
	s.Run("test.run.file", nil)
	s.RunDone()
	s.EditorKeys("20Gzt")
	s.Frame()
	s.Shot("p6-11a-test-failure-twins")
	for _, ex := range fixTwins(s) {
		s.Click(apptest.Centre(ex.Bounds), 0)
	}
	s.ExplainSettle()
	s.Shot("p6-11b-test-failure-explained")

	c, root := apptest.LSPScene(t, "go", "gopls", "main.go")
	showSession(t, c, "greet-typo", "greet", root)
	c.Registry().Contribute(registry.Explainer{Name: "show", Explain: rig.ShowExplainer, Label: func() string { return "fork of claude · knows this session" }})
	c.Run("editor.open", registry.Args{"path": filepath.Join(root, "main.go")})
	c.LSPSettle()
	c.CursorAt(12, 26)
	c.Press("<Space>ca")
	c.LSPSettle()
	c.Shot("p6-11c-code-actions-agent")
	c.ClickText("Ask the agent to explain")
	c.ExplainSettle()
	c.Shot("p6-11d-diagnostic-explained")
}
