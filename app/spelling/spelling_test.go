package spelling_test

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/render"
)

// fieldScene is the Settings page's search field holding text, focused, and the session host's fake agent, which
// offers a default model and a small one.
func fieldScene(t *testing.T, text string) (*apptest.Scene, *agenttest.Fake, render.Rect) {
	fake := agenttest.NewFake()
	fake.Offers = []agent.Model{{ID: "big", Name: "Big", Default: true}, {ID: "tiny", Name: "Tiny", Small: true}}
	lw := rig.NewHost(t, fake)
	s := apptest.New(t, apptest.Options{Config: apptest.Connect(lw.W)})
	s.Run("settings.open", nil)
	s.Frames(2)
	field := s.FindRole(render.RoleTextBox, "Search settings")
	if field == nil {
		t.Fatalf("no settings search field:\n%s", s.Text())
	}
	s.Click(apptest.Centre(field.Bounds), 0)
	s.TypeText(text)
	s.Frames(2)
	return s, fake, field.Bounds
}

func value(s *apptest.Scene) string { return s.FindRole(render.RoleTextBox, "Search settings").Value }

func chooseFix(t *testing.T, s *apptest.Scene, at render.Rect) {
	t.Helper()
	s.RightClick(apptest.Centre(at))
	n := s.FindRole(render.RoleMenuItem, "Fix spelling & grammar")
	if n == nil || n.Disabled {
		t.Fatalf("no Fix spelling & grammar to choose:\n%s", s.Text())
	}
	s.Click(apptest.Centre(n.Bounds), 0)
	s.Frames(2) // the menu closes, and the field has the focus back
}

// help ui/context-menu: "Fix spelling & grammar sends ... the whole field with nothing selected, to a small model, and
// puts its fix in place as one edit ... The field shows a spinner while it waits".
func TestFixSpellingPutsTheFixInTheField(t *testing.T) {
	s, fake, at := fieldScene(t, "teh quick brwon fox")
	chooseFix(t, s, at)
	ua := fake.WaitStarted(t)
	if ua.Spec.Model != "tiny" {
		t.Errorf("the fix runs on %q, want the provider's small model", ua.Spec.Model)
	}
	if first := ua.WaitSent(t); !strings.Contains(first.Text, "<text>\nteh quick brwon fox\n</text>") {
		t.Errorf("the model was sent %q", first.Text)
	}
	s.Midway(func() {
		s.Until("the field's spinner", func() bool { return s.FindRole(render.RoleStatus, "working on the text") != nil })
	})
	ua.Emit(agent.Message{Text: "The quick brown fox"})
	ua.Emit(agent.TurnEnd{})
	s.Until("the fix in the field", func() bool { return value(s) == "The quick brown fox" })
	if s.FindRole(render.RoleStatus, "working on the text") != nil {
		t.Error("the spinner stays once the fix is in")
	}
}

// help ui/context-menu: "if you change that text meanwhile, the fix is dropped rather than put over what you typed."
// Typing after it leaves it as it was sent, and the fix goes in; typing before it moves it, and the fix is dropped.
func TestFixSpellingLeavesTextChangedMeanwhile(t *testing.T) {
	s, fake, at := fieldScene(t, "teh fox")
	chooseFix(t, s, at)
	ua := fake.WaitStarted(t)
	ua.WaitSent(t)
	s.TypeText("es")
	s.Frames(2)
	ua.Emit(agent.Message{Text: "The fox"})
	ua.Emit(agent.TurnEnd{})
	s.Until("the fix in, before what was typed after it", func() bool { return value(s) == "The foxes" })

	chooseFix(t, s, at)
	ua = fake.WaitStarted(t)
	ua.WaitSent(t)
	s.Type("Home")
	s.TypeText("so,")
	s.Frames(2)
	ua.Emit(agent.Message{Text: "The foxes!"})
	ua.Emit(agent.TurnEnd{})
	s.Quiet()
	s.Frames(2)
	if v := value(s); v != "so,The foxes" {
		t.Errorf("the field holds %q, want what was typed meanwhile", v)
	}
	if n, _ := apptest.LastNotice(s.Store().State(), "spelling"); !strings.Contains(n.Text, "changed while it was being fixed") {
		t.Errorf("no word that the fix was dropped: %+v", n)
	}
}

// The menu item isn't offered on a field with nothing in it.
func TestFixSpellingNeedsText(t *testing.T) {
	s, _, at := fieldScene(t, "   ")
	s.RightClick(apptest.Centre(at))
	if n := s.FindRole(render.RoleMenuItem, "Fix spelling & grammar"); n == nil || !n.Disabled {
		t.Errorf("Fix spelling & grammar is offered on a blank field: %+v", n)
	}
}
