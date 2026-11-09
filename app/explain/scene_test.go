package explain_test

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/explain"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/text"
)

func explainer(s *apptest.Scene) *explain.Plugin {
	return apptest.PluginOf[*explain.Plugin](s, "explain")
}
func walker(s *apptest.Scene) *explain.Walkthrough {
	return apptest.PluginOf[*explain.Walkthrough](s, "explain.change")
}

type fakeExplainer = apptest.FakeExplainer

func explainScene(t *testing.T) (*apptest.Scene, *fakeExplainer, string) {
	return apptest.ExplainScene(t)
}

func settle(s *apptest.Scene) { s.ExplainSettle() }

// cursorTo puts the editor's cursor on a 1-based line, and lets what the line asks for come in.
func cursorTo(s *apptest.Scene, line int) {
	s.Editor().SetCursor(text.Pos{Line: line - 1})
	s.Quiet()
}

func explainKeys(s *apptest.Scene, keys ...string) { s.ExplainKeys(keys...) }

// cardOpen says the explain card is open: its commands are available.
func cardOpen(s *apptest.Scene) bool { return s.Host().Available("explain.close", nil) }

func unitMarks(s *apptest.Scene) map[int]string { return s.UnitMarks() }

// wantText fails the test for each of wants the frame lacks.
func wantText(t *testing.T, s *apptest.Scene, what string, wants ...string) {
	t.Helper()
	text := s.Text()
	for _, w := range wants {
		if !strings.Contains(text, w) {
			t.Errorf("%s: no %q in\n%s", what, w, text)
		}
	}
}

func walkScene(t *testing.T) (s *apptest.Scene, f *fakeExplainer, pricing, billing string) {
	return apptest.WalkScene(t)
}

func walkSettle(s *apptest.Scene) { s.WalkSettle() }

func walkKeys(s *apptest.Scene, keys ...string) { s.WalkKeys(keys...) }

// glyphOpacity is how opaque the first run of text is drawn, through the layers it's in.
func glyphOpacity(t *testing.T, s *apptest.Scene, text string) float32 {
	t.Helper()
	stack := []float32{1}
	for _, op := range s.List().Ops {
		switch op.Kind {
		case render.OpPushClip:
			stack = append(stack, stack[len(stack)-1])
		case render.OpPushOpacity:
			stack = append(stack, stack[len(stack)-1]*op.Amount)
		case render.OpPop:
			stack = stack[:len(stack)-1]
		case render.OpGlyphs:
			if op.Run.Text == text {
				return stack[len(stack)-1]
			}
		}
	}
	t.Fatalf("no %q in the frame", text)
	return 0
}

// threadWith waits for session w's thread whose first comment is body.
func threadWith(t *testing.T, w *apptest.Window, body string) core.Thread {
	t.Helper()
	return waitThread(t, w, func(th core.Thread) bool { return len(th.Comments) > 0 && th.Comments[0].Body == body })
}

// waitThread waits for a thread of session w that ok says is the one.
func waitThread(t *testing.T, w *apptest.Window, ok func(core.Thread) bool) core.Thread {
	t.Helper()
	var th core.Thread
	w.UntilState("the thread", func(st core.State) bool {
		se, found := st.Session("w")
		if !found {
			return false
		}
		for _, x := range se.Snapshot().Threads {
			if ok(x) {
				th = x
				return true
			}
		}
		return false
	})
	return th
}
