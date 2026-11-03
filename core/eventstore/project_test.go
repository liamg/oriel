package eventstore

import (
	"testing"

	"github.com/liamg/oriel/core"
)

// The open project, each project's last panel and colour are the session's, kept by events, and a
// sync of the session leaves them be.
func TestProjectFocusAndPanels(t *testing.T) {
	s := New()
	three := []core.Project{{Name: "admin-ui", Dir: "/w/admin-ui"}, {Name: "api", Dir: "/w/api"}, {Name: "web", Dir: "/w/web"}}
	for _, ev := range []core.Event{
		core.SessionSynced{Session: core.SessionState{ID: "inf", Kind: core.KindWork, Projects: three}},
		core.ProjectOpened{ID: "inf", Project: "api"},
		core.ProjectPanelOpened{ID: "inf", Project: "admin-ui", Panel: "explorer"},
		core.ProjectPanelOpened{ID: "inf", Project: "api", Panel: "pull-request"},
		core.ProjectColoured{ID: "inf", Project: "web", Colour: "red"},
		core.SessionSynced{Session: core.SessionState{ID: "inf", Kind: core.KindWork, Projects: three, Branch: "eng-2310"}},
	} {
		if err := s.Apply(ev); err != nil {
			t.Fatalf("%T: %v", ev, err)
		}
	}
	se, _ := s.State().Session("inf")
	st := se.Snapshot()
	if got := st.OpenProject(); got != "api" {
		t.Errorf("open project %q, want api", got)
	}
	if st.ProjectUI.Panels["admin-ui"] != "explorer" || st.ProjectUI.Panels["api"] != "pull-request" {
		t.Errorf("panels %v", st.ProjectUI.Panels)
	}
	if got := []string{st.ProjectColour("admin-ui"), st.ProjectColour("api"), st.ProjectColour("web")}; got[0] == got[1] || got[2] != "red" {
		t.Errorf("colours %v: each project's own, web's as set", got)
	}
	if err := s.Apply(core.ProjectOpened{ID: "inf", Project: "nope"}); err == nil {
		t.Error("opened a project the session hasn't got")
	}
}

// With nothing opened yet, the open project is the primary one.
func TestOpenProjectDefaultsToThePrimary(t *testing.T) {
	st := core.SessionState{Projects: []core.Project{{Name: "a"}, {Name: "b", Primary: true}}}
	if got := st.OpenProject(); got != "b" {
		t.Errorf("open project %q, want b", got)
	}
}
