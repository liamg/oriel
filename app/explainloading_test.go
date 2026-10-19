package app

import (
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/registry"
)

// ui.md, "Loading": asking for a walkthrough can take minutes, so the view says how long it's been and offers
// Cancel, which drops the answer when it comes.
func TestExplainChangeAskingShowsTimeAndCancels(t *testing.T) {
	s, _, _, _ := walkScene(t)
	s.now = at(14, 30, 0)
	s.run("explain.change", nil)
	s.frame()
	s.now = s.now.Add(5 * time.Second)
	s.frame()
	s.wantText(t, "asking", "Asking for a walkthrough of the change…", "· 5s", "Cancel")
	s.clickText(t, "Cancel")
	s.walkSettle()
	if text := s.frameText(); strings.Contains(text, "Asking for a walkthrough") || strings.Contains(text, "STEP") {
		t.Errorf("after Cancel the walkthrough still came:\n%s", text)
	}
	s.wantText(t, "cancelled", "No walkthrough")
}

// The explain card says it's asking, and for how long, until the answer comes.
func TestExplainCardAsking(t *testing.T) {
	s, _, _ := explainScene(t)
	s.now = at(14, 30, 0)
	s.cursorTo(13)
	s.keyCommand(t, "<Space>e")
	s.now = s.now.Add(4 * time.Second)
	s.frame()
	s.wantText(t, "asking", "Asking…", "· 4s")
	s.explainSettle()
	s.now = s.now.Add(time.Second)
	s.frame()
	s.wantText(t, "answered", "Answer 1 (brief)")
	if strings.Contains(s.frameText(), "Asking…") {
		t.Error("the card still says it's asking")
	}
}

// explain.ask with no key says at once that it's asking, and takes that down when the answer comes.
func TestExplainAskSaysItsAsking(t *testing.T) {
	s, _, _ := explainScene(t)
	s.run("explain.ask", registry.Args{"text": "x := 1", "subject": "an assignment"})
	s.frame()
	s.wantText(t, "asking", "Asking the explainer; its answer comes here.")
	s.explainSettle()
	s.frame()
	if strings.Contains(s.frameText(), "Asking the explainer") {
		t.Errorf("the asking notice stayed up:\n%s", s.frameText())
	}
}

// The quiz says it's writing the questions while the fork works, and once a reply has taken a while, how long.
func TestVerifyQuizWaiting(t *testing.T) {
	s, _, _, _ := verifyScene(t)
	s.run("ship.pr.open", registry.Args{"project": "search-api"})
	s.verifySettle(t, "the gates counted", func(f string) bool { return strings.Contains(f, "Verify…") && !strings.Contains(f, "counting") })
	s.click(centre(s.access("Verify…").Bounds))
	s.frame()
	s.now = s.now.Add(time.Second)
	s.frame()
	s.wantFrame(t, "Writing the questions…")
	s.verifySettle(t, "the first question", func(f string) bool { return strings.Contains(f, "QUESTION 1") })
	s.answer(t, "It rounds to even")
	s.frame()
	s.now = s.now.Add(4 * time.Second)
	s.frame()
	if n := s.access("Thinking"); n.Role != "status" {
		t.Errorf("the dots read as %+v", n)
	}
	s.wantFrame(t, "4s")
	s.verifySettle(t, "the verdict", func(f string) bool { return !strings.Contains(f, "4s") })
}
