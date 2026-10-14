package registry_test

import (
	"errors"
	"testing"

	"github.com/liamg/oriel/registry"
)

// SH-16: a command is available while it's registered, on, and its Available agrees.
func TestAvailable(t *testing.T) {
	r := registry.New()
	if r.Available("nope", nil) {
		t.Error("an unregistered command is available")
	}
	views := map[string]bool{"explorer": false}
	r.Command(registry.Command{ID: "open", Run: func(registry.Ctx, registry.Args) error { return nil },
		Available: func(a registry.Args) bool { return views[a["id"].(string)] }})
	if r.Available("open", registry.Args{"id": "explorer"}) {
		t.Error("available before its view is")
	}
	views["explorer"] = true
	if !r.Available("open", registry.Args{"id": "explorer"}) {
		t.Error("not available once its view is")
	}
	r.TurnOff(registry.KindCommand, "open", errors.New("broken"))
	if r.Available("open", registry.Args{"id": "explorer"}) {
		t.Error("available while turned off")
	}
	r.Command(registry.Command{ID: "boom", Run: func(registry.Ctx, registry.Args) error { return nil },
		Available: func(registry.Args) bool { panic("boom") }})
	if r.Available("boom", nil) {
		t.Error("a panicking Available says yes")
	}
}

// SH-16: rail entries list top ones, then the bottom ones, each by order; a disposed entry goes.
func TestRailEntries(t *testing.T) {
	r := registry.New()
	r.Contribute(registry.RailEntry{Name: "settings", Order: 1, Bottom: true})
	r.Contribute(registry.RailEntry{Name: "search", Order: 20})
	d := r.Contribute(registry.RailEntry{Name: "explorer", Order: 10})
	got := names(registry.RailEntries(r))
	if want := "explorer search settings"; got != want {
		t.Errorf("rail = %q, want %q", got, want)
	}
	d.Dispose()
	if got, want := names(registry.RailEntries(r)), "search settings"; got != want {
		t.Errorf("after dispose rail = %q, want %q", got, want)
	}
}

func names(es []registry.RailEntry) string {
	s := ""
	for i, e := range es {
		if i > 0 {
			s += " "
		}
		s += e.Name
	}
	return s
}

// SH-17: rail entries are a project's panels or the session's; a project entry runs its command with the project.
func TestRailEntryScopes(t *testing.T) {
	r := registry.New()
	r.Contribute(registry.RailEntry{Name: "explorer", Scope: registry.ScopeProjectEntry, Order: 10, Command: "view.toggle", Args: registry.Args{"id": "explorer"}})
	r.Contribute(registry.RailEntry{Name: "requests", Order: 40})
	r.Contribute(registry.RailEntry{Name: "tests", Scope: registry.ScopeProjectEntry, Order: 60})
	r.Contribute(registry.RailEntry{Name: "settings", Order: 10, Bottom: true})
	if got, want := names(registry.ProjectEntries(r)), "explorer tests"; got != want {
		t.Errorf("project entries %q, want %q", got, want)
	}
	if got, want := names(registry.SessionEntries(r)), "requests settings"; got != want {
		t.Errorf("session entries %q, want %q", got, want)
	}
	e := registry.ProjectEntries(r)[0]
	if a := e.ArgsFor("api"); a.String("project") != "api" || a.String("id") != "explorer" {
		t.Errorf("args for api: %v", a)
	}
	if e.Args["project"] != nil {
		t.Error("ArgsFor changed the entry's own args")
	}
	if a := registry.SessionEntries(r)[0].ArgsFor("api"); a["project"] != nil {
		t.Errorf("a session entry's args name a project: %v", a)
	}
}

// SH-17: what plugins add to a project's header line, by order.
func TestProjectStateParts(t *testing.T) {
	r := registry.New()
	r.Contribute(registry.ProjectState{Name: "ci", Order: 20, State: func(p, dir string) []registry.StatePart {
		return []registry.StatePart{{Text: "CI failing", Style: "error", Dot: true}}
	}})
	r.Contribute(registry.ProjectState{Name: "pr", Order: 10, State: func(p, dir string) []registry.StatePart {
		return []registry.StatePart{{Text: "#790 " + p}}
	}})
	r.Contribute(registry.ProjectState{Name: "boom", Order: 30, State: func(string, string) []registry.StatePart { panic("boom") }})
	got := registry.ProjectStateParts(r, "api", "/w/api")
	if len(got) != 2 || got[0].Text != "#790 api" || got[1].Text != "CI failing" || !got[1].Dot {
		t.Errorf("parts %+v", got)
	}
}
