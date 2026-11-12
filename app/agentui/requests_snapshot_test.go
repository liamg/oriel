package agentui_test

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
)

func requestsScene(t *testing.T) *apptest.Scene { return apptest.RequestsScene(t) }

// The Requests list grouped Working / Needs you / Open / Resolved with its filters, and the
// request in front: its anchored code, your prompt as markdown with a pasted image, Claude's reply,
// the composer, and what the request changed and spawned.
func TestRequestsSnapshot(t *testing.T) {
	s := requestsScene(t)
	s.Run("view.open", registry.Args{"id": kit.RequestsView, "place": "dock", "edge": "left", "size": 320})
	s.Run("request.open", registry.Args{"thread": "7d45e1a0"})
	s.Frame()
	s.Lint("the requests view")
	s.Golden("requests")
	text := s.Text()
	for _, want := range []string{"WORKING", "NEEDS YOU", "OPEN", "RESOLVED", "Negative values round the wrong way", "CLAUDE · WORKING",
		"anchored · follows edits", "case 0:", "make the negative cases table-driven alongside the positive ones", "pasted · invoice-diff.png",
		"round_test.go", "URGENT · INTERRUPTS TURN", "CHANGED BY THIS REQUEST", "Negative tie handling"} {
		if !strings.Contains(text, want) {
			t.Errorf("the frame lacks %q", want)
		}
	}
	for _, not := range []string{"ACTIVITY", "14 passed"} {
		if strings.Contains(text, not) {
			t.Errorf("the reply shows %q", not)
		}
	}
}

// A question the agent asks waits in its thread: in the Requests view at the foot of the conversation, and between
// the editor's lines under the thread it's about, with its options and a field for an answer of your own; nothing
// goes over the window.
func TestAQuestionInItsThreadSnapshot(t *testing.T) {
	s := requestsScene(t)
	focus := s.Store().State().Focus
	if err := s.Store().Apply(core.AskOpened{Session: focus, Ask: core.Ask{ID: "q1", Kind: core.AskQuestion, Thread: "7d45e1a0",
		Questions: []core.Question{{Question: "Should the negative cases round away from zero, or half to even like the positive ones?",
			Options: []string{"Half to even, as the positives", "Away from zero"}}}}}); err != nil {
		t.Fatal(err)
	}
	s.Run("view.open", registry.Args{"id": kit.RequestsView, "place": "dock", "edge": "left", "size": 320})
	s.Run("request.open", registry.Args{"thread": "7d45e1a0"})
	s.Frame()
	s.Until("the question in the thread", func() bool { return strings.Contains(s.Text(), "Away from zero") })
	s.Lint("a question in its thread")
	s.Golden("question-in-requests")
	if n := strings.Count(s.FullText(), "Away from zero"); n != 1 {
		t.Errorf("the question is drawn %d times, want once, in its thread", n)
	}
}

// The same question between the editor's lines, under the thread on the code it's about.
func TestAQuestionBetweenTheLinesSnapshot(t *testing.T) {
	s := requestsScene(t)
	focus := s.Store().State().Focus
	if err := s.Store().Apply(core.AskOpened{Session: focus, Ask: core.Ask{ID: "q1", Kind: core.AskQuestion, Thread: "7d45e1a0",
		Questions: []core.Question{{Question: "Should the negative cases round away from zero, or half to even like the positive ones?",
			Options: []string{"Half to even, as the positives", "Away from zero"}}}}}); err != nil {
		t.Fatal(err)
	}
	s.Run("editor.open", registry.Args{"path": "internal/pricing/round.go"})
	s.Frame()
	s.EditorKeys("40Gzt") // a few lines above the thread, at the top, so the thread shows whole
	s.Until("the question between the lines", func() bool { return strings.Contains(s.Text(), "Away from zero") })
	s.Lint("a question between the lines")
	s.Golden("question-in-editor")
	if n := strings.Count(s.FullText(), "Away from zero"); n != 1 {
		t.Errorf("the question is drawn %d times, want once, in its thread", n)
	}
}
