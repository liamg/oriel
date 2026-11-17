package agentui_test

import (
	"strings"
	"testing"
)

// The Inbox panel is worked from the keyboard: j and k move down and up its rows, stopping at the ends, and the
// selected row's card opens under it, where its actions answer it. Both cards are stacked over the window too, so
// each open card's action shows twice.
func TestInboxPanelByKeys(t *testing.T) {
	s, b := busScene(t)
	s.Run("inbox.open", nil)
	s.Frame()
	selected := func(title string) bool {
		n := s.Find(title)
		return n != nil && n.Selected
	}
	shown := func(label string) int { return strings.Count(s.Text(), label) }
	if shown("Not yet") != 1 || shown("Claim + Open") != 1 {
		t.Fatalf("with nothing selected the panel has a card open:\n%s", s.Text())
	}
	steps := []struct{ key, selected, open, shut string }{
		{"j", "tom's agent asks about RoundMinor", "Not yet", "Claim + Open"},
		{"j", "priya → you: #4418 Add saved searches", "Claim + Open", "Not yet"},
		{"j", "priya → you: #4418 Add saved searches", "Claim + Open", "Not yet"},
		{"k", "tom's agent asks about RoundMinor", "Not yet", "Claim + Open"},
	}
	for i, st := range steps {
		s.Press(st.key)
		s.Frame()
		if !selected(st.selected) || shown(st.open) != 2 || shown(st.shut) != 1 {
			t.Fatalf("step %d, %s: %q selected %v, %q shows %d times, %q %d", i, st.key, st.selected, selected(st.selected),
				st.open, shown(st.open), st.shut, shown(st.shut))
		}
	}
	s.Press("<CR>")
	s.Frame()
	if !selected("tom's agent asks about RoundMinor") || shown("Not yet") != 2 {
		t.Error("⏎ on a card with nowhere else to answer it closed it")
	}
	s.ClickTop("Not yet")
	if len(b.ran) != 1 || b.ran[0] != "q1: not yet" {
		t.Errorf("its action ran %v", b.ran)
	}
}
