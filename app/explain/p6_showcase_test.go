//go:build showcase

package explain_test

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

func TestShowcaseP6Explain(t *testing.T) {
	s, _, path := explainScene(t)
	showSession(t, s, "pricing-rounding", "pricing", filepath.Dir(path))
	s.Registry().Contribute(registry.Explainer{Name: "show", Order: -1, Explain: rig.ShowExplainer, Label: func() string { return "fork of claude · knows this session" }})
	s.Shot("p6-08a-explain-marks")
	s.CursorTo(13)
	s.ExplainKeys("<Space>e")
	s.Shot("p6-08b-explain-card")
	s.ClickText("DETAILED")
	s.ExplainSettle()
	s.Shot("p6-08c-explain-detailed")
	s.ExplainKeys("<Esc>")

	s.Run("explain.units", nil)
	s.ExplainSettle()
	s.ExplainSettle()
	s.Shot("p6-08d-changed-units")

	s.CursorTo(22)
	s.EditorKeys("Vj")
	s.RightClick(s.TextPoint("Errorf"))
	s.Shot("p6-08e-explain-menu")
}

func TestShowcaseP6Walkthrough(t *testing.T) {
	s, _, _, _ := walkScene(t)
	s.Run("explain.change", nil)
	s.WalkSettle()
	s.Shot("p6-09a-walkthrough")
	s.WalkKeys("<Right>")
	s.WalkSettle()
	s.Shot("p6-09b-walkthrough-step2")
}
