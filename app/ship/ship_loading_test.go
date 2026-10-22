package ship_test

import (
	"strings"
	"testing"
	"time"
)

// A Ship column shows placeholders until its project is first read, never a made-up "clean"; a
// failed read shows why with Retry; once read, the steps show, and a later read keeps them under a refresh bar.
func TestShipColumnsLoad(t *testing.T) {
	s, se := shipSession(t)
	x := shipOf(s)
	fail, restore := x.Pending(se.ID)
	s.Run("ship.open", nil)
	s.Frame()
	s.Now = s.Now.Add(time.Second)
	s.Frame()
	if n := s.Node("Reading search-api"); n.Role != "status" {
		t.Errorf("the column's placeholder reads as %+v", n)
	}
	if text := s.Text(); strings.Contains(text, "no remote") || strings.Contains(text, "Uncommitted") {
		t.Errorf("a column claims a state before it's read:\n%s", text)
	}
	for _, b := range []string{"Sync all", "Commit all"} {
		if n := s.Node(b); !n.Disabled {
			t.Errorf("%s can be pressed before the projects are read", b)
		}
	}

	fail("gh: not logged in")
	s.Now = s.Now.Add(time.Second)
	s.Frame()
	s.WantFrame("gh: not logged in", "Retry")
	s.ClickNode("Retry") // this scene has no session host: the read runs again and says so
	shipIdle(s)
	s.Now = s.Now.Add(time.Second)
	s.Frame()
	s.WantFrame("there are no sessions in this window", "Retry")

	restore()
	s.Now = s.Now.Add(time.Second)
	s.Frame()
	s.WantFrame("Uncommitted", "Behind main")
	if n := s.Node("Sync all"); n.Disabled {
		t.Error("Sync all is still off once the projects are read")
	}

	x.SetLoading(se.ID, true)
	s.Frame()
	s.Now = s.Now.Add(200 * time.Millisecond)
	s.Frame()
	s.WantFrame("Uncommitted", "Behind main")
	s.Node("Reading search-api") // the refresh bar
}

// A running step shows on its own button, busy and labelled as the step; the project's other steps wait, and other
// projects' don't.
func TestShipStepButtonsShowTheirStep(t *testing.T) {
	s, se := shipSession(t)
	x := shipOf(s)
	s.Run("ship.open", nil)
	s.SettleGit()
	x.SetDoing(se.ID, "search-api", "Rebasing")
	s.Frame()
	if n := s.Node("Rebasing…"); !n.Disabled {
		t.Errorf("the busy button can be pressed: %+v", n)
	}
	if n := s.Node("Commit"); !n.Disabled {
		t.Error("Commit can be pressed while the project rebases")
	}
	if n := s.Node("Ready for review"); n.Disabled {
		t.Error("another project's step waits on this one's")
	}
}
