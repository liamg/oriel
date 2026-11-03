package agentui_test

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
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
