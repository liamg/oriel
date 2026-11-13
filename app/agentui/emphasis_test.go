package agentui_test

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// An agent's *emphasis* is drawn in the italic face, its **strong** in the bold, and neither keeps its asterisks;
// a name like snake_case_name stays as written.
//
// ref:   gfm/spec.md#Emphasis and strong emphasis
// quote: "A single `*` character [can open emphasis](@) iff (if and only if) it is part of a [left-flanking delimiter run]."
func TestTheConsoleDrawsEmphasis(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	se := core.SessionState{ID: "w", Name: "w", Kind: core.KindWork, Status: core.StatusQuiet,
		Agent:      core.AgentState{Provider: "claude", Running: true},
		Transcript: []core.Item{{ID: "a", Kind: core.ItemAssistant, Text: "The release went *mostly* well: **one** flaky test in snake_case_name."}}}
	for _, ev := range []core.Event{core.SessionSynced{Session: se}, core.SessionFocused{ID: se.ID}} {
		if err := s.Store().Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	s.Run("view.open", registry.Args{"id": kit.AgentConsoleView, "place": "tab", "title": "Agent"})
	s.Until("the reply", func() bool { return strings.Contains(s.Text(), "flaky test") })
	runs := map[string]*render.Op{}
	for i, op := range s.List().Ops {
		if op.Kind == render.OpGlyphs {
			runs[strings.TrimSpace(op.Run.Text)] = &s.List().Ops[i]
		}
	}
	for text := range runs {
		if strings.Contains(text, "*") {
			t.Errorf("%q is drawn with its asterisks", text)
		}
	}
	if op := runs["mostly"]; op == nil || !op.Run.Font.Italic || op.Run.Glyphs[0].Face == nil || !op.Run.Glyphs[0].Face.Italic {
		t.Errorf("mostly: %+v; want it in an italic face", op)
	}
	if op := runs["one"]; op == nil || op.Run.Font.Italic || op.Run.Font.Weight < 600 {
		t.Errorf("one: %+v; want it bold", op)
	}
	if !strings.Contains(s.Text(), "snake_case_name") {
		t.Errorf("snake_case_name was changed:\n%s", s.Text())
	}
}
