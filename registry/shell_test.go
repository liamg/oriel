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
