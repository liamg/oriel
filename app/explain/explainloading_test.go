package explain_test

import (
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/registry"
)

// Asking for a walkthrough can take minutes, so the view says how long it's been and offers
// Cancel, which drops the answer when it comes.
func TestExplainChangeAskingShowsTimeAndCancels(t *testing.T) {
	s, _, _, _ := walkScene(t)
	s.Now = apptest.At(14, 30, 0)
	s.Run("explain.change", nil)
	s.Frame()
	s.Now = s.Now.Add(5 * time.Second)
	s.Frame()
	wantText(t, s, "asking", "Asking for a walkthrough of the change…", "· 5s", "Cancel")
	s.ClickTop("Cancel")
	walkSettle(s)
	if text := s.Text(); strings.Contains(text, "Asking for a walkthrough") || strings.Contains(text, "STEP") {
		t.Errorf("after Cancel the walkthrough still came:\n%s", text)
	}
	wantText(t, s, "cancelled", "No walkthrough")
}

// The explain card says it's asking, and for how long, until the answer comes.
func TestExplainCardAsking(t *testing.T) {
	s, _, _ := explainScene(t)
	s.Now = apptest.At(14, 30, 0)
	cursorTo(s, 13)
	s.Press("<Space>e")
	s.Now = s.Now.Add(4 * time.Second)
	s.Frame()
	wantText(t, s, "asking", "Asking…", "· 4s")
	settle(s)
	s.Now = s.Now.Add(time.Second)
	s.Frame()
	wantText(t, s, "answered", "Answer 1 (brief)")
	if strings.Contains(s.Text(), "Asking…") {
		t.Error("the card still says it's asking")
	}
}

// explain.ask with no key says at once that it's asking, and takes that down when the answer comes.
func TestExplainAskSaysItsAsking(t *testing.T) {
	s, _, _ := explainScene(t)
	s.Run("explain.ask", registry.Args{"text": "x := 1", "subject": "an assignment"})
	s.Frame()
	wantText(t, s, "asking", "Asking the explainer; its answer comes here.")
	settle(s)
	s.Frame()
	if strings.Contains(s.Text(), "Asking the explainer") {
		t.Errorf("the asking notice stayed up:\n%s", s.Text())
	}
}
