package registry_test

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
)

func kindsReg(front *core.SessionState) registry.Registry {
	r := registry.New()
	r.Contribute(registry.KindEntry{SessionKind: registry.Kind{KindSpec: registry.KindSpec{Kind: "work", Order: 10,
		Caps: []core.Capability{core.Ships, core.AddProjects}}}})
	r.Contribute(registry.KindEntry{SessionKind: registry.Kind{KindSpec: registry.KindSpec{Kind: "plan", Order: 20},
		LabelFunc: func(se core.SessionState) string {
			if se.Started {
				return "plan → work"
			}
			return "plan"
		},
		ChipsFunc: func(se core.SessionState) []registry.Chip { return []registry.Chip{{Text: se.Ticket}} }}})
	r.Contribute(registry.Service{Name: registry.SessionsService, Impl: testSessions{front}})
	return r
}

// testSessions are the Sessions service over one session in front, or none.
type testSessions struct{ front *core.SessionState }

func (s testSessions) Front() (core.SessionState, bool) {
	if s.front == nil {
		return core.SessionState{}, false
	}
	return *s.front, true
}

func (s testSessions) Get(id core.SessionID) (core.SessionState, bool) {
	if s.front == nil || s.front.ID != id {
		return core.SessionState{}, false
	}
	return *s.front, true
}

func (s testSessions) FromArgs(a registry.Args) (core.SessionState, error) {
	var front core.SessionID
	if s.front != nil {
		front = s.front.ID
	}
	return registry.SessionFromArgs(a, front, s.Get)
}

func (testSessions) Start(registry.Ctx, registry.Args) (core.SessionID, error) { return "", nil }

// Session kinds are contributions: listed by Order, each saying what it can do and how its sessions read; a later
// one with the same kind shadows it until it's gone.
func TestSessionKinds(t *testing.T) {
	r := kindsReg(nil)
	r.Contribute(registry.KindEntry{SessionKind: registry.Kind{KindSpec: registry.KindSpec{Kind: "incident"}}})
	var got []string
	for _, k := range registry.Kinds(r) {
		got = append(got, string(k.Spec().Kind))
	}
	if strings.Join(got, " ") != "incident work plan" {
		t.Errorf("kinds = %v", got)
	}
	if !registry.Can(r, "work", core.Ships) || registry.Can(r, "plan", core.Ships) || registry.Can(r, "nope", core.Ships) {
		t.Error("Can")
	}
	if l := registry.Label(r, core.SessionState{Kind: "plan", Started: true}); l != "plan → work" {
		t.Errorf("plan's label = %q", l)
	}
	if l := registry.Label(r, core.SessionState{Kind: "work"}); l != "work" {
		t.Errorf("work's label = %q", l)
	}
	if c := registry.Chips(r, core.SessionState{Kind: "plan", Ticket: "ENG-1"}); len(c) != 1 || c[0].Text != "ENG-1" {
		t.Errorf("chips = %+v", c)
	}
	off := r.Contribute(registry.KindEntry{SessionKind: registry.Kind{KindSpec: registry.KindSpec{Kind: "work", Order: 10}}})
	if registry.Can(r, "work", core.Ships) {
		t.Error("a shadowing kind's capabilities didn't win")
	}
	off.Dispose()
	if !registry.Can(r, "work", core.Ships) {
		t.Error("the shadowed kind isn't back")
	}
}

// A command with When is available only while the session in front's kind can do it.
func TestCommandWhenCapability(t *testing.T) {
	front := &core.SessionState{Kind: "work"}
	r := kindsReg(front)
	r.Command(registry.Command{ID: "project.add", When: core.AddProjects, Run: func(registry.Ctx, registry.Args) error { return nil }})
	if !r.Available("project.add", nil) {
		t.Error("not available on a work session")
	}
	front.Kind = "plan"
	if r.Available("project.add", nil) {
		t.Error("available on a plan session, which can't add projects")
	}
	none := kindsReg(nil)
	none.Command(registry.Command{ID: "project.add", When: core.AddProjects, Run: func(registry.Ctx, registry.Args) error { return nil }})
	if none.Available("project.add", nil) {
		t.Error("available with no session in front")
	}
}

// A plugin's card chips come after the kind's, for every kind, until disposed.
func TestCardChips(t *testing.T) {
	r := kindsReg(nil)
	d := r.Contribute(registry.CardChips{Name: "pr", Chips: func(se core.SessionState) []registry.Chip {
		return []registry.Chip{{Text: "#" + string(se.ID)}}
	}})
	if c := registry.Chips(r, core.SessionState{ID: "7", Kind: "plan", Ticket: "ENG-1"}); len(c) != 2 || c[0].Text != "ENG-1" || c[1].Text != "#7" {
		t.Errorf("chips = %+v", c)
	}
	d.Dispose()
	if c := registry.Chips(r, core.SessionState{ID: "7", Kind: "work"}); len(c) != 0 {
		t.Errorf("chips once disposed = %+v", c)
	}
}
