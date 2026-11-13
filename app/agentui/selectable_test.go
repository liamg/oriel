package agentui_test

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/render"
)

// dragAcross drags along the first run of text starting with prefix and answers what's selected.
func dragAcross(t *testing.T, s *apptest.Scene, prefix string) string {
	t.Helper()
	at, ok := s.TextStart(prefix)
	if !ok {
		t.Fatalf("no %q:\n%s", prefix, s.Text())
	}
	y := at.Y - 5
	s.Drag(render.Point{X: at.X + 12, Y: y}, render.Point{X: at.X + 260, Y: y})
	return s.UI().SelectedText()
}

// What Ori says selects to be copied, in its window as anywhere: a session's name, a test, an error, read out of it.
func TestOrisRepliesSelect(t *testing.T) {
	s, lw, _ := assistantScene(t, host.Options{}, nil, nil)
	s.Run("assistant.open", nil)
	s.Frame()
	s.TypeInto("Ask the assistant", "which test is flaky?")
	s.Press("<CR>")
	ua := lw.Fake.WaitStarted(t)
	if _, err := settleCall(t, s, asstCall(ua, "reply", map[string]any{"text": "The flaky test is TestRetryBackoff in api."})); err != nil {
		t.Fatal(err)
	}
	s.Quiet()
	if got := dragAcross(t, s, "The flaky"); !strings.Contains(got, "TestRetryBackoff") {
		t.Errorf("a drag across Ori's reply selected %q", got)
	}
}

// A full-window page's text selects: what a setting does, an error a page shows.
func TestAPagesTextSelects(t *testing.T) {
	s, _, _ := assistantScene(t, host.Options{}, nil, nil)
	s.Run("settings.open", nil)
	s.Quiet()
	if got := dragAcross(t, s, "Theme"); got == "" {
		t.Errorf("a drag across the settings page's text selected nothing")
	}
}
