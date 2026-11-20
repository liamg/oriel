package agentui_test

import (
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/registry/uireg"
)

// Mark all read in the Inbox panel's header marks every card it shows read: they leave the stack over the window
// and stop counting on the title bar and the session's tab, but wait on in the panel to be answered. With nothing
// unread the button is off; a card that comes after is new again.
func TestInboxMarkAllRead(t *testing.T) {
	s, b := busScene(t)
	s.Node("Inbox: 2 waiting")
	s.Node("2 waiting on you")
	s.Run("inbox.open", nil)
	s.Frame()

	s.ClickNode("Mark all read")
	s.Frame()
	if n := s.Find("Inbox: 2 waiting"); n != nil {
		t.Error("the title bar still counts cards marked read")
	}
	if strings.Contains(s.FullText(), "waiting on you") {
		t.Error("the session's tab still counts cards marked read")
	}
	if len(b.dismissed) != 2 {
		t.Errorf("the source heard of %v taken off the stack, want both", b.dismissed)
	}
	if !inPanel(s, "tom's agent asks about RoundMinor") || !inPanel(s, "priya → you") {
		t.Error("cards marked read left the panel: they still wait to be answered")
	}
	if n := s.Find("Mark all read"); n == nil || !n.Disabled {
		t.Errorf("with nothing unread the button is still on: %+v", n)
	}

	s.ClickNode("tom's agent asks about RoundMinor")
	s.ClickTop("Not yet")
	if len(b.ran) != 1 || b.ran[0] != "q1: not yet" {
		t.Fatalf("a card marked read didn't answer from the panel: %v", b.ran)
	}

	b.cards = append(b.cards, uireg.InboxCard{ID: "r2", Group: uireg.GroupBus, Session: "search-service", Title: "sam → you: #4420 Drop v1",
		Created: s.Now().Add(-time.Second)})
	s.Frame()
	s.Node("Inbox: 1 waiting")
	if n := s.Find("Mark all read"); n == nil || n.Disabled {
		t.Fatalf("a new card left the button off: %+v", n)
	}
	s.Run("inbox.unread.clear", nil)
	s.Frame()
	if s.Find("Inbox: 1 waiting") != nil {
		t.Error("inbox.unread.clear left the new card counted")
	}
}

// Mark all read keeps to the filter: cards the panel doesn't show stay unread.
func TestInboxMarkAllReadKeepsToTheFilter(t *testing.T) {
	s, _ := busScene(t)
	s.Run("inbox.open", nil)
	s.Frame()
	s.ClickTop("Me")
	s.ClickNode("Mark all read")
	s.Frame()
	s.Node("Inbox: 1 waiting") // the question isn't for you by name: Me doesn't show it
}
